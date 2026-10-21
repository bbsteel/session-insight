package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

type parsedFile struct {
	Session model.Session
	Detail  *model.SessionDetail
	Events  []model.RenderEvent
}

type sessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type sessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Provider  string          `json:"provider"`
	ModelID   string          `json:"modelId"`
	// Model is the usage entry's own attribution target (UsageEntry.model).
	// It is distinct from model_change's modelId and must not be confused
	// with the session's selected model.
	Model   string          `json:"model"`
	Name    string          `json:"name"`
	Summary string          `json:"summary"`
	Kind    string          `json:"kind"`
	Usage   *piUsage        `json:"usage"`
	Content json.RawMessage `json:"content"`
	Display *bool           `json:"display"`
}

type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Usage        *piUsage        `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	DurationMs   int64           `json:"durationMs"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	IsError      bool            `json:"isError"`

	// Direct-shell fields (role "bashExecution"). Pi records a user-issued
	// !/!! command and its output as a first-class message, not as a tool
	// result, so replay must render it from these fields.
	Command        string `json:"command"`
	Output         string `json:"output"`
	ExitCode       *int   `json:"exitCode"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath"`
}

type piUsage struct {
	Input       int64  `json:"input"`
	Output      int64  `json:"output"`
	CacheRead   int64  `json:"cacheRead"`
	CacheWrite  int64  `json:"cacheWrite"`
	Reasoning   *int64 `json:"reasoning"`
	TotalTokens int64  `json:"totalTokens"`
	Cost        piCost `json:"cost"`
}

type piCost struct {
	Total float64 `json:"total"`
}

type contentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Thinking  string         `json:"thinking"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func parseSessionFile(path string) (*parsedFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	session := model.Session{
		AgentType: agentType,
		ResumeID:  "",
		CreatedAt: info.ModTime(),
		UpdatedAt: info.ModTime(),
	}
	var (
		headerSeen    bool
		skipped       int
		named         string
		preview       string
		modelName     string
		modelProvider string
		messageCount  int
		turnIndex     = -1
		eventCounter  int
		turnOpen      bool
		lastMalformed bool
		events        []model.RenderEvent
		turns         []model.TurnVM
		callEventIDs  = map[string]string{}
		billingUsage  model.TokenUsage
		billingCost   float64
		hasUsage      bool
		byModel       = map[string]*model.ModelUsage{}
	)

	ensureTurn := func(ts time.Time) int {
		if turnIndex < 0 {
			turnIndex = 0
			turns = append(turns, model.TurnVM{TurnIndex: 0})
			events = append(events, model.RenderEvent{
				Type: "TurnBoundary", Timestamp: ts, TurnIndex: 0, AgentType: agentType,
			})
		}
		return turnIndex
	}
	emit := func(event model.RenderEvent) string {
		if event.EventID == "" {
			event.EventID = fmt.Sprintf("evt-pi-%04d", eventCounter)
			eventCounter++
		}
		if event.AgentType == "" {
			event.AgentType = agentType
		}
		events = append(events, event)
		return event.EventID
	}
	currentTurn := func() *model.TurnVM {
		if turnIndex < 0 || turnIndex >= len(turns) {
			return nil
		}
		return &turns[turnIndex]
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lastMalformed = false
		var entry sessionEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			lastMalformed = true
			skipped++
			continue
		}
		ts := parseTimestamp(entry.Timestamp)
		if !ts.IsZero() && ts.After(session.UpdatedAt) {
			session.UpdatedAt = ts
		}

		switch entry.Type {
		case "session":
			var header sessionHeader
			if json.Unmarshal([]byte(line), &header) != nil {
				skipped++
				continue
			}
			headerSeen = true
			session.ID = header.ID
			session.ResumeID = header.ID
			session.CWD = header.CWD
			session.Project = shared.ResolveProject(header.CWD, "")
			if created := parseTimestamp(header.Timestamp); !created.IsZero() {
				session.CreatedAt = created
				if session.UpdatedAt.Before(created) {
					session.UpdatedAt = created
				}
			}

		case "session_info":
			if strings.TrimSpace(entry.Name) != "" {
				named = strings.TrimSpace(entry.Name)
			}

		case "model_change":
			if entry.ModelID != "" {
				modelName = entry.ModelID
			}
			if entry.Provider != "" {
				modelProvider = entry.Provider
			}

		case "usage":
			if entry.Usage != nil {
				// A standalone usage entry names its own attribution target
				// (kind "cache_warm" writes a different model than the
				// session's selected one). Bill it there; the session's
				// ModelName is deliberately left untouched.
				addUsage(&billingUsage, &billingCost, &hasUsage, byModel, entry.Usage, usageEntryModel(entry, modelName))
			}

		case "compaction":
			idx := ensureTurn(ts)
			if text := strings.TrimSpace(entry.Summary); text != "" {
				emit(model.RenderEvent{
					Type: "AgentSpecific", Subtype: "compaction", Timestamp: ts, TurnIndex: idx, Text: text,
				})
			}
			if entry.Usage != nil {
				addUsage(&billingUsage, &billingCost, &hasUsage, byModel, entry.Usage, modelName)
			}

		case "custom_message":
			if entry.Display != nil && !*entry.Display {
				continue
			}
			idx := ensureTurn(ts)
			if text := strings.TrimSpace(contentText(entry.Content)); text != "" {
				emit(model.RenderEvent{
					Type: "TextChunk", Timestamp: ts, TurnIndex: idx, Text: text,
				})
			}

		case "message":
			var msg piMessage
			if json.Unmarshal(entry.Message, &msg) != nil {
				skipped++
				continue
			}
			messageCount++
			switch msg.Role {
			case "system":
				continue
			case "user":
				turnIndex++
				turns = append(turns, model.TurnVM{TurnIndex: turnIndex})
				emit(model.RenderEvent{Type: "TurnBoundary", Timestamp: ts, TurnIndex: turnIndex})
				text := strings.TrimSpace(contentText(msg.Content))
				if text != "" {
					turns[turnIndex].UserMessage = text
					if preview == "" {
						preview = text
					}
					emit(model.RenderEvent{Type: "UserPrompt", Timestamp: ts, TurnIndex: turnIndex, Text: text})
				}
				turnOpen = true
			case "assistant":
				idx := ensureTurn(ts)
				turn := currentTurn()
				if msg.Model != "" {
					modelName = msg.Model
				}
				if msg.Provider != "" {
					modelProvider = msg.Provider
				}
				if msg.DurationMs > 0 && turn != nil {
					turn.DurationMs += msg.DurationMs
				}
				if msg.StopReason == "error" && turn != nil {
					turn.ErrorCount++
				}
				if msg.Usage != nil {
					tokens := usageToTokens(msg.Usage)
					if turn != nil {
						turn.TokenUsage.AddUsage(tokens)
						turn.RequestCount++
					}
					// Bill under the current model: an assistant message may omit
					// "model", and model_change/earlier assistant already set modelName.
					addUsage(&billingUsage, &billingCost, &hasUsage, byModel, msg.Usage, modelName)
				}
				for _, block := range contentBlocks(msg.Content) {
					switch block.Type {
					case "thinking":
						if strings.TrimSpace(block.Thinking) != "" {
							emit(model.RenderEvent{
								Type: "ThinkingStart", Timestamp: ts, TurnIndex: idx, Text: strings.TrimSpace(block.Thinking),
							})
						}
					case "text":
						if strings.TrimSpace(block.Text) == "" {
							continue
						}
						if turn != nil {
							if turn.AssistantMessage != "" {
								turn.AssistantMessage += "\n"
							}
							turn.AssistantMessage += block.Text
						}
						emit(model.RenderEvent{Type: "TextChunk", Timestamp: ts, TurnIndex: idx, Text: block.Text})
					case "toolCall":
						callID := block.ID
						if callID == "" {
							callID = entry.ID
						}
						if turn != nil {
							turn.ToolCallCount++
							if block.Name != "" {
								turn.ToolNames = append(turn.ToolNames, block.Name)
							}
						}
						eventID := emit(model.RenderEvent{
							Type: "ToolInvocation", Timestamp: ts, TurnIndex: idx,
							ToolName: block.Name, ToolInput: block.Arguments, ToolCallID: callID,
						})
						callEventIDs[callID] = eventID
					}
				}
				if msg.ErrorMessage != "" {
					emit(model.RenderEvent{
						Type: "AgentSpecific", Subtype: "error", Timestamp: ts, TurnIndex: idx, Text: msg.ErrorMessage,
					})
				}
				turnOpen = assistantTurnOpen(msg.StopReason)
			case "toolResult":
				idx := ensureTurn(ts)
				turn := currentTurn()
				text := shared.CollapseBinarySpans(contentText(msg.Content))
				exitCode := 0
				stderr := ""
				stdout := text
				errorKind := ""
				if msg.IsError {
					exitCode = 1
					stderr = text
					stdout = ""
					errorKind = "tool_error"
					if turn != nil {
						turn.ErrorCount++
					}
				}
				if turn != nil && msg.DurationMs > 0 {
					turn.DurationMs += msg.DurationMs
				}
				if turn != nil {
					detail := model.ToolCallVM{
						Name:         msg.ToolName,
						ExitCode:     exitCode,
						Duration:     msg.DurationMs,
						ErrorKind:    errorKind,
						ErrorMessage: stderr,
					}
					turn.ToolDetails = append(turn.ToolDetails, detail)
				}
				parent := callEventIDs[msg.ToolCallID]
				emit(model.RenderEvent{
					Type: "ToolResult", Timestamp: ts, TurnIndex: idx,
					ParentEventID: parent, ToolCallID: msg.ToolCallID, ToolName: msg.ToolName,
					Stdout: stdout, Stderr: stderr, ExitCode: exitCode,
					DurationMs: msg.DurationMs, ErrorKind: errorKind,
				})
				turnOpen = true
			case "bashExecution":
				// Direct !/!! commands (and RPC bash) are persisted as their own
				// message role. Replay them as a shell invocation plus result so
				// the command, output, and outcome survive; the record is native
				// data, not a malformed or unknown entry.
				idx := ensureTurn(ts)
				turn := currentTurn()
				callID := "bash-" + entry.ID
				if entry.ID == "" {
					callID = fmt.Sprintf("bash-evt-%04d", eventCounter)
				}
				if turn != nil {
					turn.ToolCallCount++
					turn.ToolNames = append(turn.ToolNames, "bash")
				}
				invocationEventID := emit(model.RenderEvent{
					Type: "ToolInvocation", Timestamp: ts, TurnIndex: idx,
					ToolName: "bash", ToolInput: map[string]any{"command": msg.Command}, ToolCallID: callID,
				})
				callEventIDs[callID] = invocationEventID

				exitCode := 0
				errorKind := ""
				switch {
				case msg.Cancelled:
					// A cancelled command has no exit status; mark it rejected so
					// the shared renderer shows the cancellation instead of success.
					errorKind = "cancelled"
				case msg.ExitCode != nil && *msg.ExitCode != 0:
					exitCode = *msg.ExitCode
					errorKind = "tool_error"
				}
				if errorKind != "" && turn != nil {
					turn.ErrorCount++
				}
				if turn != nil {
					turn.ToolDetails = append(turn.ToolDetails, model.ToolCallVM{
						Name:         "bash",
						ExitCode:     exitCode,
						ErrorKind:    errorKind,
						ErrorMessage: bashExecutionErrorText(msg, errorKind),
						Rejected:     msg.Cancelled,
					})
				}
				emit(model.RenderEvent{
					Type: "ToolResult", Timestamp: ts, TurnIndex: idx,
					ParentEventID: invocationEventID, ToolCallID: callID, ToolName: "bash",
					Stdout: msg.Output, ExitCode: exitCode, ErrorKind: errorKind,
					Rejected: msg.Cancelled, Truncated: msg.Truncated,
				})
				turnOpen = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	skipped = retractIncompleteTail(skipped, lastMalformed, path, info.Size())
	if !headerSeen && session.ID == "" {
		session.ID = sessionIDFromFilename(filepath.Base(path))
		session.ResumeID = session.ID
	}
	if session.ID == "" {
		return nil, fmt.Errorf("pi session file has no id: %s", path)
	}

	applySessionIdentity(&session, named, preview, modelName, modelProvider, messageCount)

	filtered := shared.FilterEmptyTurns(turns)
	session.TurnCount = len(filtered)

	if turnOpen {
		if event, ok := shared.TrailingInProgress(true, info.ModTime(), turnIndex); ok {
			events = append(events, event)
		}
	}
	events = shared.DropEmptyRenderTurns(shared.InterleaveToolResults(events))
	if events == nil {
		events = []model.RenderEvent{}
	}

	detail := &model.SessionDetail{Session: session, Turns: filtered}
	if hasUsage {
		billing := &model.SessionBilling{
			Precision:     model.PrecisionExact,
			BillingUnit:   "usd",
			BillingAmount: billingCost,
			Totals:        billingUsage,
		}
		for _, usage := range byModel {
			billing.ByModel = append(billing.ByModel, *usage)
		}
		sort.SliceStable(billing.ByModel, func(i, j int) bool {
			return billing.ByModel[i].Model < billing.ByModel[j].Model
		})
		detail.Billing = billing
	} else {
		detail.Billing = &model.SessionBilling{Precision: model.PrecisionMissing}
	}
	detail.AnomalySummary = shared.RunAnomalyDetection(detail.Turns)
	attachProvenance(detail, path, skipped)
	return &parsedFile{Session: detail.Session, Detail: detail, Events: events}, nil
}

func applySessionIdentity(session *model.Session, named, preview, modelName, modelProvider string, messageCount int) {
	if named != "" {
		session.Name = shared.TruncateRunes(strings.ReplaceAll(named, "\n", " "), 50)
	} else if preview != "" {
		session.Name = shared.TruncateRunes(strings.ReplaceAll(preview, "\n", " "), 50)
	}
	session.PreviewText = shared.TruncateRunes(preview, 200)
	session.ModelName = modelName
	session.ModelProvider = modelProvider
	session.MessageCount = messageCount
	if session.AgentType == "" {
		session.AgentType = agentType
	}
}

func retractIncompleteTail(skipped int, lastMalformed bool, path string, size int64) int {
	if skipped <= 0 || !lastMalformed {
		return skipped
	}
	if fileEndsWithNewline(path, size) {
		return skipped
	}
	return skipped - 1
}

func fileEndsWithNewline(path string, size int64) bool {
	if size <= 0 {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, size-1); err != nil {
		return true
	}
	return buf[0] == '\n'
}

// scanSessionMeta reads identity, counts, and latest model/time without
// reconstructing turns, tools, or billing.
func scanSessionMeta(path string) (model.Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return model.Session{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return model.Session{}, err
	}
	defer f.Close()

	session := model.Session{
		AgentType: agentType,
		CreatedAt: info.ModTime(),
		UpdatedAt: info.ModTime(),
	}
	var (
		headerSeen     bool
		skipped        int
		lastMalformed  bool
		named          string
		preview        string
		modelName      string
		modelProvider  string
		messageCount   int
		turnCount      int
		turnOpen       bool
		turnHasContent bool
	)

	// Turn accounting must match parseSessionFile, which computes the detail
	// view's TurnCount through shared.FilterEmptyTurns. A turn survives that
	// filter when it has a user message, an assistant message, or a tool call,
	// so a turn is counted only once one of those markers is set. User messages
	// always start a turn (empty ones are dropped again by the filter), while
	// assistant, toolResult, compaction, custom_message, and usage records
	// continue the open turn and start one only when none is open.
	flushTurn := func() {
		if turnOpen && turnHasContent {
			turnCount++
		}
		turnOpen = false
	}
	openTurn := func() {
		flushTurn()
		turnOpen = true
		turnHasContent = false
	}
	startTurn := func() {
		if !turnOpen {
			openTurn()
		}
	}
	markTurnContent := func() { turnHasContent = true }
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lastMalformed = false
		var entry sessionEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			lastMalformed = true
			skipped++
			continue
		}
		ts := parseTimestamp(entry.Timestamp)
		if !ts.IsZero() && ts.After(session.UpdatedAt) {
			session.UpdatedAt = ts
		}
		switch entry.Type {
		case "session":
			var header sessionHeader
			if json.Unmarshal([]byte(line), &header) != nil {
				lastMalformed = true
				skipped++
				continue
			}
			headerSeen = true
			session.ID = header.ID
			session.ResumeID = header.ID
			session.CWD = header.CWD
			session.Project = shared.ResolveProject(header.CWD, "")
			if created := parseTimestamp(header.Timestamp); !created.IsZero() {
				session.CreatedAt = created
				if session.UpdatedAt.Before(created) {
					session.UpdatedAt = created
				}
			}
		case "session_info":
			if strings.TrimSpace(entry.Name) != "" {
				named = strings.TrimSpace(entry.Name)
			}
		case "model_change":
			if entry.ModelID != "" {
				modelName = entry.ModelID
			}
			if entry.Provider != "" {
				modelProvider = entry.Provider
			}
		case "usage":
			if entry.Usage != nil {
				startTurn()
			}
		case "compaction":
			startTurn()
		case "custom_message":
			if entry.Display == nil || *entry.Display {
				startTurn()
			}
		case "message":
			var msg piMessage
			if json.Unmarshal(entry.Message, &msg) != nil {
				lastMalformed = true
				skipped++
				continue
			}
			messageCount++
			switch msg.Role {
			case "user":
				openTurn()
				text := strings.TrimSpace(contentText(msg.Content))
				if text != "" {
					markTurnContent()
					if preview == "" {
						preview = text
					}
				}
			case "assistant":
				startTurn()
				for _, block := range contentBlocks(msg.Content) {
					switch block.Type {
					case "text":
						if strings.TrimSpace(block.Text) != "" {
							markTurnContent()
						}
					case "toolCall":
						markTurnContent()
					}
				}
			case "toolResult":
				startTurn()
			case "bashExecution":
				// Direct-shell messages replay as a bash invocation plus result,
				// so they are turn content exactly like a tool call.
				startTurn()
				markTurnContent()
			}
			if msg.Model != "" {
				modelName = msg.Model
			}
			if msg.Provider != "" {
				modelProvider = msg.Provider
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return model.Session{}, err
	}
	_ = retractIncompleteTail(skipped, lastMalformed, path, info.Size())
	flushTurn()
	if !headerSeen && session.ID == "" {
		session.ID = sessionIDFromFilename(filepath.Base(path))
		session.ResumeID = session.ID
	}
	if session.ID == "" {
		return model.Session{}, fmt.Errorf("pi session file has no id: %s", path)
	}
	applySessionIdentity(&session, named, preview, modelName, modelProvider, messageCount)
	session.TurnCount = turnCount
	return session, nil
}

func assistantTurnOpen(stopReason string) bool {
	switch stopReason {
	case "stop", "error", "aborted", "length", "deferred":
		return false
	default:
		// toolUse, pending, empty: the turn has not closed.
		return true
	}
}

func contentBlocks(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			return []contentBlock{{Type: "text", Text: text}}
		}
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	return nil
}

func contentText(raw json.RawMessage) string {
	blocks := contentBlocks(raw)
	if len(blocks) == 0 {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

func usageToTokens(u *piUsage) model.TokenUsage {
	if u == nil {
		return model.TokenUsage{}
	}
	out := model.TokenUsage{
		PromptTokens:     u.Input,
		CompletionTokens: u.Output,
		CacheReadTokens:  u.CacheRead,
		CacheWriteTokens: u.CacheWrite,
		Present: model.TokenPresence{
			Input:      model.PresenceExact,
			Output:     model.PresenceExact,
			CacheRead:  model.PresenceExact,
			CacheWrite: model.PresenceExact,
		},
	}
	if u.Reasoning != nil {
		out.ReasoningTokens = *u.Reasoning
		out.Present.Reasoning = model.PresenceExact
	}
	return out
}

// usageEntryModel is the attribution target for a standalone usage entry.
// Pi records the billed model on the entry itself (UsageEntry.model); fall back
// to the session's selected model only when the entry omits it.
func usageEntryModel(entry sessionEntry, sessionModel string) string {
	if trimmed := strings.TrimSpace(entry.Model); trimmed != "" {
		return trimmed
	}
	return sessionModel
}

// bashExecutionErrorText renders the recorded outcome of a direct-shell
// message for turn-level tool details (which carry no output channel).
func bashExecutionErrorText(msg piMessage, errorKind string) string {
	switch errorKind {
	case "cancelled":
		return "command cancelled"
	case "tool_error":
		if msg.ExitCode != nil {
			return fmt.Sprintf("exit code %d", *msg.ExitCode)
		}
		return "command failed"
	}
	return ""
}

func addUsage(totals *model.TokenUsage, cost *float64, has *bool, byModel map[string]*model.ModelUsage, u *piUsage, modelName string) {
	if u == nil {
		return
	}
	*has = true
	tokens := usageToTokens(u)
	totals.AddUsage(tokens)
	*cost += u.Cost.Total
	key := modelName
	if key == "" {
		key = "unknown"
	}
	slot := byModel[key]
	if slot == nil {
		slot = &model.ModelUsage{Model: key}
		byModel[key] = slot
	}
	slot.Requests++
	slot.BillingAmount += u.Cost.Total
	slot.Usage.AddUsage(tokens)
}
