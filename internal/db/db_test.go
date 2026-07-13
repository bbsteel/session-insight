package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	dbPath := filepath.Join(dir, "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("index.db was not created")
	}
}

func TestUpdateSessionResumeID(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	if _, err := database.Conn().Exec(`
		INSERT INTO sessions(agent_type, id, resume_id)
		VALUES ('codex', 'rollout-file', 'parent-id');
	`); err != nil {
		t.Fatalf("prepare session: %v", err)
	}
	changed, err := database.UpdateSessionResumeID("codex", "rollout-file", "child-id")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected resume id update")
	}
	var got string
	if err := database.Conn().QueryRow(`SELECT resume_id FROM sessions WHERE agent_type='codex' AND id='rollout-file'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "child-id" {
		t.Fatalf("resume id = %q, want child-id", got)
	}
	changed, err = database.UpdateSessionResumeID("codex", "rollout-file", "child-id")
	if err != nil || changed {
		t.Fatalf("idempotent update: changed=%v err=%v", changed, err)
	}
}
