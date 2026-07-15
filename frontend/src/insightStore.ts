// Module-level store for Deep Insight (根因分析) generation. It lives outside
// the React tree so an in-flight generation survives when the user switches
// tabs or sessions and unmounts DeepInsightSection: the fetch keeps streaming,
// progress accumulates here, and remounting re-attaches to the live run instead
// of aborting it. Keyed by sessionId so each session runs independently.
import {
  generateInsight, InsightBlockedError, NoProviderError,
  type InsightResult, type SendPreview,
} from './api'

export interface StageLine { text: string; ms: number | null }

export type RunStatus = 'idle' | 'running' | 'preview' | 'done' | 'error' | 'blocked'

export interface RunState {
  status: RunStatus
  stages: StageLine[]
  result: InsightResult | null
  preview: SendPreview | null
  error: string | null
  blocked: string | null
  noProvider: boolean
}

interface Entry {
  current: RunState
  ac: AbortController | null
  lastAt: number
  subs: Set<() => void>
}

const IDLE: RunState = {
  status: 'idle', stages: [], result: null, preview: null,
  error: null, blocked: null, noProvider: false,
}

const store = new Map<string, Entry>()

function entry(id: string): Entry {
  let e = store.get(id)
  if (!e) { e = { current: IDLE, ac: null, lastAt: 0, subs: new Set() }; store.set(id, e) }
  return e
}

// patch replaces `current` with a new object (stable identity between changes,
// so useSyncExternalStore's getSnapshot stays consistent) and notifies.
function patch(id: string, p: Partial<RunState>) {
  const e = entry(id)
  e.current = { ...e.current, ...p }
  e.subs.forEach(fn => fn())
}

export function subscribe(id: string, cb: () => void): () => void {
  const e = entry(id)
  e.subs.add(cb)
  return () => { e.subs.delete(cb) }
}

export function getState(id: string): RunState {
  return entry(id).current
}

// seedResult adopts a cached latest insight from the server, but never clobbers
// a live run or a result produced in this session.
export function seedResult(id: string, result: InsightResult | null) {
  const e = entry(id)
  if (result && e.current.status === 'idle' && !e.current.result) {
    patch(id, { result, status: 'done' })
  }
}

function onStage(id: string, stage: string) {
  const e = entry(id)
  let stages = e.current.stages
  if (stages.length > 0 && stages[stages.length - 1].ms == null) {
    stages = stages.slice()
    stages[stages.length - 1] = { ...stages[stages.length - 1], ms: performance.now() - e.lastAt }
  }
  stages = [...stages, { text: stage, ms: null }]
  e.lastAt = performance.now()
  patch(id, { stages })
}

function finalizeStages(id: string) {
  const e = entry(id)
  const stages = e.current.stages
  if (stages.length > 0 && stages[stages.length - 1].ms == null) {
    const copy = stages.slice()
    copy[copy.length - 1] = { ...copy[copy.length - 1], ms: performance.now() - e.lastAt }
    patch(id, { stages: copy })
  }
}

export function start(id: string, confirm: boolean, providerId = 0) {
  const e = entry(id)
  if (e.current.status === 'running') return
  const ac = new AbortController()
  e.ac = ac
  e.lastAt = performance.now()
  patch(id, { status: 'running', stages: [], error: null, blocked: null, preview: null, noProvider: false })

  generateInsight(id, stage => onStage(id, stage), ac.signal, providerId, confirm)
    .then(out => {
      if (ac.signal.aborted) return
      if ('needs_confirmation' in out) {
        patch(id, { status: 'preview', preview: out })
        return
      }
      finalizeStages(id)
      patch(id, { status: 'done', result: out })
    })
    .catch(err => {
      if (ac.signal.aborted) return
      if (err instanceof NoProviderError) { patch(id, { status: 'idle', noProvider: true }); return }
      if (err instanceof InsightBlockedError) { patch(id, { status: 'blocked', blocked: err.reason }); return }
      patch(id, { status: 'error', error: err instanceof Error ? err.message : String(err) })
    })
}

export function cancel(id: string) {
  const e = entry(id)
  e.ac?.abort()
  patch(id, { status: e.current.result ? 'done' : 'idle', stages: [] })
}

export function dismissPreview(id: string) {
  const e = entry(id)
  patch(id, { status: e.current.result ? 'done' : 'idle', preview: null })
}
