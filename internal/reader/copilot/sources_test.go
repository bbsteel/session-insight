package copilot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceInventoryCopilotLayout(t *testing.T) {
	root := t.TempDir()
	id := "sess1"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"workspace.yaml", "events.jsonl", "session.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Unknown junk must not appear.
	if err := os.WriteFile(filepath.Join(dir, "noise.bin"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources := sourceInventory(root, id)
	byRole := map[string]int{}
	paths := map[string]bool{}
	for _, s := range sources {
		byRole[s.Role]++
		paths[s.Path] = true
	}
	if byRole["metadata"] != 1 || byRole["primary_transcript"] != 1 || byRole["tool_results"] != 1 {
		t.Fatalf("roles=%v", byRole)
	}
	if paths[filepath.Join(dir, "noise.bin")] {
		t.Fatal("must not list unknown files")
	}
	if byRole["other"] != 0 {
		t.Fatalf("other=%d", byRole["other"])
	}
}
