import { useMemo, useRef, useState } from 'react'
import type { TurnVM } from '../types'

type ReplayScrollBehavior = 'auto' | 'smooth'

interface Props {
  turns: TurnVM[]
  visibleRange?: { start: number; end: number }
  scrollToIndexRef?: React.MutableRefObject<((index: number, behavior?: ReplayScrollBehavior) => void) | null>
}

type JumpTarget = 'turn' | 'user' | 'anomaly' | 'compaction'

function getTotalTokens(t: TurnVM): number {
  return t.token_usage.prompt_tokens + t.token_usage.completion_tokens
}

function clamp(n: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, n))
}

function getPressureColor(ratio: number): string {
  if (ratio > 0.95) return 'var(--error)'
  if (ratio >= 0.8) return 'var(--warning)'
  return 'var(--success)'
}

function hasCompaction(turn: TurnVM): boolean {
  return turn.anomalies?.some(a => a.includes('compaction') || a.includes('compression')) ?? false
}

export default function MiniMap({ turns, visibleRange, scrollToIndexRef }: Props) {
  const barCount = turns.length
  const containerRef = useRef<HTMLDivElement>(null)
  const draggingRef = useRef(false)
  const [isDragging, setIsDragging] = useState(false)
  const [hiddenAnomalyTypes, setHiddenAnomalyTypes] = useState<Set<string>>(new Set())
  const [showAnomalyFilter, setShowAnomalyFilter] = useState(false)

  const maxTokens = useMemo(() => Math.max(...turns.map(getTotalTokens), 1), [turns])
  const visibleCount = visibleRange ? visibleRange.end - visibleRange.start + 1 : 1

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

  function scrollTo(index: number, behavior: ReplayScrollBehavior = 'smooth') {
    if (barCount === 0) return
    scrollToIndexRef?.current?.(clamp(index, 0, barCount - 1), behavior)
  }

  function jump(direction: -1 | 1, target: JumpTarget) {
    const base = visibleRange?.start ?? 0
    if (target === 'turn') {
      scrollTo(base + direction)
      return
    }

    const indexes =
      target === 'user' ? userIndexes :
      target === 'anomaly' ? anomalyIndexes :
      compactionIndexes

    const next = direction > 0
      ? indexes.find(index => index > base)
      : [...indexes].reverse().find(index => index < base)
    if (next !== undefined) scrollTo(next)
  }

  function scrollFromPointer(clientY: number) {
    if (!containerRef.current || barCount === 0) return
    const rect = containerRef.current.getBoundingClientRect()
    const ratio = clamp((clientY - rect.top) / rect.height, 0, 1)
    const maxStart = Math.max(0, barCount - visibleCount)
    const targetIndex = Math.round(ratio * maxStart)
    scrollTo(targetIndex, 'auto')
  }

  function handlePointerDown(e: React.PointerEvent<HTMLDivElement>) {
    draggingRef.current = true
    setIsDragging(true)
    e.currentTarget.setPointerCapture(e.pointerId)
    scrollFromPointer(e.clientY)
  }

  function handlePointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!draggingRef.current) return
    scrollFromPointer(e.clientY)
  }

  function handlePointerUp(e: React.PointerEvent<HTMLDivElement>) {
    draggingRef.current = false
    setIsDragging(false)
    e.currentTarget.releasePointerCapture(e.pointerId)
  }

  if (barCount === 0) {
    return (
      <nav className="minimap-shell flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex items-center justify-center" aria-label="MiniMap">
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>MiniMap</span>
      </nav>
    )
  }

  return (
    <nav
      className="minimap-shell flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col select-none"
      aria-label="MiniMap"
    >
      <div className="grid grid-cols-4 border-b border-[var(--border-muted)] bg-[var(--bg-surface)]" style={{ height: 32 }}>
        {[
          ['T', '轮次', 'turn'],
          ['U', '用户输入', 'user'],
          ['!', '异常', 'anomaly'],
          ['C', '压缩', 'compaction'],
        ].map(([icon, label, target]) => (
          <div key={target} className="min-w-0 border-r border-[var(--border-muted)] last:border-r-0">
            <button
              className="h-4 w-full text-[8px] leading-none text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]"
              onClick={() => jump(-1, target as JumpTarget)}
              title={`上一个${label}`}
              aria-label={`上一个${label}`}
            >
              {icon}↑
            </button>
            <button
              className="h-4 w-full border-t border-[var(--border-muted)] text-[8px] leading-none text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]"
              onClick={() => jump(1, target as JumpTarget)}
              title={`下一个${label}`}
              aria-label={`下一个${label}`}
            >
              {icon}↓
            </button>
          </div>
        ))}
      </div>

      <div
        ref={containerRef}
        className="flex-1 relative overflow-hidden bg-[var(--bg-inset)]"
        style={{ cursor: isDragging ? 'grabbing' : 'grab', touchAction: 'none' }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
      >
        <div className="absolute inset-y-2 left-[8px] w-px bg-[var(--border-muted)]" />
        <div className="absolute inset-y-2 left-[20px] w-[56px] rounded-sm bg-[var(--bg-primary)] border border-[var(--border-muted)]" />
        <div className="absolute inset-y-2 right-[8px] w-px bg-[var(--border-muted)]" />

        {turns.map((turn, index) => {
          const tokens = getTotalTokens(turn)
          const rowTop = `${((index + 0.5) / barCount) * 100}%`
          const tokenWidth = Math.max(5, (tokens / maxTokens) * 50)
          const pressureRatio = tokens / maxTokens
          const isAnomaly = anomalyIndexes.includes(index)
          const isUserTurn = !!turn.user_message
          const isCompaction = hasCompaction(turn)
          const inView = visibleRange ? index >= visibleRange.start && index <= visibleRange.end : false
          const color = isAnomaly ? 'var(--error)' : getPressureColor(pressureRatio)

          return (
            <div
              key={index}
              className="pointer-events-none absolute left-0 right-0 h-3 -translate-y-1/2"
              style={{ top: rowTop }}
              title={`Turn ${turn.turn_index}: ${tokens.toLocaleString()} tokens${isAnomaly ? ' · 异常' : ''}${isUserTurn ? ' · 用户输入' : ''}`}
            >
              <span
                className="absolute left-[6px] top-1/2 h-1.5 w-1.5 -translate-y-1/2 rounded-full"
                style={{ background: isAnomaly ? 'var(--error)' : isUserTurn ? 'var(--success)' : 'var(--text-muted)' }}
              />
              <span
                className="absolute left-[23px] top-1/2 h-2 -translate-y-1/2 rounded-sm"
                style={{
                  width: tokenWidth,
                  background: color,
                  boxShadow: inView ? '0 0 0 1px var(--accent-blue)' : undefined,
                }}
              />
              {isCompaction && (
                <span className="absolute right-[15px] top-1/2 h-2.5 w-1 -translate-y-1/2 rounded-sm bg-[var(--accent-blue)]" />
              )}
              {isUserTurn && (
                <span className="absolute right-[6px] top-1/2 h-1.5 w-1.5 -translate-y-1/2 rounded-full bg-[var(--success)]" />
              )}
            </div>
          )
        })}

        {visibleRange && (
          <div
            className="absolute left-0 right-0 pointer-events-none"
            style={{
              top: `${(visibleRange.start / barCount) * 100}%`,
              height: `${Math.max((visibleCount / barCount) * 100, 4)}%`,
              background: 'rgba(37, 99, 235, 0.14)',
              borderTop: '1px solid var(--accent-blue)',
              borderBottom: '1px solid var(--accent-blue)',
              boxShadow: isDragging ? 'inset 0 0 0 1px var(--accent-blue)' : undefined,
            }}
          >
            <span className="absolute left-1/2 top-0 h-1 w-3 -translate-x-1/2 rounded-b-sm bg-[var(--accent-blue)]" />
            <span className="absolute bottom-0 left-1/2 h-1 w-3 -translate-x-1/2 rounded-t-sm bg-[var(--accent-blue)]" />
          </div>
        )}
      </div>

      <div className="relative flex h-[22px] flex-shrink-0 items-center justify-center border-t border-[var(--border-muted)] bg-[var(--bg-surface)]">
        <span className="text-meta text-[var(--text-muted)]">
          {visibleRange ? `${visibleRange.start + 1}-${visibleRange.end + 1}` : '1'} / {barCount}
        </span>
        {anomalyIndexes.length > 0 && (
          <button
            onClick={() => setShowAnomalyFilter(v => !v)}
            className={`absolute right-1 text-meta ${hiddenAnomalyTypes.size > 0 ? 'text-[var(--text-muted)]' : 'text-[var(--error)]'} hover:opacity-80 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]`}
            aria-label="异常类型过滤"
          >
            {anomalyIndexes.length}!
          </button>
        )}
        {showAnomalyFilter && (
          <div className="absolute bottom-full right-0 mb-1 min-w-[140px] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-2 shadow-md z-20">
            {['tool_failure', 'duration_spike', 'missing_shutdown'].map(type => (
              <label key={type} className="flex cursor-pointer items-center gap-1.5 rounded-sm px-1 py-0.5 text-meta text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]">
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
    </nav>
  )
}
