package copilot

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

type CopilotReader struct {
	sessionDir string
}

type workspaceYAML struct {
	ID         string `yaml:"id"`
	CWD        string `yaml:"cwd"`
	Repository string `yaml:"repository"`
	Branch     string `yaml:"branch"`
	Name       string `yaml:"name"`
	UserNamed  bool   `yaml:"user_named"`
	CreatedAt  string `yaml:"created_at"`
	UpdatedAt  string `yaml:"updated_at"`
}

func New(sessionDir string) *CopilotReader {
	return &CopilotReader{sessionDir: sessionDir}
}

func (r *CopilotReader) AgentType() string  { return "copilot" }
func (r *CopilotReader) DisplayName() string { return "Copilot" }

func validSessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

// RenderANSI implements reader.BaseSessionReader.
func (r *CopilotReader) copilotEvents(id string) ([]model.RenderEvent, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid copilot session id: %q", id)
	}
	eventsPath := filepath.Join(r.sessionDir, id, "events.jsonl")
	if _, err := os.Stat(eventsPath); err != nil {
		return nil, fmt.Errorf("copilot session not found %q: %w", id, err)
	}
	return parseCopilotRenderEvents(eventsPath)
}

func (r *CopilotReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return r.copilotEvents(id)
}

func (r *CopilotReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.copilotEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

func scanPreviewText(eventsPath string) string {
	f, err := os.Open(eventsPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	var messages []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var evt jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		if evt.Type == "user.message" {
			if content, ok := extractString(evt.Data, "content"); ok && content != "" {
				messages = append(messages, shared.TruncateRunes(content, 200))
				if len(messages) >= 5 {
					break
				}
			}
		}
	}
	joined := strings.Join(messages, " | ")
	if len(joined) > 1500 {
		return joined[:1500] + "..."
	}
	return joined
}

func (r *CopilotReader) ListSessions() ([]model.Session, error) {
	entries, err := os.ReadDir(r.sessionDir)
	if err != nil {
		return nil, err
	}

	var sessions []model.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Only list sessions that have events.jsonl
		eventsPath := filepath.Join(r.sessionDir, entry.Name(), "events.jsonl")
		if _, err := os.Stat(eventsPath); err != nil {
			continue
		}

		wsPath := filepath.Join(r.sessionDir, entry.Name(), "workspace.yaml")
		data, err := os.ReadFile(wsPath)
		if err != nil {
			continue
		}

		var ws workspaceYAML
		if err := yaml.Unmarshal(data, &ws); err != nil {
			continue
		}

			session := toSession(ws)
			session.PreviewText = scanPreviewText(eventsPath)
			// Quick line count for message_count
			if f, err := os.Open(eventsPath); err == nil {
				var newlines int
				buf := make([]byte, 32*1024)
				for {
					n, readErr := f.Read(buf)
					for _, b := range buf[:n] {
						if b == '\n' {
							newlines++
						}
					}
					if readErr != nil {
						break
					}
				}
				f.Close()
				session.MessageCount = newlines
				// Use events.jsonl mtime for UpdatedAt (revision source, and the
				// activity timestamp the server-side liveness check reads).
				// workspace.yaml UpdatedAt is unreliable for detecting content
				// changes because events.jsonl is continuously appended to.
				// Liveness itself is computed uniformly at serve time
				// (model.IsSessionLive), not here.
				if info, err := os.Stat(eventsPath); err == nil {
					if info.ModTime().After(session.UpdatedAt) {
						session.UpdatedAt = info.ModTime()
					}
				}
			}
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func toSession(ws workspaceYAML) model.Session {
	name := ""
	if ws.UserNamed && ws.Name != "" {
		name = ws.Name
	}

	createdAt, _ := time.Parse(time.RFC3339, ws.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, ws.UpdatedAt)

	return model.Session{
		ID:         ws.ID,
		AgentType:  "copilot",
		CWD:        ws.CWD,
		Repository: ws.Repository,
		Branch:     ws.Branch,
		Project:    shared.ResolveProject(ws.CWD, ws.Repository),
		Name:       name,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
}

type jsonlEvent struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

func (r *CopilotReader) GetSession(id string) (*model.SessionDetail, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid copilot session id: %q", id)
	}
	wsPath := filepath.Join(r.sessionDir, id, "workspace.yaml")
	data, err := os.ReadFile(wsPath)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	var ws workspaceYAML
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("invalid workspace.yaml: %w", err)
	}

	session := toSession(ws)

	eventsPath := filepath.Join(r.sessionDir, id, "events.jsonl")
	turns, modelName, err := parseEventsJSONL(eventsPath)
	if err != nil {
		return &model.SessionDetail{Session: session, Turns: []model.TurnVM{}}, nil
	}

	if modelName != "" {
		session.ModelName = modelName
	}
	session.TurnCount = len(turns)

	msgCount := 0
	for _, t := range turns {
		msgCount += len(t.Events)
	}
	session.MessageCount = msgCount

	detail := &model.SessionDetail{Session: session, Turns: turns}
	detail.Todos = readTodos(r.sessionDir, id)

	// Anomaly detection
	detail.AnomalySummary = shared.RunAnomalyDetection(turns)

	// MissingShutdown check (copilot-specific: session.shutdown event).
	// The same event carries the session bill (tokenDetails / modelMetrics /
	// totalNanoAiu), so capture it for billing in the same scan.
	var shutdownData map[string]any
	for _, t := range turns {
		for _, e := range t.Events {
			if e.Type == "session.shutdown" {
				shutdownData = e.Data
			}
		}
	}
	if shutdownData != nil {
		detail.Billing = parseShutdownBilling(shutdownData)
	} else if len(turns) > 0 {
		detail.AnomalySummary.MissingShutdown = true
		detail.AnomalySummary.TotalAnomalies++
		detail.Billing = &model.SessionBilling{Precision: model.PrecisionMissing}
	}

	return detail, nil
}

// parseShutdownBilling converts a session.shutdown payload into the canonical
// bill. Copilot ships two competing input semantics in the same event:
// usage.inputTokens INCLUDES cache reads, while tokenDetails.input excludes
// them. Only tokenDetails matches the canonical mutually-exclusive buckets,
// so buckets are read from tokenDetails exclusively; usage.* is consulted
// only for fields tokenDetails lacks (cacheWriteTokens, reasoningTokens).
func parseShutdownBilling(data map[string]any) *model.SessionBilling {
	b := &model.SessionBilling{
		Precision:   model.PrecisionExact,
		BillingUnit: "aiu",
		// Copilot bills in nano-AIU (1e-9 AIU).
		BillingAmount: extractFloat(data, "totalNanoAiu") / 1e9,
	}
	b.Totals.PremiumRequests = int(extractFloat(data, "totalPremiumRequests"))
	fillBucketsFromTokenDetails(&b.Totals, nestedMap(data, "tokenDetails"))

	for name, v := range nestedMap(data, "modelMetrics") {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		mu := model.ModelUsage{
			Model:         name,
			BillingAmount: extractFloat(m, "totalNanoAiu") / 1e9,
		}
		if req := nestedMap(m, "requests"); req != nil {
			mu.Requests = int(extractFloat(req, "count"))
		}
		fillBucketsFromTokenDetails(&mu.Usage, nestedMap(m, "tokenDetails"))
		if usage := nestedMap(m, "usage"); usage != nil {
			if mu.Usage.Present.CacheWrite == model.PresenceMissing {
				if v, ok := usage["cacheWriteTokens"].(float64); ok {
					mu.Usage.CacheWriteTokens = int64(v)
					mu.Usage.Present.CacheWrite = model.PresenceExact
				}
			}
			if v, ok := usage["reasoningTokens"].(float64); ok {
				mu.Usage.ReasoningTokens = int64(v)
				mu.Usage.Present.Reasoning = model.PresenceExact
			}
		}
		b.ByModel = append(b.ByModel, mu)
	}
	sort.Slice(b.ByModel, func(i, j int) bool {
		return b.ByModel[i].BillingAmount > b.ByModel[j].BillingAmount
	})

	// Session-level tokenDetails has no reasoning bucket; roll it up from the
	// per-model metrics when any model reported it.
	for _, mu := range b.ByModel {
		if mu.Usage.Present.Reasoning == model.PresenceExact {
			b.Totals.ReasoningTokens += mu.Usage.ReasoningTokens
			b.Totals.Present.Reasoning = model.PresenceExact
		}
	}
	return b
}

// fillBucketsFromTokenDetails reads Copilot's exclusive-semantics token
// buckets ({bucket: {tokenCount: N}}). Presence is set only for keys that
// actually exist — absent keys stay PresenceMissing rather than becoming 0.
func fillBucketsFromTokenDetails(u *model.TokenUsage, details map[string]any) {
	set := func(bucket string, dst *int64, p *model.Presence) {
		entry := nestedMap(details, bucket)
		if entry == nil {
			return
		}
		if v, ok := entry["tokenCount"].(float64); ok {
			*dst = int64(v)
			*p = model.PresenceExact
		}
	}
	set("input", &u.PromptTokens, &u.Present.Input)
	set("output", &u.CompletionTokens, &u.Present.Output)
	set("cache_read", &u.CacheReadTokens, &u.Present.CacheRead)
	set("cache_write", &u.CacheWriteTokens, &u.Present.CacheWrite)
}

func nestedMap(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	if m, ok := data[key].(map[string]any); ok {
		return m
	}
	return nil
}

func parseEventsJSONL(path string) ([]model.TurnVM, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	var turns []model.TurnVM
	var foundModel string
	var toolStarts = make(map[string]string) // toolCallId -> toolName
	var currentTurn *model.TurnVM
	var turnStartTimestamp string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		switch {
		case evt.Type == "user.message":
			if currentTurn != nil {
				turns = append(turns, *currentTurn)
			}
			currentTurn = &model.TurnVM{
				TurnIndex: len(turns),
				Events:    []model.EventVM{},
			}
			turnStartTimestamp = evt.Timestamp
			if content, ok := extractString(evt.Data, "content"); ok {
				currentTurn.UserMessage = content
			}

		case evt.Type == "assistant.message":
			if currentTurn == nil {
				continue
			}
			content, hasContent := extractString(evt.Data, "content")
			if !hasContent {
				content, hasContent = extractString(evt.Data, "encryptedContent")
			}
			if hasContent {
				currentTurn.AssistantMessage += content
			}
			// Copilot only reports output at message level; input/cache live
			// solely in the session.shutdown aggregate, so those buckets stay
			// PresenceMissing here and analytics takes the estimation path.
			if v, ok := evt.Data["outputTokens"]; ok {
				if f, ok := v.(float64); ok {
					currentTurn.TokenUsage.CompletionTokens += int64(f)
					currentTurn.TokenUsage.Present.Output = model.PresenceExact
				}
			}
			// Each assistant.message is one API response.
			currentTurn.RequestCount++

		case evt.Type == "skill.invoked":
			if currentTurn != nil {
				if name, ok := extractString(evt.Data, "name"); ok && name != "" {
					currentTurn.Skills = append(currentTurn.Skills, name)
				}
			}
		case evt.Type == "subagent.started":
			if currentTurn != nil {
				if name, ok := extractString(evt.Data, "agentDisplayName"); ok && name != "" {
					currentTurn.Subagents = append(currentTurn.Subagents, name)
				}
			}
		case evt.Type == "tool.execution_start":
			if currentTurn == nil {
				continue
			}
			if name, ok := extractString(evt.Data, "toolName"); ok && name != "" {
				currentTurn.ToolNames = append(currentTurn.ToolNames, name)
				if callId, ok := extractString(evt.Data, "toolCallId"); ok && callId != "" {
					toolStarts[callId] = name
				}
			}

		case evt.Type == "tool.execution_complete":
			if currentTurn == nil {
				continue
			}
			currentTurn.ToolCallCount++
			if code := extractFloat(evt.Data, "exit_code"); code != 0 {
				currentTurn.ErrorCount++
			if callId, ok := extractString(evt.Data, "toolCallId"); ok && callId != "" {
				name := toolStarts[callId]
				if name == "" {
					name = "unknown"
				}
				dur := extractFloat(evt.Data, "durationMs")
				currentTurn.ToolDetails = append(currentTurn.ToolDetails, model.ToolCallVM{
					Name:     name,
					ExitCode: int(extractFloat(evt.Data, "exitCode")),
					Duration: int64(dur),
				})
			}
			}

		case evt.Type == "session.model_change":
			if foundModel == "" {
				if name, ok := extractString(evt.Data, "newModel"); ok && name != "" {
					foundModel = name
				}
			}
		}

		if currentTurn != nil {
			currentTurn.Events = append(currentTurn.Events, model.EventVM{
				Type:      evt.Type,
				Timestamp: evt.Timestamp,
				Data:      evt.Data,
			})
		}

		if currentTurn != nil && turnStartTimestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, turnStartTimestamp); err == nil {
				if t2, err2 := time.Parse(time.RFC3339Nano, evt.Timestamp); err2 == nil {
					currentTurn.DurationMs = t2.Sub(t).Milliseconds()
				}
			}
		}
	}

	if currentTurn != nil {
		turns = append(turns, *currentTurn)
	}

	return turns, foundModel, scanner.Err()
}

