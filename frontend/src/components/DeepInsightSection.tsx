import { useEffect, useRef, useState } from 'react'
import {
  fetchLatestInsight, fetchLLMProviders, generateInsight, parseInsightMetadata,
  revokeInsightTargets, InsightBlockedError, NoProviderError,
  type InsightItem, type InsightResult, type InsightEvidenceRef, type SendPreview,
} from '../api'

interface Props {
  sessionId: string
  agentType: string
  isLive: boolean
  findingsCount: number
  onJumpToTurn?: (turnIndex: number) => void
}

interface StageLine { text: string; ms: number | null }

const confidenceLabel: Record<string, string> = { high: '高', medium: '中', low: '低' }
const epistemicLabel: Record<string, string> = { observed: '观察事实', inferred: '推断', unknown: '无法判断' }
const strengthLabel: Record<string, string> = { none: '无因果', weak: '弱因果', moderate: '中等因果', strong: '强因果' }

// DeepInsightSection is the second analysis layer: it explains why the local
// findings occurred, using a configured model, only when the user asks. It
// never auto-generates and refuses live sessions (whose bill is still moving).
export default function DeepInsightSection({ sessionId, agentType, isLive, findingsCount, onJumpToTurn }: Props) {
  const [result, setResult] = useState<InsightResult | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [hasProvider, setHasProvider] = useState(false)
  const [busy, setBusy] = useState(false)
  const [stages, setStages] = useState<StageLine[]>([])
  const [error, setError] = useState<string | null>(null)
  const [blocked, setBlocked] = useState<string | null>(null)
  const [preview, setPreview] = useState<SendPreview | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => () => abortRef.current?.abort(), [])

  useEffect(() => {
    const load = () => fetchLLMProviders().then(d => setHasProvider(d.providers.length > 0)).catch(() => {})
    load()
    window.addEventListener('si-ai-providers-changed', load)
    return () => window.removeEventListener('si-ai-providers-changed', load)
  }, [])

  // Load the cached latest insight (with its freshness) once.
  useEffect(() => {
    let cancelled = false
    setLoaded(false)
    fetchLatestInsight(sessionId, agentType)
      .then(r => { if (!cancelled) { setResult(r); setLoaded(true) } })
      .catch(() => { if (!cancelled) setLoaded(true) })
    return () => { cancelled = true }
  }, [sessionId, agentType])

  const run = async (confirm: boolean) => {
    const ac = new AbortController()
    abortRef.current = ac
    let lines: StageLine[] = []
    let lastAt = performance.now()
    const finalize = () => {
      if (lines.length > 0 && lines[lines.length - 1].ms == null) {
        lines = [...lines]
        lines[lines.length - 1] = { ...lines[lines.length - 1], ms: performance.now() - lastAt }
      }
      return lines
    }
    setBusy(true); setError(null); setBlocked(null); setStages([]); setPreview(null)
    try {
      const out = await generateInsight(sessionId, stage => {
        finalize(); lines = [...lines, { text: stage, ms: null }]; lastAt = performance.now(); setStages(lines)
      }, ac.signal, 0, confirm)
      if ('needs_confirmation' in out) {
        setPreview(out); setBusy(false); return
      }
      setResult(out); setBusy(false); setStages(finalize())
    } catch (err) {
      if (ac.signal.aborted) { setBusy(false); return }
      setBusy(false)
      if (err instanceof NoProviderError) setHasProvider(false)
      else if (err instanceof InsightBlockedError) setBlocked(err.reason)
      else setError(err instanceof Error ? err.message : String(err))
    }
  }

  const cancel = () => { abortRef.current?.abort(); setBusy(false) }

  if (findingsCount === 0) return null // no findings → no deep-analysis entry

  const meta = parseInsightMetadata(result?.generation.metadata)
  const evidenceMap = new Map<string, InsightEvidenceRef>()
  for (const e of meta?.cited_evidence ?? []) evidenceMap.set(e.evidence_id, e)
  const stale = result?.freshness.stale

  return (
    <div className="px-4 pb-4">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-body font-semibold text-[var(--text-primary)]">
          原因洞察
          <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">用配置的模型解释这些 Finding 为何出现</span>
        </h3>
        <div className="flex items-center gap-2">
          {busy ? (
            <button onClick={cancel} className={btnCls}>取消</button>
          ) : isLive ? (
            <span className="text-nav text-[var(--text-muted)]">会话结束后可深度分析</span>
          ) : !hasProvider ? (
            <button onClick={() => window.dispatchEvent(new Event('si-open-ai-settings'))} className={btnCls}>
              配置模型后深度分析
            </button>
          ) : (
            <button onClick={() => void run(false)} className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}>
              {result ? '重新分析' : '深度分析'}
            </button>
          )}
        </div>
      </div>

      {busy && (
        <div className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-2 font-mono text-helper text-[var(--text-secondary)]">
          {stages.map((line, i) => (
            <div key={i} className="leading-5">
              {line.ms != null
                ? <><span className="text-[var(--success)]">✓</span> {line.text}… <span className="text-meta text-[var(--text-muted)]">{(line.ms / 1000).toFixed(1)}s</span></>
                : <span className="text-[var(--text-primary)]"><span className="text-[var(--accent-blue)]">›</span> {line.text}…</span>}
            </div>
          ))}
          {stages.length === 0 && <span className="text-[var(--text-muted)]">正在准备…</span>}
        </div>
      )}

      {blocked && (
        <div className="rounded-md border border-dashed border-[var(--border-default)] p-3 text-helper text-[var(--text-secondary)]">
          {blocked === 'session_active' && '会话仍在活跃中，账单尚未结算，结束后再深度分析。'}
          {blocked === 'session_changing' && '会话数据正在变化，请稍后重试。'}
          {blocked === 'no_findings' && '没有可分析的初步 Finding。'}
          {blocked === 'not_found' && '会话不存在。'}
        </div>
      )}
      {error && <div className="mb-2 whitespace-pre-wrap break-all text-helper text-[var(--error)]">{error}</div>}

      {preview && (
        <SendPreviewCard
          preview={preview}
          onConfirm={() => void run(true)}
          onCancel={() => setPreview(null)}
        />
      )}

      {!busy && result && (
        <>
          {stale && (
            <div className="mb-2 flex items-center gap-2 rounded-md bg-[var(--warning)]/15 px-3 py-1.5 text-helper text-[var(--warning)]">
              <span>结果可能已过期（{staleReason(result.freshness.reasons)}）</span>
              <button onClick={() => void run(false)} className="underline">重新分析</button>
            </div>
          )}
          <div className="mb-2 text-meta text-[var(--text-muted)]">
            {result.generation.model_id} · {result.generation.created_at}
          </div>
          {meta?.parse_failed ? (
            <div>
              <div className="mb-1 text-helper text-[var(--warning)]">结构化解析失败，以下为原始输出（纯文本）</div>
              <pre className="whitespace-pre-wrap break-words rounded-md bg-[var(--bg-inset)] p-3 text-helper text-[var(--text-secondary)]">{result.generation.content}</pre>
            </div>
          ) : meta?.output ? (
            <InsightCards output={meta.output} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
          ) : (
            <pre className="whitespace-pre-wrap break-words text-helper text-[var(--text-secondary)]">{result.generation.content}</pre>
          )}
        </>
      )}

      {loaded && !result && !busy && !preview && !blocked && !error && (
        <div className="text-nav text-[var(--text-muted)]">
          {isLive ? '会话活跃时不生成，避免对变化中的数据反复付费。' : '尚未生成原因洞察。'}
        </div>
      )}
    </div>
  )
}

