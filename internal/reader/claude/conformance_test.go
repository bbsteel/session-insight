package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Minimal sanitized Claude Code JSONL (user + assistant). Layout:
// projectsDir/<project>/<sessionID>.jsonl
func writeClaudeBasicFixture(t *testing.T) (projectsDir, sessionID string) {
	t.Helper()
	projectsDir = t.TempDir()
	sessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	proj := filepath.Join(projectsDir, "-tmp-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// timestamps required for CreatedAt ≤ UpdatedAt
	content := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/proj","sessionId":"` + sessionID + `","message":{"role":"user","content":"hello conformance"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:05.000Z","message":{"role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"hi there"}]}}
`
	path := filepath.Join(proj, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectsDir, sessionID
}

func TestClaudeConformance(t *testing.T) {
	dir, sessionID := writeClaudeBasicFixture(t)
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
