package opencode

import (
	"os"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// OpenCode's on-disk in-progress marker: an assistant message row whose data
// JSON has no time.completed (and no error). Combined with a fresh db/WAL
// mtime it renders the trailing "推理中…" row.

func opencodeHasInProgress(events []model.RenderEvent) bool {
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "in_progress" {
			return true
		}
	}
	return false
}

func TestOpenCodeTrailingInProgressOpenAssistant(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "s1", "/tmp/p", "live session", "gpt-x")
	seedMessage(t, db, "m-user", "s1", 1000, `{"role":"user","time":{"created":1000}}`)
	seedPart(t, db, "p-user", "m-user", "s1", `{"type":"text","text":"do something"}`)
	// No time.completed → run still going.
	seedMessage(t, db, "m-asst", "s1", 2000, `{"role":"assistant","parentID":"m-user","time":{"created":2000}}`)
	seedPart(t, db, "p-asst", "m-asst", "s1", `{"type":"text","text":"working on it"}`)

	events, err := reader.toRenderEvents("s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opencodeHasInProgress(events) {
		t.Error("assistant message without time.completed should emit in_progress")
	}
	if last := events[len(events)-1]; last.Subtype != "in_progress" {
		t.Errorf("in_progress must be the last event, got %s/%s", last.Type, last.Subtype)
	}
}

func TestOpenCodeNoInProgressWhenCompleted(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "s1", "/tmp/p", "done session", "gpt-x")
	seedMessage(t, db, "m-user", "s1", 1000, `{"role":"user","time":{"created":1000}}`)
	seedPart(t, db, "p-user", "m-user", "s1", `{"type":"text","text":"hi"}`)
	seedMessage(t, db, "m-asst", "s1", 2000, `{"role":"assistant","parentID":"m-user","time":{"created":2000,"completed":3000}}`)
	seedPart(t, db, "p-asst", "m-asst", "s1", `{"type":"text","text":"done"}`)

	events, err := reader.toRenderEvents("s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opencodeHasInProgress(events) {
		t.Error("completed assistant message must not emit in_progress")
	}
}

func TestOpenCodeNoInProgressWhenErrored(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "s1", "/tmp/p", "aborted session", "gpt-x")
	seedMessage(t, db, "m-user", "s1", 1000, `{"role":"user","time":{"created":1000}}`)
	seedPart(t, db, "p-user", "m-user", "s1", `{"type":"text","text":"hi"}`)
	// Aborted runs carry an error instead of time.completed.
	seedMessage(t, db, "m-asst", "s1", 2000, `{"role":"assistant","parentID":"m-user","time":{"created":2000},"error":{"name":"MessageAbortedError"}}`)
	seedPart(t, db, "p-asst", "m-asst", "s1", `{"type":"text","text":"partial"}`)

	events, err := reader.toRenderEvents("s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opencodeHasInProgress(events) {
		t.Error("errored assistant message must not emit in_progress")
	}
}

func TestOpenCodeNoInProgressWhenStoreStale(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "s1", "/tmp/p", "dead session", "gpt-x")
	seedMessage(t, db, "m-user", "s1", 1000, `{"role":"user","time":{"created":1000}}`)
	seedPart(t, db, "p-user", "m-user", "s1", `{"type":"text","text":"hi"}`)
	seedMessage(t, db, "m-asst", "s1", 2000, `{"role":"assistant","parentID":"m-user","time":{"created":2000}}`)
	seedPart(t, db, "p-asst", "m-asst", "s1", `{"type":"text","text":"partial"}`)

	stale := time.Now().Add(-2 * model.LiveWindow)
	if err := os.Chtimes(reader.dbPath, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	events, err := reader.toRenderEvents("s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opencodeHasInProgress(events) {
		t.Error("stale store must not emit in_progress")
	}
}

func TestOpenCodeLiveRevision(t *testing.T) {
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()

	seedSession(t, db, "s1", "/tmp/p", "session", "gpt-x")

	rev, err := reader.LiveRevision("s1")
	if err != nil {
		t.Fatalf("LiveRevision for existing session: %v", err)
	}
	if rev == 0 {
		t.Error("expected non-zero revision")
	}

	if _, err := reader.LiveRevision("missing"); err == nil {
		t.Error("LiveRevision for unknown session must error so the handler falls through to other readers")
	}
}
