package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
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
