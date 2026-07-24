package capability

// SessionCapabilityStatus is the resolved availability of one capability for
// a single session. It is independent of the static Agent declaration and of
// current action eligibility (buttons).
type SessionCapabilityStatus struct {
	State      CapabilityState `json:"state"`
	ReasonCode string          `json:"reason_code,omitempty"`
	DetailKey  string          `json:"detail_key,omitempty"`
}

// ActionAvailability describes whether a mutation action is currently eligible.
// It is advisory: DELETE and stop endpoints always revalidate before acting.
type ActionAvailability string

const (
	// ActionAvailable: the action is currently eligible based on cheap checks.
	ActionAvailable ActionAvailability = "available"
	// ActionUnavailable: the action is not eligible (unsupported, live, etc.).
	ActionUnavailable ActionAvailability = "unavailable"
	// ActionCheckRequired: eligibility needs a runtime check (e.g. exact PIDs)
	// that is intentionally deferred out of GET session.
	ActionCheckRequired ActionAvailability = "runtime_check_required"
)

// SessionActionStatus is current eligibility for resume / delete / terminate.
type SessionActionStatus struct {
	Availability ActionAvailability `json:"availability"`
	ReasonCode   string             `json:"reason_code,omitempty"`
}

// SessionLivenessStatus separates IsLive quality from the realtime capability.
// IsLive remains a boolean for existing callers; State/ReasonCode explain how
// that boolean was obtained (exact heartbeat vs timestamp heuristic).
type SessionLivenessStatus struct {
	IsLive     bool            `json:"is_live"`
	State      CapabilityState `json:"state"`
	ReasonCode string          `json:"reason_code,omitempty"`
}

// SessionCapabilities is the full per-session resolution payload nested under
// GET /api/sessions/{id} as agent_capabilities.
type SessionCapabilities struct {
	AgentType       string                                   `json:"agent_type"`
	AdapterRevision int                                      `json:"adapter_revision"`
	Status          map[CapabilityID]SessionCapabilityStatus `json:"status"`
	Actions         map[CapabilityID]SessionActionStatus     `json:"actions,omitempty"`
	Liveness        SessionLivenessStatus                    `json:"liveness"`
}

// ActionableIDs are the capability IDs that may appear in SessionCapabilities.Actions.
func ActionableIDs() []CapabilityID {
	return []CapabilityID{
		CapabilityResume,
		CapabilityDelete,
		CapabilityTerminate,
	}
}

// IsActionable reports whether id may carry a SessionActionStatus entry.
func IsActionable(id CapabilityID) bool {
	switch id {
	case CapabilityResume, CapabilityDelete, CapabilityTerminate:
		return true
	default:
		return false
	}
}

// Stable, non-localized reason codes for resolved session status and actions.
// UI copy is owned by frontend i18n later; do not expose raw I/O errors here.
const (
	ReasonSourceNotRecorded     = "source_not_recorded"
	ReasonSessionNotFinalized   = "session_not_finalized"
	ReasonResumeIDMissing       = "resume_id_missing"
	ReasonTimestampHeuristic    = "timestamp_heuristic"
	ReasonRevisionUnavailable   = "revision_unavailable"
	ReasonSessionRunning        = "session_running"
	ReasonSessionNotLive        = "session_not_live"
	ReasonRuntimeCheckRequired  = "runtime_check_required"
	ReasonExactPIDUnavailable   = "exact_pid_unavailable"
	ReasonAdapterNotImplemented = "adapter_not_implemented"
	ReasonConceptAbsent         = "concept_absent"
	ReasonPlatformNotSupported  = "platform_not_supported"
)

// Missing constructs a session-level missing status. Prefer this over free-form
// literals so reason codes stay centralized.
func Missing(reasonCode string) SessionCapabilityStatus {
	return SessionCapabilityStatus{
		State:      CapabilityMissing,
		ReasonCode: reasonCode,
	}
}

// SessionExact is resolved exact with no reason.
func SessionExact() SessionCapabilityStatus {
	return SessionCapabilityStatus{State: CapabilityExact}
}

// SessionEstimated is resolved estimated with a reason code.
func SessionEstimated(reasonCode string) SessionCapabilityStatus {
	return SessionCapabilityStatus{
		State:      CapabilityEstimated,
		ReasonCode: reasonCode,
	}
}

// SessionFromStatic copies a static declaration into a resolved status.
// It does not allow inventing missing from static.
func SessionFromStatic(d CapabilityDeclaration) SessionCapabilityStatus {
	// CapabilityDeclaration and SessionCapabilityStatus share the same field
	// layout (state / reason / detail); convert rather than re-list fields.
	return SessionCapabilityStatus(d)
}

// ActionAvailableStatus builds an available action entry.
func ActionAvailableStatus() SessionActionStatus {
	return SessionActionStatus{Availability: ActionAvailable}
}

// ActionUnavailableStatus builds an unavailable action entry.
func ActionUnavailableStatus(reasonCode string) SessionActionStatus {
	return SessionActionStatus{
		Availability: ActionUnavailable,
		ReasonCode:   reasonCode,
	}
}

// ActionCheckRequiredStatus builds a runtime_check_required action entry.
func ActionCheckRequiredStatus() SessionActionStatus {
	return SessionActionStatus{
		Availability: ActionCheckRequired,
		ReasonCode:   ReasonRuntimeCheckRequired,
	}
}
