package dsh

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/klauspost/compress/zstd"
)

type sessionHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	CreatedAt     int64  `json:"createdAt"`
	CWD           string `json:"cwd"`
	ParentSession string `json:"parentSession"`
	Origin        string `json:"origin"`
}

type contentBlock struct {
	Type       string         `json:"type"`
	Text       string         `json:"text"`
	ToolCallID string         `json:"toolCallId"`
	Content    []contentBlock `json:"content"`
	IsError    bool           `json:"isError"`
}
type message struct {
	Content    []contentBlock `json:"content"`
	ToolCallID string         `json:"toolCallId"`
	IsError    bool           `json:"isError"`
	Source     struct {
		Kind     string `json:"kind"`
		CallID   string `json:"callId"`
		Model    string `json:"model"`
		Provider string `json:"provider"`
	} `json:"source"`
}
type nativeUsage struct {
	Input      *int64 `json:"inputTokens"`
	Output     *int64 `json:"outputTokens"`
	CacheRead  *int64 `json:"cacheReadTokens"`
	CacheWrite *int64 `json:"cacheWriteTokens"`
	Reasoning  *int64 `json:"reasoningTokens"`
}
type eventData struct {
	Turn      int          `json:"turn"`
	Message   message      `json:"message"`
	Usage     *nativeUsage `json:"usage"`
	CallID    string       `json:"callId"`
	Name      string       `json:"name"`
	Arguments string       `json:"arguments"`
	Title     string       `json:"title"`
	Model     string       `json:"model"`
	Provider  string       `json:"provider"`
}
type nativeEvent struct {
	Type string          `json:"type"`
	Seq  int             `json:"seq"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

func blockText(blocks []contentBlock, blockType string) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == blockType && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func canonicalUsage(native *nativeUsage) model.TokenUsage {
	var usage model.TokenUsage
	for _, bucket := range []struct {
		source   *int64
		target   *int64
		presence *model.Presence
	}{
		{native.Input, &usage.PromptTokens, &usage.Present.Input},
		{native.Output, &usage.CompletionTokens, &usage.Present.Output},
		{native.CacheRead, &usage.CacheReadTokens, &usage.Present.CacheRead},
		{native.CacheWrite, &usage.CacheWriteTokens, &usage.Present.CacheWrite},
		{native.Reasoning, &usage.ReasoningTokens, &usage.Present.Reasoning},
	} {
		if bucket.source != nil && *bucket.source >= 0 {
			*bucket.target = *bucket.source
			*bucket.presence = model.PresenceExact
		}
	}
	return usage
}

// readSession decodes one immutable open-file view, including concatenated
// Zstandard frames. An unfinished final frame/line retains the verified prefix
// and reports degradation instead of manufacturing a normal completion.
func readSession(path, expectedID string) (*model.SessionDetail, []model.RenderEvent, error) {
	sources := sourceInventory(path)
	fail := func(kind readerr.Kind, err error) (*model.SessionDetail, []model.RenderEvent, error) {
		return nil, nil, readerr.New(kind, string(kind), err).WithSources(sources)
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fail(readerr.SourceMissing, err)
		}
		return fail(readerr.SourceUnreadable, err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return fail(readerr.SourceUnreadable, err)
	}
	transcript := io.LimitReader(file, before.Size())
	if strings.HasSuffix(path, ".zstd") {
		decoder, err := zstd.NewReader(transcript, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
		if err != nil {
			return fail(readerr.ParseFailed, err)
		}
		defer decoder.Close()
		transcript = decoder
	}
	lines := bufio.NewReaderSize(transcript, 64<<10)
	headerLine, err := lines.ReadBytes('\n')
	if err != nil {
		return fail(readerr.ParseFailed, fmt.Errorf("incomplete dsh header: %w", err))
	}
	var header sessionHeader
	if err := json.Unmarshal(headerLine, &header); err != nil {
		return fail(readerr.ParseFailed, err)
	}
	if header.Type != "session" || header.ID == "" || header.CreatedAt < 0 {
		return fail(readerr.ParseFailed, fmt.Errorf("invalid dsh header"))
	}
	if header.ID != expectedID {
		return fail(readerr.ParseFailed, fmt.Errorf("dsh session identity mismatch"))
	}
	detail := &model.SessionDetail{Session: model.Session{
		ID: header.ID, ResumeID: header.ID, AgentType: agentType, CWD: header.CWD,
		Project: filepath.Base(header.CWD), ParentSessionID: header.ParentSession,
		IsSubagent: header.Origin == "subagent" || header.ParentSession != "",
		CreatedAt:  time.UnixMilli(header.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(header.CreatedAt).UTC(),
	}, Turns: []model.TurnVM{}}
	events := []model.RenderEvent{}
	warnings := []model.ParseWarning{}
	warn := func(code string, impacts ...string) {
		warnings = append(warnings, provenance.Warning(code, model.WarningSeverityWarning, true, impacts, model.SourceRolePrimaryTranscript, nil, 1))
	}
	attach := func(state model.RecordCompletenessState) {
		facts := provenance.Input{AdapterRevision: Capabilities().AdapterRevision, Sources: sources, Warnings: warnings, HasReplayableBody: len(detail.Turns) > 0, StateOverride: state}
		result := provenance.Build(facts)
		detail.Provenance = &result
	}
	if header.Version != generationVersion(filepath.Base(path)) || (header.Version != 3 && header.Version != 4) {
		warn(model.WarnUnsupportedSchema, model.ImpactReplay)
		attach(model.RecordParserUnsupported)
		return detail, events, nil
	}
	turnIndices := map[int]int{}
	turnStarts := map[int]int64{}
	toolInvocations := map[string]model.RenderEvent{}
	toolResults := map[string]bool{}
	modelUsage := map[string]*model.ModelUsage{}
	missingUsage := false
	currentTurn := -1
	ensureTurn := func(nativeTurn int) int {
		if index, exists := turnIndices[nativeTurn]; exists {
			return index
		}
		index := len(detail.Turns)
		turnIndices[nativeTurn] = index
		detail.Turns = append(detail.Turns, model.TurnVM{TurnIndex: index})
		return index
	}
	previousSeq := -1
	for {
		line, lineErr := lines.ReadBytes('\n')
		if lineErr != nil {
			if len(line) > 0 || lineErr != io.EOF {
				warn(model.WarnTruncatedRecord, model.ImpactReplay, model.ImpactTokens, model.ImpactTools)
			}
			break
		}
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var record nativeEvent
		if err := json.Unmarshal(line, &record); err != nil || record.Type == "" || record.Seq <= previousSeq {
			warn(model.WarnMalformedRecordSkipped, model.ImpactReplay, model.ImpactTokens, model.ImpactTools)
			continue
		}
		previousSeq = record.Seq
		var data eventData
		if err := json.Unmarshal(record.Data, &data); err != nil {
			warn(model.WarnMalformedRecordSkipped, model.ImpactReplay)
			continue
		}
		timestamp := time.UnixMilli(record.Time).UTC()
		if record.Time < 0 {
			warn(model.WarnTimestampInvalid, model.ImpactMetadata)
			timestamp = detail.UpdatedAt
		}
		if timestamp.After(detail.UpdatedAt) {
			detail.UpdatedAt = timestamp
		}
		event := model.RenderEvent{EventID: header.ID + ":" + strconv.Itoa(record.Seq), Seq: record.Seq, AgentType: agentType, Timestamp: timestamp, TurnIndex: currentTurn}
		switch record.Type {
		case "turn/start":
			currentTurn = ensureTurn(data.Turn)
			turnStarts[currentTurn] = record.Time
			boundary := event
			boundary.Type = "TurnBoundary"
			boundary.TurnIndex = currentTurn
			events = append(events, boundary)
		case "turn/end":
			currentTurn = ensureTurn(data.Turn)
			if started, exists := turnStarts[currentTurn]; exists && record.Time >= started {
				detail.Turns[currentTurn].DurationMs = record.Time - started
			}
		case "session/title":
			detail.Name = data.Title
		case "request/context", "model/selection":
			detail.ModelName = data.Model
			detail.ModelProvider = data.Provider
		case "user/message":
			var userMessage message
			if err := json.Unmarshal(record.Data, &userMessage); err != nil {
				warn(model.WarnMalformedRecordSkipped, model.ImpactReplay)
				continue
			}
			if currentTurn < 0 {
				currentTurn = ensureTurn(0)
			}
			event.TurnIndex = currentTurn
			event.Type = "UserPrompt"
			event.Text = blockText(userMessage.Content, "text")
			detail.Turns[currentTurn].UserMessage = joinText(detail.Turns[currentTurn].UserMessage, event.Text)
			detail.MessageCount++
			if detail.PreviewText == "" {
				detail.PreviewText = event.Text
			}
			events = append(events, event)
		case "assistant/message":
			currentTurn = ensureTurn(data.Turn)
			event.TurnIndex = currentTurn
			if data.Message.Source.Model != "" {
				detail.ModelName = data.Message.Source.Model
				detail.ModelProvider = data.Message.Source.Provider
			}
			if thinking := blockText(data.Message.Content, "reasoning"); thinking != "" {
				thought := event
				thought.EventID += ":reasoning"
				thought.Type = "ThinkingStart"
				thought.Text = thinking
				events = append(events, thought)
			}
			event.Type = "TextChunk"
			event.Text = blockText(data.Message.Content, "text")
			if event.Text != "" {
				events = append(events, event)
				detail.Turns[currentTurn].AssistantMessage = joinText(detail.Turns[currentTurn].AssistantMessage, event.Text)
			}
			detail.MessageCount++
			detail.Turns[currentTurn].RequestCount++
			if data.Usage != nil {
				usage := canonicalUsage(data.Usage)
				detail.Turns[currentTurn].TokenUsage.AddUsage(usage)
				if detail.Billing == nil {
					detail.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
				}
				detail.Billing.Totals.AddUsage(usage)
				bucket := modelUsage[detail.ModelName]
				if bucket == nil {
					bucket = &model.ModelUsage{Model: detail.ModelName}
					modelUsage[detail.ModelName] = bucket
				}
				bucket.Requests++
				bucket.Usage.AddUsage(usage)
			} else {
				missingUsage = true
			}
		case "tool/call":
			currentTurn = ensureTurn(data.Turn)
			event.TurnIndex = currentTurn
			event.Type = "ToolInvocation"
			event.ToolCallID = data.CallID
			event.ToolName = data.Name
			if data.CallID == "" || data.Name == "" {
				warn(model.WarnPartialToolResult, model.ImpactTools)
				continue
			}
			if err := json.Unmarshal([]byte(data.Arguments), &event.ToolInput); err != nil {
				warn(model.WarnMalformedRecordSkipped, model.ImpactTools, model.ImpactDiff)
			}
			toolInvocations[data.CallID] = event
			events = append(events, event)
			turn := &detail.Turns[currentTurn]
			turn.ToolCallCount++
			turn.ToolNames = append(turn.ToolNames, data.Name)
		case "tool/result":
			currentTurn = ensureTurn(data.Turn)
			event.TurnIndex = currentTurn
			event.Type = "ToolResult"
			resultMessage := data.Message
			event.ToolCallID = resultMessage.ToolCallID
			if event.ToolCallID == "" {
				event.ToolCallID = resultMessage.Source.CallID
			}
			event.Stdout = blockText(resultMessage.Content, "text")
			failed := resultMessage.IsError
			for _, block := range resultMessage.Content {
				if block.Type == "tool-result" {
					event.ToolCallID = block.ToolCallID
					event.Stdout = blockText(block.Content, "text")
					failed = block.IsError
				}
			}
			if invocation, exists := toolInvocations[event.ToolCallID]; exists {
				toolResults[event.ToolCallID] = true
				event.ParentEventID = invocation.EventID
				event.ToolName = invocation.ToolName
				if !timestamp.Before(invocation.Timestamp) {
					event.DurationMs = timestamp.Sub(invocation.Timestamp).Milliseconds()
				}
			} else {
				warn(model.WarnPartialToolResult, model.ImpactTools)
			}
			if failed {
				event.ExitCode = 1
				event.ErrorKind = "tool_error"
				detail.Turns[currentTurn].ErrorCount++
			}
			detail.Turns[currentTurn].ToolDetails = append(detail.Turns[currentTurn].ToolDetails, model.ToolCallVM{Name: event.ToolName, ExitCode: event.ExitCode, ErrorKind: event.ErrorKind, Duration: event.DurationMs})
			events = append(events, event)
		}
	}
	detail.TurnCount = len(detail.Turns)
	for callID := range toolInvocations {
		if !toolResults[callID] {
			warn(model.WarnPartialToolResult, model.ImpactTools)
		}
	}
	if detail.Billing != nil {
		if missingUsage {
			detail.Billing.Precision = model.PrecisionMissing
		}
		for _, bucket := range modelUsage {
			detail.Billing.ByModel = append(detail.Billing.ByModel, *bucket)
		}
		sort.Slice(detail.Billing.ByModel, func(i, j int) bool { return detail.Billing.ByModel[i].Model < detail.Billing.ByModel[j].Model })
	}
	if after, err := file.Stat(); err == nil && (after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime())) {
		warn(model.WarnSourceChangedDuringRead, model.ImpactReplay)
	}
	attach("")
	return detail, events, nil
}

func joinText(previous, next string) string {
	if previous == "" {
		return next
	}
	if next == "" {
		return previous
	}
	return previous + "\n" + next
}
