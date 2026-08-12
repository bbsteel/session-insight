package model

import "time"

// SourceFingerprintAlgorithm identifies how an authoritative adapter read was
// fingerprinted. The fingerprint covers the exact source bytes used to build
// Detail, RenderEvents, OriginGit, and Finalization; it is not a filesystem
// mtime and is unrelated to the renderer's positions/layout revision.
type SourceFingerprintAlgorithm string

const SourceFingerprintSHA256 SourceFingerprintAlgorithm = "sha256"

// SourceFingerprint identifies the immutable byte view parsed by one
// authoritative adapter read.
type SourceFingerprint struct {
	Algorithm SourceFingerprintAlgorithm `json:"algorithm"`
	Digest    string                     `json:"digest"`
	SizeBytes int64                      `json:"size_bytes"`
}

// SessionFinalizationState is the adapter's conclusion about session-level
// finalization/liveness. Unknown is required when an Agent records only turn
// boundaries: completing a turn does not finalize a resumable session.
type SessionFinalizationState string

const (
	SessionFinalized           SessionFinalizationState = "finalized"
	SessionLive                SessionFinalizationState = "live"
	SessionFinalizationUnknown SessionFinalizationState = "unknown"
)

func IsKnownSessionFinalizationState(state SessionFinalizationState) bool {
	switch state {
	case SessionFinalized, SessionLive, SessionFinalizationUnknown:
		return true
	default:
		return false
	}
}

// SessionEvidencePrecision is independent of Agent capability declarations.
// It describes one adapter-recorded session-state conclusion or signal.
type SessionEvidencePrecision string

const (
	SessionEvidenceExact       SessionEvidencePrecision = "exact"
	SessionEvidenceEstimated   SessionEvidencePrecision = "estimated"
	SessionEvidenceMissing     SessionEvidencePrecision = "missing"
	SessionEvidenceUnavailable SessionEvidencePrecision = "unavailable"
)

func IsKnownSessionEvidencePrecision(precision SessionEvidencePrecision) bool {
	switch precision {
	case SessionEvidenceExact, SessionEvidenceEstimated, SessionEvidenceMissing, SessionEvidenceUnavailable:
		return true
	default:
		return false
	}
}

// SessionEvidenceReasonCode is a stable adapter-envelope reason. Raw parser,
// filesystem, and process errors must never be used as reason codes.
type SessionEvidenceReasonCode string

const (
	ReasonSessionStateNotRecorded          SessionEvidenceReasonCode = "session_state_not_recorded"
	ReasonTurnMarkerNotSessionFinalization SessionEvidenceReasonCode = "turn_marker_not_session_finalization"
	ReasonTurnMarkerNotSessionLiveness     SessionEvidenceReasonCode = "turn_marker_not_session_liveness"
	ReasonSessionSignalTimestampInvalid    SessionEvidenceReasonCode = "session_signal_timestamp_invalid"
)

func IsKnownSessionEvidenceReasonCode(reason SessionEvidenceReasonCode) bool {
	switch reason {
	case ReasonSessionStateNotRecorded, ReasonTurnMarkerNotSessionFinalization,
		ReasonTurnMarkerNotSessionLiveness, ReasonSessionSignalTimestampInvalid:
		return true
	default:
		return false
	}
}

type SessionEvidenceAssessment struct {
	Precision  SessionEvidencePrecision  `json:"precision"`
	ReasonCode SessionEvidenceReasonCode `json:"reason_code,omitempty"`
}

func ExactSessionEvidence() SessionEvidenceAssessment {
	return SessionEvidenceAssessment{Precision: SessionEvidenceExact}
}

func NonExactSessionEvidence(precision SessionEvidencePrecision, reason SessionEvidenceReasonCode) SessionEvidenceAssessment {
	return SessionEvidenceAssessment{Precision: precision, ReasonCode: reason}
}

// SessionFinalizationSignalKind identifies the native record that informed a
// finalization assessment. Turn signals are intentionally distinct from
// session-level signals so downstream code cannot promote task_complete to a
// finalized session.
type SessionFinalizationSignalKind string

const (
	SessionSignalNone            SessionFinalizationSignalKind = "none"
	SessionSignalFinalized       SessionFinalizationSignalKind = "session_finalized"
	SessionSignalLive            SessionFinalizationSignalKind = "session_live"
	SessionSignalTurnOpen        SessionFinalizationSignalKind = "turn_open"
	SessionSignalTurnComplete    SessionFinalizationSignalKind = "turn_complete"
	SessionSignalTurnAborted     SessionFinalizationSignalKind = "turn_aborted"
	SessionSignalTurnsRolledBack SessionFinalizationSignalKind = "turns_rolled_back"
)

func IsKnownSessionFinalizationSignalKind(kind SessionFinalizationSignalKind) bool {
	switch kind {
	case SessionSignalNone, SessionSignalFinalized, SessionSignalLive,
		SessionSignalTurnOpen, SessionSignalTurnComplete,
		SessionSignalTurnAborted, SessionSignalTurnsRolledBack:
		return true
	default:
		return false
	}
}

// SessionFinalizationEvidence keeps the session-level conclusion separate
// from its native signal. SignalAssessment can be exact while Assessment is
// missing; for example, Codex exactly records task_complete, but that marker
// does not finalize a resumable session.
type SessionFinalizationEvidence struct {
	State            SessionFinalizationState      `json:"state"`
	Assessment       SessionEvidenceAssessment     `json:"assessment"`
	SignalKind       SessionFinalizationSignalKind `json:"signal_kind"`
	SignalRecordedAt *time.Time                    `json:"signal_recorded_at,omitempty"`
	SignalAssessment SessionEvidenceAssessment     `json:"signal_assessment"`
}

// IndexSnapshotEnvelope is the optional authoritative indexing contract.
// Every field is derived from one immutable source-byte view. SourceRevision
// is an opaque adapter revision (currently sha256:<digest> for Codex); it must
// never be compared with the integer positions/layout revision.
type IndexSnapshotEnvelope struct {
	Detail            *SessionDetail              `json:"detail"`
	RenderEvents      []RenderEvent               `json:"render_events"`
	SourceRevision    string                      `json:"source_revision"`
	SourceFingerprint SourceFingerprint           `json:"source_fingerprint"`
	OriginGit         *SessionGitOrigin           `json:"origin_git"`
	Finalization      SessionFinalizationEvidence `json:"finalization"`
}
