package collaboration

import (
	"time"

	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// EvidenceState is the field-level precision state for one collaboration
// fact. It is a type alias of the capability contract state so collaboration
// evidence and capability declarations share one vocabulary and one type
// identity; do not define a parallel enum.
type EvidenceState = capability.CapabilityState

const (
	EvidenceExact         = capability.CapabilityExact
	EvidenceEstimated     = capability.CapabilityEstimated
	EvidenceMissing       = capability.CapabilityMissing
	EvidenceNotApplicable = capability.CapabilityNotApplicable
	EvidenceUnsupported   = capability.CapabilityUnsupported
)

// Machine-readable, stable, non-localized reason codes for collaboration
// facts that are not exact. Frontend i18n maps them to display copy.
const (
	// ReasonSourceNotRecorded: the source never persisted this fact.
	ReasonSourceNotRecorded = "source_not_recorded"
	// ReasonFIFOJoinHeuristic: the fact was inferred by a first-in-first-out
	// join heuristic (Claude ToolResult → invocation link) rather than a
	// stable ID join.
	ReasonFIFOJoinHeuristic = "fifo_join_heuristic"
	// ReasonAggregateWindow: content attribution is an estimated aggregate
	// time window, never exact child content (Copilot lifecycle-only shape).
	ReasonAggregateWindow = "aggregate_window"
	// ReasonCompletionNotRecorded: a start was observed but no completion
	// was persisted before the Session stopped being live.
	ReasonCompletionNotRecorded = "completion_not_recorded"
	// ReasonStaleGraphRetained: indexing was interrupted; the graph is the
	// last complete indexed revision, not a fresh parse.
	ReasonStaleGraphRetained = "stale_graph_retained"
)

// FactEvidence is the precision state of one collaboration fact. Every
// non-exact state requires a ReasonCode.
type FactEvidence struct {
	State      EvidenceState `json:"state"`
	ReasonCode string        `json:"reason_code,omitempty"`
}

// ExactFact returns a FactEvidence with state exact and no reason code.
func ExactFact() FactEvidence {
	return FactEvidence{State: EvidenceExact}
}

// InvocationStatus is the normalized lifecycle status of one invocation.
// Status lives only on AgentInvocation; Delegation has no status field.
type InvocationStatus string

const (
	StatusPending   InvocationStatus = "pending"
	StatusRunning   InvocationStatus = "running"
	StatusWaiting   InvocationStatus = "waiting"
	StatusCompleted InvocationStatus = "completed"
	StatusFailed    InvocationStatus = "failed"
	StatusCancelled InvocationStatus = "cancelled"
	// StatusOrphaned: a start was observed but completion is absent after
	// the Session is no longer live.
	StatusOrphaned InvocationStatus = "orphaned"
	// StatusUnknown is a first-class outcome. It is preferred over
	// inferring success from a closed file and is never omitted.
	StatusUnknown InvocationStatus = "unknown"
)

// IsKnownStatus reports whether s is a defined InvocationStatus.
func IsKnownStatus(s InvocationStatus) bool {
	switch s {
	case StatusPending, StatusRunning, StatusWaiting, StatusCompleted,
		StatusFailed, StatusCancelled, StatusOrphaned, StatusUnknown:
		return true
	default:
		return false
	}
}

// ExecutionMode records how the parent ran the child, when the source
// records it. ExecutionUnknown is used when the source does not record it.
type ExecutionMode string

const (
	ExecutionBlocking   ExecutionMode = "blocking"
	ExecutionBackground ExecutionMode = "background"
	ExecutionUnknown    ExecutionMode = "unknown"
)

// IsKnownExecutionMode reports whether m is a defined ExecutionMode.
func IsKnownExecutionMode(m ExecutionMode) bool {
	switch m {
	case ExecutionBlocking, ExecutionBackground, ExecutionUnknown:
		return true
	default:
		return false
	}
}

// UnlinkedGroupLabel names the visible group that holds child invocations
// whose parent evidence is missing. Their transcripts are preserved.
const UnlinkedGroupLabel = "Unlinked child Agents"

// BackingSessionRef references an Agent-native standalone Session that backs
// an invocation. Presence enables "View child Agent record" (and, where the
// Agent supports it, native resume/delete); absence forbids the UI from
// implying independent Session behavior for the invocation.
type BackingSessionRef struct {
	AgentType string `json:"agent_type"`
	SessionID string `json:"session_id"`
}

// Source identity kinds: which native ID material anchors an invocation's
// identity. Accepted native IDs per source (frozen contract):
//   - Codex: payload.id (rollout file stem kept in Attributes)
//   - Chrys: parent_provider_call_id (fallback: unparsed invocation_id)
//   - Copilot: toolCallId
//   - Claude: agentId (transcript filename id = meta.json agentId)
const (
	IdentityRootSession    = "root_session"
	IdentityPayloadID      = "payload_id"
	IdentityProviderCallID = "provider_call_id"
	IdentityToolCallID     = "tool_call_id"
	IdentityAgentID        = "agent_id"
)

// SourceIdentity is the raw native identity material behind an invocation
// ID, kept for diagnostics and conformance. NativeID is the strongest
// native stable ID; Attributes holds supporting material (for example the
// Codex rollout file stem).
type SourceIdentity struct {
	Kind       string            `json:"kind"`
	NativeID   string            `json:"native_id"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SourceAnchor locates one observable fact (launch, result) in the parent
// replay without inventing global sequence numbers. The strongest available
// anchor wins: stable event/tool-call ID > stable turn index > exact
// timestamp > estimated time window.
type SourceAnchor struct {
	AgentType  string       `json:"agent_type"`
	SessionID  string       `json:"session_id"`
	EventID    string       `json:"event_id,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	TurnIndex  *int         `json:"turn_index,omitempty"`
	Timestamp  *time.Time   `json:"timestamp,omitempty"`
	Precision  FactEvidence `json:"precision"`
}

// AgentInvocation is one bounded execution of a main or child Agent within
// the selected Session's work. It may be backed by the root Session, by
// another standalone native Session, by an embedded transcript, or only by
// lifecycle evidence.
type AgentInvocation struct {
	// ID is the native stable source ID, namespaced by root Agent type and
	// root Session ID (see identity.go).
	ID string `json:"id"`
	// DisplayName falls back to a synthesized role/tool label when the
	// source records no name; it is never empty.
	DisplayName string `json:"display_name"`
	// AgentType is the registered adapter ID of the invocation's Agent.
	AgentType string `json:"agent_type"`
	// RoleLabel is source-provided display data with an open vocabulary.
	// It is never a vendor-specific enum.
	RoleLabel string `json:"role_label,omitempty"`
	// Status is the normalized invocation status; unknown is first-class.
	Status InvocationStatus `json:"status"`
	// StartedAt and EndedAt are present only with evidence. Missing
	// boundaries are represented by TimePrecision plus Status, never by
	// guessed timestamps.
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// TimePrecision is the evidence state for the timing fields as a whole.
	TimePrecision FactEvidence `json:"time_precision"`
	// ContentPrecision is the evidence state for content availability:
	// full transcript (standalone/embedded), estimated aggregate window
	// (lifecycle-only), or unavailable. A parent-stream window is never
	// presented as exact child content.
	ContentPrecision FactEvidence `json:"content_precision"`
	// BackingSession is present only for native standalone child sessions.
	BackingSession *BackingSessionRef `json:"backing_session,omitempty"`
	// SourceIdentity keeps the raw native identity material for
	// diagnostics and conformance.
	SourceIdentity SourceIdentity `json:"source_identity"`
}

// DelegationEvidence carries per-fact precision for the causal relation.
type DelegationEvidence struct {
	Trigger FactEvidence `json:"trigger"`
	Timing  FactEvidence `json:"timing"`
	Task    FactEvidence `json:"task"`
	Result  FactEvidence `json:"result"`
}

// Delegation is the causal evidence that one invocation launched another.
// It records more than topology: launch anchor, delegated task when
// observable, timing, execution mode, and result anchor when observable,
// each with per-fact precision. It carries no Status field.
type Delegation struct {
	// ID is deterministic, derived from the parent and child invocation
	// IDs (see DelegationIDFor).
	ID string `json:"id"`
	// ParentInvocationID is the one canonical causal parent in V1. Extra
	// relation evidence stays on the graph and is never discarded.
	ParentInvocationID string `json:"parent_invocation_id"`
	// ChildInvocationID references an existing invocation.
	ChildInvocationID string `json:"child_invocation_id"`
	// Trigger and Result are optional anchors with per-fact precision.
	Trigger *SourceAnchor `json:"trigger,omitempty"`
	Result  *SourceAnchor `json:"result,omitempty"`
	// TaskSummary is present only when the source records a summary (for
	// example Copilot delegation arguments). It is never AI-generated, and
	// full delegated prompts or results are never stored here.
	TaskSummary string `json:"task_summary,omitempty"`
	// ExecutionMode is unknown when the source does not record it.
	ExecutionMode ExecutionMode      `json:"execution_mode"`
	Evidence      DelegationEvidence `json:"evidence"`
}

// CollaborationGraph is the normalized collaboration structure for one
// root Session revision. The data model supports arbitrary depth even when
// a UI presentation collapses it.
type CollaborationGraph struct {
	RootAgentType string `json:"root_agent_type"`
	RootSessionID string `json:"root_session_id"`
	// Revision identifies the indexed graph revision; an interrupted parse
	// retains the last complete revision instead of writing an empty graph.
	Revision     int64             `json:"revision"`
	Completeness FactEvidence      `json:"completeness"`
	Invocations  []AgentInvocation `json:"invocations"`
	Delegations  []Delegation      `json:"delegations"`
}
