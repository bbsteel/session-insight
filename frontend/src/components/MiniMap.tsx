import type { TurnVM } from '../types'

interface Props {
  turns: TurnVM[]
  visibleRange?: { start: number; end: number }
}

function getTotalTokens(t: TurnVM): number {
  return t.token_usage.prompt_tokens + t.token_usage.completion_tokens
}

export default function MiniMap({ turns, visibleRange }: Props) {
  const barCount = turns.length

  if (barCount === 0) {
    return (
      <nav
        className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)]"
        style={{ width: '64px' }}
      >
        <div className="flex items-center justify-center h-full">
          <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>
            MiniMap
          </span>
        </div>
      </nav>
    )
  }

  const maxTokens = Math.max(...turns.map(getTotalTokens), 1)

  // Total height minus padding (2px top + 2px bottom) for the bar area
  const barAreaPercent = 100 / barCount

  return (
    <nav
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col relative"
      style={{ width: '64px' }}
    >
      {/* Scrollable bar area */}
      <div className="flex-1 overflow-hidden py-0.5 px-1">
        <div className="h-full flex flex-col" style={{ gap: '1px' }}>
          {turns.map((turn, i) => {
            const tokens = getTotalTokens(turn)
            const widthPercent = Math.max((tokens / maxTokens) * 100, 3)
            const hasError = turn.error_count > 0

            let color: string
            if (hasError) {
              color = 'var(--error)'
            } else if (tokens > 50000) {
              color = 'var(--warning)'
            } else {
              color = 'var(--accent-blue)'
            }

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
                  opacity: !visibleRange || (i >= visibleRange.start && i <= visibleRange.end) ? 1 : 0.45,
                }}
                title={`Turn ${turn.turn_index}: ${tokens.toLocaleString()} tokens${hasError ? ' (errors)' : ''}`}
              />
            )
          })}
        </div>
      </div>

      {/* Viewport indicator overlay */}
      {visibleRange && barCount > 1 && (
        <div
          className="absolute left-0 right-0 pointer-events-none border-l-2 border-[var(--accent-blue)]"
          style={{
            top: `${(visibleRange.start / barCount) * 100}%`,
            height: `${Math.max(((visibleRange.end - visibleRange.start + 1) / barCount) * 100, 1)}%`,
            background: 'var(--accent-blue)',
            opacity: 0.1,
          }}
        />
      )}

      {/* Turn count label */}
      <div className="flex-shrink-0 text-center py-1 border-t border-[var(--border-muted)]">
        <span className="text-meta text-[var(--text-muted)]">{barCount}</span>
      </div>
    </nav>
  )
}
