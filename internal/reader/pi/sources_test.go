package pi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestSourceInventoryPrimaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-01-01T00-00-00-000Z_aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := sourceInventory(path)
	if len(sources) != 1 {
		t.Fatalf("sources=%d want 1", len(sources))
	}
	if sources[0].Role != model.SourceRolePrimaryTranscript {
		t.Fatalf("role=%q", sources[0].Role)
	}
	if sources[0].Path != path {
		t.Fatalf("path=%q want %q", sources[0].Path, path)
	}
	if sources[0].Path == dir {
		t.Fatal("must not list the session directory")
	}
}

func TestSourceInventoryEmptyPath(t *testing.T) {
	if sources := sourceInventory(""); sources != nil {
		t.Fatalf("empty path must yield nil, got %+v", sources)
	}
}
