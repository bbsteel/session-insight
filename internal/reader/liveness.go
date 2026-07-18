package reader

import "github.com/bbsteel/session-insight/internal/model"

// IsSessionLive resolves the common live-session contract while preserving
// each agent's native source of truth. The timestamp window is a cheap upper
// bound: stale sessions never require agent-specific I/O. Within that window,
// readers may refine the result through a lightweight heartbeat/lock/registry
// capability. Readers without it retain the timestamp heuristic.
//
// Errors deliberately degrade to the timestamp fallback. A transient unreadable
// heartbeat or lock file should not make a genuinely active session disappear.
func IsSessionLive(source any, session model.Session) bool {
	if !model.IsSessionLive(session.UpdatedAt) {
		return false
	}

	if provider, ok := source.(SessionLivenessProvider); ok {
		if live, err := provider.SessionLive(session.ID); err == nil {
			return live
		}
	}
	return true
}
