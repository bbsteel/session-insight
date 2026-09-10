package worktreereview

import (
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// attemptView is the parsed journal state for one attempt.
type attemptView struct {
	meta   *SessionMetadata // nil when metadata.json is missing/unreadable
	events []ReviewEvent
	result *ResultDocument // nil when the attempt never reached terminal state

	sources          []model.SessionSourceFile
	skippedMalformed int
	skippedVersion   int
}

// firstEvent returns the earliest event by file order, if any.
func (v *attemptView) firstEvent() *ReviewEvent {
	if len(v.events) == 0 {
		return nil
	}
	return &v.events[0]
}

// terminalEvent returns the attempt.completed / attempt.failed event, if any.
func (v *attemptView) terminalEvent() *ReviewEvent {
	for i := len(v.events) - 1; i >= 0; i-- {
		if v.events[i].EventType == "attempt.completed" || v.events[i].EventType == "attempt.failed" {
			return &v.events[i]
		}
	}
	return nil
}

// isLive reports the design 5.2 rule: an attempt is live when it has no
// terminal state (no result.json, no terminal event) and its heartbeat is
// fresh. Anything else is not live, never guessed.
func (v *attemptView) isLive(now time.Time) bool {
	if v.result != nil || v.terminalEvent() != nil {
		return false
	}
	if v.meta == nil || v.meta.HeartbeatAt.IsZero() {
		return false
	}
	return now.Sub(v.meta.HeartbeatAt) <= model.LiveWindow
}

func payloadString(event *ReviewEvent, key string) string {
	if event.Payload == nil {
		return ""
	}
	value, ok := event.Payload[key].(string)
	if !ok {
		return ""
	}
	return value
}

func payloadInt(event *ReviewEvent, key string) (int64, bool) {
	if event.Payload == nil {
		return 0, false
	}
	switch value := event.Payload[key].(type) {
	case float64:
		return int64(value), true
	case int64:
		return value, true
	case int:
		return int64(value), true
	}
	return 0, false
}

func payloadFloat(event *ReviewEvent, key string) (float64, bool) {
	if event.Payload == nil {
		return 0, false
	}
	if value, ok := event.Payload[key].(float64); ok {
		return value, true
	}
	return 0, false
}

func payloadStringSlice(event *ReviewEvent, key string) []string {
	if event.Payload == nil {
		return nil
	}
	raw, ok := event.Payload[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// elapsedMs converts the writer's elapsed_seconds payload to milliseconds.
// Missing or malformed values yield 0 (unknown), never a fabricated number.
func elapsedMs(event *ReviewEvent) int64 {
	seconds, ok := payloadFloat(event, "elapsed_seconds")
	if !ok {
		if value, okAlt := payloadInt(event, "elapsed_seconds"); okAlt {
			return value * 1000
		}
		return 0
	}
	return int64(seconds * 1000)
}
