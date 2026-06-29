package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"session-insight/internal/model"
	"session-insight/internal/reader/shared"
)

func setupTestDB(t *testing.T) (*OpenCodeReader, *sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	for _, stmt := range []string{
		`CREATE TABLE session (id text PRIMARY KEY, directory text NOT NULL DEFAULT '', title text NOT NULL DEFAULT '', time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, time_archived integer, model text, agent text)`,
		`CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, data text NOT NULL)`,
		`CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, data text NOT NULL)`,
		`CREATE TABLE todo (session_id text NOT NULL, content text NOT NULL DEFAULT '', status text NOT NULL DEFAULT '', priority text NOT NULL DEFAULT '', position integer NOT NULL DEFAULT 0, time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0, PRIMARY KEY(session_id, position))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}

	reader, err := New(dbPath)
	if err != nil {
		db.Close()
		t.Fatalf("New reader: %v", err)
	}

	return reader, db, func() { db.Close(); reader.db.Close() }
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, query)
	}
}

func seedSession(t *testing.T, db *sql.DB, id string, directory string, title string, modelID string) {
	t.Helper()
	now := time.Now().UnixMilli()
	modelJSON, _ := json.Marshal(map[string]string{"id": modelID, "providerID": "test"})
	mustExec(t, db,
		`INSERT INTO session (id, directory, title, time_created, time_updated, model) VALUES (?, ?, ?, ?, ?, ?)`,
		id, directory, title, now, now, string(modelJSON))
}

func seedMessage(t *testing.T, db *sql.DB, id string, sessionID string, created int64, dataJSON string) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		id, sessionID, created, created, dataJSON)
}

func seedPart(t *testing.T, db *sql.DB, id string, messageID string, sessionID string, dataJSON string) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, 0, 0, ?)`,
		id, messageID, sessionID, dataJSON)
}

// ---- Tests ----

func TestListSessions(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_test001", "/home/test/proj", "Test session", "claude-sonnet")

	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s.ID != "ses_test001" {
		t.Errorf("expected id ses_test001, got %s", s.ID)
	}
	if s.AgentType != "opencode" {
		t.Errorf("expected agent_type opencode, got %s", s.AgentType)
	}
	if s.CWD != "/home/test/proj" {
		t.Errorf("expected cwd /home/test/proj, got %s", s.CWD)
	}
	if s.ModelName != "claude-sonnet" {
		t.Errorf("expected model claude-sonnet, got %s", s.ModelName)
	}
	// Liveness is no longer derived in the reader (it's a serve-time window on
	// UpdatedAt, see model.IsSessionLive), so the reader does not set IsLive.
}

func TestListSessionsPreviewTextIncludesMessageWithGeneratedSummary(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_test002", "/test", "Test", "model")

	now := time.Now().UnixMilli()

	userData := `{"role":"user","summary":{"title":"Generated title","diffs":[]},"agent":"build"}`
	seedMessage(t, db, "msg_user", "ses_test002", now, userData)
	seedPart(t, db, "prt_user_text", "msg_user", "ses_test002", `{"type":"text","text":"Fix the login bug"}`)

	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	if sessions[0].PreviewText != "Fix the login bug" {
		t.Errorf("expected PreviewText='Fix the login bug', got '%s'", sessions[0].PreviewText)
	}
	if sessions[0].TurnCount != 1 {
		t.Errorf("expected TurnCount=1, got %d", sessions[0].TurnCount)
	}
}

