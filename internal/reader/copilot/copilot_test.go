package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSessions(t *testing.T) {
	dir := t.TempDir()

	sessionDir := filepath.Join(dir, "test-session-id")
	os.MkdirAll(sessionDir, 0755)

	wsYAML := `id: test-session-id
cwd: /home/test/project
repository: owner/repo
branch: main
name: My Test Session
user_named: true
created_at: 2026-06-12T10:00:00Z
updated_at: 2026-06-12T11:00:00Z
`
	os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(wsYAML), 0644)
	os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`), 0644)

	reader := New(dir)
	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s.ID != "test-session-id" {
		t.Errorf("expected ID 'test-session-id', got '%s'", s.ID)
	}
	if s.Name != "My Test Session" {
		t.Errorf("expected Name 'My Test Session', got '%s'", s.Name)
	}
	if s.Repository != "owner/repo" {
		t.Errorf("expected Repository 'owner/repo', got '%s'", s.Repository)
	}
}

func TestListSessionsUserNotNamed(t *testing.T) {
	dir := t.TempDir()

	sessionDir := filepath.Join(dir, "auto-session")
	os.MkdirAll(sessionDir, 0755)

	wsYAML := `id: auto-session
name: "# Instructions (read first)\n\nThis is a long system prompt..."
user_named: false
created_at: 2026-06-12T10:00:00Z
updated_at: 2026-06-12T11:00:00Z
`
	os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(wsYAML), 0644)
	os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`), 0644)

	reader := New(dir)
	sessions, _ := reader.ListSessions()

	if len(sessions) == 0 {
		t.Fatal("expected 1 session")
	}
	if sessions[0].Name != "" {
		t.Errorf("expected empty Name for user_named=false, got '%s'", sessions[0].Name)
	}
}

func TestListSessionsSkipsInvalid(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, "empty-session"), 0755)

	badDir := filepath.Join(dir, "bad-session")
	os.MkdirAll(badDir, 0755)
	os.WriteFile(filepath.Join(badDir, "workspace.yaml"), []byte("not: valid: yaml: ["), 0644)

	reader := New(dir)
	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestParseCopilotRenderEventsUsesCurrentEventSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lines := []string{
		`{"type":"session.model_change","timestamp":"2026-06-20T01:00:00.000Z","data":{"newModel":"gpt-5.4"}}`,
		`{"type":"user.message","timestamp":"2026-06-20T01:00:01.000Z","data":{"content":"hello"}}`,
		`{"type":"assistant.message","timestamp":"2026-06-20T01:00:02.000Z","data":{"encryptedContent":"ciphertext-must-not-render"}}`,
		`{"type":"tool.execution_start","timestamp":"2026-06-20T01:00:03.000Z","data":{"toolCallId":"call-1","toolName":"view","arguments":{"path":"/tmp/file"}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-06-20T01:00:04.000Z","data":{"toolCallId":"call-1","success":true,"result":{"content":"file contents"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-06-20T01:00:05.000Z","data":{"toolCallId":"call-2","toolName":"shell","arguments":{"command":"false"}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-06-20T01:00:06.000Z","data":{"toolCallId":"call-2","success":false,"error":{"message":"Permission denied","code":"denied"}}}`,
		`{"type":"user.message","timestamp":"2026-06-20T01:00:07.000Z","data":{"content":"   "}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := parseCopilotRenderEvents(path)
	if err != nil {
		t.Fatalf("parseCopilotRenderEvents() failed: %v", err)
	}

	var boundaries, invocations, results int
	for _, event := range events {
		if event.TurnIndex < 0 {
			t.Errorf("event %q has negative turn index %d", event.Type, event.TurnIndex)
		}
		if event.Text == "ciphertext-must-not-render" {
			t.Error("encrypted assistant content must not be rendered as plaintext")
		}
		switch event.Type {
		case "TurnBoundary":
			boundaries++
		case "ToolInvocation":
			invocations++
			if event.ToolCallID == "call-1" && event.ToolInput["path"] != "/tmp/file" {
				t.Errorf("tool arguments not preserved: %#v", event.ToolInput)
			}
		case "ToolResult":
			results++
			switch event.ToolCallID {
			case "call-1":
				if event.Stdout != "file contents" || event.ExitCode != 0 || event.ParentEventID == "" {
					t.Errorf("unexpected successful result: %#v", event)
				}
			case "call-2":
				if event.Stderr != "Permission denied" || event.ExitCode == 0 || event.ParentEventID == "" {
					t.Errorf("unexpected failed result: %#v", event)
				}
			}
		}
	}
	if boundaries != 1 || invocations != 2 || results != 2 {
		t.Fatalf("expected 1 boundary, 2 invocations, and 2 results; got %d, %d, and %d", boundaries, invocations, results)
	}
}

func TestRenderANSIRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "events.jsonl"), []byte(`{"type":"user.message","data":{"content":"secret"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New(sessionDir).RenderANSI("../outside", 0); err == nil {
		t.Fatal("RenderANSI accepted a path-traversal session ID")
	}
	if _, err := New(sessionDir).GetSession("../outside"); err == nil {
		t.Fatal("GetSession accepted a path-traversal session ID")
	}
}