func extractString(data map[string]any, key string) (string, bool) {
	if v, ok := data[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

func extractInt64(data map[string]any, key string) int64 {
	if v, ok := data[key]; ok {
		if f, ok := v.(float64); ok {
			return int64(f)
		}
	}
	return 0
}

func extractFloat(data map[string]any, key string) float64 {
	if v, ok := data[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func readTodos(sessionDir, sessionID string) []model.Todo {
	dbPath := filepath.Join(sessionDir, sessionID, "session.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, title, description, status FROM todos ORDER BY created_at")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var todos []model.Todo
	for rows.Next() {
		var t model.Todo
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status); err != nil {
			continue
		}
		todos = append(todos, t)
	}

	// Read deps
	depRows, err := db.Query("SELECT todo_id, depends_on FROM todo_deps")
	if err == nil {
		defer depRows.Close()
		depMap := make(map[string][]string)
		for depRows.Next() {
			var todoID, dep string
			if err := depRows.Scan(&todoID, &dep); err == nil {
				depMap[todoID] = append(depMap[todoID], dep)
			}
		}
		for i := range todos {
			todos[i].Deps = depMap[todos[i].ID]
		}
	}

	return todos
}

// ---- RenderEvent adapter ----

// parseCopilotRenderEvents parses a Copilot events.jsonl file into a flat
// []model.RenderEvent stream suitable for render.FormatEvents.
func parseCopilotRenderEvents(path string) ([]model.RenderEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		events       []model.RenderEvent
		eventCtr     int
		turnIndex    int
		pendingTools = make(map[string]string) // toolCallId -> ToolInvocation EventID

		// Copilot events carry the most explicit turn brackets of any agent:
		// assistant.turn_start / assistant.turn_end pairs, plus a
		// session.shutdown on any orderly exit. An open bracket at EOF means
		// the CLI is still working (or was killed mid-turn — the LiveWindow
		// guard in shared.TrailingInProgress bounds that case).
		turnOpen bool
	)

	currentTurnIndex := func() int {
		if turnIndex == 0 {
			return 0
		}
		return turnIndex - 1
	}

	emit := func(evt model.RenderEvent) string {
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("cop-%04d-%s", eventCtr, evt.Type)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "copilot"
		}
		events = append(events, evt)
		return evt.EventID
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var jev jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &jev); err != nil {
			continue
		}

		ts := parseCopilotTimestamp(jev.Timestamp)

		switch jev.Type {
		case "assistant.turn_start":
			turnOpen = true

		case "assistant.turn_end", "session.shutdown", "session.error":
			turnOpen = false

		case "user.message":
			content, _ := extractString(jev.Data, "content")
			if strings.TrimSpace(content) == "" {
				continue
			}
			turnIndex++
			emit(model.RenderEvent{
				EventID:   fmt.Sprintf("cop-%04d-boundary", eventCtr),
				Type:      "TurnBoundary",
				Timestamp: ts,
				TurnIndex: turnIndex - 1,
			})
			eventCtr++

			emit(model.RenderEvent{
				Type:      "UserPrompt",
				Timestamp: ts,
				TurnIndex: turnIndex - 1,
				Text:      content,
			})

		case "assistant.message":
			// encryptedContent is opaque ciphertext, not displayable text.
			content, _ := extractString(jev.Data, "content")
			if content != "" {
				emit(model.RenderEvent{
					Type:      "TextChunk",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      content,
				})
			}

		case "tool.execution_start":
			toolName, _ := extractString(jev.Data, "toolName")
			toolCallID, _ := extractString(jev.Data, "toolCallId")

			var toolInput map[string]any
			if raw, ok := jev.Data["arguments"]; ok {
				if m, ok := raw.(map[string]any); ok {
					toolInput = m
				}
			} else if raw, ok := jev.Data["parameters"]; ok {
				// Retain compatibility with older event producers.
				toolInput, _ = raw.(map[string]any)
			}

			invID := emit(model.RenderEvent{
				Type:       "ToolInvocation",
				Timestamp:  ts,
				TurnIndex:  currentTurnIndex(),
				ToolName:   toolName,
				ToolCallID: toolCallID,
				ToolInput:  toolInput,
			})
			if toolCallID != "" {
				pendingTools[toolCallID] = invID
			}

		case "tool.execution_complete":
			toolCallID, _ := extractString(jev.Data, "toolCallId")
			exitCode, stdout, stderr, durationMs := copilotToolResult(jev.Data)

			parentEventID := ""
			if toolCallID != "" {
				parentEventID = pendingTools[toolCallID]
				delete(pendingTools, toolCallID)
			}

			emit(model.RenderEvent{
				Type:          "ToolResult",
				Timestamp:     ts,
				TurnIndex:     currentTurnIndex(),
				ToolCallID:    toolCallID,
				Stdout:        stdout,
				Stderr:        stderr,
				ExitCode:      exitCode,
				DurationMs:    durationMs,
				ParentEventID: parentEventID,
			})

		case "skill.invoked":
			name, _ := extractString(jev.Data, "skill_name")
			if name == "" {
				name, _ = extractString(jev.Data, "name")
			}
			if name != "" {
				emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "skill_invoked",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      name,
				})
			}

		case "subagent.started":
			name, _ := extractString(jev.Data, "agentDisplayName")
			if name == "" {
				name, _ = extractString(jev.Data, "subagent_id")
			}
			if name != "" {
				emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "subagent_started",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      name,
				})
			}

		case "session.model_change":
			if newModel, ok := extractString(jev.Data, "newModel"); ok && newModel != "" {
				emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "model_change",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      newModel,
				})
			}
		}
	}

	// Trailing "推理中…" row for a turn still bracket-open at EOF.
	if turnOpen && turnIndex > 0 {
		if fi, statErr := f.Stat(); statErr == nil {
			if evt, ok := shared.TrailingInProgress(true, fi.ModTime(), turnIndex-1); ok {
				emit(evt)
			}
		}
	}

	return events, scanner.Err()
}