func TestGetSessionTurnParsing(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_test003", "/test", "Test", "claude-sonnet-4")

	now := time.Now().UnixMilli()

	seedMessage(t, db, "msg_u1", "ses_test003", now, `{"role":"user","agent":"build"}`)
	seedPart(t, db, "prt_u1_t", "msg_u1", "ses_test003", `{"type":"text","text":"Hello"}`)

	asstData := fmt.Sprintf(`{"role":"assistant","parentID":"msg_u1","modelID":"claude-sonnet-4","providerID":"anthropic","agent":"build","tokens":{"input":100,"output":50,"reasoning":7,"cache":{"read":11,"write":3}},"time":{"created":%d,"completed":%d}}`, now, now+5000)
	seedMessage(t, db, "msg_a1", "ses_test003", now+1, asstData)
	seedPart(t, db, "prt_a1_t", "msg_a1", "ses_test003", `{"type":"text","text":"Hi there!"}`)
	seedPart(t, db, "prt_a1_r", "msg_a1", "ses_test003", `{"type":"reasoning","text":"thinking..."}`)
	seedPart(t, db, "prt_a1_tool1", "msg_a1", "ses_test003", fmt.Sprintf(`{"type":"tool","callID":"c1","tool":"Bash","state":{"status":"completed","input":{},"output":"ok","title":"ls","time":{"start":%d,"end":%d}}}`, now, now+3000))
	seedPart(t, db, "prt_a1_tool2", "msg_a1", "ses_test003", fmt.Sprintf(`{"type":"tool","callID":"c2","tool":"Read","state":{"status":"error","error":"not found","time":{"start":%d,"end":%d}}}`, now, now+1000))

	detail, err := reader.GetSession("ses_test003")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if len(detail.Turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(detail.Turns))
	}

	turn := detail.Turns[0]
	if turn.UserMessage != "Hello" {
		t.Errorf("expected UserMessage='Hello', got '%s'", turn.UserMessage)
	}
	if !strings.Contains(turn.AssistantMessage, "Hi there!") {
		t.Errorf("AssistantMessage missing 'Hi there!'")
	}
	if !strings.Contains(turn.AssistantMessage, "[思考]") {
		t.Errorf("AssistantMessage missing [思考]")
	}
	if !strings.Contains(turn.AssistantMessage, "thinking...") {
		t.Errorf("AssistantMessage missing reasoning")
	}
	if turn.ToolCallCount != 2 {
		t.Errorf("expected 2 tools, got %d", turn.ToolCallCount)
	}
	if turn.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", turn.ErrorCount)
	}
	if turn.TokenUsage.PromptTokens != 100 || turn.TokenUsage.CompletionTokens != 57 {
		t.Errorf("token usage mismatch: prompt=%d completion=%d", turn.TokenUsage.PromptTokens, turn.TokenUsage.CompletionTokens)
	}
	if turn.TokenUsage.CacheReadTokens != 11 || turn.TokenUsage.CacheWriteTokens != 3 {
		t.Errorf("cache token usage mismatch: read=%d write=%d", turn.TokenUsage.CacheReadTokens, turn.TokenUsage.CacheWriteTokens)
	}
	if turn.DurationMs != 5000 {
		t.Errorf("expected duration=5000ms, got %d", turn.DurationMs)
	}
}

func TestGetSessionKeepsUserTextWhenMessageHasGeneratedSummary(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_summary", "/test", "Test", "model")
	now := time.Now().UnixMilli()
	seedMessage(t, db, "msg_user", "ses_summary", now,
		`{"role":"user","summary":{"title":"Generated title","diffs":[]},"agent":"build"}`)
	seedPart(t, db, "prt_user", "msg_user", "ses_summary", `{"type":"text","text":"Keep this prompt"}`)
	seedMessage(t, db, "msg_assistant", "ses_summary", now+1,
		fmt.Sprintf(`{"role":"assistant","parentID":"msg_user","time":{"created":%d,"completed":%d}}`, now+1, now+2))
	seedPart(t, db, "prt_assistant", "msg_assistant", "ses_summary", `{"type":"text","text":"Done"}`)

	detail, err := reader.GetSession("ses_summary")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(detail.Turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(detail.Turns))
	}
	if detail.Turns[0].UserMessage != "Keep this prompt" {
		t.Errorf("expected summarized message text to be retained, got %q", detail.Turns[0].UserMessage)
	}
}

func TestGetSessionUsesFullTurnDurationAcrossAssistantSteps(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_duration", "/test", "Test", "model")
	seedMessage(t, db, "msg_user", "ses_duration", 1000, `{"role":"user","agent":"build"}`)
	seedPart(t, db, "prt_user", "msg_user", "ses_duration", `{"type":"text","text":"Run it"}`)
	seedMessage(t, db, "msg_a1", "ses_duration", 1100,
		`{"role":"assistant","parentID":"msg_user","time":{"created":1100,"completed":2100}}`)
	seedMessage(t, db, "msg_a2", "ses_duration", 2200,
		`{"role":"assistant","parentID":"msg_user","time":{"created":2200,"completed":5200}}`)

	detail, err := reader.GetSession("ses_duration")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got := detail.Turns[0].DurationMs; got != 4100 {
		t.Errorf("expected full turn duration 4100ms, got %d", got)
	}
}

