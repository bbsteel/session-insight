import { useState } from 'react'
import type { TurnVM } from '../types'
import Badge from './Badge'
import MarkdownRenderer from './MarkdownRenderer'
import { resolveAgentStyle } from '../agentStyles'

function fmtTokens(n: number): string {
  return n.toLocaleString()
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
  cumulativeTokens?: number
  agentType?: string
}

export default function TurnCard({ turn, mode = 'full', density = 'standard', cumulativeTokens, agentType }: Props) {
  const agentStyle = resolveAgentStyle(agentType)
  const isTight = density === 'tight'
  const isDigest = mode === 'digest'
  const totalTokens = turn.token_usage.prompt_tokens + turn.token_usage.completion_tokens
  const hasAnomaly = turn.anomalies && turn.anomalies.length > 0
  const [showTools, setShowTools] = useState(false)
  const [showTokens, setShowTokens] = useState(false)

  return (
    <div
      id={`turn-${turn.turn_index}`}
      className={`border-b border-[var(--border-muted)] bg-[var(--bg-surface)] ${hasAnomaly ? 'border-l-2 border-l-[var(--error)]' : ''}`}
    >
      {/* Badge bar */}
      <div role="list" className={`flex items-center gap-1.5 flex-wrap ${isTight ? 'px-2 py-1' : 'px-4 py-2'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`} style={agentStyle ? { borderLeft: `2px solid ${agentStyle.accent}` } : undefined}>
        <span className="mr-1 text-meta text-[var(--text-muted)]">Turn {turn.turn_index}</span>
        <button onClick={() => setShowTokens(v => !v)} className="cursor-pointer rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">
            <Badge label="Token" value={fmtTokens(totalTokens)} intent={totalTokens > 100000 ? 'warning' : 'default'} />
          </button>
        {cumulativeTokens !== undefined && <Badge label="Context" value={fmtTokens(cumulativeTokens)} />}
        {turn.tool_call_count > 0 && (
          <button onClick={() => setShowTools(v => !v)} className="cursor-pointer rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">
            <Badge label="Tool" value={String(turn.tool_call_count)} />
          </button>
        )}
        {turn.error_count > 0 && <Badge label="Err" value={String(turn.error_count)} intent="error" />}
        {turn.duration_ms > 0 && <Badge label="Duration" value={fmtDuration(turn.duration_ms)} />}
      </div>

      {/* Token breakdown */}
      {showTokens && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
          <div className="text-helper flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="text-[var(--text-secondary)]">Token 分解:</span>
            <span className="text-[var(--text-primary)] font-medium">{fmtTokens(totalTokens)}</span>
            {turn.token_usage.prompt_tokens > 0 && <span className="text-[var(--text-muted)]">prompt {fmtTokens(turn.token_usage.prompt_tokens)}</span>}
            {turn.token_usage.completion_tokens > 0 && <span className="text-[var(--text-muted)]">compl {fmtTokens(turn.token_usage.completion_tokens)}</span>}
            {turn.token_usage.cache_read_tokens > 0 && <span className="text-[var(--accent-green)]">cache {fmtTokens(turn.token_usage.cache_read_tokens)}</span>}
            {turn.token_usage.premium_requests > 0 && <span className="text-[var(--warning)]">premium {turn.token_usage.premium_requests}</span>}
            {cumulativeTokens !== undefined && (
              <span className="text-[var(--text-muted)]">context {fmtTokens(cumulativeTokens)}</span>
            )}
          </div>
        </div>
      )}

      {/* Tool expansion */}
      {showTools && turn.tool_details && turn.tool_details.length > 0 && (
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-1.5'} bg-[var(--bg-inset)] border-b border-[var(--border-muted)]`}>
          <div className="text-helper flex flex-col gap-0.5">
            {turn.tool_details.map((td, i) => (
              <div key={i} className="flex items-center gap-1.5">
                <span className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${
                  td.rejected ? 'bg-[var(--warning)]' :
                  td.timed_out ? 'bg-[var(--error)]' :
                  td.exit_code === 0 ? 'bg-[var(--success)]' : 'bg-[var(--error)]'
                }`} />
                <span className="text-[var(--text-primary)] text-meta">{td.name}</span>
                {td.tool_kind && <span className="text-[var(--text-muted)] text-meta">[{td.tool_kind}]</span>}
                {td.duration_ms > 0 && <span className="text-[var(--text-muted)] text-meta">{td.duration_ms}ms</span>}
                {td.rejected && <span className="text-[var(--warning)] text-meta">rejected</span>}
                {td.timed_out && <span className="text-[var(--error)] text-meta">timeout{td.timeout_seconds ? ` (${td.timeout_seconds}s)` : ''}</span>}
                {td.exit_code !== 0 && !td.rejected && !td.timed_out && <span className="text-[var(--error)] text-meta">exit {td.exit_code}</span>}
                {td.error_kind && td.error_kind !== 'failed' && !td.rejected && !td.timed_out && <span className="text-[var(--text-muted)] text-meta">{td.error_kind}</span>}
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
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-2'} grid gap-2${agentStyle ? ' bg-[var(--user-bg)]' : ''}`} style={{ gridTemplateColumns: '18px minmax(0, 1fr)' }}>
          <div className="text-body leading-5" style={{ color: agentStyle ? agentStyle.accent : 'var(--text-muted)' }}>
            {agentStyle?.userPrefix ?? '>'}
          </div>
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
        <div className={`${isTight ? 'px-2 py-1' : 'px-4 py-2'} grid gap-2${agentStyle ? ' bg-[var(--assistant-bg)]' : ''}`} style={{ gridTemplateColumns: '18px minmax(0, 1fr)' }}>
          <div className="text-body leading-5" style={{ color: agentStyle ? agentStyle.accent : 'var(--accent-blue)' }}>
            {agentStyle?.assistantPrefix ?? '●'}
          </div>
          <div className="prose-custom min-w-0">
            <MarkdownRenderer content={turn.assistant_message} />
          </div>
        </div>
      )}
    </div>
  )
}
