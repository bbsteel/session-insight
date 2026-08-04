package model

import "time"

// RecordCompletenessState is the mutually exclusive overall record status for
// one session snapshot. It is independent of Agent capability resolution.
type RecordCompletenessState string

const (
	RecordComplete          RecordCompletenessState = "complete"
	RecordDegraded          RecordCompletenessState = "degraded"
	RecordMetadataOnly      RecordCompletenessState = "metadata_only"
	RecordSourceMissing     RecordCompletenessState = "source_missing"
	RecordParserUnsupported RecordCompletenessState = "parser_unsupported"
)

// SourceFileState describes whether SI could read a particular source file.
type SourceFileState string

const (
	SourcePresent     SourceFileState = "present"
	SourceMissing     SourceFileState = "missing"
	SourceUnreadable  SourceFileState = "unreadable"
	SourceUnsupported SourceFileState = "unsupported"
)

// Stable source roles (cross-adapter). Not filenames.
// Each adapter maps its on-disk layout onto these roles; do not invent
// agent-specific role strings in the API. Prefer a precise role over "other".
const (
	SourceRolePrimaryTranscript = "primary_transcript"
	SourceRoleMetadata          = "metadata"
	SourceRoleEvents            = "events"
	SourceRoleUpdates           = "updates"
	SourceRoleToolResults       = "tool_results"
	SourceRoleCollaboration     = "collaboration"
	// Recovery is a crash/in-flight sidecar that can supersede the primary
	// (e.g. chrys session.recovery.json).
	SourceRoleRecovery = "recovery"
	// Snapshot is a point-in-time turn/session checkpoint file.
	SourceRoleSnapshot = "snapshot"
	// EditCache is agent-local file-edit mutation/cache blobs (e.g. chrys
	// mutations/). UI may collapse this group by default.
	SourceRoleEditCache = "edit_cache"
	// Other is a last resort for a real source that fits no stable role.
	// Adapters should rarely use it; prefer a precise role above.
	SourceRoleOther = "other"
)

// Warning severities.
const (
	WarningSeverityInfo    = "info"
	WarningSeverityWarning = "warning"
	WarningSeverityError   = "error"
)

// Warning impact domains (allow-list).
const (
	ImpactMetadata      = "metadata"
	ImpactReplay        = "replay"
	ImpactNavigation    = "navigation"
	ImpactTokens        = "tokens"
	ImpactTools         = "tools"
	ImpactDiff          = "diff"
	ImpactCollaboration = "collaboration"
	ImpactRealtime      = "realtime"
)

// First-batch stable warning codes (de-identified, localizable).
const (
	WarnMalformedRecordSkipped  = "malformed_record_skipped"
	WarnTruncatedRecord         = "truncated_record"
	WarnSidecarMissing          = "sidecar_missing"
	WarnSourceUnreadable        = "source_unreadable"
	WarnSourceChangedDuringRead = "source_changed_during_read"
	WarnUnsupportedSchema       = "unsupported_schema_revision"
	WarnIdentityMismatch        = "identity_mismatch"
	WarnTimestampInvalid        = "timestamp_invalid"
	WarnPartialToolResult       = "partial_tool_result"
	WarnPartialCollaboration    = "partial_collaboration_graph"
	WarnUnknownRecordIgnored    = "unknown_record_ignored"
)

// SessionSourceFile is one file SI consulted for a session snapshot.
type SessionSourceFile struct {
	Role      string          `json:"role"`
	Path      string          `json:"path"`
	State     SourceFileState `json:"state"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
	SizeBytes *int64          `json:"size_bytes,omitempty"`
}

// ParseWarning is an aggregated parse/read warning (not a raw I/O string).
type ParseWarning struct {
	Code                string   `json:"code"`
	Severity            string   `json:"severity"` // info | warning | error
	AffectsCompleteness bool     `json:"affects_completeness"`
	Impacts             []string `json:"impacts,omitempty"`
	Count               int      `json:"count"`
	SourceRole          string   `json:"source_role,omitempty"`
	FirstRecord         *int64   `json:"first_record,omitempty"`
}

// WarningSummary is the rolled-up warning counts for a snapshot.
type WarningSummary struct {
	Total        int            `json:"total"`
	Info         int            `json:"info"`
	Warning      int            `json:"warning"`
	Error        int            `json:"error"`
	ImpactCounts map[string]int `json:"impact_counts,omitempty"`
}

// SessionProvenance is the full record-completeness / provenance contract.
type SessionProvenance struct {
	State            RecordCompletenessState `json:"state"`
	ReasonCode       string                  `json:"reason_code,omitempty"`
	CapturedAt       time.Time               `json:"captured_at"`
	SourceUpdatedAt  *time.Time              `json:"source_updated_at,omitempty"`
	AdapterRevision  int                     `json:"adapter_revision"`
	Sources          []SessionSourceFile     `json:"sources"`
	WarningSummary   WarningSummary          `json:"warning_summary"`
	Warnings         []ParseWarning          `json:"warnings"`
	LastSuccessfulAt *time.Time              `json:"last_successful_at,omitempty"`
	MissingSince     *time.Time              `json:"missing_since,omitempty"`
}

// RecordStatus is the compact list-surface projection of provenance.
// Absolute source paths and raw warnings must not appear here.
type RecordStatus struct {
	State        RecordCompletenessState `json:"state"`
	WarningCount int                     `json:"warning_count"`
	CapturedAt   time.Time               `json:"captured_at"`
}

// CompactRecordStatus projects full provenance for list endpoints.
// Returns nil when p is nil (caller should omit the field / report unavailable).
func CompactRecordStatus(p *SessionProvenance) *RecordStatus {
	if p == nil {
		return nil
	}
	return &RecordStatus{
		State:        p.State,
		WarningCount: p.WarningSummary.Total,
		CapturedAt:   p.CapturedAt,
	}
}
