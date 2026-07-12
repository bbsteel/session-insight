package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// Codex rollouts bracket every turn with task_started / task_complete (or
// turn_aborted). An open bracket at EOF plus a fresh file mtime renders the
// trailing "推理中…" row.

func writeCodexFixture(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func hasInProgress(events []model.RenderEvent) bool {
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "in_progress" {
			return true
		}
	}
	return false
}

func TestCodexTrailingInProgressOpenBracket(t *testing.T) {
	fixture := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"do something"}}
{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{}"}}
`
	events, err := codexToRenderEvents(writeCodexFixture(t, fixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasInProgress(events) {
		t.Error("open task bracket should emit trailing in_progress event")
	}
	if last := events[len(events)-1]; last.Subtype != "in_progress" {
		t.Errorf("in_progress must be the last event, got %s/%s", last.Type, last.Subtype)
	}
}

func TestCodexNoInProgressAfterTaskComplete(t *testing.T) {
	fixture := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"do something"}}
{"timestamp":"2026-01-01T00:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"done"}}
{"timestamp":"2026-01-01T00:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
`
	events, err := codexToRenderEvents(writeCodexFixture(t, fixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasInProgress(events) {
		t.Error("closed task bracket must not emit in_progress")
	}
}

func TestCodexNoInProgressAfterTurnAborted(t *testing.T) {
	fixture := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"do something"}}
{"timestamp":"2026-01-01T00:00:02Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1"}}
`
	events, err := codexToRenderEvents(writeCodexFixture(t, fixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasInProgress(events) {
		t.Error("aborted turn must not emit in_progress")
	}
}

func TestCodexNoInProgressWhenStale(t *testing.T) {
	fixture := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"do something"}}
`
	path := writeCodexFixture(t, fixture)
	stale := time.Now().Add(-2 * model.LiveWindow)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasInProgress(events) {
		t.Error("stale rollout must not emit in_progress")
	}
}
