package claude

import (
	"os"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// The trailing "推理中…" in-progress row: emitted when the last turn is
// unclosed on disk (no assistant line with stop_reason "end_turn") AND the
// file was written within model.LiveWindow. Fixtures written by the test
// are always fresh, so staleness is simulated with os.Chtimes.

func lastEvent(t *testing.T, events []model.RenderEvent) model.RenderEvent {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events parsed")
	}
	return events[len(events)-1]
}

func TestTrailingInProgressOpenTurn(t *testing.T) {
	// Turn tail: assistant issued a tool_use, tool_result logged, next
	// assistant line not yet written — exactly what a live session looks
	// like mid-turn.
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"do something"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:02Z","toolUseResult":{"stdout":"ok"},"message":{"role":"user","content":[{"type":"tool_result"}]}}
`
	path := writeTempJSONL(t, "open_turn.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := lastEvent(t, events)
	if last.Type != "AgentSpecific" || last.Subtype != "in_progress" {
		t.Errorf("expected trailing in_progress event, got %s/%s", last.Type, last.Subtype)
	}
}

func TestNoTrailingInProgressWhenTurnClosed(t *testing.T) {
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}
`
	path := writeTempJSONL(t, "closed_turn.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range events {
		if e.Subtype == "in_progress" {
			t.Errorf("closed turn must not emit in_progress, got %+v", e)
		}
	}
}

func TestNoTrailingInProgressWhenStale(t *testing.T) {
	// Unclosed turn but the file went quiet past the live window — the
	// session was interrupted/killed, not still running.
	fixture := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"do something"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
`
	path := writeTempJSONL(t, "stale_open_turn.jsonl", fixture)
	stale := time.Now().Add(-2 * model.LiveWindow)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range events {
		if e.Subtype == "in_progress" {
			t.Errorf("stale session must not emit in_progress, got %+v", e)
		}
	}
}

func TestNoTrailingInProgressInSubagentParse(t *testing.T) {
	// A subagent transcript (baseDepth > 0) never emits its own row — the
	// parent's pending Agent tool call already represents the live state.
	fixture := `{"type":"user","uuid":"u1","isSidechain":true,"timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"sub task"}}
{"type":"assistant","uuid":"a1","isSidechain":true,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
`
	path := writeTempJSONL(t, "subagent_open.jsonl", fixture)
	events, _, err := ParseClaudeRenderEvents(path, 1, "parent-evt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range events {
		if e.Subtype == "in_progress" {
			t.Errorf("subagent parse must not emit in_progress, got %+v", e)
		}
	}
}