function InsightCards({ output, evidenceMap, onJumpToTurn }: {
  output: NonNullable<ReturnType<typeof parseInsightMetadata>>['output']
  evidenceMap: Map<string, InsightEvidenceRef>
  onJumpToTurn?: (t: number) => void
}) {
  if (!output) return null
  if (output.insights.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-[var(--border-default)] p-3 text-helper text-[var(--text-secondary)]">
        <div>当前证据不足以给出原因洞察。</div>
        {output.evidence_gaps && output.evidence_gaps.length > 0 && (
          <ul className="mt-1 list-disc pl-5 text-meta text-[var(--text-muted)]">
            {output.evidence_gaps.map((g, i) => <li key={i}>{g}</li>)}
          </ul>
        )}
      </div>
    )
  }
  return (
    <div className="space-y-3">
      {output.summary && <div className="text-body text-[var(--text-primary)]">{output.summary}</div>}
      {output.insights.map((ins, i) => <InsightCard key={i} ins={ins} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />)}
      {output.evidence_gaps && output.evidence_gaps.length > 0 && (
        <div className="rounded-md bg-[var(--bg-inset)] px-3 py-2">
          <div className="text-helper font-medium text-[var(--text-secondary)]">数据缺口</div>
          <ul className="mt-1 list-disc pl-5 text-meta text-[var(--text-muted)]">
            {output.evidence_gaps.map((g, i) => <li key={i}>{g}</li>)}
          </ul>
        </div>
      )}
    </div>
  )
}

