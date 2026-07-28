/**
 * Collaboration graph → timeline view-model normalization.
 *
 * Pure TypeScript: no React, DOM, SVG, Canvas, ECharts, theme, or i18n
 * imports. The hierarchy is derived from Delegation records (never a copied
 * parentId field) using the same deterministic canonical projection as the
 * frozen Go contract validation (internal/collaboration/validate.go):
 *
 *   - delegations sorted by (id, parent, child);
 *   - duplicate delegation IDs, unknown children, self-links, edges into the
 *     root, duplicate parent→child pairs, and cycle-closing edges are excluded
 *     from the canonical projection;
 *   - the first surviving delegation per child wins; extra relation evidence
 *     is preserved on the graph but not canonical;
 *   - a child whose parent invocation is missing attaches to the visible
 *     "Unlinked child Agents" group; its record is never discarded.
 *
 * Output order is deterministic: the root lane first, then a pre-order walk
 * with children ordered by launch time and stable ID, then the Unlinked group
 * (when non-empty) with its children ordered the same way.
 */

import type {
  AgentInvocationDTO,
  CollaborationGraphDTO,
  DelegationDTO,
  EvidenceState,
  ExecutionMode,
  FactEvidenceDTO,
  InvocationStatus,
  SourceAnchorDTO,
} from './types.js'

/** Synthetic lane ID for the visible "Unlinked child Agents" group. */
export const UNLINKED_GROUP_ID = 'collab:unlinked-group'

/** One closed activity span in epoch milliseconds. */
export interface ActivitySpan {
  startMs: number
  endMs: number
}

export interface TimelineInvocation {
  id: string
  /** Derived canonical parent: an invocation ID, UNLINKED_GROUP_ID, or null (root/group). */
  parentId: string | null
  label: string
  agentType: string
  roleLabel: string
  status: InvocationStatus
  startedAtMs: number | null
  endedAtMs: number | null
  timePrecision: FactEvidenceDTO
  contentPrecision: FactEvidenceDTO
  hasBackingSession: boolean
  /**
   * Closed activity spans. Live lanes and lanes with open boundaries emit no
   * span here; the layout engine derives effective geometry from the status,
   * boundaries, and the current-time input instead.
   */
  spans: ActivitySpan[]
  /** First known time anchor, used to place markers when the start is missing. */
  fallbackAnchorMs: number | null
  depth: number
  /** Synthetic grouping lane (the Unlinked group); renders no interval/markers. */
  isGroup: boolean
  /** Canonical delegation view, when this invocation is a linked child. */
  delegationId: string | null
  executionMode: ExecutionMode | null
  taskSummary: string | null
  triggerAnchor: SourceAnchorDTO | null
  resultAnchor: SourceAnchorDTO | null
}

export type TimelineRelationKind = 'launch' | 'result'

export interface TimelineRelation {
  id: string
  kind: TimelineRelationKind
  parentId: string
  childId: string
  /** Launch time for launch relations, result-return time for result relations. */
  atMs: number | null
  precision: EvidenceState
}

export interface TimelineModel {
  rootId: string
  invocations: TimelineInvocation[]
  relations: TimelineRelation[]
  unlinkedGroupId: string | null
  unlinkedCount: number
  domainStartMs: number
  domainEndMs: number
  /** True when any invocation is pending/running/waiting (needs live refresh). */
  live: boolean
  /** Count of delegations excluded from the canonical projection. */
  quarantinedCount: number
}

/** Percent-escapes one ID component exactly like the Go contract helpers. */
export function escapeIDComponent(s: string): string {
  return s.replace(/%/g, '%25').replace(/:/g, '%3A').replace(/>/g, '%3E')
}

/** Deterministic root invocation ID: <agent_type>:<root_session_id>:root. */
export function rootInvocationID(agentType: string, rootSessionID: string): string {
  return `${escapeIDComponent(agentType)}:${escapeIDComponent(rootSessionID)}:root`
}

function parseTimeMs(value: string | undefined): number | null {
  if (!value) return null
  const ms = Date.parse(value)
  return Number.isNaN(ms) ? null : ms
}

function anchorTimeMs(anchor: SourceAnchorDTO | undefined): number | null {
  return anchor ? parseTimeMs(anchor.timestamp) : null
}

function isLiveStatus(status: InvocationStatus): boolean {
  return status === 'pending' || status === 'running' || status === 'waiting'
}

interface CanonicalEdge {
  delegation: DelegationDTO
  parentId: string
  childId: string
}

