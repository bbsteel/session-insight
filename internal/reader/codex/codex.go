package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

type CodexReader struct {
	sessionsDir string
}

func New(sessionsDir string) *CodexReader {
	return &CodexReader{sessionsDir: sessionsDir}
}

func (r *CodexReader) WatchRoots() []string { return []string{r.sessionsDir} }

func (r *CodexReader) AgentType() string   { return "codex" }
func (r *CodexReader) DisplayName() string { return "Codex" }

// RenderANSI implements reader.BaseSessionReader.
func (r *CodexReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("codex session not found: %s", id)
	}
	return codexToRenderEvents(path)
}

func (r *CodexReader) RenderANSI(id string, cols int) (string, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return "", fmt.Errorf("codex session not found: %s", id)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

// ---- JSONL shapes ----

type codexEvent struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	ParentThreadID   string `json:"parent_thread_id"`
	ForkedFromID     string `json:"forked_from_id"`
	ThreadSource     string `json:"thread_source"`
	AgentPath        string `json:"agent_path"`
	Timestamp        string `json:"timestamp"`
	CWD              string `json:"cwd"`
	ModelProvider    string `json:"model_provider"`
	BaseInstructions struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
}

type codexPayload struct {
	Type    string          `json:"type"`
	TurnID  string          `json:"turn_id"`
	Message string          `json:"message"`
	Role    string          `json:"role"`
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
	// thread_rolled_back
	NumTurns int `json:"num_turns"`
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

var exitCodeRe = regexp.MustCompile(`Process exited with code (\d+)`)

// ---- helpers ----

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
		nativeID     string
		parentID     string
		agentPath    string
		isSubagent   bool
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
				if nativeID == "" {
					// Codex resume resolves the rollout's own payload.id. For
					// subagent rollouts, session_id points at the parent thread and
					// must not replace the child rollout's resumable UUID.
					if m.ID != "" {
						nativeID = m.ID
					} else if m.SessionID != "" {
						nativeID = m.SessionID
					}
				}
				if m.ThreadSource == "subagent" {
					isSubagent = true
					if parentID == "" {
						parentID = m.ParentThreadID
						if parentID == "" {
							parentID = m.ForkedFromID
						}
					}
					if agentPath == "" {
						agentPath = m.AgentPath
					}
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
	previewText := shared.BuildPreviewText(userMessages)

	msgCount := shared.EstimateLineCount(headLines, headBytes, fileSize)

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
		ID:              sessionID,
		AgentType:       "codex",
		CWD:             cwd,
		Project:         shared.ResolveProject(cwd, ""),
		Name:            name,
		ModelName:       modelName,
		ResumeID:        nativeID,
		ParentSessionID: parentID,
		AgentPath:       agentPath,
		IsSubagent:      isSubagent,
		PreviewText:     previewText,
		MessageCount:    msgCount,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}, true
}

