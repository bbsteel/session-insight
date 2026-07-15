import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import {
  fetchLatestInsight, fetchLLMProviders, parseInsightMetadata, revokeInsightTargets,
  type InsightItem, type InsightOutput, type InsightEvidenceRef, type SendPreview,
} from '../api'
import { subscribe, getState, seedResult, start, cancel, dismissPreview } from '../insightStore'

interface Props {
  sessionId: string
  agentType: string
  isLive: boolean
  findingsCount: number
  onJumpToTurn?: (turnIndex: number) => void
}

const confidenceLabel: Record<string, string> = { high: '高', medium: '中', low: '低' }
const epistemicLabel: Record<string, string> = { observed: '观察事实', inferred: '推断', unknown: '无法判断' }
const strengthLabel: Record<string, string> = { none: '无因果', weak: '弱因果', moderate: '中等因果', strong: '强因果' }
const kindLabel: Record<string, string> = {
  session: '会话', turn: '轮次', subagent: '子代理', metric: '指标', tool: '工具', skill: '技能',
}

// DeepInsightSection is the second analysis layer (根因分析): it explains why
// the local findings occurred, using a configured model, only when the user
// asks. It never auto-generates and refuses live sessions (whose bill is still
// moving). Generation state lives in insightStore so switching away mid-run
// keeps the analysis going and coming back re-attaches to its progress.
export default function DeepInsightSection({ sessionId, agentType, isLive, findingsCount, onJumpToTurn }: Props) {
  const [loaded, setLoaded] = useState(false)
  const [hasProvider, setHasProvider] = useState(false)

  const state = useSyncExternalStore(
    useCallback(cb => subscribe(sessionId, cb), [sessionId]),
    useCallback(() => getState(sessionId), [sessionId]),
  )

  useEffect(() => {
    const load = () => fetchLLMProviders().then(d => setHasProvider(d.providers.length > 0)).catch(() => {})
    load()
    window.addEventListener('si-ai-providers-changed', load)
    return () => window.removeEventListener('si-ai-providers-changed', load)
  }, [])

  // Load the cached latest insight (with its freshness) and seed the store.
  useEffect(() => {
    let cancelled = false
    setLoaded(false)
    fetchLatestInsight(sessionId, agentType)
      .then(r => { if (!cancelled) { seedResult(sessionId, r); setLoaded(true) } })
      .catch(() => { if (!cancelled) setLoaded(true) })
    return () => { cancelled = true }
  }, [sessionId, agentType])

  if (findingsCount === 0) return null // no findings → no deep-analysis entry

  const busy = state.status === 'running'
  const result = state.result
  const preview = state.status === 'preview' ? state.preview : null
  const blocked = state.status === 'blocked' ? state.blocked : null
  const error = state.status === 'error' ? state.error : null
  const providerMissing = !hasProvider || state.noProvider

  const meta = parseInsightMetadata(result?.generation.metadata)
  const evidenceMap = new Map<string, InsightEvidenceRef>()
  for (const e of meta?.cited_evidence ?? []) evidenceMap.set(e.evidence_id, e)
  const stale = result?.freshness.stale
  const canExport = !!result && !meta?.parse_failed && !!meta?.output && meta.output.insights.length > 0

  return (
    <div className="px-4 pb-4">
      <div className="flex items-center gap-3 mb-2 flex-wrap">
        <h3 className="text-body font-semibold text-[var(--text-primary)]">根因分析</h3>
        {/* Launch control sits right next to the title, not pushed to the edge. */}
        {busy ? (
          <button onClick={() => cancel(sessionId)} className={btnCls}>取消</button>
        ) : isLive ? (
          <span className="text-nav text-[var(--text-muted)]">会话结束后可分析</span>
        ) : providerMissing ? (
          <button onClick={() => window.dispatchEvent(new Event('si-open-ai-settings'))} className={btnCls}>
            配置模型后启动
          </button>
        ) : (
          <button onClick={() => start(sessionId, false)} className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}>
            {result ? '重新分析' : '启动分析'}
          </button>
        )}
        <span className="text-nav font-normal text-[var(--text-secondary)]">用配置的模型解释这些发现为何出现</span>

        {canExport && (
          <div className="ml-auto flex items-center gap-2">
            <ExportButtons output={meta!.output!} evidenceMap={evidenceMap} generation={result!.generation} />
          </div>
        )}
      </div>

      {busy && (
        <div className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-2 font-mono text-helper text-[var(--text-secondary)]">
          {state.stages.map((line, i) => (
            <div key={i} className="leading-5">
              {line.ms != null
                ? <><span className="text-[var(--success)]">✓</span> {line.text}… <span className="text-meta text-[var(--text-muted)]">{(line.ms / 1000).toFixed(1)}s</span></>
                : <span className="text-[var(--text-primary)]"><span className="text-[var(--accent-blue)]">›</span> {line.text}…</span>}
            </div>
          ))}
          {state.stages.length === 0 && <span className="text-[var(--text-muted)]">正在准备…</span>}
        </div>
      )}

      {blocked && (
        <div className="rounded-md border border-dashed border-[var(--border-default)] p-3 text-helper text-[var(--text-secondary)]">
          {blocked === 'session_active' && '会话仍在活跃中，账单尚未结算，结束后再分析。'}
          {blocked === 'session_changing' && '会话数据正在变化，请稍后重试。'}
          {blocked === 'no_findings' && '没有可分析的初步发现。'}
          {blocked === 'not_found' && '会话不存在。'}
        </div>
      )}
      {error && <div className="mb-2 whitespace-pre-wrap break-all text-helper text-[var(--error)]">{error}</div>}

      {preview && (
        <SendPreviewCard
          preview={preview}
          onConfirm={() => start(sessionId, true)}
          onCancel={() => dismissPreview(sessionId)}
        />
      )}

      {!busy && result && (
        <>
          {stale && (
            <div className="mb-2 flex items-center gap-2 rounded-md bg-[var(--warning)]/15 px-3 py-1.5 text-helper text-[var(--warning)]">
              <span>结果可能已过期（{staleReason(result.freshness.reasons)}）</span>
              <button onClick={() => start(sessionId, false)} className="underline">重新分析</button>
            </div>
          )}
          <div className="mb-2 text-meta text-[var(--text-muted)]">
            {result.generation.model_id} · {result.generation.created_at}
          </div>
          {meta?.parse_failed ? (
            <div>
              <div className="mb-1 text-helper text-[var(--warning)]">结构化解析失败，以下为原始输出（纯文本）</div>
              <pre className="whitespace-pre-wrap break-words rounded-md bg-[var(--bg-inset)] p-3 text-body text-[var(--text-secondary)]">{result.generation.content}</pre>
            </div>
          ) : meta?.output ? (
            <InsightCards output={meta.output} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
          ) : (
            <pre className="whitespace-pre-wrap break-words text-body text-[var(--text-secondary)]">{result.generation.content}</pre>
          )}
        </>
      )}

      {loaded && !result && !busy && !preview && !blocked && !error && (
        <div className="text-nav text-[var(--text-muted)]">
          {isLive ? '会话活跃时不生成，避免对变化中的数据反复付费。' : '尚未生成根因分析。'}
        </div>
      )}
    </div>
  )
}

