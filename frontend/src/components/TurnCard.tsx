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
  const [showTokens, setShowTokens] = useState(false)

  return (
    <div
      id={`turn-${turn.turn_index}`}
      className={`border-b border-[var(--border-muted)] ${hasAnomaly ? 'border-l-2 border-l-[var(--error)]' : ''}`}
    >
      {/* Badge bar */}
      <div className={`flex items-center gap-1.5 flex-wrap ${isTight ? 'px-2 py-0.5' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
        <button onClick={() => setShowTokens(v => !v)} className="cursor-pointer">
            <Badge label="tok" value={fmtTokens(totalTokens)} intent={totalTokens > 100000 ? 'warning' : 'default'} />
          </button>
        {turn.tool_call_count > 0 && (
          <button onClick={() => setShowTools(v => !v)} className="cursor-pointer">
            <Badge label="tool" value={String(turn.tool_call_count)} />
          </button>
        )}
        {turn.error_count > 0 && <Badge label="err" value={String(turn.error_count)} intent="error" />}
        {turn.duration_ms > 0 && <Badge label="dur" value={fmtDuration(turn.duration_ms)} />}
      </div>

      {/* Token breakdown */}
      {showTokens && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
          <div className="text-helper flex items-center gap-3">
            <span className="text-[var(--text-secondary)]">Tokens:</span>
            <span className="text-[var(--text-primary)] font-medium">{fmtTokens(totalTokens)}</span>
            {turn.token_usage.prompt_tokens > 0 && <span className="text-[var(--text-muted)]">prompt {fmtTokens(turn.token_usage.prompt_tokens)}</span>}
            {turn.token_usage.completion_tokens > 0 && <span className="text-[var(--text-muted)]">compl {fmtTokens(turn.token_usage.completion_tokens)}</span>}
            {turn.token_usage.cache_read_tokens > 0 && <span className="text-[var(--accent-green)]">cache {fmtTokens(turn.token_usage.cache_read_tokens)}</span>}
            {turn.token_usage.premium_requests > 0 && <span className="text-[var(--warning)]">premium {turn.token_usage.premium_requests}</span>}
          </div>
        </div>
      )}

      {/* Tool expansion */}
      {showTools && turn.tool_details && turn.tool_details.length > 0 && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
          <div className="text-helper flex flex-col gap-0.5">
            {turn.tool_details.map((td, i) => (
              <div key={i} className="flex items-center gap-1.5">
                <span className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${td.exit_code === 0 ? 'bg-[var(--success)]' : 'bg-[var(--error)]'}`} />
                <span className="text-[var(--text-primary)] text-meta">{td.name}</span>
                {td.duration_ms > 0 && <span className="text-[var(--text-muted)] text-meta">{td.duration_ms}ms</span>}
                {td.exit_code !== 0 && <span className="text-[var(--error)] text-meta">exit {td.exit_code}</span>}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Fallback: tool names only */}
      {showTools && (!turn.tool_details || turn.tool_details.length === 0) && turn.tool_names && turn.tool_names.length > 0 && (
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

      {/* Skill invocations */}
      {turn.skills && turn.skills.length > 0 && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)]/30 border-b border-[var(--border-muted)]`}>
          <div className="text-helper text-[var(--text-muted)] flex flex-wrap items-center gap-1.5">
            <span className="text-meta">&#9881;</span>
            {turn.skills.map((name, i) => (
              <span key={i} className="bg-[var(--accent-purple)]/10 text-[var(--accent-purple)] px-1.5 py-0.5 rounded-sm text-meta font-medium">{name}</span>
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
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} text-helper text-[var(--text-muted)]`}>
          &#128274; Content encrypted. Run <code className="bg-[var(--bg-surface)] px-1 rounded-sm text-meta">copilot config set contentEncryption false</code> to enable viewing.
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
