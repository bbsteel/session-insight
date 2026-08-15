package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/render"
)

// TestGetSessionMetaMatchesGetSessionRevision locks the contract the
// server's position-cache fast path relies on: the cheap meta scan must
// report the same UpdatedAt as the full parse, so revisions derived from
// either path are bit-identical.
func TestGetSessionMetaMatchesGetSessionRevision(t *testing.T) {
	codexDir := t.TempDir()
	dayDir := filepath.Join(codexDir, "sessions", "2026", "07", "12")
	if err := os.MkdirAll(dayDir, 0755); err != nil {
		t.Fatal(err)
	}
	const id = "rollout-2026-07-12T10-09-30-019f5416-399c-7ff1-b016-ca888adbb3b5"
	lines := []string{
		`{"timestamp":"2026-07-12T10:09:30.000Z","type":"session_meta","payload":{"id":"x","cwd":"/tmp"}}`,
		`{"timestamp":"2026-07-12T10:10:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`,
		`{"timestamp":"2026-07-12T10:10:05.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`,
		`{"timestamp":"2026-07-12T10:10:09.000Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	}
	if err := os.WriteFile(filepath.Join(dayDir, id+".jsonl"), []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
	r := New(filepath.Join(codexDir, "sessions"))

	meta, err := r.GetSessionMeta(id)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	detail, err := r.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !meta.UpdatedAt.Equal(detail.UpdatedAt) {
		t.Errorf("UpdatedAt mismatch: meta=%v detail=%v", meta.UpdatedAt, detail.UpdatedAt)
	}
	if meta.ID != detail.ID || meta.AgentType != detail.AgentType {
		t.Errorf("identity mismatch: meta=%+v detail=%+v", meta, detail.Session)
	}
	// The server's position cache keys on render.PositionsRevision; the meta
	// fast path must produce the identical revision as the full parse.
	opts := render.Options{TimestampUser: true}
	if got, want := render.PositionsRevision(*meta, opts), render.PositionsRevision(detail.Session, opts); got != want {
		t.Errorf("PositionsRevision mismatch: meta=%d detail=%d", got, want)
	}
	if _, err := r.GetSessionMeta("rollout-2026-07-12T10-09-30-does-not-exist"); err == nil {
		t.Error("expected error for unknown session id")
	}
}
