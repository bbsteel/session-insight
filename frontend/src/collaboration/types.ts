/**
 * Timeline-local DTO types mirroring the frozen `internal/collaboration`
 * JSON contract (see internal/collaboration/types.go). Field names match the
 * Go JSON tags exactly so a CollaborationGraphDTO can be produced by JSON.parse
 * of the contract payload (including the committed golden fixtures).
 *
 * These types are owned by the timeline core. The later integration task owns
 * the API mapper; do not add fetch logic here.
 */

/** Field-level precision state; one shared vocabulary with the capability contract. */
export type EvidenceState = 'exact' | 'estimated' | 'missing' | 'not_applicable' | 'unsupported'

/** Normalized invocation status; `unknown` is first-class. */
export type InvocationStatus =
  | 'pending'
  | 'running'
  | 'waiting'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'orphaned'
  | 'unknown'

export type ExecutionMode = 'blocking' | 'background' | 'unknown'

export interface FactEvidenceDTO {
  state: EvidenceState
  reason_code?: string
}

export interface SourceAnchorDTO {
  agent_type: string
  session_id: string
  event_id?: string
  tool_call_id?: string
  turn_index?: number
  timestamp?: string
  precision: FactEvidenceDTO
}

export interface BackingSessionRefDTO {
  agent_type: string
  session_id: string
}

export interface SourceIdentityDTO {
  kind: string
  native_id: string
  attributes?: Record<string, string>
}

export interface AgentInvocationDTO {
  id: string
  display_name: string
  agent_type: string
  role_label?: string
  status: InvocationStatus
  started_at?: string
  ended_at?: string
  time_precision: FactEvidenceDTO
  content_precision: FactEvidenceDTO
  backing_session?: BackingSessionRefDTO
  source_identity: SourceIdentityDTO
}

export interface DelegationEvidenceDTO {
  trigger: FactEvidenceDTO
  timing: FactEvidenceDTO
  task: FactEvidenceDTO
  result: FactEvidenceDTO
}

export interface DelegationDTO {
  id: string
  parent_invocation_id: string
  child_invocation_id: string
  trigger?: SourceAnchorDTO
  result?: SourceAnchorDTO
  task_summary?: string
  execution_mode: ExecutionMode
  evidence: DelegationEvidenceDTO
}

export interface CollaborationGraphDTO {
  root_agent_type: string
  root_session_id: string
  revision: number
  completeness: FactEvidenceDTO
  invocations: AgentInvocationDTO[]
  delegations: DelegationDTO[]
}
