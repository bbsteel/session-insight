import { lazy, Suspense, useCallback, useEffect, useState, useRef, useMemo } from 'react'
import { Virtuoso } from 'react-virtuoso'
import type { VirtuosoHandle } from 'react-virtuoso'
import { fetchSession } from '../api'
import type { SessionDetail, TurnVM } from '../types'
import type { ScrollMetrics } from '../minimapGeometry'
import TurnCard from './TurnCard'
import ThemeToggle from './ThemeToggle'

const AnalyticsView = lazy(() => import('./AnalyticsView'))

type ReplayScrollBehavior = 'auto' | 'smooth'
type JumpTarget = 'turn' | 'user' | 'anomaly' | 'compaction'

function hasCompaction(turn: TurnVM): boolean {
  return turn.anomalies?.some(a => a.includes('compaction') || a.includes('compression')) ?? false
}

interface Props {
  sessionId: string | null
  onTurnsChange?: (turns: TurnVM[]) => void
  onVisibleRangeChange?: (range: { start: number; end: number }) => void
  onScrollMetricsChange?: (metrics: ScrollMetrics) => void
  scrollToIndexRef?: React.MutableRefObject<((index: number, behavior?: ReplayScrollBehavior) => void) | null>
  scrollToTopRef?: React.MutableRefObject<((top: number, behavior?: ScrollBehavior) => void) | null>
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

export default function ReplayView({ sessionId, onTurnsChange, onVisibleRangeChange, onScrollMetricsChange, scrollToIndexRef, scrollToTopRef }: Props) {
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [visibleRange, setVisibleRange] = useState<{ start: number; end: number }>()
  const [mode, setMode] = useState<'full' | 'digest'>('full')
  const [density, setDensity] = useState<'standard' | 'tight'>('standard')
  const [showAnalytics, setShowAnalytics] = useState(false)
  const [showHelp, setShowHelp] = useState(false)
  const [userScrolled, setUserScrolled] = useState(false)
  const [hiddenAnomalyTypes, setHiddenAnomalyTypes] = useState<Set<string>>(new Set())
  const [showAnomalyFilter, setShowAnomalyFilter] = useState(false)
  const virtuosoRef = useRef<VirtuosoHandle>(null)
  const scrollerRef = useRef<HTMLElement | null>(null)
  const [scrollerElement, setScrollerElement] = useState<HTMLElement | null>(null)

  const captureScroller = useCallback((ref: HTMLElement | Window | null) => {
    const element = ref instanceof HTMLElement ? ref : null
    scrollerRef.current = element
    setScrollerElement(element)
  }, [])

  useEffect(() => {
    if (scrollToIndexRef && virtuosoRef.current) {
      scrollToIndexRef.current = (index: number, behavior: ReplayScrollBehavior = 'smooth') => {
        virtuosoRef.current?.scrollToIndex({ index, align: 'start', behavior })
      }
    }
    if (scrollToTopRef) {
      scrollToTopRef.current = (top: number, behavior: ScrollBehavior = 'auto') => {
        const scroller = scrollerRef.current
        if (!scroller) {
          virtuosoRef.current?.scrollTo({ top, behavior })
          return
        }
        if (behavior === 'auto') {
          scroller.scrollTop = top
          return
        }
        scroller.scrollTo({ top, behavior })
      }
    }
  }, [scrollToIndexRef, scrollToTopRef, scrollerElement, session])

  useEffect(() => {
    const scroller = scrollerElement
    if (!scroller || !onScrollMetricsChange) return

    let frame = 0
    const emitMetrics = () => {
      frame = 0
      onScrollMetricsChange({
        scrollTop: scroller.scrollTop,
        scrollHeight: scroller.scrollHeight,
        clientHeight: scroller.clientHeight,
      })
    }
    const scheduleMetrics = () => {
      if (frame) return
      frame = window.requestAnimationFrame(emitMetrics)
    }

    emitMetrics()
    scroller.addEventListener('scroll', scheduleMetrics, { passive: true })
    const resizeObserver = new ResizeObserver(scheduleMetrics)
    resizeObserver.observe(scroller)

    return () => {
      scroller.removeEventListener('scroll', scheduleMetrics)
      resizeObserver.disconnect()
      if (frame) window.cancelAnimationFrame(frame)
    }
  }, [onScrollMetricsChange, scrollerElement, session, showAnalytics, mode, density])

  // Keyboard navigation
  useEffect(() => {
    if (!sessionId || !session?.turns.length) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault()
        jump(1, 'turn')
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault()
        jump(-1, 'turn')
      } else if (e.key === '?' && !e.shiftKey && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        setShowHelp(h => !h)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [sessionId, session, visibleRange])

  useEffect(() => {
    if (!sessionId) { setSession(null); onTurnsChange?.([]); return }
    setLoading(true)
    fetchSession(sessionId).then(data => { setSession(data); onTurnsChange?.(data.turns) }).catch(console.error).finally(() => setLoading(false))
  }, [sessionId])

  const turns = session?.turns ?? []

  const jumpBaseRef = useRef(0)
  useEffect(() => {
    if (visibleRange) jumpBaseRef.current = visibleRange.start
  }, [visibleRange])

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
    const scroller = scrollerRef.current
    const base = jumpBaseRef.current
    let targetIndex = -1

    if (target === 'turn') {
      targetIndex = Math.max(0, Math.min(base + direction, barCount - 1))
    } else {
      const indexes =
        target === 'user' ? userIndexes :
        target === 'anomaly' ? anomalyIndexes :
        compactionIndexes
      const found = direction > 0
        ? indexes.find(index => index > base)
        : [...indexes].reverse().find(index => index < base)
      if (found === undefined) return
      targetIndex = found
    }

    jumpBaseRef.current = targetIndex
    if (scroller && barCount > 0) {
      const ratio = targetIndex / (barCount - 1)
      scroller.scrollTop = ratio * (scroller.scrollHeight - scroller.clientHeight)
    }
  }

  if (!sessionId) return (
    <main className="flex-1 flex flex-col min-w-[360px] bg-[var(--bg-surface)]">
      <GlobalTopBar />
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center px-6">
          <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--bg-inset)] text-nav text-[var(--text-muted)]">SI</div>
          <h3 className="text-body font-medium text-[var(--text-primary)]">还没有选中会话</h3>
          <p className="text-helper text-[var(--text-muted)] mt-1">从左侧选择一个会话后，这里会显示对话回放。</p>
        </div>
      </div>
    </main>
  )
  if (loading) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)]">
      <GlobalTopBar />
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
      <GlobalTopBar />
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
      <GlobalTopBar />
      <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-3" style={{ height: '40px' }}>
        <div className="flex items-center gap-2">
          <button onClick={() => setMode(m => m === 'full' ? 'digest' : 'full')} className="h-7 rounded-md px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">{mode === 'full' ? 'Full' : 'Digest'}</button>
          <button onClick={() => setDensity(d => d === 'standard' ? 'tight' : 'standard')} className="h-7 rounded-md px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">{density === 'standard' ? 'Standard' : 'Tight'}</button>
          <span className="text-[var(--border-default)]">|</span>
          <button onClick={() => setShowAnalytics(a => !a)} className={`h-7 rounded-md px-2 text-nav ${showAnalytics ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}>
            {showAnalytics ? '回放' : '分析'}
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
          {session.repository && <span className="text-[var(--text-muted)]"> · {session.repository.split('/').pop()}</span>}{session.branch && <span className="text-[var(--text-muted)]">@{session.branch}</span>}
          {session.created_at && (
            <span className="text-[var(--text-muted)] ml-1 text-meta">
              {new Date(session.created_at).toLocaleDateString()}
            </span>
          )}
          {session.todos && session.todos.length > 0 && (
            <span className="ml-1 text-[var(--accent-green)]">{session.todos.filter(t => t.status === 'done').length}/{session.todos.length} done</span>
          )}
        </span>
        <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">
          Turn {visibleRange ? `${visibleRange.start + 1}-${visibleRange.end + 1}` : '?'}/{session.turn_count}
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
                ['Esc', '关闭面板'],
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

      {showAnalytics ? (
        <Suspense fallback={<AnalyticsSkeleton />}>
          <AnalyticsView sessionId={session.id} />
        </Suspense>
      ) : (
        <div className="flex-1 relative">
          <Virtuoso
            ref={virtuosoRef}
            scrollerRef={captureScroller}
            style={{ height: '100%' }}
            data={session.turns}
            atBottomStateChange={(atBottom) => {
              if (!atBottom && !userScrolled) {
                setUserScrolled(true)
              } else if (atBottom && userScrolled) {
                setUserScrolled(false)
              }
            }}
            rangeChanged={(range) => {
              const newRange = { start: range.startIndex, end: range.endIndex }
              setVisibleRange(newRange)
              onVisibleRangeChange?.(newRange)
            }}
            totalListHeightChanged={() => {
              const scroller = scrollerRef.current
              if (!scroller) return
              onScrollMetricsChange?.({
                scrollTop: scroller.scrollTop,
                scrollHeight: scroller.scrollHeight,
                clientHeight: scroller.clientHeight,
              })
            }}
            itemContent={(_: number, turn: TurnVM) => {
              const currentIndex = session.turns.findIndex(t => t.turn_index === turn.turn_index)
              const cumul = session.turns.slice(0, currentIndex + 1).reduce((s, t) => s + t.token_usage.prompt_tokens + t.token_usage.completion_tokens, 0)
              return <TurnCard turn={turn} mode={mode} density={density} cumulativeTokens={cumul} />
            }}
          />
          {visibleRange && visibleRange.start > 1 && (
            <button
              onClick={() => virtuosoRef.current?.scrollToIndex({ index: 0, behavior: 'smooth' })}
              className="absolute top-3 right-3 bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-md px-2 py-1 text-meta text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] shadow-sm transition-colors duration-fast z-10"
            >
              &#9650; 顶部
            </button>
          )}
          {userScrolled && (
            <button
              onClick={() => {
                virtuosoRef.current?.scrollToIndex({ index: session.turns.length - 1, behavior: 'smooth' })
                setUserScrolled(false)
              }}
              className="absolute bottom-3 right-3 bg-[var(--accent-blue)] text-[var(--text-inverse)] border border-[var(--accent-blue)] rounded-md px-2 py-1 text-meta hover:opacity-90 shadow-sm transition-colors duration-fast z-10"
            >
              &#9660; 回到底部
            </button>
          )}
        </div>
      )}
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

function GlobalTopBar() {
  return (
    <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center gap-2 px-3" style={{ height: '40px', zIndex: 'var(--z-sticky)' }}>
      <div className="relative w-full max-w-[360px]">
        <input
          type="search"
          placeholder="全文搜索..."
          className="h-[34px] w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-primary)]"
          aria-label="全文搜索"
        />
      </div>
      <div className="ml-auto flex items-center gap-1">
        <ThemeToggle />
        <button className="h-7 w-7 rounded-md text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]" title="设置" aria-label="设置">⚙</button>
      </div>
    </header>
  )
}
