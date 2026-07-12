package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// Copilot events bracket turns with assistant.turn_start / assistant.turn_end
// and close sessions with session.shutdown. An open bracket at EOF plus a
// fresh events.jsonl mtime renders the trailing "推理中…" row.

func writeCopilotEvents(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func copilotHasInProgress(events []model.RenderEvent) bool {
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "in_progress" {
			return true
		}
	}
	return false
}

func TestCopilotTrailingInProgressOpenBracket(t *testing.T) {
	events, err := parseCopilotRenderEvents(writeCopilotEvents(t, []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"do something"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-01-01T00:00:01Z","data":{}}`,
		`{"type":"tool.execution_start","timestamp":"2026-01-01T00:00:02Z","data":{"toolCallId":"c1","toolName":"shell","arguments":{"command":"ls"}}}`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !copilotHasInProgress(events) {
		t.Error("open turn bracket should emit trailing in_progress event")
	}
	if last := events[len(events)-1]; last.Subtype != "in_progress" {
		t.Errorf("in_progress must be the last event, got %s/%s", last.Type, last.Subtype)
	}
}

func TestCopilotNoInProgressAfterTurnEnd(t *testing.T) {
	events, err := parseCopilotRenderEvents(writeCopilotEvents(t, []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-01-01T00:00:01Z","data":{}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:00:02Z","data":{"content":"done"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-01-01T00:00:03Z","data":{}}`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if copilotHasInProgress(events) {
		t.Error("closed turn bracket must not emit in_progress")
	}
}

func TestCopilotNoInProgressAfterShutdown(t *testing.T) {
	// A shutdown can arrive without an explicit turn_end (e.g. error path);
	// the session is over either way.
	events, err := parseCopilotRenderEvents(writeCopilotEvents(t, []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-01-01T00:00:01Z","data":{}}`,
		`{"type":"session.shutdown","timestamp":"2026-01-01T00:00:02Z","data":{"shutdownType":"routine"}}`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if copilotHasInProgress(events) {
		t.Error("shutdown must close the turn bracket")
	}
}

func TestCopilotNoInProgressWhenStale(t *testing.T) {
	path := writeCopilotEvents(t, []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"do something"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-01-01T00:00:01Z","data":{}}`,
	})
	stale := time.Now().Add(-2 * model.LiveWindow)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	events, err := parseCopilotRenderEvents(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if copilotHasInProgress(events) {
		t.Error("stale events file must not emit in_progress")
	}
}