func resolveName(firstUserMsg string, createdAt time.Time) string {
	if firstUserMsg != "" {
		return shared.TruncateRunes(firstUserMsg, 50)
	}
	if !createdAt.IsZero() {
		return "Codex " + createdAt.Format("01-02 15:04")
	}
	return "Codex Session"
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

	parsed, modelName := parseCodexEvents(jsonlPath)
	if modelName != "" {
		session.ModelName = modelName
	}
	session.TurnCount = len(parsed.Active)
	session.HistoricalTurnCount = parsed.Historical
	for _, group := range parsed.RollbackGroups {
		session.RolledBackTurnCount += len(group.Turns)
	}

	detail := &model.SessionDetail{Session: session, Turns: parsed.Active, RollbackGroups: parsed.RollbackGroups}

	detail.AnomalySummary = shared.RunAnomalyDetection(parsed.Active)

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

type codexParsedTurns struct {
	Active         []model.TurnVM
	RollbackGroups []model.RollbackGroupVM
	Historical     int
}

type codexTurnAttempt struct {
	turn          model.TurnVM
	originalIndex int
}

type codexRollbackAttempt struct {
	after     *codexTurnAttempt
	timestamp string
	removed   []*codexTurnAttempt
}

func parseCodexEvents(path string) (codexParsedTurns, string) {
	f, err := os.Open(path)
	if err != nil {
		return codexParsedTurns{}, ""
	}
	defer f.Close()

	var (
		attempts    []*codexTurnAttempt
		active      []*codexTurnAttempt
		rollbacks   []codexRollbackAttempt
		foundModel  string
		current     *codexTurnAttempt
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
				current = &codexTurnAttempt{turn: model.TurnVM{
					TurnIndex: len(attempts),
					Events:    []model.EventVM{},
				}}
				attempts = append(attempts, current)
				active = append(active, current)
				turnStartTS = evt.Timestamp

			case "thread_rolled_back":
				n := p.NumTurns
				if n < 0 {
					n = 0
				}
				if n > len(active) {
					n = len(active)
				}
				removed := append([]*codexTurnAttempt(nil), active[len(active)-n:]...)
				active = active[:len(active)-n]
				var after *codexTurnAttempt
				if len(active) > 0 {
					after = active[len(active)-1]
				}
				rollbacks = append(rollbacks, codexRollbackAttempt{after: after, timestamp: evt.Timestamp, removed: removed})
				current = nil
				turnStartTS = ""

			case "user_message":
				if current != nil && p.Message != "" {
					current.turn.UserMessage = p.Message
				}
				// user_message before any task_started: record for session name but don't create turn
				if current == nil {
					continue
				}

			case "agent_message":
				if current != nil && p.Message != "" {
					current.turn.AssistantMessage += p.Message
				}

			case "token_count":
				if current != nil && p.Info != nil {
					current.turn.RequestCount++
					u := &current.turn.TokenUsage
					last := p.Info.LastTokenUsage
					// Codex uses inclusive semantics: cached_input_tokens is a
					// subset of input_tokens, and reasoning_output_tokens a
					// subset of output_tokens. Canonical buckets are mutually
					// exclusive, so subtract cache from input; reasoning stays
					// an annotation and is never added to output.
					fresh := last.InputTokens - last.CachedInputTokens
					if fresh < 0 {
						fresh = 0
					}
					u.PromptTokens += fresh
					u.CompletionTokens += last.OutputTokens
					u.CacheReadTokens += last.CachedInputTokens
					u.ReasoningTokens += last.ReasoningOutputTokens
					u.Present.Input = model.PresenceExact
					u.Present.Output = model.PresenceExact
					u.Present.CacheRead = model.PresenceExact
					u.Present.Reasoning = model.PresenceExact
					// OpenAI prompt caching is automatic and free: the
					// cache_write concept does not exist for this agent.
					u.Present.CacheWrite = model.PresenceNA
				}

			case "patch_apply_end":
				if current != nil {
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "patch_apply_end",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"success": p.Success, "stdout": p.Stdout, "stderr": p.Stderr},
					})
					if !p.Success {
						current.turn.ErrorCount++
					}
				}

			case "task_complete":
				// turn boundary marker — keep turn open but record completion
				if current != nil {
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "task_complete",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{},
					})
				}

			case "turn_aborted":
				if current != nil {
					current.turn.DurationMs = p.DurationMs
				}
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "message":
				if current == nil {
					continue
				}
				switch p.Role {
				case "assistant":
					// Skip: the event_msg/agent_message branch already
					// accumulated this assistant text. Codex logs every
					// assistant message twice (once as agent_message, once as
					// this response_item), so appending here would duplicate it
					// — the same reason codexToRenderEvents skips it.
				case "user":
					// system-context user message, not the actual user prompt
					// skip to avoid overwriting user_message
				}

			case "reasoning":
				// reasoning may be encrypted; skip text extraction

			case "function_call":
				if current != nil {
					current.turn.ToolCallCount++
					if p.Name != "" {
						current.turn.ToolNames = append(current.turn.ToolNames, p.Name)
					}
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "function_call",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"name": p.Name, "call_id": p.CallID},
					})
				}

			case "function_call_output":
				if current != nil {
					exitCode := extractExitCode(p.Output)
					isErr := exitCode != 0
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "function_call_output",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"call_id": p.CallID, "exit_code": exitCode},
					})
					if isErr {
						current.turn.ErrorCount++
					}
				}

			case "custom_tool_call":
				if current != nil {
					current.turn.ToolCallCount++
					name := p.Name
					if name == "" {
						name = p.CustomToolName
					}
					if name != "" {
						current.turn.ToolNames = append(current.turn.ToolNames, name)
					}
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "custom_tool_call",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"name": name, "call_id": p.CallID, "status": p.Status},
					})
				}

			case "custom_tool_call_output":
				if current != nil {
					exitCode := extractExitCode(p.Output)
					isErr := exitCode != 0
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "custom_tool_call_output",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"call_id": p.CallID, "exit_code": exitCode},
					})
					if isErr {
						current.turn.ErrorCount++
					}
				}
			}
		}

		// Track duration from turn start to latest event
		if current != nil && turnStartTS != "" && evt.Timestamp != "" {
			if t1 := parseTimestamp(turnStartTS); !t1.IsZero() {
				if t2 := parseTimestamp(evt.Timestamp); !t2.IsZero() {
					dur := t2.Sub(t1).Milliseconds()
					if dur > current.turn.DurationMs {
						current.turn.DurationMs = dur
					}
				}
			}
		}
	}

	// Rollback counts operate on raw task attempts. Only after replaying them
	// may empty/noise turns be removed; doing this earlier would make an
	// interrupted empty task's rollback delete the preceding real turn.
	visible := make(map[*codexTurnAttempt]bool, len(attempts))
	original := 0
	for _, a := range attempts {
		filtered := shared.FilterEmptyTurns([]model.TurnVM{a.turn})
		if len(filtered) == 0 {
			continue
		}
		a.turn = filtered[0]
		a.originalIndex = original
		original++
		visible[a] = true
	}

	result := codexParsedTurns{Historical: original}
	activeIndex := make(map[*codexTurnAttempt]int)
	for _, a := range active {
		if !visible[a] {
			continue
		}
		a.turn.TurnIndex = len(result.Active)
		a.turn.OriginalTurnIndex = a.originalIndex
		activeIndex[a] = a.turn.TurnIndex
		result.Active = append(result.Active, a.turn)
	}
	for _, rb := range rollbacks {
		group := model.RollbackGroupVM{AfterTurnIndex: -1, Timestamp: rb.timestamp}
		if idx, ok := activeIndex[rb.after]; ok {
			group.AfterTurnIndex = idx
		}
		for _, a := range rb.removed {
			if !visible[a] {
				continue
			}
			t := a.turn
			t.TurnIndex = a.originalIndex
			t.OriginalTurnIndex = a.originalIndex
			t.RolledBack = true
			group.Turns = append(group.Turns, t)
		}
		if len(group.Turns) > 0 {
			result.RollbackGroups = append(result.RollbackGroups, group)
		}
	}

	return result, foundModel
}