function InsightCards({ output, evidenceMap, onJumpToTurn }: {
  output: InsightOutput
  evidenceMap: Map<string, InsightEvidenceRef>
  onJumpToTurn?: (t: number) => void
}) {
  if (output.insights.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-[var(--border-default)] p-3 text-body text-[var(--text-secondary)]">
        <div>当前证据不足以给出根因分析。</div>
        {output.evidence_gaps && output.evidence_gaps.length > 0 && (
          <ul className="mt-1 list-disc pl-5 text-nav text-[var(--text-muted)]">
            {output.evidence_gaps.map((g, i) => <li key={i}>{g}</li>)}
          </ul>
        )}
      </div>
    )
  }
  return (
    <div className="space-y-3">
      {output.summary && <div className="text-prose text-[var(--text-primary)]">{output.summary}</div>}
      {output.insights.map((ins, i) => <InsightCard key={i} ins={ins} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />)}
      {output.evidence_gaps && output.evidence_gaps.length > 0 && (
        <div className="rounded-md bg-[var(--bg-inset)] px-3 py-2">
          <div className="text-body font-medium text-[var(--text-secondary)]">数据缺口</div>
          <ul className="mt-1 list-disc pl-5 text-nav text-[var(--text-muted)]">
            {output.evidence_gaps.map((g, i) => <li key={i}>{g}</li>)}
          </ul>
        </div>
      )}
    </div>
  )
}

