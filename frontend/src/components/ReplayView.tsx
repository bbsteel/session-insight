import { lazy, Suspense, useCallback, useEffect, useState, useRef, useMemo, startTransition } from 'react'
import { fetchPositions, fetchSession, fetchSessionEdits } from '../api'
import type { EditCall, PositionsResponse, SessionDetail, TurnVM } from '../types'
import type { ScrollMetrics } from '../minimapGeometry'
import { TERMINAL_LINE_HEIGHT, type TerminalControl } from '../terminalControl'
import MiniMap, { type MiniMapControl } from './MiniMap'
import GlobalSearch from './GlobalSearch'
import DiffModal from './DiffModal'
import { getVisibleTurnRange, isSameVisibleRange, type VisibleTurnRange } from '../scrollSync'
import { parseEditHeaderLine } from '../terminalInteractionGeometry'

const AnalyticsView = lazy(() => import('./AnalyticsView'))
const TerminalPanel = lazy(() => import('./TerminalPanel'))

type ReplayScrollBehavior = 'auto' | 'smooth'
type JumpTarget = 'turn' | 'user' | 'anomaly' | 'compaction'
type ViewMode = 'terminal' | 'analytics'

function hasCompaction(turn: TurnVM): boolean {
  return turn.anomalies?.some(a => a.includes('compaction') || a.includes('compression')) ?? false
}

interface Props {
  sessionId: string | null
  onSelect?: (id: string) => void
}

