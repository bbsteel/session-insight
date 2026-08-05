package chrys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
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

func TestChrysProvenanceComplete(t *testing.T) {
	dir, sessionID := writeChrysBasicFixture(t)
	sessDir := filepath.Join(dir, sessionID)
	// Related files chrys actually writes alongside session.json.
	if err := os.WriteFile(filepath.Join(sessDir, "session.json.bak"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "session.recovery.json"), []byte(`{
  "meta": {"schema_version":1,"session_id":"conformance01-full","updated_at":"2025-01-01T00:00:00+00:00","message_count":0,"primary_cwd":"/tmp","title":"old"},
  "state": {"messages":[],"compressed_msgs":[],"turn_counter":0}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(sessDir, "sub_agents", "sessions")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subPath := filepath.Join(subDir, "explore_agent_abc12345.json")
	if err := os.WriteFile(subPath, []byte(`{"meta":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(sessDir, "snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapPath := filepath.Join(snapDir, "turn_1.json")
	if err := os.WriteFile(snapPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mutDir := filepath.Join(sessDir, "mutations")
	if err := os.MkdirAll(mutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mutPath := filepath.Join(mutDir, "deadbeefcafe")
	if err := os.WriteFile(mutPath, []byte(`patch`), 0o644); err != nil {
		t.Fatal(err)
	}

	detail, err := New(dir).GetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceComplete(t, detail, Capabilities())

	wantPrimary := filepath.Join(sessDir, "session.json")
	pathsByRole := map[string][]string{}
	for _, s := range detail.Provenance.Sources {
		if s.Path == sessDir {
			t.Fatalf("sources must not list the session directory: %+v", detail.Provenance.Sources)
		}
		if fi, err := os.Stat(s.Path); err != nil || fi.IsDir() {
			t.Fatalf("source must be a regular file: path=%q err=%v", s.Path, err)
		}
		pathsByRole[s.Role] = append(pathsByRole[s.Role], s.Path)
	}
	if len(pathsByRole["primary_transcript"]) != 1 || pathsByRole["primary_transcript"][0] != wantPrimary {
		t.Fatalf("primary = %v, want [%q]", pathsByRole["primary_transcript"], wantPrimary)
	}
	// Chrys maps each layout path to a precise stable role (not "other").
	mustContain := func(role, path string) {
		t.Helper()
		for _, p := range pathsByRole[role] {
			if p == path {
				return
			}
		}
		t.Fatalf("role %s missing %q; got %v", role, path, pathsByRole[role])
	}
	mustContain("recovery", filepath.Join(sessDir, "session.recovery.json"))
	mustContain("snapshot", filepath.Join(sessDir, "session.json.bak"))
	mustContain("snapshot", snapPath)
	mustContain("edit_cache", mutPath)
	mustContain("collaboration", subPath)
	// Must not dump agent-specific files as the catch-all "other".
	if len(pathsByRole["other"]) > 0 {
		t.Fatalf("chrys sources must not use role other; got %v", pathsByRole["other"])
	}
}

func TestChrysProvenanceSourceMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := New(dir).GetSession("missing-id")
	if err == nil {
		t.Fatal("expected error")
	}
	sre, ok := readerr.As(err)
	if !ok {
		t.Fatalf("expected typed readerr, got %T %v", err, err)
	}
	if sre.Kind != readerr.SourceMissing {
		t.Fatalf("kind=%s", sre.Kind)
	}
}

func TestChrysProvenanceMetadataOnly(t *testing.T) {
	// Meta present, no messages → no replayable body.
	sessionsDir := t.TempDir()
	sessionID := "metaonly01"
	dir := filepath.Join(sessionsDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "meta": {
    "schema_version": 1,
    "session_id": "metaonly01-full",
    "agent_profile": "Code",
    "model_id": "test-model",
    "created_at": "2026-01-01T00:00:00+00:00",
    "updated_at": "2026-01-01T00:01:00+00:00",
    "message_count": 0,
    "primary_cwd": "/tmp/proj",
    "title": "Metadata Only"
  },
  "state": {
    "messages": [],
    "compressed_msgs": [],
    "turn_counter": 0,
    "total_session_input_tokens": 0,
    "total_session_output_tokens": 0,
    "total_session_cache_hit_tokens": 0
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(body), 0o644); err != nil {
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
	if len(detail.Turns) != 0 {
		t.Fatalf("metadata_only must have zero turns, got %d", len(detail.Turns))
	}
	if len(detail.Provenance.Sources) == 0 {
		t.Fatal("expected source inventory")
	}
}