function InsightCard({ ins, evidenceMap, onJumpToTurn }: {
  ins: InsightItem
  evidenceMap: Map<string, InsightEvidenceRef>
  onJumpToTurn?: (t: number) => void
}) {
  const conf = ins.confidence
  const confColor = conf === 'high' ? 'var(--error)' : conf === 'medium' ? 'var(--warning)' : 'var(--accent-blue)'
  return (
    <div className="rounded-md border-l-2 bg-[var(--bg-inset)] p-3" style={{ borderLeftColor: confColor }}>
      <div className="flex items-center justify-between">
        <div className="text-body font-medium text-[var(--text-primary)]">{ins.title}</div>
        <span className="text-meta px-1.5 py-0.5 rounded-sm" style={{ background: `color-mix(in srgb, ${confColor} 18%, transparent)`, color: confColor }}>
          置信度 {confidenceLabel[conf] ?? conf}
        </span>
      </div>

      <div className="mt-1.5 text-helper text-[var(--text-secondary)]">
        <span className="text-meta text-[var(--text-muted)]">主要原因（{epistemicLabel[ins.cause.epistemic_status] ?? ins.cause.epistemic_status}·{strengthLabel[ins.cause.causal_strength] ?? ins.cause.causal_strength}）：</span>
        {ins.cause.statement}
      </div>
      <EvidenceList label="关键证据" ids={ins.cause.evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
      {ins.cause.confounders && ins.cause.confounders.length > 0 && (
        <Bullets label="已排查的混淆因素" items={ins.cause.confounders} />
      )}

      {ins.impact.statement && (
        <div className="mt-1.5 text-helper text-[var(--text-secondary)]">
          <span className="text-meta text-[var(--text-muted)]">影响：</span>{ins.impact.statement}
        </div>
      )}
      <EvidenceList label="影响证据" ids={ins.impact.evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
      <EvidenceList label="反证" ids={ins.counter_evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />

      {ins.alternatives && ins.alternatives.map((alt, i) => (
        <div key={i} className="mt-1.5 text-helper text-[var(--text-secondary)]">
          <span className="text-meta text-[var(--text-muted)]">替代解释：</span>{alt.statement}
          {alt.assessment && <span className="text-meta text-[var(--text-muted)]">（{alt.assessment}）</span>}
          <EvidenceList label="支持" ids={alt.evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
          <EvidenceList label="反对" ids={alt.opposing_evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
        </div>
      ))}

      {ins.recommendations && ins.recommendations.length > 0 && <Bullets label="下一次可改进" items={ins.recommendations} accent />}
      {ins.caveats && ins.caveats.length > 0 && <Bullets label="数据边界" items={ins.caveats} />}
    </div>
  )
}

function EvidenceList({ label, ids, evidenceMap, onJumpToTurn }: {
  label: string
  ids?: string[]
  evidenceMap: Map<string, InsightEvidenceRef>
  onJumpToTurn?: (t: number) => void
}) {
  if (!ids || ids.length === 0) return null
  return (
    <div className="mt-1">
      <span className="text-meta text-[var(--text-muted)]">{label}：</span>
      <div className="mt-0.5 space-y-0.5">
        {ids.map(id => {
          const ev = evidenceMap.get(id)
          const clickable = ev?.turn_index !== undefined && onJumpToTurn
          const Tag = clickable ? 'button' : 'div'
          return (
            <Tag
              key={id}
              onClick={clickable ? () => onJumpToTurn!(ev!.turn_index!) : undefined}
              className={`block w-full text-left text-meta ${clickable ? 'text-[var(--accent-blue)] hover:underline cursor-pointer' : 'text-[var(--text-muted)]'}`}
            >
              <span className="font-mono">{id}</span>{ev?.statement ? ` · ${ev.statement}` : ''}
            </Tag>
          )
        })}
      </div>
    </div>
  )
}

function Bullets({ label, items, accent }: { label: string; items: string[]; accent?: boolean }) {
  return (
    <div className="mt-1.5">
      <span className="text-meta text-[var(--text-muted)]">{label}：</span>
      <ul className={`mt-0.5 list-disc pl-5 text-helper ${accent ? 'text-[var(--text-primary)]' : 'text-[var(--text-secondary)]'}`}>
        {items.map((it, i) => <li key={i}>{it}</li>)}
      </ul>
    </div>
  )
}

function SendPreviewCard({ preview, onConfirm, onCancel }: { preview: SendPreview; onConfirm: () => void; onCancel: () => void }) {
  return (
    <div className="rounded-md border border-[var(--accent-blue)] bg-[var(--bg-inset)] p-3">
      <div className="text-helper font-semibold text-[var(--text-primary)]">首次发送到该模型目标前请确认</div>
      <div className="mt-1 text-meta text-[var(--text-secondary)]">目标：{preview.target_label}</div>
      <div className="mt-1 text-meta text-[var(--text-secondary)]">将发送的数据类别：{preview.data_categories.join('、')}</div>
      <div className="mt-1 grid grid-cols-2 gap-x-4 gap-y-0.5 text-meta text-[var(--text-muted)]">
        <span>证据事实：{preview.fact_count} 条</span>
        <span>字符数：约 {preview.char_count}</span>
        <span>已截断片段：{preview.truncated_count}</span>
        <span>已脱敏项：{preview.redacted_count}</span>
      </div>
      <div className="mt-1 text-meta text-[var(--text-muted)]">{preview.note}</div>
      <div className="mt-2 flex items-center gap-2">
        <button onClick={onConfirm} className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}>确认并发送</button>
        <button onClick={onCancel} className={btnCls}>取消</button>
        <button onClick={() => void revokeInsightTargets()} className="text-meta text-[var(--text-muted)] underline">撤销所有已授权目标</button>
      </div>
    </div>
  )
}

function staleReason(reasons: string[]): string {
  const map: Record<string, string> = {
    session_revision_changed: '会话已变化',
    skill_version_changed: '分析规则已更新',
  }
  return reasons.map(r => map[r] ?? r).join('、') || '未知原因'
}

const btnCls = 'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'
