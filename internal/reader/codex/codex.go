package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"session-insight/internal/model"
	"session-insight/internal/render"
)

type CodexReader struct {
	sessionsDir string
}

func New(sessionsDir string) *CodexReader {
	return &CodexReader{sessionsDir: sessionsDir}
}

func (r *CodexReader) AgentType() string  { return "codex" }
func (r *CodexReader) DisplayName() string { return "Codex" }

// RenderANSI implements reader.BaseSessionReader.
func (r *CodexReader) RenderANSI(id string) (string, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return "", fmt.Errorf("codex session not found: %s", id)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events), nil
}

// ---- JSONL shapes ----

type codexEvent struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	CWD              string `json:"cwd"`
	ModelProvider    string `json:"model_provider"`
	BaseInstructions struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
}

type codexPayload struct {
	Type    string `json:"type"`
	TurnID  string `json:"turn_id"`
	Message string `json:"message"`
	Role    string `json:"role"`
	Content json.RawMessage `json:"content"`
	// function_call / custom_tool_call
	Name           string `json:"name"`
	CustomToolName string `json:"custom_tool_name"`
	CallID         string `json:"call_id"`
	Output         string `json:"output"`
	Success        bool   `json:"success"`
	Status         string `json:"status"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	// token_count
	Info *codexTokenCountInfo `json:"info"`
	// task_complete / turn_aborted
	DurationMs int64 `json:"duration_ms"`
	// turn_aborted reason
	Reason string `json:"reason"`
	// function_call arguments (JSON string)
	Arguments string `json:"arguments"`
	// custom_tool_call input (raw string)
	Input string `json:"input"`
}

type codexTokenCountInfo struct {
	TotalTokenUsage codexTokenUsage `json:"total_token_usage"`
	LastTokenUsage  codexTokenUsage `json:"last_token_usage"`
}

type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

type codexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

var exitCodeRe = regexp.MustCompile(`Process exited with code (\d+)`)

// ---- helpers ----

func extractContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var blocks []codexContentBlock
	if json.Unmarshal(content, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if (b.Type == "output_text" || b.Type == "input_text") && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func extractExitCode(output string) int {
	m := exitCodeRe.FindStringSubmatch(output)
	if len(m) == 2 {
		var code int
		fmt.Sscanf(m[1], "%d", &code)
		return code
	}
	return 0
}

func extractModelName(meta *codexSessionMeta) string {
	if meta == nil {
		return ""
	}
	text := meta.BaseInstructions.Text
	if idx := strings.Index(text, "based on "); idx >= 0 {
		rest := text[idx+len("based on "):]
		if end := strings.IndexAny(rest, ".\n"); end > 0 {
			return rest[:end]
		}
		return rest
	}
	if meta.ModelProvider != "" {
		return meta.ModelProvider
	}
	return ""
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func parseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---- ListSessions ----

func (r *CodexReader) ListSessions() ([]model.Session, error) {
	var files []string
	err := filepath.WalkDir(r.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	type result struct {
		session model.Session
		ok      bool
	}
	results := make(chan result, len(files))
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup

	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer func() { <-sem; wg.Done() }()
			if sess, ok := readSessionMeta(path); ok {
				results <- result{sess, true}
			}
		}(f)
	}
	wg.Wait()
	close(results)

	var sessions []model.Session
	for r := range results {
		sessions = append(sessions, r.session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func readSessionMeta(jsonlPath string) (model.Session, bool) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return model.Session{}, false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return model.Session{}, false
	}
	fileSize := fi.Size()

	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")

	var (
		cwd          string
		modelName    string
		firstUserMsg string
		userMessages []string
		createdAt    time.Time
		updatedAt    time.Time
		headLines    int
		headBytes    int64
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	const headMax = 200
	for scanner.Scan() && headLines < headMax {
		headLines++
		headBytes += int64(len(scanner.Bytes()) + 1)

		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		if evt.Timestamp != "" {
			if t := parseTimestamp(evt.Timestamp); !t.IsZero() {
				if createdAt.IsZero() || t.Before(createdAt) {
					createdAt = t
				}
				if t.After(updatedAt) {
					updatedAt = t
				}
			}
		}

		switch evt.Type {
		case "session_meta":
			var m codexSessionMeta
			if json.Unmarshal(evt.Payload, &m) == nil {
				if cwd == "" && m.CWD != "" {
					cwd = m.CWD
				}
				if modelName == "" {
					modelName = extractModelName(&m)
				}
			}

		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			if p.Type == "user_message" && p.Message != "" {
				if firstUserMsg == "" {
					firstUserMsg = p.Message
				}
				if len(userMessages) < 5 {
					userMessages = append(userMessages, p.Message)
				}
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			if p.Type == "message" && p.Role == "assistant" && modelName == "" {
				// Best-effort: try content for model hints (unlikely to find here)
				_ = p
			}
		}
	}

	if updatedAt.IsZero() {
		updatedAt = fi.ModTime()
	}
	if createdAt.IsZero() {
		createdAt = updatedAt
	}

	name := resolveName(firstUserMsg, createdAt)
	previewText := buildPreviewText(userMessages)

	msgCount := estimateLineCount(headLines, headBytes, fileSize)

	// Tail scan for updatedAt from last event timestamp
	const tailBytes = 8 * 1024
	if fileSize > tailBytes {
		seekPos := fileSize - tailBytes
		if _, err := f.Seek(seekPos, 0); err == nil {
			tailScanner := bufio.NewScanner(f)
			tailScanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
			tailScanner.Scan() // skip partial first line
			for tailScanner.Scan() {
				var evt codexEvent
				if json.Unmarshal(tailScanner.Bytes(), &evt) != nil {
					continue
				}
				if evt.Timestamp != "" {
					if t := parseTimestamp(evt.Timestamp); !t.IsZero() && t.After(updatedAt) {
						updatedAt = t
					}
				}
			}
		}
	}

	return model.Session{
		ID:           sessionID,
		AgentType:    "codex",
		CWD:          cwd,
		Name:         name,
		ModelName:    modelName,
		PreviewText:  previewText,
		MessageCount: msgCount,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, true
}

func resolveName(firstUserMsg string, createdAt time.Time) string {
	if firstUserMsg != "" {
		return truncateRunes(firstUserMsg, 50)
	}
	if !createdAt.IsZero() {
		return "Codex " + createdAt.Format("01-02 15:04")
	}
	return "Codex Session"
}

func buildPreviewText(messages []string) string {
	const maxPerMsg = 200
	const maxTotal = 1500
	var parts []string
	for _, m := range messages {
		parts = append(parts, truncateRunes(m, maxPerMsg))
	}
	joined := strings.Join(parts, " | ")
	if len(joined) > maxTotal {
		return joined[:maxTotal] + "..."
	}
	return joined
}

func estimateLineCount(headLines int, headBytes int64, fileSize int64) int {
	if headLines == 0 || headBytes == 0 {
		return 0
	}
	avgLineLen := float64(headBytes) / float64(headLines)
	return int(float64(fileSize)/avgLineLen * 1.1)
}

// ---- GetSession ----

func (r *CodexReader) GetSession(id string) (*model.SessionDetail, error) {
	jsonlPath := r.findSessionFile(id)
	if jsonlPath == "" {
		return nil, fmt.Errorf("codex session not found: %s", id)
	}

	session, ok := readSessionMeta(jsonlPath)
	if !ok {
		return nil, fmt.Errorf("failed to read codex session: %s", id)
	}

	turns, modelName := parseCodexEvents(jsonlPath)
	if modelName != "" {
		session.ModelName = modelName
	}
	session.TurnCount = len(turns)

	detail := &model.SessionDetail{Session: session, Turns: turns}

	// Anomaly detection
	var durations []int64
	for _, t := range turns {
		if t.DurationMs > 0 {
			durations = append(durations, t.DurationMs)
		}
	}

	if len(durations) > 1 {
		var sum int64
		for _, d := range durations {
			sum += d
		}
		mean := float64(sum) / float64(len(durations))
		var variance float64
		for _, d := range durations {
			variance += (float64(d) - mean) * (float64(d) - mean)
		}
		stdDev := math.Sqrt(variance / float64(len(durations)))
		threshold := mean + 3*stdDev

		summary := model.AnomalySummary{}
		for i := range turns {
			if turns[i].ErrorCount > 0 {
				turns[i].Anomalies = append(turns[i].Anomalies, "tool_failure")
				summary.ToolFailures++
			}
			if float64(turns[i].DurationMs) > threshold && turns[i].DurationMs > 30000 {
				turns[i].Anomalies = append(turns[i].Anomalies, "duration_spike")
				summary.DurationSpikes++
			}
		}
		summary.TotalAnomalies = summary.ToolFailures + summary.DurationSpikes
		if len(turns) > 0 {
			detail.AnomalySummary = summary
		}
	}

	return detail, nil
}

func (r *CodexReader) findSessionFile(sessionID string) string {
	var found string
	filepath.WalkDir(r.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !d.IsDir() && d.Name() == sessionID+".jsonl" {
			found = path
		}
		return nil
	})
	return found
}

// ---- Event parsing ----

func parseCodexEvents(path string) ([]model.TurnVM, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ""
	}
	defer f.Close()

	var (
		turns       []model.TurnVM
		foundModel  string
		currentTurn *model.TurnVM
		turnStartTS string
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		switch evt.Type {
		case "session_meta":
			if foundModel == "" {
				var m codexSessionMeta
				if json.Unmarshal(evt.Payload, &m) == nil {
					foundModel = extractModelName(&m)
				}
			}

		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "task_started":
				if currentTurn != nil {
					turns = append(turns, *currentTurn)
				}
				currentTurn = &model.TurnVM{
					TurnIndex: len(turns),
					Events:    []model.EventVM{},
				}
				turnStartTS = evt.Timestamp

			case "user_message":
				if currentTurn != nil && p.Message != "" {
					currentTurn.UserMessage = p.Message
				}
				// user_message before any task_started: record for session name but don't create turn
				if currentTurn == nil {
					continue
				}

			case "agent_message":
				if currentTurn != nil && p.Message != "" {
					currentTurn.AssistantMessage += p.Message
				}

			case "token_count":
				if currentTurn != nil && p.Info != nil {
					u := &currentTurn.TokenUsage
					u.PromptTokens += p.Info.LastTokenUsage.InputTokens
					u.CompletionTokens += p.Info.LastTokenUsage.OutputTokens
					u.CacheReadTokens += p.Info.LastTokenUsage.CachedInputTokens
				}

			case "patch_apply_end":
				if currentTurn != nil {
					currentTurn.Events = append(currentTurn.Events, model.EventVM{
						Type:      "patch_apply_end",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"success": p.Success, "stdout": p.Stdout, "stderr": p.Stderr},
					})
					if !p.Success {
						currentTurn.ErrorCount++
					}
				}

			case "task_complete":
				// turn boundary marker — keep turn open but record completion
				if currentTurn != nil {
					currentTurn.Events = append(currentTurn.Events, model.EventVM{
						Type:      "task_complete",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{},
					})
				}

			case "turn_aborted":
				if currentTurn != nil {
					currentTurn.DurationMs = p.DurationMs
				}
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "message":
				if currentTurn == nil {
					continue
				}
				text := extractContentText(p.Content)
				switch p.Role {
				case "assistant":
					currentTurn.AssistantMessage += text
				case "user":
					// system-context user message, not the actual user prompt
					// skip to avoid overwriting user_message
				}

			case "reasoning":
				// reasoning may be encrypted; skip text extraction

			case "function_call":
				if currentTurn != nil {
					currentTurn.ToolCallCount++
					if p.Name != "" {
						currentTurn.ToolNames = append(currentTurn.ToolNames, p.Name)
					}
					currentTurn.Events = append(currentTurn.Events, model.EventVM{
						Type:      "function_call",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"name": p.Name, "call_id": p.CallID},
					})
				}

			case "function_call_output":
				if currentTurn != nil {
					exitCode := extractExitCode(p.Output)
					isErr := exitCode != 0
					currentTurn.Events = append(currentTurn.Events, model.EventVM{
						Type:      "function_call_output",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"call_id": p.CallID, "exit_code": exitCode},
					})
					if isErr {
						currentTurn.ErrorCount++
					}
				}

			case "custom_tool_call":
				if currentTurn != nil {
					currentTurn.ToolCallCount++
					name := p.Name
					if name == "" {
						name = p.CustomToolName
					}
					if name != "" {
						currentTurn.ToolNames = append(currentTurn.ToolNames, name)
					}
					currentTurn.Events = append(currentTurn.Events, model.EventVM{
						Type:      "custom_tool_call",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"name": name, "call_id": p.CallID, "status": p.Status},
					})
				}

			case "custom_tool_call_output":
				if currentTurn != nil {
					exitCode := extractExitCode(p.Output)
					isErr := exitCode != 0
					currentTurn.Events = append(currentTurn.Events, model.EventVM{
						Type:      "custom_tool_call_output",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"call_id": p.CallID, "exit_code": exitCode},
					})
					if isErr {
						currentTurn.ErrorCount++
					}
				}
			}
		}

		// Track duration from turn start to latest event
		if currentTurn != nil && turnStartTS != "" && evt.Timestamp != "" {
			if t1 := parseTimestamp(turnStartTS); !t1.IsZero() {
				if t2 := parseTimestamp(evt.Timestamp); !t2.IsZero() {
					dur := t2.Sub(t1).Milliseconds()
					if dur > currentTurn.DurationMs {
						currentTurn.DurationMs = dur
					}
				}
			}
		}
	}

	if currentTurn != nil {
		turns = append(turns, *currentTurn)
	}

	// Filter empty turns
	turns = filterEmptyTurns(turns)

	return turns, foundModel
}

func filterEmptyTurns(turns []model.TurnVM) []model.TurnVM {
	filtered := turns[:0]
	for _, t := range turns {
		if t.UserMessage == "" && t.AssistantMessage == "" && t.ToolCallCount == 0 {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// ---- RenderEvent adapter ----

// codexToRenderEvents parses a Codex JSONL session file into a flat
// []model.RenderEvent stream suitable for render.FormatEvents.
func codexToRenderEvents(path string) ([]model.RenderEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		events       []model.RenderEvent
		fileTag      = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		eventCtr     int
		turnIndex    int
		pendingTools = make(map[string]string) // callID -> ToolInvocation EventID
		completed    = make(map[string]bool)   // patch_apply_end already emitted the result
	)

	currentTurnIndex := func() int {
		if turnIndex == 0 {
			return 0
		}
		return turnIndex - 1
	}

	emit := func(evt model.RenderEvent) string {
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("evt-%s-%04d", fileTag, eventCtr)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "codex"
		}
		events = append(events, evt)
		return evt.EventID
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		ts := parseTimestamp(evt.Timestamp)

		switch evt.Type {
		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "task_started":
				turnIndex++
				emit(model.RenderEvent{
					Type:      "TurnBoundary",
					Timestamp: ts,
					TurnIndex: turnIndex - 1,
				})

			case "user_message":
				if p.Message != "" {
					emit(model.RenderEvent{
						Type:      "UserPrompt",
						Timestamp: ts,
						TurnIndex: currentTurnIndex(),
						Text:      p.Message,
					})
				}

			case "agent_message":
				if p.Message != "" {
					emit(model.RenderEvent{
						Type:      "TextChunk",
						Timestamp: ts,
						TurnIndex: currentTurnIndex(),
						Text:      p.Message,
					})
				}

			case "patch_apply_end":
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
					completed[p.CallID] = true
				}
				exitCode := 0
				if !p.Success {
					exitCode = 1
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     currentTurnIndex(),
					ToolCallID:    p.CallID,
					Stdout:        p.Stdout,
					Stderr:        p.Stderr,
					ExitCode:      exitCode,
					ParentEventID: parentEventID,
				})
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "message":
				// Skip: agent_message already covers the assistant text;
				// response_item message blocks would duplicate it.
			case "function_call":
				invID := emit(model.RenderEvent{
					Type:       "ToolInvocation",
					Timestamp:  ts,
					TurnIndex:  currentTurnIndex(),
					ToolName:   p.Name,
					ToolCallID: p.CallID,
					ToolInput:  parseArguments(p.Arguments),
				})
				if p.CallID != "" {
					pendingTools[p.CallID] = invID
				}

			case "function_call_output":
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     currentTurnIndex(),
					ToolCallID:    p.CallID,
					Stdout:        p.Output,
					ExitCode:      extractExitCode(p.Output),
					ParentEventID: parentEventID,
				})

			case "custom_tool_call":
				name := p.Name
				if name == "" {
					name = p.CustomToolName
				}
				input := p.Input
				if input == "" {
					input = p.Arguments
				}
				invID := emit(model.RenderEvent{
					Type:       "ToolInvocation",
					Timestamp:  ts,
					TurnIndex:  currentTurnIndex(),
					ToolName:   name,
					ToolCallID: p.CallID,
					ToolInput:  parseArguments(input),
				})
				if p.CallID != "" {
					pendingTools[p.CallID] = invID
				}

			case "custom_tool_call_output":
				if p.CallID != "" && completed[p.CallID] {
					delete(completed, p.CallID)
					continue
				}
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     currentTurnIndex(),
					ToolCallID:    p.CallID,
					Stdout:        p.Output,
					ExitCode:      extractExitCode(p.Output),
					ParentEventID: parentEventID,
				})
			}
		}
	}

	return dropEmptyCodexRenderTurns(events), scanner.Err()
}

func dropEmptyCodexRenderTurns(events []model.RenderEvent) []model.RenderEvent {
	hasContent := make(map[int]bool)
	for _, event := range events {
		switch event.Type {
		case "TurnBoundary":
		case "UserPrompt":
			if strings.TrimSpace(event.Text) != "" {
				hasContent[event.TurnIndex] = true
			}
		default:
			hasContent[event.TurnIndex] = true
		}
	}

	filtered := make([]model.RenderEvent, 0, len(events))
	for _, event := range events {
		if (event.Type == "TurnBoundary" || event.Type == "UserPrompt") && !hasContent[event.TurnIndex] {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

// parseArguments attempts to unmarshal a JSON string into map[string]any.
// On failure, it wraps the raw string as {"args": args}.
func parseArguments(args string) map[string]any {
	if args == "" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(args), &result); err != nil {
		return map[string]any{"args": args}
	}
	return result
}
