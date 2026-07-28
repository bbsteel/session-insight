/**
 * Deterministic synthetic datasets for the production collaboration timeline
 * tests, benchmark, and browser harness.
 *
 * This is a production-test adapter: it emits contract-shaped
 * CollaborationGraphDTO payloads (the same shape as the frozen
 * internal/collaboration JSON contract) and never imports spike-only types.
 * All randomness comes from mulberry32(seed). No Date.now(), no Math.random().
 *
 * Every dataset includes the edge cases the design requires:
 * - recursive depth four (a guaranteed chain);
 * - running, waiting, failed, orphaned, and unknown states;
 * - missing and estimated start/end boundaries;
 * - an hours-long root containing seconds-long children;
 * - dense parallel starts (bursts);
 * - English and Chinese labels (some deliberately long);
 * - backed (standalone Session) and embedded/lifecycle-only shapes.
 *
 * withActivitySpans enriches a normalized model with synthetic sub-spans so
 * the layout benchmark reproduces the accepted spike segment counts (the V1
 * contract carries one time span per invocation; per-tool activity ticks are
 * a future data source the LOD path must already absorb).
 */

import { normalizeTimelineModel, type TimelineModel } from '../../../src/collaboration/normalizeTimelineModel.js'
import type {
  AgentInvocationDTO,
  CollaborationGraphDTO,
  DelegationDTO,
  EvidenceState,
  FactEvidenceDTO,
  InvocationStatus,
} from '../../../src/collaboration/types.js'

export type Rng = () => number

