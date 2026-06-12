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
  mode?: 'full' | 'digest'
  density?: 'standard' | 'tight'
}

export default function TurnCard({ turn, mode = 'full', density = 'standard' }: Props) {
  const isTight = density === 'tight'
  const isDigest = mode === 'digest'
  const totalTokens = turn.token_usage.prompt_tokens + turn.token_usage.completion_tokens
  const hasAnomaly = turn.anomalies && turn.anomalies.length > 0

  return (
    <div
      id={`turn-${turn.turn_index}`}
      className={`border-b border-[var(--border-muted)] ${hasAnomaly ? 'border-l-2 border-l-[var(--error)]' : ''}`}
    >
      {/* Badge bar */}
      <div className={`flex items-center gap-1.5 ${isTight ? 'px-2 py-0.5' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
        <Badge label="tok" value={fmtTokens(totalTokens)} intent={totalTokens > 100000 ? 'warning' : 'default'} />
        {turn.tool_call_count > 0 && <Badge label="tool" value={String(turn.tool_call_count)} />}
        {turn.error_count > 0 && <Badge label="err" value={String(turn.error_count)} intent="error" />}
        {turn.duration_ms > 0 && <Badge label="dur" value={fmtDuration(turn.duration_ms)} />}
      </div>

      {/* Turn index + summary line (Digest mode only) */}
      {isDigest && (
        <div className={`${isTight ? 'px-2 py-0.5' : 'px-4 py-1'} text-helper text-[var(--text-muted)] border-b border-[var(--border-muted)]`}>
          Turn {turn.turn_index}
          {turn.tool_call_count > 0 && ` · ${turn.tool_call_count} tools`}
          {turn.duration_ms > 0 && ` · ${fmtDuration(turn.duration_ms)}`}
          {hasAnomaly && ` · ⚠ ${turn.anomalies!.join(', ')}`}
        </div>
      )}

      {/* User message */}
      {turn.user_message && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-2'}`}>
          <div className={`text-[var(--text-primary)] whitespace-pre-wrap break-words ${isTight ? 'text-nav' : 'text-body'}`}>
            {turn.user_message}
          </div>
        </div>
      )}

      {/* Assistant message */}
      {turn.assistant_message && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-2'} prose-custom`}>
          <MarkdownRenderer content={turn.assistant_message} />
        </div>
      )}
    </div>
  )
}
