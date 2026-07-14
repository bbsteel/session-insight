package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	delUUID   = "019f5416-399c-7ff1-b016-ca888adbb3b5"
	keepUUID  = "019eb7de-3c42-7730-a273-8055f8b48520"
	delID     = "rollout-2026-07-12T10-09-30-" + delUUID
	keepID    = "rollout-2026-07-13T14-59-00-" + keepUUID
)

// writeDeleteFixture builds a fake ~/.codex: sessions/<date>/ with two
// rollout files plus a shared history.jsonl mixing both sessions' lines.
func writeDeleteFixture(t *testing.T) (codexDir string, r *CodexReader) {
	t.Helper()
	codexDir = t.TempDir()
	dayDir := filepath.Join(codexDir, "sessions", "2026", "07", "12")
	if err := os.MkdirAll(dayDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{delID, keepID} {
		content := `{"timestamp":"2026-07-12T10:09:30.000Z","type":"session_meta","payload":{"id":"x","cwd":"/tmp"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dayDir, id+".jsonl"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	history := strings.Join([]string{
		`{"session_id":"` + delUUID + `","ts":1,"text":"hello"}`,
		`{"session_id":"` + keepUUID + `","ts":2,"text":"other session"}`,
		// uuid mentioned inside another session's text must survive:
		`{"session_id":"` + keepUUID + `","ts":3,"text":"mentions ` + delUUID + ` in text"}`,
		`{"session_id":"` + delUUID + `","ts":4,"text":"bye"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(codexDir, "history.jsonl"), []byte(history), 0644); err != nil {
		t.Fatal(err)
	}
	return codexDir, New(filepath.Join(codexDir, "sessions"))
}

func TestDeleteSessionRemovesFileAndHistory(t *testing.T) {
	codexDir, r := writeDeleteFixture(t)

	if err := r.DeleteSession(delID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if r.findSessionFile(delID) != "" {
		t.Error("rollout file still present after delete")
	}
	if r.findSessionFile(keepID) == "" {
		t.Error("unrelated rollout file was removed")
	}

	data, err := os.ReadFile(filepath.Join(codexDir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("history.jsonl has %d lines, want 2: %q", len(lines), lines)
	}
	for _, line := range lines {
		if strings.Contains(line, `"session_id":"`+delUUID+`"`) {
			t.Errorf("deleted session's history line survived: %s", line)
		}
	}
	// The keep-session line that merely mentions the deleted uuid in its
	// text must still be there (field match, not substring match).
	if !strings.Contains(string(data), "mentions "+delUUID) {
		t.Error("history line mentioning the uuid in text was wrongly purged")
	}
}

func TestDeleteSessionNotFound(t *testing.T) {
	_, r := writeDeleteFixture(t)
	if err := r.DeleteSession("rollout-2026-01-01T00-00-00-00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestDeleteSessionMissingHistory(t *testing.T) {
	codexDir, r := writeDeleteFixture(t)
	if err := os.Remove(filepath.Join(codexDir, "history.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteSession(delID); err != nil {
		t.Fatalf("DeleteSession without history.jsonl: %v", err)
	}
	if r.findSessionFile(delID) != "" {
		t.Error("rollout file still present after delete")
	}
}

func TestSessionProcessesStoppedSession(t *testing.T) {
	_, r := writeDeleteFixture(t)
	pids, err := r.SessionProcesses(delID)
	if err != nil {
		t.Fatalf("SessionProcesses: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("stopped session reports holders: %v", pids)
	}
}