// A single insight card: the main conclusion (title + confidence + cause) is
// always visible; supporting detail (evidence, impact, alternatives,
// recommendations, caveats) collapses behind a toggle to keep the report scannable.
function InsightCard({ ins, evidenceMap, onJumpToTurn }: {
  ins: InsightItem
  evidenceMap: Map<string, InsightEvidenceRef>
  onJumpToTurn?: (t: number) => void
}) {
  const [open, setOpen] = useState(false)
  const conf = ins.confidence
  const confColor = conf === 'high' ? 'var(--error)' : conf === 'medium' ? 'var(--warning)' : 'var(--accent-blue)'

  const hasDetail = (ins.cause.evidence_ids?.length ?? 0) > 0
    || (ins.cause.confounders?.length ?? 0) > 0
    || !!ins.impact.statement || (ins.impact.evidence_ids?.length ?? 0) > 0
    || (ins.counter_evidence_ids?.length ?? 0) > 0
    || (ins.alternatives?.length ?? 0) > 0
    || (ins.recommendations?.length ?? 0) > 0
    || (ins.caveats?.length ?? 0) > 0

  return (
    <div className="rounded-md border-l-2 bg-[var(--bg-inset)] p-3" style={{ borderLeftColor: confColor }}>
      <div className="flex items-center justify-between gap-2">
        <div className="text-prose font-semibold text-[var(--text-primary)]">{ins.title}</div>
        <span className="shrink-0 text-nav px-1.5 py-0.5 rounded-sm" style={{ background: `color-mix(in srgb, ${confColor} 18%, transparent)`, color: confColor }}>
          置信度 {confidenceLabel[conf] ?? conf}
        </span>
      </div>

      {/* Main conclusion — always visible. */}
      <div className="mt-2 text-body text-[var(--text-primary)]">
        <span className="text-nav text-[var(--text-muted)]">主要原因（{epistemicLabel[ins.cause.epistemic_status] ?? ins.cause.epistemic_status} · {strengthLabel[ins.cause.causal_strength] ?? ins.cause.causal_strength}）</span>
        <div className="mt-0.5">{ins.cause.statement}</div>
      </div>

      {hasDetail && (
        <button
          onClick={() => setOpen(o => !o)}
          className="mt-2 text-nav text-[var(--accent-blue)] hover:underline"
        >
          {open ? '收起详情 ▲' : '展开详情 ▼'}
        </button>
      )}

      {open && (
        <div className="mt-2 space-y-3 border-t border-[var(--border-muted)] pt-2">
          <EvidenceSection label="关键证据" ids={ins.cause.evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
          {ins.cause.confounders && ins.cause.confounders.length > 0 && (
            <Bullets label="已排查的混淆因素" items={ins.cause.confounders} />
          )}

          {(ins.impact.statement || (ins.impact.evidence_ids?.length ?? 0) > 0) && (
            <div className="space-y-1">
              {ins.impact.statement && (
                <div className="text-body text-[var(--text-secondary)]">
                  <span className="text-nav text-[var(--text-muted)]">影响：</span>{ins.impact.statement}
                </div>
              )}
              <EvidenceSection label="影响证据" ids={ins.impact.evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
            </div>
          )}

          <EvidenceSection label="反证" ids={ins.counter_evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />

          {ins.alternatives && ins.alternatives.map((alt, i) => (
            <div key={i} className="rounded-md bg-[var(--bg-surface)] p-2">
              <div className="text-body text-[var(--text-secondary)]">
                <span className="text-nav text-[var(--text-muted)]">替代解释：</span>{alt.statement}
                {alt.assessment && <span className="text-nav text-[var(--text-muted)]">（{alt.assessment}）</span>}
              </div>
              <EvidenceSection label="支持" ids={alt.evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
              <EvidenceSection label="反对" ids={alt.opposing_evidence_ids} evidenceMap={evidenceMap} onJumpToTurn={onJumpToTurn} />
            </div>
          ))}

          {ins.recommendations && ins.recommendations.length > 0 && <Bullets label="下一次可改进" items={ins.recommendations} accent />}
          {ins.caveats && ins.caveats.length > 0 && <Bullets label="数据边界" items={ins.caveats} />}
        </div>
      )}
    </div>
  )
}

// EvidenceSection renders each cited fact as its own row: the statement is plain
// text and only a compact jump chip is clickable — no more clicking a long line.
function EvidenceSection({ label, ids, evidenceMap, onJumpToTurn }: {
  label: string
  ids?: string[]
  evidenceMap: Map<string, InsightEvidenceRef>
  onJumpToTurn?: (t: number) => void
}) {
  if (!ids || ids.length === 0) return null
  return (
    <div>
      <div className="text-nav font-medium text-[var(--text-muted)]">{label}</div>
      <div className="mt-1 space-y-1">
        {ids.map(id => {
          const ev = evidenceMap.get(id)
          const kind = ev?.kind ? (kindLabel[ev.kind] ?? ev.kind) : '证据'
          const canJump = ev?.turn_index !== undefined && onJumpToTurn
          return (
            <div key={id} className="flex items-start gap-2 rounded-sm bg-[var(--bg-surface)] px-2 py-1.5">
              <span className="shrink-0 mt-0.5 text-meta px-1 py-0.5 rounded-sm bg-[var(--bg-inset)] text-[var(--text-muted)]">{kind}</span>
              <span className="flex-1 text-body text-[var(--text-secondary)]">{ev?.statement ?? id}</span>
              {canJump && (
                <button
                  onClick={() => onJumpToTurn!(ev!.turn_index!)}
                  className="shrink-0 text-nav text-[var(--accent-blue)] hover:underline cursor-pointer"
                >
                  跳转 T{ev!.turn_index} ↗
                </button>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function Bullets({ label, items, accent }: { label: string; items: string[]; accent?: boolean }) {
  return (
    <div>
      <div className="text-nav font-medium text-[var(--text-muted)]">{label}</div>
      <ul className={`mt-1 list-disc pl-5 text-body ${accent ? 'text-[var(--text-primary)]' : 'text-[var(--text-secondary)]'}`}>
        {items.map((it, i) => <li key={i} className="leading-6">{it}</li>)}
      </ul>
    </div>
  )
}

function ExportButtons({ output, evidenceMap, generation }: {
  output: InsightOutput
  evidenceMap: Map<string, InsightEvidenceRef>
  generation: { model_id: string; created_at: string }
}) {
  const [copied, setCopied] = useState(false)
  const md = () => buildMarkdown(output, evidenceMap, generation)

  const download = () => {
    const blob = new Blob([md()], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `根因分析-${generation.created_at.replace(/[:\s]/g, '-')}.md`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  const copy = async () => {
    try { await navigator.clipboard.writeText(md()); setCopied(true); setTimeout(() => setCopied(false), 1500) } catch { /* clipboard blocked */ }
  }

  return (
    <>
      <button onClick={download} className={btnCls}>导出 Markdown</button>
      <button onClick={copy} className={btnCls}>{copied ? '已复制' : '复制'}</button>
    </>
  )
}

// buildMarkdown flattens the structured report (and its cited evidence) into a
// portable Markdown document for archiving or sharing.
function buildMarkdown(
  output: InsightOutput,
  evidenceMap: Map<string, InsightEvidenceRef>,
  generation: { model_id: string; created_at: string },
): string {
  const L: string[] = []
  L.push('# 根因分析报告')
  L.push('')
  L.push(`> 模型 ${generation.model_id} · ${generation.created_at}`)
  L.push('')
  if (output.summary) { L.push('## 概要', '', output.summary, '') }

  const evLines = (label: string, ids?: string[]) => {
    if (!ids || ids.length === 0) return
    L.push(`**${label}：**`)
    for (const id of ids) {
      const ev = evidenceMap.get(id)
      const kind = ev?.kind ? (kindLabel[ev.kind] ?? ev.kind) : '证据'
      const jump = ev?.turn_index !== undefined ? `（T${ev.turn_index}）` : ''
      L.push(`- [${kind}] ${ev?.statement ?? id}${jump}`)
    }
    L.push('')
  }

  output.insights.forEach((ins, i) => {
    L.push(`## 洞察 ${i + 1}：${ins.title}`)
    L.push('')
    L.push(`- 置信度：${confidenceLabel[ins.confidence] ?? ins.confidence}`)
    L.push(`- 主要原因（${epistemicLabel[ins.cause.epistemic_status] ?? ins.cause.epistemic_status} · ${strengthLabel[ins.cause.causal_strength] ?? ins.cause.causal_strength}）：${ins.cause.statement}`)
    L.push('')
    evLines('关键证据', ins.cause.evidence_ids)
    if (ins.cause.confounders && ins.cause.confounders.length > 0) {
      L.push('**已排查的混淆因素：**')
      ins.cause.confounders.forEach(c => L.push(`- ${c}`))
      L.push('')
    }
    if (ins.impact.statement) { L.push(`**影响：** ${ins.impact.statement}`, '') }
    evLines('影响证据', ins.impact.evidence_ids)
    evLines('反证', ins.counter_evidence_ids)
    ins.alternatives?.forEach(alt => {
      L.push(`**替代解释：** ${alt.statement}${alt.assessment ? `（${alt.assessment}）` : ''}`, '')
      evLines('支持', alt.evidence_ids)
      evLines('反对', alt.opposing_evidence_ids)
    })
    if (ins.recommendations && ins.recommendations.length > 0) {
      L.push('**下一次可改进：**')
      ins.recommendations.forEach(r => L.push(`- ${r}`))
      L.push('')
    }
    if (ins.caveats && ins.caveats.length > 0) {
      L.push('**数据边界：**')
      ins.caveats.forEach(c => L.push(`- ${c}`))
      L.push('')
    }
  })

  if (output.evidence_gaps && output.evidence_gaps.length > 0) {
    L.push('## 数据缺口', '')
    output.evidence_gaps.forEach(g => L.push(`- ${g}`))
    L.push('')
  }
  return L.join('\n')
}

function SendPreviewCard({ preview, onConfirm, onCancel }: { preview: SendPreview; onConfirm: () => void; onCancel: () => void }) {
  return (
    <div className="rounded-md border border-[var(--accent-blue)] bg-[var(--bg-inset)] p-3">
      <div className="text-body font-semibold text-[var(--text-primary)]">首次发送到该模型目标前请确认</div>
      <div className="mt-1 text-nav text-[var(--text-secondary)]">目标：{preview.target_label}</div>
      <div className="mt-1 text-nav text-[var(--text-secondary)]">将发送的数据类别：{preview.data_categories.join('、')}</div>
      <div className="mt-1 grid grid-cols-2 gap-x-4 gap-y-0.5 text-nav text-[var(--text-muted)]">
        <span>证据事实：{preview.fact_count} 条</span>
        <span>字符数：约 {preview.char_count}</span>
        <span>已截断片段：{preview.truncated_count}</span>
        <span>已脱敏项：{preview.redacted_count}</span>
      </div>
      <div className="mt-1 text-nav text-[var(--text-muted)]">{preview.note}</div>
      <div className="mt-2 flex items-center gap-2">
        <button onClick={onConfirm} className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}>确认并发送</button>
        <button onClick={onCancel} className={btnCls}>取消</button>
        <button onClick={() => void revokeInsightTargets()} className="text-nav text-[var(--text-muted)] underline">撤销所有已授权目标</button>
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