/** Code-unit comparison matching Go's byte-order string sort. */
function cmpID(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

/**
 * Canonical projection of the delegation list, mirroring the frozen Go
 * validation order and quarantine rules. Returns the accepted edges plus the
 * number of excluded delegations.
 */
export function canonicalDelegations(
  graph: CollaborationGraphDTO,
  invocationIds: ReadonlySet<string>,
  rootId: string,
): { edges: CanonicalEdge[]; quarantinedCount: number } {
  const sorted = [...graph.delegations].sort(
    (a, b) =>
      cmpID(a.id, b.id) ||
      cmpID(a.parent_invocation_id, b.parent_invocation_id) ||
      cmpID(a.child_invocation_id, b.child_invocation_id),
  )
  const seenIDs = new Set<string>()
  const seenPairs = new Set<string>()
  const canonicalChild = new Set<string>()
  const parentChain = new Map<string, string>()
  const edges: CanonicalEdge[] = []
  let quarantinedCount = 0

  const createsCycle = (parent: string, child: string): boolean => {
    for (let cur: string | undefined = parent; cur !== undefined; ) {
      if (cur === child) return true
      cur = parentChain.get(cur)
    }
    return false
  }

  for (const d of sorted) {
    const parent = d.parent_invocation_id
    const child = d.child_invocation_id
    if (seenIDs.has(d.id)) {
      quarantinedCount++
      continue
    }
    seenIDs.add(d.id)
    if (!invocationIds.has(child)) {
      quarantinedCount++
      continue
    }
    if (parent === child || child === rootId) {
      quarantinedCount++
      continue
    }
    if (!invocationIds.has(parent)) {
      // Missing parent: not quarantined; the child attaches to the Unlinked group.
      continue
    }
    const pair = `${parent}\x00${child}`
    if (seenPairs.has(pair)) {
      quarantinedCount++
      continue
    }
    seenPairs.add(pair)
    if (canonicalChild.has(child)) {
      // Multiple parents: extra evidence preserved, never canonical.
      continue
    }
    if (createsCycle(parent, child)) {
      quarantinedCount++
      continue
    }
    parentChain.set(child, parent)
    canonicalChild.add(child)
    edges.push({ delegation: d, parentId: parent, childId: child })
  }
  return { edges, quarantinedCount }
}

/** Normalizes a contract-shaped graph into the deterministic timeline model. */
export function normalizeTimelineModel(graph: CollaborationGraphDTO): TimelineModel {
  // First occurrence wins for duplicate invocation IDs (Go: "first kept").
  const byId = new Map<string, AgentInvocationDTO>()
  for (const inv of graph.invocations) {
    if (!byId.has(inv.id)) byId.set(inv.id, inv)
  }
  const rootId = rootInvocationID(graph.root_agent_type, graph.root_session_id)
  const { edges, quarantinedCount } = canonicalDelegations(graph, new Set(byId.keys()), rootId)

  const edgeByChild = new Map<string, CanonicalEdge>()
  for (const e of edges) edgeByChild.set(e.childId, e)

  const startOf = (inv: AgentInvocationDTO): number | null => parseTimeMs(inv.started_at)
  const endOf = (inv: AgentInvocationDTO): number | null => parseTimeMs(inv.ended_at)

  // Deterministic sibling sort key: earliest known time, then stable ID.
  const sortKey = (inv: AgentInvocationDTO): number => {
    const edge = edgeByChild.get(inv.id)
    const times = [
      startOf(inv),
      edge ? anchorTimeMs(edge.delegation.trigger) : null,
      edge ? anchorTimeMs(edge.delegation.result) : null,
    ].filter((t): t is number => t !== null)
    return times.length > 0 ? Math.min(...times) : Number.POSITIVE_INFINITY
  }

  // Children adjacency from the canonical projection.
  const childrenOf = new Map<string, AgentInvocationDTO[]>()
  for (const e of edges) {
    const child = byId.get(e.childId)
    if (!child) continue
    const list = childrenOf.get(e.parentId) ?? []
    list.push(child)
    childrenOf.set(e.parentId, list)
  }
  for (const list of childrenOf.values()) {
    list.sort((a, b) => sortKey(a) - sortKey(b) || a.id.localeCompare(b.id))
  }

  const linkedIds = new Set(edgeByChild.keys())
  const unlinked = [...byId.values()]
    .filter((inv) => inv.id !== rootId && !linkedIds.has(inv.id))
    .sort((a, b) => sortKey(a) - sortKey(b) || a.id.localeCompare(b.id))

  const invocations: TimelineInvocation[] = []
  let live = false

  const toView = (
    inv: AgentInvocationDTO,
    parentId: string | null,
    depth: number,
  ): TimelineInvocation => {
    const edge = edgeByChild.get(inv.id)
    const startedAtMs = startOf(inv)
    const endedAtMs = endOf(inv)
    if (isLiveStatus(inv.status)) live = true
    const spans: ActivitySpan[] =
      startedAtMs !== null && endedAtMs !== null && endedAtMs > startedAtMs
        ? [{ startMs: startedAtMs, endMs: endedAtMs }]
        : []
    const parent = parentId ? byId.get(parentId) : undefined
    const anchorCandidates = [
      edge ? anchorTimeMs(edge.delegation.trigger) : null,
      edge ? anchorTimeMs(edge.delegation.result) : null,
      parent ? startOf(parent) : null,
    ].filter((t): t is number => t !== null)
    return {
      id: inv.id,
      parentId,
      label: inv.display_name || inv.role_label || inv.id,
      agentType: inv.agent_type,
      roleLabel: inv.role_label ?? '',
      status: inv.status,
      startedAtMs,
      endedAtMs,
      timePrecision: inv.time_precision,
      contentPrecision: inv.content_precision,
      hasBackingSession: inv.backing_session != null,
      spans,
      fallbackAnchorMs: anchorCandidates.length > 0 ? Math.min(...anchorCandidates) : null,
      depth,
      isGroup: false,
      delegationId: edge?.delegation.id ?? null,
      executionMode: edge?.delegation.execution_mode ?? null,
      taskSummary: edge?.delegation.task_summary || null,
      triggerAnchor: edge?.delegation.trigger ?? null,
      resultAnchor: edge?.delegation.result ?? null,
    }
  }

  // Pre-order walk from the root: root lane first, children by launch time + ID.
  const root = byId.get(rootId)
  if (root) {
    const walk = (inv: AgentInvocationDTO, parentId: string | null, depth: number) => {
      invocations.push(toView(inv, parentId, depth))
      for (const child of childrenOf.get(inv.id) ?? []) walk(child, inv.id, depth + 1)
    }
    walk(root, null, 0)
  }
  // Any invocation unreachable from the root but canonically linked (can only
  // happen with a missing root) still renders, attached under its parent chain.
  for (const e of edges) {
    if (invocations.some((v) => v.id === e.childId)) continue
    const child = byId.get(e.childId)
    if (!child) continue
    const walkOrphan = (inv: AgentInvocationDTO, parentId: string | null, depth: number) => {
      if (invocations.some((v) => v.id === inv.id)) return
      invocations.push(toView(inv, parentId, depth))
      for (const c of childrenOf.get(inv.id) ?? []) walkOrphan(c, inv.id, depth + 1)
    }
    walkOrphan(child, e.parentId, 0)
  }

  // Visible Unlinked group (localized label is applied by the component).
  let unlinkedGroupId: string | null = null
  if (unlinked.length > 0) {
    unlinkedGroupId = UNLINKED_GROUP_ID
    invocations.push({
      id: UNLINKED_GROUP_ID,
      parentId: null,
      label: UNLINKED_GROUP_ID,
      agentType: '',
      roleLabel: '',
      status: 'unknown',
      startedAtMs: null,
      endedAtMs: null,
      timePrecision: { state: 'not_applicable' },
      contentPrecision: { state: 'not_applicable' },
      hasBackingSession: false,
      spans: [],
      fallbackAnchorMs: null,
      depth: 0,
      isGroup: true,
      delegationId: null,
      executionMode: null,
      taskSummary: null,
      triggerAnchor: null,
      resultAnchor: null,
    })
    for (const inv of unlinked) invocations.push(toView(inv, UNLINKED_GROUP_ID, 1))
  }

  // Launch/result relations from canonical delegations. Result edges render
  // only when a result anchor or child completion is known (design §10.5).
  const relations: TimelineRelation[] = []
  for (const e of edges) {
    const child = byId.get(e.childId)
    const launchAt = anchorTimeMs(e.delegation.trigger) ?? (child ? startOf(child) : null)
    relations.push({
      id: `${e.delegation.id}#launch`,
      kind: 'launch',
      parentId: e.parentId,
      childId: e.childId,
      atMs: launchAt,
      precision: e.delegation.trigger?.precision.state ?? 'missing',
    })
    const resultAt = anchorTimeMs(e.delegation.result) ?? (child ? endOf(child) : null)
    if (resultAt !== null) {
      relations.push({
        id: `${e.delegation.id}#result`,
        kind: 'result',
        parentId: e.parentId,
        childId: e.childId,
        atMs: resultAt,
        precision: e.delegation.result?.precision.state ?? 'estimated',
      })
    }
  }

  // Time domain from every known time fact.
  const knownTimes: number[] = []
  for (const inv of byId.values()) {
    const s = startOf(inv)
    const e = endOf(inv)
    if (s !== null) knownTimes.push(s)
    if (e !== null) knownTimes.push(e)
  }
  for (const d of graph.delegations) {
    const t = anchorTimeMs(d.trigger)
    const r = anchorTimeMs(d.result)
    if (t !== null) knownTimes.push(t)
    if (r !== null) knownTimes.push(r)
  }
  const domainStartMs = knownTimes.length > 0 ? Math.min(...knownTimes) : 0
  const domainEndMs = knownTimes.length > 0 ? Math.max(...knownTimes) : 1

  return {
    rootId,
    invocations,
    relations,
    unlinkedGroupId,
    unlinkedCount: unlinked.length,
    domainStartMs,
    domainEndMs: Math.max(domainEndMs, domainStartMs + 1),
    live,
    quarantinedCount,
  }
}

/** Ancestor chain + self for the selected causal path. */
export function selectedPathIds(model: TimelineModel, selectedId: string | null): Set<string> {
  const path = new Set<string>()
  if (!selectedId) return path
  const byId = new Map(model.invocations.map((inv) => [inv.id, inv]))
  let cur = byId.get(selectedId)
  while (cur) {
    path.add(cur.id)
    cur = cur.parentId && cur.parentId !== UNLINKED_GROUP_ID ? byId.get(cur.parentId) : undefined
  }
  return path
}
