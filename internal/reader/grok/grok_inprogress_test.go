package grok

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestTrailingInProgressOpenBracket(t *testing.T) {
	root := t.TempDir()
	id := "open1111-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "proj", id, summaryFile{}, sampleUpdatesOpen(), sampleEventsOpen())
	// Ensure fresh mtime within LiveWindow
	dir := filepath.Join(root, "proj", id)
	now := time.Now()
	for _, name := range []string{"updates.jsonl", "events.jsonl", "summary.json"} {
		_ = os.Chtimes(filepath.Join(dir, name), now, now)
	}

	r := New(root)
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	if !hasInProgress(events) {
		t.Error("open turn bracket should emit trailing in_progress")
	}
	if last := events[len(events)-1]; last.Subtype != "in_progress" {
		t.Errorf("in_progress must be last, got %s/%s", last.Type, last.Subtype)
	}
}

func TestNoInProgressAfterTurnEnded(t *testing.T) {
	root := t.TempDir()
	id := "closed11-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "proj", id, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
	r := New(root)
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	if hasInProgress(events) {
		t.Error("closed turn must not emit in_progress")
	}
}

func TestNoInProgressWhenStale(t *testing.T) {
	root := t.TempDir()
	id := "stale111-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "proj", id, summaryFile{}, sampleUpdatesOpen(), sampleEventsOpen())
	dir := filepath.Join(root, "proj", id)
	stale := time.Now().Add(-2 * model.LiveWindow)
	for _, name := range []string{"updates.jsonl", "events.jsonl", "summary.json"} {
		if err := os.Chtimes(filepath.Join(dir, name), stale, stale); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	r := New(root)
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	if hasInProgress(events) {
		t.Error("stale open turn must not emit in_progress")
	}
}

func TestInProgressFromUpdatesWhenNoEventsFile(t *testing.T) {
	root := t.TempDir()
	id := "nolev111-bbbb-cccc-dddd-eeeeeeeeeeee"
	// Open updates (no turn_completed) and no events.jsonl
	writeSession(t, root, "proj", id, summaryFile{}, sampleUpdatesOpen(), "")
	dir := filepath.Join(root, "proj", id)
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, "updates.jsonl"), now, now)
	_ = os.Chtimes(filepath.Join(dir, "summary.json"), now, now)

	r := New(root)
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	if !hasInProgress(events) {
		t.Error("open updates without turn_completed should emit in_progress")
	}
}
