package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSessionMetaPrefersRolloutIDForSubagent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-date-child-id.jsonl")
	line := `{"timestamp":"2026-07-12T09:04:30Z","type":"session_meta","payload":{"session_id":"parent-id","id":"child-id","cwd":"/tmp/project"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, ok := readSessionMeta(path)
	if !ok {
		t.Fatal("expected session metadata")
	}
	if session.ResumeID != "child-id" {
		t.Fatalf("resume id = %q, want child-id", session.ResumeID)
	}
}
