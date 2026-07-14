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
