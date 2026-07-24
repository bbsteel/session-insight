package chrys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Minimal Chrys session directory with session.json (reuses the layout from
// existing tests; content is synthetic and sanitized).
func writeChrysBasicFixture(t *testing.T) (sessionsDir, sessionID string) {
	t.Helper()
	sessionsDir = t.TempDir()
	sessionID = "conformance01"
	dir := filepath.Join(sessionsDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Compact closed single-turn session.
	body := `{
  "meta": {
    "schema_version": 1,
    "session_id": "conformance01-full",
    "agent_profile": "Code",
    "model_id": "test-model",
    "created_at": "2026-01-01T00:00:00+00:00",
    "updated_at": "2026-01-01T00:01:00+00:00",
    "message_count": 2,
    "primary_cwd": "/tmp/proj",
    "title": "Conformance"
  },
  "state": {
    "messages": [
      {
        "type": "message", "role": "user",
        "contents": [{"type": "text", "text": "hello conformance", "additional_properties": {}}],
        "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:00+00:00"}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [{"type": "text", "text": "hi", "additional_properties": {}}],
        "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:30+00:00"}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [{"type": "text", "text": "", "additional_properties": {}}],
        "additional_properties": {"_chrys_kind": "turn", "_turn_id": "turn_1", "_turn": 1}
      }
    ],
    "compressed_msgs": [],
    "turn_counter": 1,
    "total_session_input_tokens": 10,
    "total_session_output_tokens": 5,
    "total_session_cache_hit_tokens": 0
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionsDir, sessionID
}

func TestChrysConformance(t *testing.T) {
	dir, sessionID := writeChrysBasicFixture(t)
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