function fmtTokens(n: number): string {
  return n.toLocaleString()
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${totalSeconds % 60}s`
  return `${totalSeconds}s`
}

export default function ReplayView({ sessionId, onSelect }: Props) {
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [viewMode, setViewMode] = useState<ViewMode>('terminal')
  const [showHelp, setShowHelp] = useState(false)
  const [showDiffModal, setShowDiffModal] = useState(false)
  const [initialDiffIdx, setInitialDiffIdx] = useState(0)
  const [hiddenAnomalyTypes, setHiddenAnomalyTypes] = useState<Set<string>>(new Set())
  const [showAnomalyFilter, setShowAnomalyFilter] = useState(false)
  const [terminalCols, setTerminalCols] = useState<number | null>(null)
  const [positionsData, setPositionsData] = useState<PositionsResponse | null>(null)
  const [positionsBuilding, setPositionsBuilding] = useState(false)
  const [edits, setEdits] = useState<EditCall[]>([])
  const termControlRef = useRef<TerminalControl | null>(null)
  const miniMapControlRef = useRef<MiniMapControl | null>(null)
  const scrollToIndexRef = useRef<((index: number, behavior?: ReplayScrollBehavior) => void) | null>(null)
  const scrollToTopRef = useRef<((top: number, behavior?: ScrollBehavior) => void) | null>(null)
  const visibleRangeRef = useRef<VisibleTurnRange>()
  const visibleRangeLabelRef = useRef<HTMLSpanElement>(null)
  const pollTimerRef = useRef<ReturnType<typeof setTimeout>>()
  const lastMetricsRef = useRef<ScrollMetrics>()

  const handleColsReady = useCallback((cols: number) => {
    setTerminalCols(cols)
    setPositionsData(null)
    // Register edit line matcher once terminal is ready.
    // Buffer scan (in TerminalPanel) finds ✏️ header lines in document order,
    // matchIndex N → edits[N] since both are ordered by document position.
    termControlRef.current?.setLineMatchers([{
      match: (text) => {
        return parseEditHeaderLine(text)
      },
      tooltip: '打开 Diff 明细',
      onActivate: (_bufLine, _data, matchIndex) => {
        setInitialDiffIdx(matchIndex)
        setShowDiffModal(true)
      },
    }])
  }, [])

  const turns = session?.turns ?? []

  // Wire scrollToIndexRef and scrollToTopRef to the terminal control.
  // When analytics is shown the terminal is unmounted so these become no-ops.
  useEffect(() => {
    if (scrollToIndexRef) {
      scrollToIndexRef.current = (index: number) => {
        const ctrl = termControlRef.current
        if (!ctrl) return
        const metrics = ctrl.getMetrics()
        const totalLines = Math.floor(metrics.scrollHeight / TERMINAL_LINE_HEIGHT)
        const visibleLines = Math.floor(metrics.clientHeight / TERMINAL_LINE_HEIGHT)
        const barCount = turns.length
        const ratio = barCount > 1 ? index / (barCount - 1) : 0
        const line = Math.floor(ratio * Math.max(0, totalLines - visibleLines))
        ctrl.scrollToLine(line)
      }
    }
    if (scrollToTopRef) {
      scrollToTopRef.current = (top: number) => {
        termControlRef.current?.scrollToLine(Math.floor(top / TERMINAL_LINE_HEIGHT))
      }
    }
  }, [scrollToIndexRef, scrollToTopRef, turns])

  // Keyboard navigation
  useEffect(() => {
    if (!sessionId || !session?.turns.length) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      if (e.key === 'j' || e.key === 'ArrowDown') { e.preventDefault(); jump(1, 'turn') }
      else if (e.key === 'k' || e.key === 'ArrowUp') { e.preventDefault(); jump(-1, 'turn') }
      else if (e.key === '?' && !e.shiftKey && !e.metaKey && !e.ctrlKey) { e.preventDefault(); setShowHelp(h => !h) }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [sessionId, session])

  useEffect(() => {
    if (!sessionId) { setSession(null); setEdits([]); return }
    visibleRangeRef.current = undefined
    jumpBaseRef.current = 0
    setTerminalCols(null)
    setPositionsData(null)
    setPositionsBuilding(false)
    clearTimeout(pollTimerRef.current)
    setLoading(true)
    fetchSession(sessionId)
      .then(data => { setSession(data) })
      .catch(console.error)
      .finally(() => setLoading(false))
  }, [sessionId])

  useEffect(() => {
    if (!sessionId) return
    fetchSessionEdits(sessionId)
      .then(setEdits)
      .catch(() => setEdits([]))
  }, [sessionId])

  // Fetch positions once terminal cols are stable. Polls on 202 until ready.
  useEffect(() => {
    if (!sessionId || terminalCols === null) return
    let cancelled = false

    const poll = () => {
      fetchPositions(sessionId, terminalCols)
        .then(result => {
          if (cancelled) return
          if (result.status === 'building') {
            setPositionsBuilding(true)
            pollTimerRef.current = setTimeout(poll, 1000)
          } else {
            setPositionsBuilding(false)
            setPositionsData(result.data)
          }
        })
        .catch(() => {
          if (!cancelled) {
            setPositionsBuilding(false)
            setPositionsData(null) // error → turn-index fallback
          }
        })
    }

    poll()
    return () => {
      cancelled = true
      clearTimeout(pollTimerRef.current)
    }
  }, [sessionId, terminalCols])

  // Translate terminal scroll events into ScrollMetrics + visibleRange so that
  // MiniMap stays in sync even though there's no DOM scroller to observe.
  const handleTerminalScrollMetrics = useCallback((metrics: ScrollMetrics) => {
    lastMetricsRef.current = metrics
    const range = getVisibleTurnRange(metrics, turns.length)
    miniMapControlRef?.current?.updateViewport(metrics, range)
    if (range && !isSameVisibleRange(visibleRangeRef.current, range)) {
      visibleRangeRef.current = range
      jumpBaseRef.current = range.start
      if (visibleRangeLabelRef.current) {
        visibleRangeLabelRef.current.textContent = `Turn ${range.start + 1}-${range.end + 1}/${turns.length}`
      }
    }
  }, [turns, miniMapControlRef])

  useEffect(() => {
    const metrics = lastMetricsRef.current ?? termControlRef.current?.getMetrics()
    if (!metrics) return

    const range = getVisibleTurnRange(metrics, turns.length)
    const frame = window.requestAnimationFrame(() => {
      miniMapControlRef.current?.updateViewport(metrics, range)
    })
    return () => window.cancelAnimationFrame(frame)
  }, [turns.length, positionsData, positionsBuilding, viewMode])

  const jumpBaseRef = useRef(0)

  const anomalyIndexes = useMemo(() => turns
    .map((turn, index) => ({ turn, index }))
    .filter(({ turn }) => {
      const hasVisibleAnomaly = turn.anomalies?.some(a => !hiddenAnomalyTypes.has(a))
      const hasVisibleError = turn.error_count > 0 && !hiddenAnomalyTypes.has('tool_failure')
      return hasVisibleAnomaly || hasVisibleError
    })
    .map(({ index }) => index), [turns, hiddenAnomalyTypes])

  const userIndexes = useMemo(() => turns
    .map((turn, index) => turn.user_message ? index : -1)
    .filter(index => index >= 0), [turns])

  const compactionIndexes = useMemo(() => turns
    .map((turn, index) => hasCompaction(turn) ? index : -1)
    .filter(index => index >= 0), [turns])

  function jump(direction: -1 | 1, target: JumpTarget) {
    const barCount = turns.length
    if (barCount === 0) return
    const base = jumpBaseRef.current
    let targetIndex = -1

    if (target === 'turn') {
      targetIndex = Math.max(0, Math.min(base + direction, barCount - 1))
    } else {
      const indexes = target === 'user' ? userIndexes : target === 'anomaly' ? anomalyIndexes : compactionIndexes
      const found = direction > 0
        ? indexes.find(i => i > base)
        : [...indexes].reverse().find(i => i < base)
      if (found === undefined) return
      targetIndex = found
    }

    jumpBaseRef.current = targetIndex
    scrollToIndexRef?.current?.(targetIndex)
  }

  if (!sessionId) return (
    <main className="flex-1 flex flex-col min-w-[360px] bg-[var(--bg-surface)]">
      <GlobalSearch onSelect={onSelect} />
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center px-6">
          <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--bg-inset)] text-nav text-[var(--text-muted)]">SI</div>
          <h3 className="text-body font-medium text-[var(--text-primary)]">还没有选中会话</h3>
          <p className="text-helper text-[var(--text-muted)] mt-1">从左侧选择一个会话后，这里会显示终端内容。</p>
        </div>
      </div>
    </main>
  )

  if (loading) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)]">
      <GlobalSearch onSelect={onSelect} />
      <div className="p-4 space-y-3">{Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="rounded-lg border border-[var(--border-muted)] bg-[var(--bg-surface)] p-3">
          <div className="h-5 w-44 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
          <div className="mt-3 h-3 w-3/4 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
          <div className="mt-2 h-3 w-1/2 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
        </div>
      ))}</div>
    </main>
  )

  if (!session || !session.turns.length) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)] flex flex-col">
      <GlobalSearch onSelect={onSelect} />
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center px-6">
          <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--bg-inset)] text-nav text-[var(--text-muted)]">MSG</div>
          <h3 className="text-body font-medium text-[var(--text-primary)]">还没有会话记录</h3>
          <p className="text-helper text-[var(--text-muted)] mt-1">使用 agent 进行编码后，会话将自动出现在这里。</p>
        </div>
      </div>
    </main>
  )

  const totalTokens = session.turns.reduce((sum, t) => sum + t.token_usage.prompt_tokens + t.token_usage.completion_tokens, 0)
  const modelName = session.model_name || session.agent_type || 'unknown'
  const sessionDuration = formatDuration(session.turns.reduce((sum, t) => sum + t.duration_ms, 0))

  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden relative">
      <GlobalSearch onSelect={onSelect} />
      <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-3" style={{ height: '40px' }}>
        <div className="flex items-center gap-2">
          <button
            onClick={() => startTransition(() => setViewMode(v => v === 'analytics' ? 'terminal' : 'analytics'))}
            className={`h-7 rounded-md px-2 text-nav ${viewMode === 'analytics' ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
          >
            分析
          </button>
          <span className="text-[var(--border-default)]">|</span>
          <a href={`/api/sessions/${session.id}/export`} className="h-7 rounded-md px-2 inline-flex items-center text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] no-underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">导出</a>
        </div>
        <span className="text-[var(--border-default)] mx-1">|</span>
        <div className="flex items-center gap-1">
          {([
            ['turn', '轮次', '上一轮', '下一轮'],
            ['user', '用户', '上个用户输入', '下个用户输入'],
            ['anomaly', '异常', '上个异常', '下个异常'],
            ['compaction', '压缩', '上个压缩点', '下个压缩点'],
          ] as const).map(([target, label, prevTitle, nextTitle]) => (
            <div key={target} className="flex items-center gap-px">
              <button
                onClick={() => jump(-1, target)}
                className="h-7 w-7 rounded flex items-center justify-center text-sm text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                title={prevTitle}
                aria-label={prevTitle}
              >←</button>
              <span className="text-meta text-[var(--text-muted)] select-none px-0.5">{label}</span>
              <button
                onClick={() => jump(1, target)}
                className="h-7 w-7 rounded flex items-center justify-center text-sm text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                title={nextTitle}
                aria-label={nextTitle}
              >→</button>
            </div>
          ))}
          <div className="relative">
            <button
              onClick={() => setShowAnomalyFilter(v => !v)}
              className={`h-7 rounded-md px-1.5 text-nav ${hiddenAnomalyTypes.size > 0 ? 'text-[var(--text-muted)]' : 'text-[var(--error)]'} hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
              title="异常过滤"
              aria-label="异常过滤"
            >
              {anomalyIndexes.length}!
            </button>
            {showAnomalyFilter && (
              <div className="absolute top-full left-0 mt-1 min-w-[150px] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-2 shadow-md z-20">
                {['tool_failure', 'duration_spike', 'missing_shutdown'].map(type => (
                  <label key={type} className="flex cursor-pointer items-center gap-1.5 rounded-sm px-1 py-0.5 text-meta text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] whitespace-nowrap">
                    <input
                      type="checkbox"
                      checked={!hiddenAnomalyTypes.has(type)}
                      onChange={() => setHiddenAnomalyTypes(prev => {
                        const next = new Set(prev)
                        prev.has(type) ? next.delete(type) : next.add(type)
                        return next
                      })}
                      className="h-3 w-3"
                    />
                    {type.replace(/_/g, ' ')}
                  </label>
                ))}
              </div>
            )}
          </div>
        </div>
        <span className="flex-1 text-center text-helper text-[var(--text-secondary)] truncate px-2">
          {session.agent_type || 'agent'} · {modelName} · {fmtTokens(totalTokens)} tok · {session.turn_count} turns · {sessionDuration}
          {session.repository && <span className="text-[var(--text-muted)]"> · {session.repository.split('/').pop()}</span>}
          {session.branch && <span className="text-[var(--text-muted)]">@{session.branch}</span>}
          {session.created_at && (
            <span className="text-[var(--text-muted)] ml-1 text-meta">
              {new Date(session.created_at).toLocaleDateString()}
            </span>
          )}
          {session.todos && session.todos.length > 0 && (
            <span className="ml-1 text-[var(--accent-green)]">{session.todos.filter(t => t.status === 'done').length}/{session.todos.length} done</span>
          )}
        </span>
        <span ref={visibleRangeLabelRef} className="flex-shrink-0 text-meta text-[var(--text-muted)]">
          Turn ?/{session.turn_count}
        </span>
      </header>

      {showHelp && (
        <div className="absolute inset-0 z-20 flex items-center justify-center bg-[rgba(0,0,0,var(--opacity-overlay))]" onClick={() => setShowHelp(false)}>
          <div className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-lg p-6 max-w-sm" onClick={e => e.stopPropagation()}>
            <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-3">快捷键</h3>
            <div className="space-y-2 text-helper">
              {[
                ['j / ↓', '下一轮'],
                ['k / ↑', '上一轮'],
                ['?', '打开/关闭帮助'],
              ].map(([key, desc]) => (
                <div key={key} className="flex items-center gap-3">
                  <kbd className="bg-[var(--bg-inset)] px-1.5 py-0.5 rounded-sm border border-[var(--border-default)] text-meta text-[var(--text-primary)] min-w-[60px] text-center">{key}</kbd>
                  <span className="text-[var(--text-secondary)]">{desc}</span>
                </div>
              ))}
            </div>
            <button onClick={() => setShowHelp(false)} className="mt-4 text-meta text-[var(--accent-blue)] hover:underline">关闭</button>
          </div>
        </div>
      )}

      {showDiffModal && session && (
        <DiffModal sessionId={session.id} onClose={() => setShowDiffModal(false)} initialIdx={initialDiffIdx} />
      )}

      <div className="flex min-h-0 flex-1 overflow-hidden">
        <div className="flex min-w-0 flex-1 overflow-hidden">
          {viewMode === 'analytics' ? (
            <Suspense fallback={<AnalyticsSkeleton />}>
              <AnalyticsView sessionId={session.id} agentType={session.agent_type} />
            </Suspense>
          ) : (
            <Suspense fallback={<div className="flex-1 bg-[#1a1b26]" />}>
              <TerminalPanel
                sessionId={session.id}
                onScrollMetrics={handleTerminalScrollMetrics}
                onColsReady={handleColsReady}
                controlRef={termControlRef}
              />
            </Suspense>
          )}
        </div>
        <MiniMap
          turns={turns}
          positions={positionsBuilding ? null : positionsData}
          controlRef={miniMapControlRef}
          scrollToIndexRef={scrollToIndexRef}
          scrollToTopRef={scrollToTopRef}
        />
      </div>
    </main>
  )
}

function AnalyticsSkeleton() {
  return (
    <div className="p-4 space-y-3">
      <div className="grid grid-cols-4 gap-3">
        {Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-16 rounded-md bg-[var(--bg-inset)] animate-pulse" />)}
      </div>
      <div className="h-[200px] rounded-lg bg-[var(--bg-inset)] animate-pulse" />
    </div>
  )
}