// ---- RenderEvent adapter ----

type codexRenderAttempt struct {
	events        []model.RenderEvent
	groups        []*codexRenderRollback
	originalIndex int
}

type codexRenderRollback struct {
	timestamp time.Time
	removed   []*codexRenderAttempt
}

func codexRenderAttemptVisible(a *codexRenderAttempt) bool {
	if a == nil {
		return false
	}
	for _, evt := range a.events {
		switch evt.Type {
		case "TurnBoundary":
		case "UserPrompt":
			if strings.TrimSpace(evt.Text) != "" {
				return true
			}
		case "AgentSpecific":
			if evt.Subtype != "turn_duration" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// codexToRenderEvents parses a Codex JSONL session file into a flat
// []model.RenderEvent stream suitable for render.FormatEvents.
func codexToRenderEvents(path string) ([]model.RenderEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		attempts     []*codexRenderAttempt
		active       []*codexRenderAttempt
		rootGroups   []*codexRenderRollback
		current      *codexRenderAttempt
		fileTag      = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		eventCtr     int
		pendingTools = make(map[string]string) // callID -> ToolInvocation EventID
		completed    = make(map[string]bool)   // patch_apply_end already emitted the result

		// Codex rollouts carry explicit turn brackets: task_started opens a
		// turn, task_complete / turn_aborted closes it. An open bracket at
		// EOF means the CLI is still working (or died mid-turn — the
		// LiveWindow guard in shared.TrailingInProgress bounds that case).
		turnOpen bool
	)

	emit := func(evt model.RenderEvent) string {
		if current == nil {
			return ""
		}
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("evt-%s-%04d", fileTag, eventCtr)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "codex"
		}
		current.events = append(current.events, evt)
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
				turnOpen = true
				current = &codexRenderAttempt{}
				attempts = append(attempts, current)
				active = append(active, current)
				emit(model.RenderEvent{
					Type:      "TurnBoundary",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
				})

			case "task_complete", "turn_aborted":
				turnOpen = false

			case "thread_rolled_back":
				n := p.NumTurns
				if n < 0 {
					n = 0
				}
				if n > len(active) {
					n = len(active)
				}
				if n > 0 {
					group := &codexRenderRollback{
						timestamp: ts,
						removed:   append([]*codexRenderAttempt(nil), active[len(active)-n:]...),
					}
					active = active[:len(active)-n]
					if len(active) == 0 {
						rootGroups = append(rootGroups, group)
					} else {
						target := active[len(active)-1]
						target.groups = append(target.groups, group)
					}
				}
				current = nil
				turnOpen = false

			case "user_message":
				if p.Message != "" {
					emit(model.RenderEvent{
						Type:      "UserPrompt",
						Timestamp: ts,
						TurnIndex: len(attempts) - 1,
						Text:      p.Message,
					})
				}

			case "agent_message":
				if p.Message != "" {
					emit(model.RenderEvent{
						Type:      "TextChunk",
						Timestamp: ts,
						TurnIndex: len(attempts) - 1,
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
					TurnIndex:     len(attempts) - 1,
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
					TurnIndex:  len(attempts) - 1,
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
					TurnIndex:     len(attempts) - 1,
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
					TurnIndex:  len(attempts) - 1,
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
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        p.Output,
					ExitCode:      extractExitCode(p.Output),
					ParentEventID: parentEventID,
				})
			}
		}
	}

	// Trailing "推理中…" row for a turn still bracket-open at EOF. Runs
	// only when the trailing turn already has visible content: a bare
	// task_started must not be resurrected by the progress marker.
	if turnOpen && current != nil && codexRenderAttemptVisible(current) {
		if fi, statErr := f.Stat(); statErr == nil {
			if evt, ok := shared.TrailingInProgress(true, fi.ModTime(), len(attempts)-1); ok {
				emit(evt)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	visible := make(map[*codexRenderAttempt]bool, len(attempts))
	original := 0
	for _, attempt := range attempts {
		if !codexRenderAttemptVisible(attempt) {
			continue
		}
		attempt.originalIndex = original
		original++
		visible[attempt] = true
	}
	activeIndex := make(map[*codexRenderAttempt]int, len(active))
	for _, attempt := range active {
		if visible[attempt] {
			activeIndex[attempt] = len(activeIndex)
		}
	}

	var events []model.RenderEvent
	var appendAttempt func(*codexRenderAttempt, bool)
	appendGroup := func(group *codexRenderRollback, target *codexRenderAttempt, rolledBackTarget bool) {}
	appendAttempt = func(attempt *codexRenderAttempt, rolledBack bool) {
		if !visible[attempt] {
			return
		}
		idx := activeIndex[attempt]
		if rolledBack {
			idx = -(attempt.originalIndex + 1)
		}
		for i, evt := range attempt.events {
			evt.TurnIndex = idx
			if i == 0 && evt.Type == "TurnBoundary" {
				if evt.Metadata == nil {
					evt.Metadata = map[string]any{}
				}
				evt.Metadata["original_turn_index"] = attempt.originalIndex
				if rolledBack {
					evt.Metadata["rolled_back"] = true
				}
			}
			events = append(events, evt)
		}
		for _, group := range attempt.groups {
			appendGroup(group, attempt, rolledBack)
		}
	}
	appendGroup = func(group *codexRenderRollback, target *codexRenderAttempt, rolledBackTarget bool) {
		visibleRemoved := make([]*codexRenderAttempt, 0, len(group.removed))
		for _, attempt := range group.removed {
			if visible[attempt] {
				visibleRemoved = append(visibleRemoved, attempt)
			}
		}
		if len(visibleRemoved) == 0 {
			return
		}
		targetIndex := -1
		resumeTurn := 0
		if target != nil {
			resumeTurn = target.originalIndex + 1
			if rolledBackTarget {
				targetIndex = -(target.originalIndex + 1)
			} else {
				targetIndex = activeIndex[target]
			}
		}
		meta := map[string]any{
			"count":       len(visibleRemoved),
			"resume_turn": resumeTurn,
		}
		events = append(events, model.RenderEvent{
			Type:      "RollbackStart",
			Timestamp: group.timestamp,
			TurnIndex: targetIndex,
			AgentType: "codex",
			Metadata:  meta,
		})
		for _, attempt := range visibleRemoved {
			appendAttempt(attempt, true)
		}
		events = append(events, model.RenderEvent{
			Type:      "RollbackEnd",
			Timestamp: group.timestamp,
			TurnIndex: targetIndex,
			AgentType: "codex",
			Metadata:  meta,
		})
	}

	for _, group := range rootGroups {
		appendGroup(group, nil, false)
	}
	for _, attempt := range active {
		appendAttempt(attempt, false)
	}

	return events, nil
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

// LiveRevision is a stat-only change marker for live-tail polling.
func (r *CodexReader) LiveRevision(id string) (int64, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return 0, fmt.Errorf("codex session not found: %s", id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano() + info.Size(), nil
}
