package chrys

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteSessionRemovesWholeDirectory(t *testing.T) {
	root := writeFixture(t)
	r := New(root)

	if err := r.DeleteSession("28491d6d491e"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "28491d6d491e")); !os.IsNotExist(err) {
		t.Errorf("session dir should be gone, stat err=%v", err)
	}
	// The unrelated non-session directory must survive.
	if _, err := os.Stat(filepath.Join(root, "attribution")); err != nil {
		t.Errorf("sibling dir damaged: %v", err)
	}
}

func TestDeleteSessionRejectsBadIDs(t *testing.T) {
	root := writeFixture(t)
	r := New(root)
	for _, id := range []string{"", ".", "..", "../x", "a/b", "no-such-session"} {
		if err := r.DeleteSession(id); err == nil {
			t.Errorf("DeleteSession(%q) should fail", id)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "28491d6d491e", "session.json")); err != nil {
		t.Errorf("session damaged by rejected deletes: %v", err)
	}
}

// inFlightFixture is a session whose last message is chrys's recovery
// checkpoint (kind=interrupted without _interrupted_by): the on-disk shape
// of a turn that is still running.
const inFlightFixture = `{
  "meta": {
    "session_id": "run1",
    "agent_profile": "Default",
    "model_id": "deepseek-v4-pro",
    "created_at": "2026-07-14T10:00:00+00:00",
    "updated_at": "2026-07-14T10:00:05+00:00"
  },
  "state": {
    "messages": [
      {"type": "message", "role": "user",
       "contents": [{"type": "text", "text": "do something", "additional_properties": {}}],
       "additional_properties": {}},
      {"type": "message", "role": "user",
       "contents": [],
       "additional_properties": {"_chrys_kind": "interrupted"}}
    ],
    "compressed_msgs": [],
    "turn_counter": 1
  }
}`

func writeInFlight(t *testing.T, mtime time.Time) *ChrysReader {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "run1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte(inFlightFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return New(root)
}

func TestSessionRunningFreshCheckpoint(t *testing.T) {
	r := writeInFlight(t, time.Now())
	running, err := r.SessionRunning("run1")
	if err != nil {
		t.Fatalf("SessionRunning: %v", err)
	}
	if !running {
		t.Error("fresh in-flight checkpoint should count as running")
	}
}

func TestSessionRunningStaleCheckpointIsDead(t *testing.T) {
	r := writeInFlight(t, time.Now().Add(-time.Hour))
	running, err := r.SessionRunning("run1")
	if err != nil {
		t.Fatalf("SessionRunning: %v", err)
	}
	if running {
		t.Error("hour-old checkpoint means the session was killed, not running")
	}
}

func TestSessionRunningCompletedSession(t *testing.T) {
	r := New(writeFixture(t))
	running, err := r.SessionRunning("28491d6d491e")
	if err != nil {
		t.Fatalf("SessionRunning: %v", err)
	}
	if running {
		t.Error("completed session must not count as running")
	}
}
