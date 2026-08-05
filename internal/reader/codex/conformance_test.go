package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
)

// Minimal Codex rollout under sessions/YYYY/MM/DD/rollout-....jsonl
func writeCodexBasicFixture(t *testing.T) (sessionsDir, sessionID string) {
	t.Helper()
	root := t.TempDir()
	sessionsDir = filepath.Join(root, "sessions")
	day := filepath.Join(sessionsDir, "2026", "01", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	uuid := "019f0000-0000-7000-8000-000000000001"
	sessionID = "rollout-2026-01-01T00-00-00-" + uuid
	content := `{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"` + uuid + `","cwd":"/tmp/proj","model_provider":"openai"}}
{"timestamp":"2026-01-01T00:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello conformance"}}
{"timestamp":"2026-01-01T00:00:02.000Z","type":"event_msg","payload":{"type":"agent_message","message":"hi"}}
`
	if err := os.WriteFile(filepath.Join(day, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionsDir, sessionID
}

func TestCodexConformance(t *testing.T) {
	dir, sessionID := writeCodexBasicFixture(t)
	adaptertest.Run(t, adaptertest.Config{
		Capabilities: Capabilities(),
		NewReader: func(t *testing.T) adaptertest.Reader {
			return New(dir)
		},
		Expect: adaptertest.Expectations{
			SessionCount: 1,
			SessionIDs:   []string{sessionID},
		},
	})
}

func TestCodexProvenanceComplete(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	day := filepath.Join(sessionsDir, "2026", "01", "03")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	uuid := "019f0000-0000-7000-8000-0000000000aa"
	sessionID := "rollout-2026-01-03T00-00-00-" + uuid
	// Codex opens turns on task_started; user/agent messages alone yield no Active turns.
	content := `{"timestamp":"2026-01-03T00:00:00.000Z","type":"session_meta","payload":{"id":"` + uuid + `","cwd":"/tmp/proj","model_provider":"openai"}}
{"timestamp":"2026-01-03T00:00:01.000Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-01-03T00:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello complete"}}
{"timestamp":"2026-01-03T00:00:03.000Z","type":"event_msg","payload":{"type":"agent_message","message":"hi"}}
`
	if err := os.WriteFile(filepath.Join(day, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	detail, err := New(sessionsDir).GetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceComplete(t, detail, Capabilities())
}

func TestCodexProvenanceMetadataOnly(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	day := filepath.Join(sessionsDir, "2026", "01", "02")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	uuid := "019f0000-0000-7000-8000-000000000099"
	sessionID := "rollout-2026-01-02T00-00-00-" + uuid
	content := `{"timestamp":"2026-01-02T00:00:00.000Z","type":"session_meta","payload":{"id":"` + uuid + `","cwd":"/tmp/proj","model_provider":"openai"}}
`
	if err := os.WriteFile(filepath.Join(day, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	detail, err := New(sessionsDir).GetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
	if detail.Provenance.State != "metadata_only" {
		t.Fatalf("state=%s", detail.Provenance.State)
	}
}

func TestCodexFirstMissingRetainsDiscoveredSourcePath(t *testing.T) {
	dir, sessionID := writeCodexBasicFixture(t)
	r := New(dir)
	sessions, complete, err := r.ListSessionsDetailed()
	if err != nil || !complete || len(sessions) != 1 {
		t.Fatalf("list complete=%v sessions=%d err=%v", complete, len(sessions), err)
	}
	knownPath := r.knownSessionFile(sessionID)
	if knownPath == "" {
		t.Fatal("missing discovered path")
	}
	if err := os.Remove(knownPath); err != nil {
		t.Fatal(err)
	}
	_, _, err = r.ReadIndexSnapshot(context.Background(), sessions[0])
	if err == nil {
		t.Fatal("expected source missing error")
	}
	sre, ok := readerr.As(err)
	if !ok || len(sre.Sources) != 1 || sre.Sources[0].Path != knownPath || sre.Sources[0].State != "missing" {
		t.Fatalf("read error facts=%+v", sre)
	}
}
