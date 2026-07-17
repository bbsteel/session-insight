import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import {
  fetchLatestInsight, fetchLLMProviders, parseInsightMetadata, revokeInsightTargets,
  type InsightItem, type InsightOutput, type InsightEvidenceRef, type SendPreview, type LLMProvider,
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
  const [providers, setProviders] = useState<LLMProvider[]>([])
  // 0 = use the server-side default provider.
  const [providerId, setProviderId] = useState(0)

  const state = useSyncExternalStore(
    useCallback(cb => subscribe(sessionId, cb), [sessionId]),
    useCallback(() => getState(sessionId), [sessionId]),
  )

  useEffect(() => {
    const load = () => fetchLLMProviders().then(d => setProviders(d.providers)).catch(() => {})
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
  const providerMissing = providers.length === 0 || state.noProvider
  const defaultProvider = providers.find(p => p.is_default)

  const meta = parseInsightMetadata(result?.generation.metadata)
  const evidenceMap = new Map<string, InsightEvidenceRef>()
  for (const e of meta?.cited_evidence ?? []) evidenceMap.set(e.evidence_id, e)
  const stale = result?.freshness.stale
  const canExport = !!result && !meta?.parse_failed && !!meta?.output && meta.output.insights.length > 0

  return (
    <div className="px-4 pb-4">
      <div className="flex items-center gap-3 mb-2 flex-wrap">
        <h3 className="text-body font-semibold text-[var(--text-primary)]">根因分析</h3>
        {/* Launch control + model picker sit right next to the title. */}
        {!busy && !isLive && !providerMissing && providers.length > 0 && (
          <select
            value={providerId}
            onChange={e => setProviderId(Number(e.target.value))}
            className="h-7 max-w-[220px] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-1.5 text-helper text-[var(--text-secondary)] focus:outline-none focus:border-[var(--accent-blue)]"
            title="用哪个模型源做根因分析"
          >
            <option value={0}>
              {defaultProvider ? `${defaultProvider.name}（默认）` : '（未设置默认）'}
            </option>
            {providers.map(p => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </select>
        )}
        {busy ? (
          <button onClick={() => cancel(sessionId)} className={btnCls}>取消</button>
        ) : isLive ? (
          <span className="text-nav text-[var(--text-muted)]">会话结束后可分析</span>
        ) : providerMissing ? (
          <button onClick={() => window.dispatchEvent(new Event('si-open-ai-settings'))} className={btnCls}>
            配置模型后启动
          </button>
        ) : (
          <button onClick={() => start(sessionId, false, providerId)} className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}>
            {result ? '重新分析' : '启动分析'}
          </button>
        )}
        <span className="text-nav font-normal text-[var(--text-secondary)]">用配置的模型解释这些发现为何出现</span>

        {canExport && (
          <div className="flex items-center gap-2">
            <ExportButtons
              output={meta!.output!}
              evidenceMap={evidenceMap}
              generation={result!.generation}
              sessionId={sessionId}
              agentType={agentType}
            />
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
          onConfirm={() => start(sessionId, true, providerId)}
          onCancel={() => dismissPreview(sessionId)}
        />
      )}

      {!busy && result && (
        <>
          {stale && (
            <div className="mb-2 flex items-center gap-2 rounded-md bg-[var(--warning)]/15 px-3 py-1.5 text-helper text-[var(--warning)]">
              <span>结果可能已过期（{staleReason(result.freshness.reasons)}）</span>
              <button onClick={() => start(sessionId, false, providerId)} className="underline">重新分析</button>
            </div>
          )}
          <div className="mb-2 text-meta text-[var(--text-muted)]">
            {result.generation.model_id} · {result.generation.created_at}
          </div>
          {meta?.parse_failed ? (
            <div>
              <div className="mb-1 text-helper text-[var(--warning)]">
                结构化解析失败，模型未按格式输出（下方为原始输出）。若模型拒答或答非所问，多为模型能力不足或对数据里的指令样文本过度敏感——可换用 API 类型或更强的模型源重试。
              </div>
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

function ExportButtons({ output, evidenceMap, generation, sessionId, agentType }: {
  output: InsightOutput
  evidenceMap: Map<string, InsightEvidenceRef>
  generation: { model_id: string; created_at: string }
  sessionId: string
  agentType: string
}) {
  const [copied, setCopied] = useState(false)
  const stem = fileStem(agentType, sessionId, generation.created_at)

  const save = (content: string, ext: string, mime: string) => {
    const blob = new Blob([content], { type: mime })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${stem}.${ext}`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(buildMarkdown(output, evidenceMap, generation))
      setCopied(true); setTimeout(() => setCopied(false), 1500)
    } catch { /* clipboard blocked */ }
  }

  return (
    <>
      <button onClick={() => save(buildMarkdown(output, evidenceMap, generation), 'md', 'text/markdown;charset=utf-8')} className={btnCls}>导出 Markdown</button>
      <button onClick={() => save(buildHTML(output, evidenceMap, generation), 'html', 'text/html;charset=utf-8')} className={btnCls}>导出 HTML</button>
      <button onClick={copy} className={btnCls}>{copied ? '已复制' : '复制'}</button>
    </>
  )
}

// fileStem builds a readable, session-identifying filename base, e.g.
// 根因分析_codex_019f5fa6_20260715-145545.
function fileStem(agent: string, sessionId: string, createdAt: string): string {
  const m = createdAt.match(/(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})/)
  const ts = m ? `${m[1]}${m[2]}${m[3]}-${m[4]}${m[5]}${m[6]}` : createdAt.replace(/[^\d]/g, '')
  return `根因分析_${agent || 'session'}_${sessionId.slice(0, 8)}_${ts}`
}

function trunc(s: string, n: number): string {
  const a = [...s]
  return a.length > n ? a.slice(0, n).join('') + '…' : s
}

// compactRefs turns a citation list into deduped short tags (T3 for turns,
// kind label otherwise) so an insight's basis reads as one short line rather
// than a wall of repeated full-text evidence.
function compactRefs(evidenceMap: Map<string, InsightEvidenceRef>, ...lists: (string[] | undefined)[]): string {
  const seen = new Set<string>()
  const tags: string[] = []
  for (const ids of lists) {
    for (const id of ids ?? []) {
      const ev = evidenceMap.get(id)
      const tag = ev?.turn_index !== undefined ? `T${ev.turn_index}` : (kindLabel[ev?.kind ?? ''] ?? ev?.kind ?? '证据')
      if (seen.has(tag)) continue
      seen.add(tag); tags.push(tag)
    }
  }
  return tags.join('、')
}

// citedIds collects every evidence id an insight references, in first-seen order.
function citedIds(output: InsightOutput): string[] {
  const seen = new Set<string>()
  const push = (ids?: string[]) => ids?.forEach(i => seen.add(i))
  for (const ins of output.insights) {
    push(ins.cause.evidence_ids); push(ins.impact.evidence_ids); push(ins.counter_evidence_ids)
    ins.alternatives?.forEach(a => { push(a.evidence_ids); push(a.opposing_evidence_ids) })
  }
  return [...seen]
}

// buildMarkdown renders a conclusion-first report: each insight shows its cause,
// impact, recommendations and a one-line basis; full evidence text appears once
// in an appendix (truncated) instead of being repeated under every insight.
function buildMarkdown(
  output: InsightOutput,
  evidenceMap: Map<string, InsightEvidenceRef>,
  generation: { model_id: string; created_at: string },
): string {
  const L: string[] = []
  L.push('# 根因分析报告', '', `> 模型 ${generation.model_id} · ${generation.created_at}`, '')
  if (output.summary) L.push('## 概要', '', output.summary, '')

  output.insights.forEach((ins, i) => {
    L.push(`## ${i + 1}. ${ins.title}（置信度 ${confidenceLabel[ins.confidence] ?? ins.confidence}）`, '')
    L.push(`**原因**（${epistemicLabel[ins.cause.epistemic_status] ?? ins.cause.epistemic_status} · ${strengthLabel[ins.cause.causal_strength] ?? ins.cause.causal_strength}）：${ins.cause.statement}`, '')
    if (ins.impact.statement) L.push(`**影响**：${ins.impact.statement}`, '')
    if (ins.recommendations && ins.recommendations.length > 0) {
      L.push('**建议**')
      ins.recommendations.forEach(r => L.push(`- ${r}`))
      L.push('')
    }
    ins.alternatives?.forEach(alt => L.push(`**替代解释**：${alt.statement}${alt.assessment ? `（${alt.assessment}）` : ''}`, ''))
    if (ins.caveats && ins.caveats.length > 0) {
      L.push('**数据边界**')
      ins.caveats.forEach(c => L.push(`- ${c}`))
      L.push('')
    }
    const basis = compactRefs(evidenceMap, ins.cause.evidence_ids, ins.impact.evidence_ids)
    const counter = compactRefs(evidenceMap, ins.counter_evidence_ids)
    if (basis) L.push(`**依据**：${basis}${counter ? ` **反证**：${counter}` : ''}`, '')
  })

  if (output.evidence_gaps && output.evidence_gaps.length > 0) {
    L.push('## 数据缺口', '')
    output.evidence_gaps.forEach(g => L.push(`- ${g}`))
    L.push('')
  }

  const ids = citedIds(output)
  if (ids.length > 0) {
    L.push('---', '', '## 证据附录', '')
    for (const id of ids) {
      const ev = evidenceMap.get(id)
      const kind = ev?.kind ? (kindLabel[ev.kind] ?? ev.kind) : '证据'
      const tag = ev?.turn_index !== undefined ? `T${ev.turn_index}` : kind
      L.push(`- **[${tag}]** ${trunc(ev?.statement ?? id, 140)}`)
    }
    L.push('')
  }
  return L.join('\n')
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// buildHTML renders the same conclusion-first report as a self-contained,
// lightly-styled HTML document for archiving or sharing outside the app.
function buildHTML(
  output: InsightOutput,
  evidenceMap: Map<string, InsightEvidenceRef>,
  generation: { model_id: string; created_at: string },
): string {
  const H: string[] = []
  H.push(`<h1>根因分析报告</h1>`)
  H.push(`<p class="meta">模型 ${esc(generation.model_id)} · ${esc(generation.created_at)}</p>`)
  if (output.summary) H.push(`<div class="summary">${esc(output.summary)}</div>`)

  output.insights.forEach((ins, i) => {
    const conf = ins.confidence
    H.push(`<section class="card ${conf}">`)
    H.push(`<h2><span class="idx">${i + 1}</span> ${esc(ins.title)} <span class="badge ${conf}">置信度 ${confidenceLabel[conf] ?? conf}</span></h2>`)
    H.push(`<p><b>原因</b> <span class="dim">（${epistemicLabel[ins.cause.epistemic_status] ?? ins.cause.epistemic_status} · ${strengthLabel[ins.cause.causal_strength] ?? ins.cause.causal_strength}）</span>：${esc(ins.cause.statement)}</p>`)
    if (ins.impact.statement) H.push(`<p><b>影响</b>：${esc(ins.impact.statement)}</p>`)
    if (ins.recommendations && ins.recommendations.length > 0) {
      H.push(`<p><b>建议</b></p><ul>${ins.recommendations.map(r => `<li>${esc(r)}</li>`).join('')}</ul>`)
    }
    ins.alternatives?.forEach(alt => H.push(`<p><b>替代解释</b>：${esc(alt.statement)}${alt.assessment ? `（${esc(alt.assessment)}）` : ''}</p>`))
    if (ins.caveats && ins.caveats.length > 0) {
      H.push(`<p><b>数据边界</b></p><ul>${ins.caveats.map(c => `<li>${esc(c)}</li>`).join('')}</ul>`)
    }
    const basis = compactRefs(evidenceMap, ins.cause.evidence_ids, ins.impact.evidence_ids)
    const counter = compactRefs(evidenceMap, ins.counter_evidence_ids)
    if (basis) H.push(`<p class="basis"><b>依据</b>：${esc(basis)}${counter ? ` <b>反证</b>：${esc(counter)}` : ''}</p>`)
    H.push(`</section>`)
  })

  if (output.evidence_gaps && output.evidence_gaps.length > 0) {
    H.push(`<h2>数据缺口</h2><ul>${output.evidence_gaps.map(g => `<li>${esc(g)}</li>`).join('')}</ul>`)
  }

  const ids = citedIds(output)
  if (ids.length > 0) {
    H.push(`<h2>证据附录</h2><ul class="appendix">`)
    for (const id of ids) {
      const ev = evidenceMap.get(id)
      const kind = ev?.kind ? (kindLabel[ev.kind] ?? ev.kind) : '证据'
      const tag = ev?.turn_index !== undefined ? `T${ev.turn_index}` : kind
      H.push(`<li><span class="tag">${esc(tag)}</span> ${esc(trunc(ev?.statement ?? id, 200))}</li>`)
    }
    H.push(`</ul>`)
  }

  return `<!doctype html><html lang="zh"><head><meta charset="utf-8"><title>根因分析报告</title>
<style>
:root{color-scheme:light dark}
body{font:14px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif;max-width:820px;margin:32px auto;padding:0 20px;color:#1f2328}
h1{font-size:22px;margin:0 0 4px}
.meta{color:#656d76;margin:0 0 20px;font-size:12px}
.summary{background:#f6f8fa;border-radius:8px;padding:12px 14px;margin-bottom:20px;font-size:15px}
.card{border-left:3px solid #6b7280;background:#f6f8fa;border-radius:8px;padding:12px 16px;margin:14px 0}
.card.high{border-left-color:#cf222e}.card.medium{border-left-color:#bf8700}.card.low{border-left-color:#0969da}
h2{font-size:16px;margin:18px 0 8px}
.idx{color:#8250df;font-weight:700;margin-right:4px}
.badge{font-size:11px;padding:2px 6px;border-radius:4px;background:#eaeef2;color:#57606a;font-weight:500;vertical-align:middle}
.badge.high{background:#ffebe9;color:#cf222e}.badge.medium{background:#fff8c5;color:#7d4e00}.badge.low{background:#ddf4ff;color:#0969da}
p{margin:6px 0}.dim{color:#656d76;font-size:12px}
.basis{color:#57606a;font-size:13px;border-top:1px solid #d0d7de;padding-top:6px;margin-top:8px}
ul{margin:6px 0;padding-left:22px}li{margin:2px 0}
.appendix li{color:#57606a;font-size:13px}
.tag{display:inline-block;font-size:11px;background:#eaeef2;color:#57606a;border-radius:4px;padding:1px 5px;margin-right:4px}
</style></head><body>
${H.join('\n')}
</body></html>`
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
