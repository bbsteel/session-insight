package shared

import (
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// TrailingInProgress reports whether a session whose last turn looks
// unclosed on disk should render a trailing "推理中…" row, and builds that
// event. Two conditions, both required:
//
//   - turnOpen: the agent's own precise on-disk marker says the last turn
//     never closed (claude: no stop_reason=end_turn; codex: task_started
//     without task_complete; copilot: turn_start without turn_end;
//     opencode: assistant message without time.completed).
//   - the source was written within model.LiveWindow: the close marker is
//     never written when a session is interrupted/killed, so without this
//     guard a dead session would show "推理中" forever. The window is the
//     bound on that failure mode, not the primary signal.
//
// The rendered row is the same one chrys emits from its in-flight
// checkpoint: render/formatter.go draws "  推理中…" (two leading spaces
// reserve the cell for the frontend's spinning-hourglass decoration).
func TrailingInProgress(turnOpen bool, lastWrite time.Time, turnIndex int) (model.RenderEvent, bool) {
	if !turnOpen || !model.IsSessionLive(lastWrite) {
		return model.RenderEvent{}, false
	}
	return model.RenderEvent{
		Type:      "AgentSpecific",
		Subtype:   "in_progress",
		Timestamp: lastWrite,
		TurnIndex: turnIndex,
	}, true
}

// DropEmptyRenderTurns removes TurnBoundary+UserPrompt pairs for turns that carry
// no real content. AgentSpecific/turn_duration markers do not count as content.
func DropEmptyRenderTurns(events []model.RenderEvent) []model.RenderEvent {
	hasContent := make(map[int]bool)
	for _, e := range events {
		switch e.Type {
		case "TurnBoundary":
		case "UserPrompt":
			if strings.TrimSpace(e.Text) != "" {
				hasContent[e.TurnIndex] = true
			}
		case "AgentSpecific":
			if e.Subtype != "turn_duration" {
				hasContent[e.TurnIndex] = true
			}
		default:
			hasContent[e.TurnIndex] = true
		}
	}

	filtered := make([]model.RenderEvent, 0, len(events))
	for _, e := range events {
		if (e.Type == "TurnBoundary" || e.Type == "UserPrompt") && !hasContent[e.TurnIndex] {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// HasTrailingInProgress reports whether a render event stream ends on the
// "推理中…" marker — the shared signal that the session's last turn never
// closed. Backs reader.SessionLivenessChecker for agents without an exact
// PID source. Callers whose in_progress emission is not already bounded by
// model.LiveWindow (chrys emits it from the raw checkpoint marker) must
// AND this with a source-mtime freshness check, or a killed session would
// count as running forever.
func HasTrailingInProgress(events []model.RenderEvent) bool {
	if len(events) == 0 {
		return false
	}
	last := events[len(events)-1]
	return last.Type == "AgentSpecific" && last.Subtype == "in_progress"
}

// InterleaveToolResults moves each ToolResult directly after its parent
// ToolInvocation. Agents that support parallel tool calls store one assistant
// message carrying N tool_calls followed by N separate result records, so
// raw stream order renders call1, call2, result1, result2 — the result boxes
// detach from the invocation they belong to. Pairing by ParentEventID
// restores call1, result1, call2, result2. Results whose parent invocation
// is unknown or in another turn keep their stream position, and multiple
// results under one invocation keep their relative order.
func InterleaveToolResults(events []model.RenderEvent) []model.RenderEvent {
	invocations := map[string]int{}
	for i, event := range events {
		if event.Type == "ToolInvocation" {
			invocations[event.EventID] = i
		}
	}
	if len(invocations) == 0 {
		return events
	}
	// A result may only move within its own turn: never reorder across a
	// TurnBoundary.
	turnOf := make([]int, len(events))
	turn := -1
	for i, event := range events {
		if event.Type == "TurnBoundary" {
			turn++
		}
		turnOf[i] = turn
	}
	resultsByParent := map[string][]int{}
	moved := make([]bool, len(events))
	for i, event := range events {
		if event.Type != "ToolResult" || event.ParentEventID == "" {
			continue
		}
		parent, ok := invocations[event.ParentEventID]
		if !ok || turnOf[parent] != turnOf[i] {
			continue
		}
		resultsByParent[event.ParentEventID] = append(resultsByParent[event.ParentEventID], i)
		moved[i] = true
	}
	if len(resultsByParent) == 0 {
		return events
	}
	out := make([]model.RenderEvent, 0, len(events))
	for i, event := range events {
		if moved[i] {
			continue
		}
		out = append(out, event)
		for _, resultIdx := range resultsByParent[event.EventID] {
			out = append(out, events[resultIdx])
		}
	}
	return out
}