func TestSingleTurnToolFailureIsReportedAsAnomaly(t *testing.T) {
	turns := []model.TurnVM{{ErrorCount: 1, DurationMs: 1000}}

	summary := shared.RunAnomalyDetection(turns)

	if summary.ToolFailures != 1 || summary.TotalAnomalies != 1 {
		t.Fatalf("expected one tool failure anomaly, got %+v", summary)
	}
	if len(turns[0].Anomalies) != 1 || turns[0].Anomalies[0] != "tool_failure" {
		t.Fatalf("expected turn tool_failure anomaly, got %+v", turns[0].Anomalies)
	}
}

func TestTodoMapping(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	mustExec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated) VALUES (?, ?, ?, 0, 0)`,
		"ses_todo", "/test", "Todo test")
	mustExec(t, db, `INSERT INTO todo (session_id, content, status, priority, position, time_created, time_updated) VALUES (?, ?, ?, ?, ?, 0, 0)`,
		"ses_todo", "Fix the bug", "done", "high", 0)
	mustExec(t, db, `INSERT INTO todo (session_id, content, status, priority, position, time_created, time_updated) VALUES (?, ?, ?, ?, ?, 0, 0)`,
		"ses_todo", "Write tests", "in_progress", "medium", 1)

	todos := reader.readTodos("ses_todo")
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(todos))
	}
	if todos[0].ID != "pos-0" || todos[0].Title != "Fix the bug" || todos[0].Status != "done" {
		t.Errorf("todo[0] mismatch: %+v", todos[0])
	}
	if todos[1].ID != "pos-1" || todos[1].Title != "Write tests" || todos[1].Status != "in_progress" {
		t.Errorf("todo[1] mismatch: %+v", todos[1])
	}
}

func TestResolveDBPath(t *testing.T) {
	path, ok := ResolveDBPath()
	homeDir, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		xdg = filepath.Join(homeDir, ".local", "share")
	}
	defaultPath := filepath.Join(xdg, "opencode", "opencode.db")

	if ok && path != defaultPath {
		t.Errorf("expected default path %s, got %s", defaultPath, path)
	}
}

func TestRenderANSIUnknownSession(t *testing.T) {
	reader, _, cleanup := setupTestDB(t)
	defer cleanup()

	// Unknown session: no rows in DB → empty ANSI output, no error.
	out, err := reader.RenderANSI("unknown-session", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for unknown session, got %q", out)
	}
}

func TestGetRenderEventsNormalizesEditInput(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_edit", "/test", "Edit test", "model")
	seedMessage(t, db, "msg_user", "ses_edit", 1000, `{"role":"user","agent":"build"}`)
	seedPart(t, db, "prt_user", "msg_user", "ses_edit", `{"type":"text","text":"Edit it"}`)
	seedMessage(t, db, "msg_assistant", "ses_edit", 1100,
		`{"role":"assistant","parentID":"msg_user","time":{"created":1100,"completed":1200}}`)
	seedPart(t, db, "prt_edit", "msg_assistant", "ses_edit",
		`{"type":"tool","callID":"call-1","tool":"edit","state":{"status":"completed","input":{"filePath":"main.go","oldString":"old","newString":"new"},"output":"ok"}}`)

	events, err := reader.GetRenderEvents("ses_edit")
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}

	for _, event := range events {
		if event.Type != "ToolInvocation" {
			continue
		}
		if event.ToolName != "edit" {
			t.Fatalf("ToolName: got %q", event.ToolName)
		}
		if event.ToolInput["file_path"] != "main.go" {
			t.Fatalf("file_path: got %#v", event.ToolInput["file_path"])
		}
		if event.ToolInput["old_string"] != "old" || event.ToolInput["new_string"] != "new" {
			t.Fatalf("edit strings were not normalized: %#v", event.ToolInput)
		}
		return
	}
	t.Fatal("edit ToolInvocation not found")
}

func TestAgentTypeAndDisplayName(t *testing.T) {
	reader, _, cleanup := setupTestDB(t)
	defer cleanup()

	if reader.AgentType() != "opencode" {
		t.Errorf("AgentType: expected 'opencode', got '%s'", reader.AgentType())
	}
	if reader.DisplayName() != "OpenCode" {
		t.Errorf("DisplayName: expected 'OpenCode', got '%s'", reader.DisplayName())
	}
}
