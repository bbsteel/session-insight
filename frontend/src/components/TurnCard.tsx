import type { TurnVM } from '../types'
import Badge from './Badge'
import MarkdownRenderer from './MarkdownRenderer'

function fmtTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`
  return String(n)
}

function fmtDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
}

interface Props {
  turn: TurnVM
}

export default function TurnCard({ turn }: Props) {
  const totalTokens = turn.token_usage.prompt_tokens + turn.token_usage.completion_tokens
  const hasAnomaly = turn.anomalies && turn.anomalies.length > 0

  return (
    <div id={`turn-${turn.turn_index}`} className={`border-b border-[var(--border-muted)] ${hasAnomaly ? 'border-l-2 border-l-[var(--error)]' : ''}`}>
      {/* Badge bar */}
      <div className="flex items-center gap-1.5 px-4 py-1.5 bg-[var(--bg-inset)] border-b border-[var(--border-muted)]">
        <Badge label="tok" value={fmtTokens(totalTokens)} intent={totalTokens > 100000 ? 'warning' : 'default'} />
        <Badge label="tool" value={String(turn.tool_call_count)} />
        {turn.error_count > 0 && <Badge label="err" value={String(turn.error_count)} intent="error" />}
        {turn.duration_ms > 0 && <Badge label="dur" value={fmtDuration(turn.duration_ms)} />}
      </div>

      {/* User message */}
      {turn.user_message && (
        <div className="px-4 py-2">
          <div className="text-body text-[var(--text-primary)] whitespace-pre-wrap break-words">
            {turn.user_message}
          </div>
        </div>
      )}

      {/* Assistant message */}
      {turn.assistant_message && (
        <div className="px-4 py-2 prose-custom">
          <MarkdownRenderer content={turn.assistant_message} />
        </div>
      )}
    </div>
  )
}
