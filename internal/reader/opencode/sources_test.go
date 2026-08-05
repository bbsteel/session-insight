package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceInventoryOpenCodeDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-wal", []byte("w"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources := sourceInventory(dbPath)
	if len(sources) != 1 {
		t.Fatalf("want only the db (no wal/shm), got %v", sources)
	}
	if sources[0].Role != "primary_transcript" || sources[0].Path != dbPath {
		t.Fatalf("got %+v", sources[0])
	}
}
