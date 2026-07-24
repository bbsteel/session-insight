package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Minimal Copilot session-state layout: <root>/<id>/{workspace.yaml,events.jsonl}
func writeCopilotBasicFixture(t *testing.T) (stateDir, sessionID string) {
	t.Helper()
	stateDir = t.TempDir()
	sessionID = "conformance-copilot-1"
	sessionDir := filepath.Join(stateDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := `id: conformance-copilot-1
cwd: /tmp/proj
repository: owner/repo
branch: main
name: Conformance Session
user_named: true
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:01:00Z
`
	if err := os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}
	events := `{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hello conformance"}}
{"type":"assistant.message","timestamp":"2026-01-01T00:00:05Z","data":{"content":"hi"}}
`
	if err := os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	return stateDir, sessionID
}

func TestCopilotConformance(t *testing.T) {
	dir, sessionID := writeCopilotBasicFixture(t)
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
