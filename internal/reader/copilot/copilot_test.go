package copilot

import (
	"os"
	"path/filepath"
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