export function mulberry32(seed: number): Rng {
  let a = seed >>> 0
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export function randInt(rng: Rng, minInclusive: number, maxInclusive: number): number {
  return minInclusive + Math.floor(rng() * (maxInclusive - minInclusive + 1))
}

export function pick<T>(rng: Rng, items: readonly T[]): T {
  return items[Math.floor(rng() * items.length)]
}

/** Log-uniform between min and max: many small values, few large ones. */
export function logUniform(rng: Rng, min: number, max: number): number {
  const v = Math.exp(Math.log(min) + rng() * (Math.log(max) - Math.log(min)))
  return Math.min(max, Math.max(min, v))
}

/** FNV-1a 32-bit hash of a string, hex encoded. Used as the fixture hash. */
export function fnv1a(input: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(16).padStart(8, '0')
}

export interface DatasetSpec {
  name: string
  lanes: number
  segments: number
  relations: number
  seed: number
}

/** Same scales as the accepted spike benchmark. */
export const DATASET_SPECS: Record<string, DatasetSpec> = {
  typical: { name: 'typical', lanes: 30, segments: 300, relations: 100, seed: 0xc0a101 },
  large: { name: 'large', lanes: 100, segments: 1000, relations: 350, seed: 0xc0a102 },
  stress: { name: 'stress', lanes: 200, segments: 2000, relations: 500, seed: 0xc0a103 },
}

const SECOND = 1000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE
/** Hours-long root span. */
const ROOT_SPAN_MS = 3 * HOUR
/** Fixed epoch base so ISO timestamps are deterministic. */
const EPOCH_BASE_MS = Date.parse('2026-01-01T00:00:00Z')

const EN_ROLES = [
  'reviewer',
  'researcher',
  'planner',
  'test-runner',
  'code-review',
  'docs-writer',
  'general-purpose',
  'very-long-role-label-for-overflow-checking',
] as const
const ZH_ROLES = ['审查代理', '研究助手', '规划器', '测试运行器', '代码评审', '文档撰写', '通用代理', '数据管道批处理'] as const

function makeLabel(rng: Rng, index: number): string {
  return `#${index} ${pick(rng, EN_ROLES)} · ${pick(rng, ZH_ROLES)}`
}

function fact(state: EvidenceState, reason?: string): FactEvidenceDTO {
  return reason ? { state, reason_code: reason } : { state }
}

function rollPrecision(rng: Rng): EvidenceState {
  const r = rng()
  if (r < 0.7) return 'exact'
  if (r < 0.9) return 'estimated'
  return 'missing'
}

function rollStatus(rng: Rng): InvocationStatus {
  const r = rng()
  if (r < 0.03) return 'running'
  if (r < 0.05) return 'waiting'
  if (r < 0.1) return 'failed'
  if (r < 0.14) return 'orphaned'
  if (r < 0.18) return 'unknown'
  if (r < 0.2) return 'cancelled'
  if (r < 0.22) return 'pending'
  return 'completed'
}

/** Statuses that must appear at least once in every dataset. */
const REQUIRED_STATUSES: InvocationStatus[] = ['running', 'waiting', 'failed', 'orphaned', 'unknown']

const iso = (ms: number): string => new Date(EPOCH_BASE_MS + ms).toISOString()

export interface SyntheticGraph {
  spec: DatasetSpec
  seed: number
  graph: CollaborationGraphDTO
  /** Fixed "current time" (offset from EPOCH_BASE_MS) for live intervals. */
  nowMs: number
}

interface PendingNode {
  id: string
  parentId: string | null
  label: string
  status: InvocationStatus
  startedAtMs: number | null
  endedAtMs: number | null
  precision: EvidenceState
  depth: number
}

/** Generates a deterministic contract-shaped collaboration graph. */
export function generateGraph(spec: DatasetSpec, seedOffset = 0): SyntheticGraph {
  const rng = mulberry32(spec.seed + seedOffset)
  const seed = spec.seed + seedOffset
  const agentType = 'synthetic'
  const rootSessionId = `${spec.name}-${seed.toString(16)}`
  const rootId = `${agentType}:${rootSessionId}:root`
  const domainStart = 0
  const domainEnd = ROOT_SPAN_MS
  const nowMs = domainEnd

  const nodes: PendingNode[] = []
  const root: PendingNode = {
    id: rootId,
    parentId: null,
    label: `Main Agent 主代理 · ${spec.name}`,
    status: 'completed',
    startedAtMs: domainStart,
    endedAtMs: domainEnd,
    precision: 'exact',
    depth: 0,
  }
  nodes.push(root)

  // Guaranteed depth-4 chain: root -> chain-1 -> chain-2 -> chain-3 -> chain-4.
  let chainParent = root
  for (let depth = 1; depth <= 4; depth++) {
    const parentStart = chainParent.startedAtMs ?? domainStart
    const start = parentStart + depth * 2 * MINUTE
    const dur = (5 - depth) * 8 * MINUTE
    const node: PendingNode = {
      id: `${agentType}:${rootSessionId}:child:chain-${depth}`,
      parentId: chainParent.id,
      label: `#chain-${depth} nested-worker · 嵌套 worker 第${depth}层`,
      status: depth === 4 ? 'failed' : 'completed',
      startedAtMs: start,
      endedAtMs: start + dur,
      precision: 'exact',
      depth,
    }
    nodes.push(node)
    chainParent = node
  }

  // Remaining lanes. Burst scheduling forces dense parallel starts.
  const burstCount = Math.max(2, Math.floor(spec.lanes / 25))
  const bursts: Array<{ parentId: string; atMs: number; remaining: number }> = []
  for (let b = 0; b < burstCount; b++) {
    bursts.push({ parentId: rootId, atMs: randInt(rng, 10, 170) * MINUTE, remaining: randInt(rng, 3, 6) })
  }

  let forcedStatusIndex = 0
  while (nodes.length < spec.lanes) {
    const i = nodes.length
    let parent: PendingNode
    let startMs: number
    const burst = bursts.find((b) => b.remaining > 0)
    if (burst) {
      parent = nodes.find((n) => n.id === burst.parentId) ?? root
      burst.remaining--
      startMs = burst.atMs + randInt(rng, -1500, 1500)
    } else {
      const candidates = nodes.filter(
        (n) => n.depth < 4 && n.startedAtMs !== null && n.endedAtMs !== null,
      )
      parent = candidates[Math.floor(Math.pow(rng(), 1.8) * candidates.length)] ?? root
      const pStart = parent.startedAtMs ?? domainStart
      const pEnd = parent.endedAtMs ?? domainEnd
      startMs = pStart + rng() * Math.max(1, (pEnd - pStart) * 0.9)
    }

    let status: InvocationStatus
    if (forcedStatusIndex < REQUIRED_STATUSES.length) {
      status = REQUIRED_STATUSES[forcedStatusIndex++]
    } else {
      status = rollStatus(rng)
    }

    const precision = rollPrecision(rng)
    const parentEnd = parent.endedAtMs ?? domainEnd
    const maxDur = Math.max(10 * SECOND, Math.min(20 * MINUTE, parentEnd - startMs))
    const dur = logUniform(rng, 5 * SECOND, maxDur)

    const live = status === 'running' || status === 'waiting' || status === 'pending'
    const missingStart = precision === 'missing' && rng() < 0.5
    const startedAtMs = missingStart ? null : startMs
    let endedAtMs: number | null
    if (live) {
      endedAtMs = null
    } else if (status === 'orphaned' || status === 'unknown') {
      endedAtMs = rng() < 0.6 ? null : startMs + dur
    } else {
      endedAtMs = startMs + dur
    }

    nodes.push({
      id: `${agentType}:${rootSessionId}:child:inv-${i}`,
      parentId: parent.id,
      label: makeLabel(rng, i),
      status,
      startedAtMs,
      endedAtMs,
      precision,
      depth: parent.depth + 1,
    })
  }

  const childId = (n: PendingNode): string => n.id.split(':child:')[1] ?? n.id

  const invocations: AgentInvocationDTO[] = nodes.map((n, idx) => {
    const isRoot = n.parentId === null
    const backed = !isRoot && idx % 7 === 3 // subset backed by standalone Sessions
    const lifecycleOnly = !isRoot && !backed && idx % 5 === 4 // estimated content window
    return {
      id: n.id,
      display_name: n.label,
      agent_type: agentType,
      role_label: isRoot ? '' : makeRoleSuffix(rng),
      status: n.status,
      ...(n.startedAtMs !== null ? { started_at: iso(n.startedAtMs) } : {}),
      ...(n.endedAtMs !== null ? { ended_at: iso(n.endedAtMs) } : {}),
      time_precision: n.precision === 'exact' ? fact('exact') : fact(n.precision, 'source_not_recorded'),
      content_precision: isRoot
        ? fact('exact')
        : lifecycleOnly
          ? fact('estimated', 'aggregate_window')
          : fact('exact'),
      ...(backed ? { backing_session: { agent_type: agentType, session_id: `backing-${childId(n)}` } } : {}),
      source_identity: isRoot
        ? { kind: 'root_session', native_id: rootSessionId }
        : { kind: 'tool_call_id', native_id: childId(n) },
    }
  })

  // One canonical delegation per non-root invocation with anchors whose
  // precision follows the invocation timing evidence.
  const delegations: DelegationDTO[] = []
  for (const n of nodes) {
    if (n.parentId === null) continue
    const hasTrigger = n.precision !== 'missing' || rng() < 0.5
    const hasResult = n.endedAtMs !== null && (n.precision === 'exact' || rng() < 0.75)
    const triggerPrecision: EvidenceState = n.precision === 'estimated' ? 'estimated' : n.precision === 'missing' ? 'missing' : 'exact'
    delegations.push({
      id: `${n.parentId}->${n.id}`,
      parent_invocation_id: n.parentId,
      child_invocation_id: n.id,
      ...(hasTrigger && n.startedAtMs !== null
        ? {
            trigger: {
              agent_type: agentType,
              session_id: rootSessionId,
              tool_call_id: childId(n),
              timestamp: iso(n.startedAtMs),
              precision:
                triggerPrecision === 'exact'
                  ? fact('exact')
                  : fact(triggerPrecision, triggerPrecision === 'estimated' ? 'fifo_join_heuristic' : 'source_not_recorded'),
            },
          }
        : {}),
      ...(hasResult && n.endedAtMs !== null
        ? {
            result: {
              agent_type: agentType,
              session_id: rootSessionId,
              tool_call_id: childId(n),
              timestamp: iso(n.endedAtMs),
              precision: n.precision === 'exact' ? fact('exact') : fact('estimated', 'fifo_join_heuristic'),
            },
          }
        : {}),
      ...(rng() < 0.4 ? { task_summary: `Delegated task ${childId(n)} · 委托任务` } : {}),
      execution_mode: rng() < 0.7 ? 'blocking' : rng() < 0.5 ? 'background' : 'unknown',
      evidence: {
        trigger: hasTrigger ? fact(triggerPrecision, triggerPrecision === 'exact' ? undefined : 'source_not_recorded') : fact('missing', 'source_not_recorded'),
        timing: n.precision === 'exact' ? fact('exact') : fact(n.precision, 'source_not_recorded'),
        task: rng() < 0.4 ? fact('exact') : fact('missing', 'source_not_recorded'),
        result: hasResult ? fact(n.precision === 'exact' ? 'exact' : 'estimated', n.precision === 'exact' ? undefined : 'fifo_join_heuristic') : fact('missing', 'completion_not_recorded'),
      },
    })
  }

  return {
    spec,
    seed,
    graph: {
      root_agent_type: agentType,
      root_session_id: rootSessionId,
      revision: seed,
      completeness: fact('exact'),
      invocations,
      delegations,
    },
    nowMs,
  }
}

function makeRoleSuffix(rng: Rng): string {
  return pick(rng, EN_ROLES)
}

/**
 * Splits each closed lane span into deterministic sub-spans until the model
 * carries at least `targetSegments` activity spans, reproducing the accepted
 * spike's segment pressure for the layout LOD path. Live/open lanes keep
 * their layout-derived geometry.
 */
export function withActivitySpans(model: TimelineModel, targetSegments: number, seed: number): TimelineModel {
  const rng = mulberry32(seed ^ 0x5eed)
  const current = model.invocations.reduce((n, inv) => n + inv.spans.length, 0)
  if (current >= targetSegments) return model
  const invocations = model.invocations.map((inv) => ({ ...inv, spans: [...inv.spans] }))
  let total = current
  let rounds = 0
  while (total < targetSegments && rounds < 8) {
    rounds++
    for (const inv of invocations) {
      if (total >= targetSegments) break
      const next: { startMs: number; endMs: number }[] = []
      for (const span of inv.spans) {
        const dur = span.endMs - span.startMs
        if (dur < 4 * SECOND || total >= targetSegments) {
          next.push(span)
          continue
        }
        const parts = Math.min(1 + randInt(rng, 1, 3), Math.max(1, Math.floor(dur / (2 * SECOND))))
        if (parts <= 1) {
          next.push(span)
          continue
        }
        const slice = dur / parts
        for (let k = 0; k < parts; k++) {
          const s = span.startMs + k * slice
          const e = s + slice * (0.55 + rng() * 0.35)
          next.push({ startMs: Math.round(s), endMs: Math.round(Math.min(e, span.endMs)) })
        }
        total += parts - 1
      }
      inv.spans = next
    }
  }
  return { ...model, invocations }
}

export interface SyntheticDataset {
  spec: DatasetSpec
  seed: number
  graph: CollaborationGraphDTO
  model: TimelineModel
  nowMs: number
}

/** Full pipeline: contract graph -> normalized model (+ activity spans). */
export function generateDataset(spec: DatasetSpec, seedOffset = 0): SyntheticDataset {
  const { graph, seed, nowMs } = generateGraph(spec, seedOffset)
  const normalized = normalizeTimelineModel(graph)
  const model = withActivitySpans(normalized, spec.segments, seed)
  return { spec, seed, graph, model, nowMs: EPOCH_BASE_MS + nowMs }
}

/** Stable fixture hash for benchmark records (of the contract graph JSON). */
export function datasetHash(dataset: SyntheticDataset): string {
  return fnv1a(JSON.stringify(dataset.graph))
}
