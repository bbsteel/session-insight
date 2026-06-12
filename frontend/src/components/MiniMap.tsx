import { useState, useRef, useCallback } from 'react'
import type { TurnVM } from '../types'

interface Props {
  turns: TurnVM[]
  visibleRange?: { start: number; end: number }
  scrollToIndexRef?: React.MutableRefObject<((index: number) => void) | null>
  currentTurnIndex?: number
  onTurnNav?: (direction: 'prev' | 'next') => void
}

function getTotalTokens(t: TurnVM): number {
  return t.token_usage.prompt_tokens + t.token_usage.completion_tokens
}

export default function MiniMap({ turns, visibleRange, scrollToIndexRef, currentTurnIndex, onTurnNav }: Props) {
  const barCount = turns.length
  const containerRef = useRef<HTMLDivElement>(null)
  const [isDragging, setIsDragging] = useState(false)

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

  const handlePointerUp = useCallback(() => {
    setIsDragging(false)
  }, [])

  if (barCount === 0) {
    return (
      <nav className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex items-center justify-center" style={{ width: '64px' }}>
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>MiniMap</span>
      </nav>
    )
  }

  const maxTokens = Math.max(...turns.map(getTotalTokens), 1)
  const barAreaPercent = 100 / barCount

  return (
    <nav
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col relative"
      style={{ width: '64px' }}
    >
      {/* Turn nav controls */}
      {barCount > 1 && (
        <div className="flex-shrink-0 flex flex-col border-b border-[var(--border-muted)]" style={{ height: '32px' }}>
          <button
            className="flex-1 flex items-center justify-center hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-meta text-[var(--text-muted)]"
            onClick={() => scrollToIndexRef?.current?.(Math.max(0, (visibleRange?.start ?? 0) - 1))}
            title="Previous turn (k)"
          >
            ▲
          </button>
          <button
            className="flex-1 flex items-center justify-center hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast text-meta text-[var(--text-muted)] border-t border-[var(--border-muted)]"
            onClick={() => {
              const nextIndex = (visibleRange?.start ?? 0) + 1
              if (nextIndex < barCount) scrollToIndexRef?.current?.(nextIndex)
            }}
            title="Next turn (j)"
          >
            ▼
          </button>
        </div>
      )}

      {/* Bar area — draggable */}
      <div
        ref={containerRef}
        className="flex-1 overflow-hidden py-0.5 px-1 relative cursor-grab"
        style={{ touchAction: 'none' }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
      >
        <div className="h-full flex flex-col" style={{ gap: '1px' }}>
          {turns.map((turn, i) => {
            const tokens = getTotalTokens(turn)
            const widthPercent = Math.max((tokens / maxTokens) * 100, 3)
            const hasError = turn.error_count > 0
            const inView = !visibleRange || (i >= visibleRange.start && i <= visibleRange.end)

            let color: string
            if (hasError) color = 'var(--error)'
            else if (tokens > 50000) color = 'var(--warning)'
            else color = 'var(--accent-blue)'

            return (
              <div
                key={i}
                className="rounded-sm flex-shrink-0 transition-colors duration-fast"
                style={{
                  height: `${barAreaPercent}%`,
                  minHeight: '1px',
                  maxHeight: '6px',
                  width: `${widthPercent}%`,
                  background: color,
                  opacity: inView ? 1 : 0.45,
                }}
                title={`Turn ${turn.turn_index}: ${tokens.toLocaleString()} tokens`}
              />
            )
          })}
        </div>
      </div>

      {/* Viewport indicator */}
      {visibleRange && barCount > 1 && (
        <div
          className="absolute left-0 right-0 pointer-events-none"
          style={{
            top: `${(visibleRange.start / barCount) * 100}%`,
            height: `${Math.max(((visibleRange.end - visibleRange.start + 1) / barCount) * 100, 2)}%`,
            background: 'var(--accent-blue)',
            opacity: isDragging ? 0.2 : 0.08,
            borderLeft: '2px solid var(--accent-blue)',
            transition: isDragging ? 'none' : 'opacity 150ms ease-out',
          }}
        />
      )}

      {/* Footer */}
      <div className="flex-shrink-0 text-center py-0.5 border-t border-[var(--border-muted)]">
        <span className="text-meta text-[var(--text-muted)]">{barCount}</span>
      </div>
    </nav>
  )
}
