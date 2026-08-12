package model

import "time"

// ChangeRequestCreationEvidence is an adapter-recorded, local-only fact that
// a successful tool invocation created the referenced Pull/Merge Request.
// It does not imply that hosted metadata or diff content has been fetched.
type ChangeRequestCreationEvidence struct {
	EvidenceID     string                 `json:"evidence_id"`
	Reference      ChangeRequestReference `json:"reference"`
	CommandKind    string                 `json:"command_kind"`
	ToolName       string                 `json:"tool_name"`
	EventID        string                 `json:"event_id"`
	ToolCallID     string                 `json:"tool_call_id,omitempty"`
	TurnIndex      int                    `json:"turn_index"`
	InvocationID   string                 `json:"invocation_id,omitempty"`
	RecordedAt     time.Time              `json:"recorded_at"`
	SourceRevision string                 `json:"source_revision"`
	Assessment     GitEvidenceAssessment  `json:"assessment"`
}

// ChangeRequestCreationSessionMatch is the reverse-lookup projection. Session
// identity comes from the local index; no hosted-provider request is required.
type ChangeRequestCreationSessionMatch struct {
	RootAgentType string                        `json:"root_agent_type"`
	RootSessionID string                        `json:"root_session_id"`
	Evidence      ChangeRequestCreationEvidence `json:"evidence"`
}