func copilotToolResult(data map[string]any) (exitCode int, stdout, stderr string, durationMs int64) {
	exitCode = int(extractFloat(data, "exit_code"))
	if exitCode == 0 {
		exitCode = int(extractFloat(data, "exitCode"))
	}
	stdout, _ = extractString(data, "stdout")
	stderr, _ = extractString(data, "stderr")
	durationMs = int64(extractFloat(data, "duration_ms"))
	if durationMs == 0 {
		durationMs = int64(extractFloat(data, "durationMs"))
	}

	if result, ok := data["result"].(map[string]any); ok {
		if stdout == "" {
			stdout, _ = extractString(result, "content")
		}
		if stdout == "" {
			stdout, _ = extractString(result, "detailedContent")
		}
	}
	if failure, ok := data["error"].(map[string]any); ok && stderr == "" {
		stderr, _ = extractString(failure, "message")
	}
	if success, ok := data["success"].(bool); ok && !success && exitCode == 0 {
		exitCode = 1
	}
	return exitCode, stdout, stderr, durationMs
}

// parseCopilotTimestamp tries RFC3339Nano first, falls back to RFC3339.
func parseCopilotTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	return time.Time{}
}

// LiveRevision is a stat-only change marker for live-tail polling.
func (r *CopilotReader) LiveRevision(id string) (int64, error) {
	if !validSessionID(id) {
		return 0, fmt.Errorf("invalid copilot session id: %q", id)
	}
	info, err := os.Stat(filepath.Join(r.sessionDir, id, "events.jsonl"))
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano() + info.Size(), nil
}
