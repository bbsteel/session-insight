import { useState } from 'react'
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
  const [showTools, setShowTools] = useState(false)

  return (
    <div
      id={`turn-${turn.turn_index}`}
      className={`border-b border-[var(--border-muted)] ${hasAnomaly ? 'border-l-2 border-l-[var(--error)]' : ''}`}
    >
      {/* Badge bar */}
      <div className={`flex items-center gap-1.5 ${isTight ? 'px-2 py-0.5' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
        <Badge label="tok" value={fmtTokens(totalTokens)} intent={totalTokens > 100000 ? 'warning' : 'default'} />
        {turn.tool_call_count > 0 && (
          <button onClick={() => setShowTools(v => !v)} className="cursor-pointer">
            <Badge label="tool" value={String(turn.tool_call_count)} />
          </button>
        )}
        {turn.error_count > 0 && <Badge label="err" value={String(turn.error_count)} intent="error" />}
        {turn.duration_ms > 0 && <Badge label="dur" value={fmtDuration(turn.duration_ms)} />}
      </div>

      {/* Tool name expansion */}
      {showTools && turn.tool_names && turn.tool_names.length > 0 && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
          <div className="text-helper text-[var(--text-secondary)] flex flex-wrap gap-1">
            {turn.tool_names.map((name, i) => (
              <span key={i} className="bg-[var(--bg-surface)] px-1.5 py-0.5 rounded-sm text-meta">{name}</span>
            ))}
          </div>
        </div>
      )}

      {/* Subagent cards */}
      {turn.subagents && turn.subagents.length > 0 && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)]/50 border-b border-[var(--border-muted)]`}>
          <div className="text-helper text-[var(--text-muted)] flex flex-wrap items-center gap-1.5">
            <span className="text-meta">&#129302;</span>
            {turn.subagents.map((name, i) => (
              <span key={i} className="bg-[var(--accent-coral)]/10 text-[var(--accent-coral)] px-1.5 py-0.5 rounded-sm text-meta font-medium">{name}</span>
            ))}
          </div>
        </div>
      )}

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

      {/* Encrypted content notice */}
      {!turn.assistant_message && totalTokens > 0 && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} text-helper text-[var(--text-muted)] italic`}>
          &#128274; Content encrypted by Copilot. Disable content encryption in Copilot settings to view.
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
