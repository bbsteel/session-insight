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
	// One pass: index invocations and segment turns by TurnBoundary. A
	// result may only move within its own turn — never across a boundary.
	invocations := map[string]int{}
	turnOf := make([]int, len(events))
	turn := -1
	for i, event := range events {
		if event.Type == "TurnBoundary" {
			turn++
		}
		turnOf[i] = turn
		if event.Type == "ToolInvocation" {
			invocations[event.EventID] = i
		}
	}
	if len(invocations) == 0 {
		return events
	}
	return spliceResultsAfterInvocations(events, invocations, turnOf)
}

// spliceResultsAfterInvocations rebuilds the stream with every ToolResult
// emitted directly after its parent ToolInvocation. The collection pass
// visits results in ascending index order, so movedIdxs is already sorted
// and the emit pass can skip moved events with a cursor instead of an
// O(n) moved-marker slice.
func spliceResultsAfterInvocations(events []model.RenderEvent, invocations map[string]int, turnOf []int) []model.RenderEvent {
	resultsByParent := map[string][]int{}
	var movedIdxs []int
	for i, event := range events {
		if event.Type != "ToolResult" || event.ParentEventID == "" {
			continue
		}
		parent, ok := invocations[event.ParentEventID]
		if !ok || turnOf[parent] != turnOf[i] {
			continue
		}
		resultsByParent[event.ParentEventID] = append(resultsByParent[event.ParentEventID], i)
		movedIdxs = append(movedIdxs, i)
	}
	if len(movedIdxs) == 0 {
		return events
	}
	out := make([]model.RenderEvent, 0, len(events))
	next := 0
	for i, event := range events {
		if next < len(movedIdxs) && movedIdxs[next] == i {
			next++
			continue
		}
		out = append(out, event)
		for _, resultIdx := range resultsByParent[event.EventID] {
			out = append(out, events[resultIdx])
		}
	}
	return out
}
