package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceInventorySingleRollout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-01-01T00-00-00-uuid.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := sourceInventory(path)
	if len(sources) != 1 {
		t.Fatalf("sources=%v", sources)
	}
	if sources[0].Role != "primary_transcript" || sources[0].Path != path {
		t.Fatalf("got %+v", sources[0])
	}
}
