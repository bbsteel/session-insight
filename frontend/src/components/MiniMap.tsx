import { useMemo, useState, useRef, useCallback } from 'react'
import type { TurnVM } from '../types'

interface Props {
  turns: TurnVM[]
  visibleRange?: { start: number; end: number }
  scrollToIndexRef?: React.MutableRefObject<((index: number) => void) | null>
}

function getTotalTokens(t: TurnVM): number {
  return t.token_usage.prompt_tokens + t.token_usage.completion_tokens
}

function getPressureColor(ratio: number): string {
  if (ratio > 0.95) return 'var(--error)'
  if (ratio >= 0.8) return 'var(--warning)'
  return 'var(--success)'
}

export default function MiniMap({ turns, visibleRange, scrollToIndexRef }: Props) {
  const barCount = turns.length
  const containerRef = useRef<HTMLDivElement>(null)
  const [isDragging, setIsDragging] = useState(false)
  const [showAnomalyFilter, setShowAnomalyFilter] = useState(false)
  const [hiddenAnomalyTypes, setHiddenAnomalyTypes] = useState<Set<string>>(new Set())

  const handlePointerDown = useCallback((e: React.PointerEvent) => {
    setIsDragging(true)
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  }, [])

  const scrollByY = useCallback((clientY: number) => {
    if (!isDragging || !containerRef.current || !scrollToIndexRef?.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const y = clientY - rect.top
    const ratio = Math.max(0, Math.min(1, y / rect.height))
    const targetIndex = Math.min(barCount - 1, Math.floor(ratio * barCount))
    scrollToIndexRef.current(targetIndex)
  }, [isDragging, barCount, scrollToIndexRef])

  const handlePointerMove = useCallback((e: React.PointerEvent) => scrollByY(e.clientY), [scrollByY])
  const handlePointerUp = useCallback(() => setIsDragging(false), [])

  const anomalyIndexes = useMemo(() => turns
    .map((t, i) => ({ t, i }))
    .filter(({ t }) => (t.anomalies && t.anomalies.some(a => !hiddenAnomalyTypes.has(a))) || (t.error_count > 0 && !hiddenAnomalyTypes.has('tool_failure')))
    .map(({ i }) => i), [turns, hiddenAnomalyTypes])

  const userIndexes = useMemo(() => turns.map((t, i) => t.user_message ? i : -1).filter(i => i >= 0), [turns])
  const compactionIndexes = useMemo(() => turns.map((t, i) => t.anomalies?.some(a => a.includes('compaction') || a.includes('compression')) ? i : -1).filter(i => i >= 0), [turns])

  function jump(direction: -1 | 1, indexes?: number[]) {
    const base = visibleRange?.start ?? 0
    if (!indexes) {
      scrollToIndexRef?.current?.(Math.min(barCount - 1, Math.max(0, base + direction)))
      return
    }
    const target = direction > 0
      ? indexes.find(i => i > base)
      : [...indexes].reverse().find(i => i < base)
    if (target !== undefined) scrollToIndexRef?.current?.(target)
  }

  // Empty state
  if (barCount === 0) {
    return (
      <nav className="minimap-shell flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex items-center justify-center" aria-label="MiniMap">
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>MiniMap</span>
      </nav>
    )
  }

  const maxTokens = Math.max(...turns.map(getTotalTokens), 1)
  const rowHeight = 100 / barCount

  return (
    <nav
      className="minimap-shell flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col select-none"
      aria-label="MiniMap"
    >
      {/* Control bar — 32px */}
      {barCount > 1 && (
        <div className="flex-shrink-0 grid grid-cols-4 border-b border-[var(--border-muted)]" style={{ height: '32px' }}>
          {[
            { label: '轮次', up: () => jump(-1), down: () => jump(1), title: '上一轮/下一轮' },
            { label: '用户', up: () => jump(-1, userIndexes), down: () => jump(1, userIndexes), title: '上个/下个用户输入' },
            { label: '异常', up: () => jump(-1, anomalyIndexes), down: () => jump(1, anomalyIndexes), title: '上个/下个异常点' },
            { label: '压缩', up: () => jump(-1, compactionIndexes), down: () => jump(1, compactionIndexes), title: '上个/下个压缩点' },
          ].map(item => (
            <div key={item.label} className="min-w-0 border-r border-[var(--border-muted)] last:border-r-0">
              <button
                className="h-4 w-full flex items-center justify-center hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-[8px] text-[var(--text-muted)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]"
                onClick={item.up}
                title={`${item.title}：向上`}
                aria-label={`${item.label}向上`}
              >▲</button>
              <button
                className="h-4 w-full flex items-center justify-center hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-[8px] text-[var(--text-muted)] border-t border-[var(--border-muted)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]"
                onClick={item.down}
                title={`${item.title}：向下`}
                aria-label={`${item.label}向下`}
              >▼</button>
            </div>
          ))}
        </div>
      )}

      {/* Channel area: 5px type + 44px token/pressure + 5px compaction + 5px user */}
      <div
        ref={containerRef}
        className="flex-1 relative overflow-hidden"
        style={{ touchAction: 'none', cursor: isDragging ? 'grabbing' : 'grab' }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
      >
        <div className="absolute inset-0">
          {turns.map((turn, i) => {
            const tokens = getTotalTokens(turn)
            const widthPercent = Math.max((tokens / maxTokens) * 100, 5)
            const pressureRatio = tokens / maxTokens
            const hasAnomaly = turn.anomalies && turn.anomalies.some(a => !hiddenAnomalyTypes.has(a))
            const hasError = turn.error_count > 0 && !hiddenAnomalyTypes.has('tool_failure')
            const isUserTurn = !!turn.user_message
            const hasCompaction = turn.anomalies?.some(a => a.includes('compaction') || a.includes('compression'))
            const inView = !visibleRange || (i >= visibleRange.start && i <= visibleRange.end)
            const barColor = (hasAnomaly || hasError) ? 'var(--error)' : getPressureColor(pressureRatio)
            const top = `${i * rowHeight}%`
            const height = `${Math.max(rowHeight, 0.4)}%`

            return (
              <button
                key={i}
                className="absolute left-0 right-0 grid items-center transition-opacity duration-fast hover:bg-[var(--accent-blue)]/10 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]"
                style={{
                  top,
                  height,
                  gridTemplateColumns: '5px 44px 5px 5px',
                  columnGap: '2px',
                  paddingLeft: '2px',
                  paddingRight: '1px',
                  opacity: inView ? 1 : 0.35,
                }}
                onClick={(e) => {
                  e.stopPropagation()
                  scrollToIndexRef?.current?.(i)
                }}
                aria-label={`Turn ${turn.turn_index}, ${tokens.toLocaleString()} tokens, ${turn.tool_call_count} tools, ${(turn.anomalies?.length || 0) + turn.error_count} anomalies`}
                title={`Turn ${turn.turn_index}: ${tokens.toLocaleString()} tokens${hasAnomaly ? ' ⚠ ' + turn.anomalies?.join(', ') : ''}${isUserTurn ? ' 💬 user' : ''}`}
              >
                <span className="h-1.5 w-1.5 rounded-full flex items-center justify-center text-[7px]" style={{ color: hasAnomaly || hasError ? 'var(--error)' : 'var(--text-muted)' }}>
                  {hasAnomaly || hasError ? '!' : isUserTurn ? '›' : '•'}
                </span>
                <div
                  className="h-1.5 rounded-sm"
                  style={{
                    width: `${widthPercent}%`,
                    background: barColor,
                  }}
                />
                <span className="h-1.5 w-1.5 rounded-sm" style={{ background: hasCompaction ? 'var(--accent-blue)' : 'transparent' }} />
                <span className="h-1.5 w-1.5 rounded-full" style={{ background: isUserTurn ? 'var(--success)' : 'transparent' }} />
              </button>
            )
          })}
        </div>

        {/* Viewport indicator */}
        {visibleRange && barCount > 1 && (
          <div
            className="absolute left-0 right-0 pointer-events-none"
            style={{
              top: `${(visibleRange.start / barCount) * 100}%`,
              height: `${Math.max(((visibleRange.end - visibleRange.start + 1) / barCount) * 100, 3)}%`,
              background: 'rgba(37, 99, 235, var(--opacity-viewport))',
              opacity: isDragging ? 1 : 0.9,
              borderTop: '1px solid var(--accent-blue)',
              borderBottom: '1px solid var(--accent-blue)',
              transition: isDragging ? 'none' : 'opacity 150ms ease-out',
            }}
          >
            <span className="absolute -top-1 left-1/2 -translate-x-1/2 text-[8px] leading-none text-[var(--accent-blue)]">▲</span>
            <span className="absolute -bottom-1 left-1/2 -translate-x-1/2 text-[8px] leading-none text-[var(--accent-blue)]">▼</span>
          </div>
        )}
      </div>

      {/* Footer — turn count + anomalies */}
      <div className="flex-shrink-0 text-center py-0.5 border-t border-[var(--border-muted)] flex items-center justify-center gap-1.5 relative">
        <span className="text-meta text-[var(--text-muted)]">{barCount}t</span>
        {turns.filter(t => t.anomalies?.length || t.error_count > 0).length > 0 && (
          <button
            onClick={() => setShowAnomalyFilter(v => !v)}
            className={`text-meta ${hiddenAnomalyTypes.size > 0 ? 'text-[var(--text-muted)]' : 'text-[var(--error)]'} hover:opacity-80 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-blue)]`}
            aria-label="异常类型过滤"
          >
            {turns.filter(t => (t.anomalies?.length || t.error_count > 0) &&
              (!t.anomalies || t.anomalies.some(a => !hiddenAnomalyTypes.has(a))) &&
              (!t.error_count || !hiddenAnomalyTypes.has('tool_failure'))
            ).length} ⚠
          </button>
        )}
        {showAnomalyFilter && (
          <div className="absolute bottom-full right-0 mb-1 bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-md p-2 shadow-md z-20 min-w-[140px]">
            {['tool_failure', 'duration_spike', 'missing_shutdown'].map(type => (
              <label key={type} className="flex items-center gap-1.5 py-0.5 text-meta text-[var(--text-primary)] cursor-pointer hover:bg-[var(--bg-surface-hover)] px-1 rounded-sm">
                <input
                  type="checkbox"
                  checked={!hiddenAnomalyTypes.has(type)}
                  onChange={() => setHiddenAnomalyTypes(prev => {
                    const next = new Set(prev)
                    prev.has(type) ? next.delete(type) : next.add(type)
                    return next
                  })}
                  className="w-3 h-3"
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
