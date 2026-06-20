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
	if !s.IsLive {
		t.Error("expected session to be live (time_archived IS NULL)")
	}
}

func TestListSessionsPreviewTextSkipsSummary(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_test002", "/test", "Test", "model")

	now := time.Now().UnixMilli()

	summaryData := `{"role":"user","summary":{"title":"Greeting","diffs":[]},"agent":"build"}`
	seedMessage(t, db, "msg_sum", "ses_test002", now, summaryData)
	seedPart(t, db, "prt_sum_text", "msg_sum", "ses_test002", `{"type":"text","text":"summary text"}`)

	userData := `{"role":"user","agent":"build"}`
	seedMessage(t, db, "msg_real", "ses_test002", now+1, userData)
	seedPart(t, db, "prt_real_text", "msg_real", "ses_test002", `{"type":"text","text":"Fix the login bug"}`)

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
}

func TestGetSessionTurnParsing(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "ses_test003", "/test", "Test", "claude-sonnet-4")

	now := time.Now().UnixMilli()

	seedMessage(t, db, "msg_u1", "ses_test003", now, `{"role":"user","agent":"build"}`)
	seedPart(t, db, "prt_u1_t", "msg_u1", "ses_test003", `{"type":"text","text":"Hello"}`)

	asstData := fmt.Sprintf(`{"role":"assistant","parentID":"msg_u1","modelID":"claude-sonnet-4","providerID":"anthropic","agent":"build","tokens":{"input":100,"output":50,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":%d,"completed":%d}}`, now, now+5000)
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
	if turn.TokenUsage.PromptTokens != 100 || turn.TokenUsage.CompletionTokens != 50 {
		t.Errorf("token usage mismatch: prompt=%d completion=%d", turn.TokenUsage.PromptTokens, turn.TokenUsage.CompletionTokens)
	}
	if turn.DurationMs != 5000 {
		t.Errorf("expected duration=5000ms, got %d", turn.DurationMs)
	}
}

func TestGetSessionArchivedIsNotLive(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	archivedAt := now - 86400000
	mustExec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated, time_archived) VALUES (?, ?, ?, 0, 0, ?)`,
		"ses_archived", "/test", "Archived", archivedAt)

	meta, err := reader.readSessionMeta("ses_archived")
	if err != nil {
		t.Fatalf("readSessionMeta: %v", err)
	}
	if meta.IsLive {
		t.Error("archived session should not be live")
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

func TestRenderANSIUnimplemented(t *testing.T) {
	reader, _, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := reader.RenderANSI("any")
	if err == nil {
		t.Error("expected error for unimplemented rendering")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("unexpected error: %v", err)
	}
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
