/**
 * Collaboration dock state helpers (pure; no React/DOM/fetch).
 *
 * The dock (src/components/CollaborationDock.tsx) classifies detail-endpoint
 * outcomes into its visible states and derives backing-session affordances
 * from the frozen contract payload. All capability distinctions come from
 * contract fields (backing_session presence, precision evidence, error
 * codes) — never from agent_type branching.
 */

import {
  rootInvocationID,
  UNLINKED_GROUP_ID,
} from './normalizeTimelineModel.js'
import type {
  AgentInvocationDTO,
  BackingSessionRefDTO,
  CollaborationGraphDTO,
} from './types.js'

/** Dock-level classification of a detail-endpoint error code. */
export type CollaborationErrorKind =
  | 'unsupported'
  | 'not_indexed'
  | 'session_missing'
  | 'generic'

export function classifyCollaborationError(code: string): CollaborationErrorKind {
  switch (code) {
    case 'collaboration_unsupported':
      return 'unsupported'
    case 'collaboration_not_indexed':
      return 'not_indexed'
    case 'session_not_found':
      return 'session_missing'
    default:
      return 'generic'
  }
}

/** Invocations other than the root lane and the synthetic Unlinked group. */
export function childInvocations(graph: CollaborationGraphDTO): AgentInvocationDTO[] {
  const rootId = rootInvocationID(graph.root_agent_type, graph.root_session_id)
  return graph.invocations.filter((inv) => inv.id !== rootId && inv.id !== UNLINKED_GROUP_ID)
}

/**
 * Exact zero-child graphs are a first-class "empty" state, distinct from
 * "unsupported" (no reader) and "not indexed" (reader, no stored graph).
 */
export function isGraphEmpty(graph: CollaborationGraphDTO): boolean {
  return childInvocations(graph).length === 0
}

/**
 * Backing-session reference for one invocation, or null. The reference is the
 * only thing that gates "open the standalone child session" affordances; an
 * invocation without it must still render normally.
 */
export function backingSessionOf(
  graph: CollaborationGraphDTO,
  invocationId: string,
): BackingSessionRefDTO | null {
  const inv = graph.invocations.find((candidate) => candidate.id === invocationId)
  return inv?.backing_session ?? null
}

/** True when the invocation exists in this revision of the graph. */
export function hasInvocation(graph: CollaborationGraphDTO, invocationId: string): boolean {
  return graph.invocations.some((inv) => inv.id === invocationId)
}
