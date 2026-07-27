/**
 * Deterministic synthetic dataset generator for the renderer spike.
 *
 * Every dataset includes the edge cases required by the spike brief:
 * - recursive depth four (a guaranteed chain);
 * - running, waiting, failed, orphaned, and unknown states;
 * - missing and estimated start/end boundaries;
 * - an hours-long root containing seconds-long children;
 * - dense parallel starts (bursts);
 * - English and Chinese labels (some deliberately long).
 *
 * All randomness comes from mulberry32(seed). No Date.now(), no Math.random().
 */

import { fnv1a, logUniform, mulberry32, pick, randInt, type Rng } from './prng'
import type {
  EvidenceState,
  InvocationStatus,
  SpikeDataset,
  SpikeInvocation,
  SpikeRelation,
} from './types'

export interface DatasetSpec {
  name: string
  lanes: number
  segments: number
  relations: number
  seed: number
}

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
  const en = pick(rng, EN_ROLES)
  const zh = pick(rng, ZH_ROLES)
  return `#${index} ${en} · ${zh}`
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

export function generateDataset(spec: DatasetSpec, seedOffset = 0): SpikeDataset {
  const rng: Rng = mulberry32(spec.seed + seedOffset)
  const domainStart = 0
  const domainEnd = ROOT_SPAN_MS
  const nowMs = domainEnd

  const invocations: SpikeInvocation[] = []
  const root: SpikeInvocation = {
    id: 'inv-root',
    parentId: null,
    label: `Main Agent 主代理 · ${spec.name}`,
    status: 'completed',
    startedAtMs: domainStart,
    endedAtMs: domainEnd,
    timePrecision: 'exact',
    depth: 0,
    segments: [],
  }
  invocations.push(root)

  // Guaranteed depth-4 chain: root -> chain-1 -> chain-2 -> chain-3 -> chain-4.
  let chainParent = root
  for (let depth = 1; depth <= 4; depth++) {
    const parentStart = chainParent.startedAtMs ?? domainStart
    const start = parentStart + depth * 2 * MINUTE
    const dur = (5 - depth) * 8 * MINUTE
    const node: SpikeInvocation = {
      id: `inv-chain-${depth}`,
      parentId: chainParent.id,
      label: `#chain-${depth} nested-worker · 嵌套 worker 第${depth}层`,
      status: depth === 4 ? 'failed' : 'completed',
      startedAtMs: start,
      endedAtMs: start + dur,
      timePrecision: 'exact',
      depth,
      segments: [],
    }
    invocations.push(node)
    chainParent = node
  }

  // Remaining lanes. Burst scheduling forces dense parallel starts.
  const totalLanes = spec.lanes
  const burstCount = Math.max(2, Math.floor(totalLanes / 25))
  const bursts: Array<{ parentId: string; atMs: number; remaining: number }> = []
  for (let b = 0; b < burstCount; b++) {
    bursts.push({ parentId: 'inv-root', atMs: randInt(rng, 10, 170) * MINUTE, remaining: randInt(rng, 3, 6) })
  }

  let forcedStatusIndex = 0
  while (invocations.length < totalLanes) {
    const i = invocations.length
    let parent: SpikeInvocation
    let startMs: number
    const burst = bursts.find((b) => b.remaining > 0)
    if (burst) {
      parent = invocations.find((inv) => inv.id === burst.parentId) ?? root
      burst.remaining--
      // Dense parallel starts: siblings within ±1.5 s of the burst anchor.
      startMs = burst.atMs + randInt(rng, -1500, 1500)
    } else {
      const candidates = invocations.filter(
        (inv) => inv.depth < 4 && inv.startedAtMs !== null && inv.endedAtMs !== null,
      )
      // Bias toward shallow parents so fan-out stays realistic.
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
    // Seconds-long children inside an hours-long root: log-uniform 5 s..20 min.
    const dur = logUniform(rng, 5 * SECOND, maxDur)

    const live = status === 'running' || status === 'waiting' || status === 'pending'
    const missingStart = precision === 'missing' && rng() < 0.5
    const startedAtMs = missingStart ? null : startMs
    let endedAtMs: number | null
    if (live) {
      endedAtMs = null // live lanes extend to the now marker
    } else if (status === 'orphaned' || status === 'unknown') {
      endedAtMs = rng() < 0.6 ? null : startMs + dur // missing completion evidence
    } else {
      endedAtMs = startMs + dur
    }

    invocations.push({
      id: `inv-${i}`,
      parentId: parent.id,
      label: makeLabel(rng, i),
      status,
      startedAtMs,
      endedAtMs,
      timePrecision: precision,
      depth: parent.depth + 1,
      segments: [],
    })
  }

  // Effective window used for segment placement when exact bounds are missing.
  const effStart = (inv: SpikeInvocation): number => inv.startedAtMs ?? domainStart + 5 * MINUTE
  const effEnd = (inv: SpikeInvocation): number =>
    inv.endedAtMs ?? (inv.startedAtMs !== null ? Math.min(inv.startedAtMs + 15 * MINUTE, nowMs) : nowMs)

  // Distribute activity segments across lanes proportional to duration.
  const weights = invocations.map((inv) => Math.max(SECOND, effEnd(inv) - effStart(inv)))
  const totalWeight = weights.reduce((a, b) => a + b, 0)
  invocations.forEach((inv, idx) => {
    const count = Math.max(1, Math.round((weights[idx] / totalWeight) * spec.segments))
    const s = effStart(inv)
    const e = effEnd(inv)
    const segs: Array<{ startMs: number; endMs: number }> = []
    let cursor = s
    for (let k = 0; k < count && cursor < e; k++) {
      const remaining = e - cursor
      const segDur = Math.min(remaining, logUniform(rng, 2 * SECOND, Math.max(4 * SECOND, remaining / Math.max(1, count - k))))
      const gap = Math.min(remaining - segDur, logUniform(rng, SECOND, Math.max(2 * SECOND, remaining / Math.max(1, count - k))))
      if (segDur > 0) segs.push({ startMs: cursor, endMs: cursor + segDur })
      cursor += segDur + Math.max(0, gap)
    }
    inv.segments = segs
  })

  // Relations: one canonical launch delegation per non-root invocation, then
  // result-return relations until the spec target is reached.
  const relations: SpikeRelation[] = []
  for (const inv of invocations) {
    if (inv.parentId === null) continue
    relations.push({
      id: `rel-launch-${inv.id}`,
      kind: 'launch',
      parentId: inv.parentId,
      childId: inv.id,
      atMs: inv.startedAtMs,
      precision: inv.timePrecision,
    })
  }
  const ended = invocations.filter((inv) => inv.parentId !== null && inv.endedAtMs !== null)
  let n = 0
  while (relations.length < spec.relations && ended.length > 0) {
    const child = ended[n++ % ended.length]
    relations.push({
      id: `rel-result-${child.id}-${n}`,
      kind: 'result',
      parentId: child.parentId ?? 'inv-root',
      childId: child.id,
      atMs: child.endedAtMs,
      precision: rng() < 0.75 ? 'exact' : 'estimated',
    })
  }

  return {
    name: spec.name,
    seed: spec.seed + seedOffset,
    invocations,
    relations,
    domainStartMs: domainStart,
    domainEndMs: domainEnd,
    nowMs,
  }
}

/** Stable fixture hash for benchmark records. */
export function datasetHash(dataset: SpikeDataset): string {
  return fnv1a(JSON.stringify(dataset))
}
