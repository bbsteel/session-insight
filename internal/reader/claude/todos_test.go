package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadClaudeTodosMatchesOnlySessionSidecars(t *testing.T) {
	root := t.TempDir()
	todoDirectory := filepath.Join(root, "todos")
	if err := os.Mkdir(todoDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for filename, body := range map[string]string{
		"session.json":                   `[{"id":"direct","content":"Direct task","status":"pending"}]`,
		"session-agent-child.json":       `[{"title":"Child task","status":"completed"}]`,
		"session-other.json":             `[{"content":"Unrelated suffix"}]`,
		"session-extra-agent-child.json": `[{"content":"Different session"}]`,
		"other-agent-child.json":         `[{"content":"Other session"}]`,
		"session-agent-malformed.json":   `not json`,
		"session-agent-backup.txt":       `[{"content":"Not JSON sidecar"}]`,
	} {
		if err := os.WriteFile(filepath.Join(todoDirectory, filename), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	todos := readClaudeTodos(root, "session")
	if len(todos) != 2 {
		t.Fatalf("todos = %+v", todos)
	}
	if todos[0].ID != "session-agent-child.json:0" || todos[0].Title != "Child task" || todos[0].Status != "completed" || todos[1].ID != "direct" || todos[1].Title != "Direct task" {
		t.Fatalf("sidecar fields = %+v", todos)
	}
	for _, sessionID := range []string{"", ".", "..", "../session", "nested/session", "*", "session*", "?ession", "[s]ession"} {
		if todos := readClaudeTodos(root, sessionID); len(todos) != 0 {
			t.Errorf("session %q read unrelated todos: %+v", sessionID, todos)
		}
	}
}

func TestReadClaudeTodosCannotFollowEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	todoDirectory := filepath.Join(root, "todos")
	if err := os.Mkdir(todoDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outsideFile, []byte(`[{"content":"Outside task"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(todoDirectory, "session-agent-escape.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if todos := readClaudeTodos(root, "session"); len(todos) != 0 {
		t.Fatalf("read through escaping symlink: %+v", todos)
	}
}

func TestReadClaudeTodosMissingDirectory(t *testing.T) {
	if todos := readClaudeTodos(t.TempDir(), "session"); len(todos) != 0 {
		t.Fatalf("missing directory todos = %+v", todos)
	}
}
