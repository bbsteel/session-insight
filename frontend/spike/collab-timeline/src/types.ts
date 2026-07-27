/**
 * NON-AUTHORITATIVE spike-only input shapes for the collaboration timeline
 * renderer spike. These mirror the proposed collaboration API only enough to
 * exercise layout and rendering. The gate coordinator, not this spike, freezes
 * the production contract.
 */

export type InvocationStatus =
  | 'pending'
  | 'running'
  | 'waiting'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'orphaned'
  | 'unknown'

export type EvidenceState = 'exact' | 'estimated' | 'missing' | 'not_applicable' | 'unsupported'

export interface ActivitySegment {
  startMs: number
  endMs: number
}

export interface SpikeInvocation {
  id: string
  parentId: string | null
  label: string
  status: InvocationStatus
  startedAtMs: number | null
  endedAtMs: number | null
  timePrecision: EvidenceState
  depth: number
  segments: ActivitySegment[]
}

export type RelationKind = 'launch' | 'result'

export interface SpikeRelation {
  id: string
  kind: RelationKind
  parentId: string
  childId: string
  /** Launch time for launch relations, result-return time for result relations. */
  atMs: number | null
  precision: EvidenceState
}

export interface SpikeDataset {
  name: string
  seed: number
  invocations: SpikeInvocation[]
  relations: SpikeRelation[]
  domainStartMs: number
  domainEndMs: number
  /** Fixed "current time" used to extend live intervals. Never Date.now(). */
  nowMs: number
}
