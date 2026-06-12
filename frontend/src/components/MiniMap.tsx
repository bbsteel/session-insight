import { useState, useRef, useCallback } from 'react'
import type { TurnVM } from '../types'

interface Props {
  turns: TurnVM[]
  visibleRange?: { start: number; end: number }
  scrollToIndexRef?: React.MutableRefObject<((index: number) => void) | null>
}

function getTotalTokens(t: TurnVM): number {
  return t.token_usage.prompt_tokens + t.token_usage.completion_tokens
}

export default function MiniMap({ turns, visibleRange, scrollToIndexRef }: Props) {
  const barCount = turns.length
  const containerRef = useRef<HTMLDivElement>(null)
  const [isDragging, setIsDragging] = useState(false)

  // Empty state
  if (barCount === 0) {
    return (
      <nav className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex items-center justify-center" style={{ width: '64px' }}>
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>MiniMap</span>
      </nav>
    )
  }

  const maxTokens = Math.max(...turns.map(getTotalTokens), 1)

  // Pointer event handlers for drag scroll
  const handlePointerDown = useCallback((e: React.PointerEvent) => {
    setIsDragging(true)
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  }, [])

  const handlePointerMove = useCallback((e: React.PointerEvent) => {
    if (!isDragging || !containerRef.current || !scrollToIndexRef?.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const y = e.clientY - rect.top
    const ratio = Math.max(0, Math.min(1, y / rect.height))
    const targetIndex = Math.floor(ratio * barCount)
    scrollToIndexRef.current(targetIndex)
  }, [isDragging, barCount, scrollToIndexRef])

  const handlePointerUp = useCallback(() => setIsDragging(false), [])

  return (
    <nav
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col select-none"
      style={{ width: '64px' }}
    >
      {/* Control bar — 32px */}
      {barCount > 1 && (
        <div className="flex-shrink-0 flex flex-col border-b border-[var(--border-muted)]" style={{ height: '32px' }}>
          <button
            className="flex-1 flex items-center justify-center hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-meta text-[var(--text-muted)]"
            onClick={() => {
              const idx = Math.max(0, (visibleRange?.start ?? 0) - 1)
              scrollToIndexRef?.current?.(idx)
            }}
            title="Previous turn (k/↑)"
          >▲</button>
          <button
            className="flex-1 flex items-center justify-center hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-meta text-[var(--text-muted)] border-t border-[var(--border-muted)]"
            onClick={() => {
              const idx = Math.min(barCount - 1, (visibleRange?.start ?? 0) + 1)
              scrollToIndexRef?.current?.(idx)
            }}
            title="Next turn (j/↓)"
          >▼</button>
        </div>
      )}

      {/* Bar area — 3 channels */}
      <div
        ref={containerRef}
        className="flex-1 relative overflow-hidden"
        style={{ touchAction: 'none', cursor: isDragging ? 'grabbing' : 'grab' }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
      >
        <div className="h-full flex px-0.5" style={{ gap: '0.5px' }}>
          {turns.map((turn, i) => {
            const tokens = getTotalTokens(turn)
            const heightPercent = Math.max((tokens / maxTokens) * 100, 2)
            const hasAnomaly = turn.anomalies && turn.anomalies.length > 0
            const hasError = turn.error_count > 0
            const isUserTurn = !!turn.user_message
            const inView = !visibleRange || (i >= visibleRange.start && i <= visibleRange.end)

            // Color: blue normal, yellow high token, red anomaly
            let barColor = 'var(--accent-blue)'
            if (hasAnomaly || hasError) barColor = 'var(--error)'
            else if (tokens > 50000) barColor = 'var(--warning)'

            return (
              <div
                key={i}
                className="flex-shrink-0 flex items-end rounded-sm transition-opacity duration-fast"
                style={{
                  width: `calc(100% / ${barCount})`,
                  maxWidth: `${Math.min(100 / barCount, 6)}px`,
                  minWidth: '3px',
                  opacity: inView ? 1 : 0.35,
                }}
                title={`Turn ${turn.turn_index}: ${tokens.toLocaleString()} tokens${hasAnomaly ? ' ⚠ ' + turn.anomalies?.join(', ') : ''}${isUserTurn ? ' 💬 user' : ''}`}
              >
                {/* Main token bar */}
                <div
                  className="w-full rounded-sm"
                  style={{
                    height: `${heightPercent}%`,
                    minHeight: '2px',
                    background: barColor,
                  }}
                />
              </div>
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
              background: 'var(--accent-blue)',
              opacity: isDragging ? 0.25 : 0.1,
              borderTop: '1px solid var(--accent-blue)',
              borderBottom: '1px solid var(--accent-blue)',
              transition: isDragging ? 'none' : 'opacity 150ms ease-out',
            }}
          />
        )}
      </div>

      {/* Footer — turn count */}
      <div className="flex-shrink-0 text-center py-0.5 border-t border-[var(--border-muted)]">
        <span className="text-meta text-[var(--text-muted)]">{barCount}</span>
      </div>
    </nav>
  )
}
