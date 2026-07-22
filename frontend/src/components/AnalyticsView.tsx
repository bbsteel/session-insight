import { useEffect, useState } from 'react'
import ReactEChartsCore from 'echarts-for-react/lib/core'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import DeepInsightSection from './DeepInsightSection'
import { formatNumber, useI18n } from '../i18n'

echarts.use([BarChart, LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

interface TokenPresence {
  input?: string
  output?: string
  cache_read?: string
  cache_write?: string
  reasoning?: string
}

interface BillTokenUsage {
  prompt_tokens: number
  completion_tokens: number
  reasoning_tokens?: number
  cache_read_tokens: number
  cache_write_tokens: number
  premium_requests: number
  present: TokenPresence
}

interface ModelUsage {
  model: string
  requests: number
  billing_amount?: number
  usage: BillTokenUsage
}

interface SessionBilling {
  precision: string
  billing_unit?: string
  billing_amount?: number
  totals: BillTokenUsage
  by_model?: ModelUsage[]
}

interface AnalyticsData {
  total_tokens: number
  todo_count: number
  todo_done: number
  todos: { id: string; title: string; description: string; status: string; deps?: string[] }[]
  prompt_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cache_hit_rate: number
  billing?: SessionBilling
  total_tools: number
  total_errors: number
  anomaly_count: number
  turn_count: number
  historical_turn_count?: number
  rolled_back_turn_count?: number
  rolled_back_tokens?: number
  token_efficiency: number
  timeline: {
    turn_index: number
    tokens: number
    duration_ms: number
    tool_count: number
    error_count: number
    requests: number
    est_cost?: number
    rolled_back?: boolean
    original_turn_index?: number
  }[]
  cost_precision?: string
  findings?: {
    code?: string
    severity: 'critical' | 'warn' | 'info'
    title: string
    detail: string
    turn_index?: number
    metrics?: Record<string, unknown>
  }[]
  tool_freq: Record<string, number>
  context_window: number
  context_peak: number
  pressure_pct: number
  skill_freq: Record<string, number>
  tool_success: Record<string, number>
  tool_total: Record<string, number>
}

type Finding = NonNullable<AnalyticsData['findings']>[number]
type Translate = (key: string, vars?: Record<string, string | number>) => string

function findingCopy(finding: Finding, locale: 'en' | 'zh-CN', t: Translate): { title: string; detail: string } {
  if (!finding.code || !finding.metrics) return { title: finding.title, detail: finding.detail }
  const value = (key: string) => Number(finding.metrics?.[key] ?? 0)
  const count = (key: string) => formatNumber(locale, value(key))
  const percent = (key: string) => new Intl.NumberFormat(locale, { style: 'percent', maximumFractionDigits: 0 }).format(value(key))
  const varsByCode: Record<string, Record<string, string | number>> = {
    instruction_amplification: { factor: count('factor'), messages: count('user_messages'), requests: count('total_requests') },
    cost_concentration: { turn: value('turn_index'), share: percent('share'), requests: count('requests'), tools: count('tool_count') },
    long_context_replay: { tokens: count('cache_read_tokens'), share: percent('cache_read_share') },
    subagent_fanout: { count: count('subagent_count'), turns: count('subagent_turns') },
    tool_loop: { turn: value('turn_index'), count: count('tool_count') },
    tool_failure_churn: { rate: percent('failure_rate'), total: count('total_tools'), errors: count('total_errors') },
    tool_timeout_churn: { count: count('timeout_count') },
    tool_rejection_churn: { count: count('rejected_count') },
    continuation_pressure: { count: count('nudge_count') },
  }
  const vars = varsByCode[finding.code]
  if (!vars) return { title: finding.title, detail: finding.detail }
  return {
    title: t(`analytics.finding.${finding.code}.title`, vars),
    detail: t(`analytics.finding.${finding.code}.detail`, vars),
  }
}

interface Props {
  sessionId: string
  agentType?: string
  // Whether the session is currently live: Deep Insight is refused while a
  // session's data and bill are still changing.
  isLive?: boolean
  onJumpToTurn?: (turnIndex: number) => void
  // 点击 Tool Usage 的工具 chip:切回终端并打开工具面板、按该工具筛选。
  onJumpToTool?: (name: string) => void
}

interface AgentReference {
  concepts: [string, string][]
  links: [string, string][]
}

// Universal token-bucket meanings, shown as hover tooltips wherever the
// buckets appear on the bill. Agent-specific billing concepts live in
// AGENT_REFERENCES instead.
const BUCKET_HINT_KEYS: Record<string, string> = {
  input: 'analytics.bucket.input',
  cache_read: 'analytics.bucket.cacheRead',
  cache_write: 'analytics.bucket.cacheWrite',
  output: 'analytics.bucket.output',
  reasoning: 'analytics.bucket.reasoning',
}

const AGENT_REFERENCES: Record<string, AgentReference> = {
  copilot: {
    concepts: [
      ['AIU', 'analytics.ref.copilotAiu'],
      ['Premium Requests', 'analytics.ref.copilotPremium'],
    ],
    links: [
      ['analytics.ref.copilotDocs', 'https://docs.github.com/en/copilot/managing-copilot/understanding-and-managing-copilot-usage'],
    ],
  },
  claude: {
    concepts: [
      ['analytics.ref.claudeUsdTerm', 'analytics.ref.claudeUsd'],
      ['Cache TTL', 'analytics.ref.cacheTtl'],
    ],
    links: [
      ['analytics.ref.claudeDocs', 'https://docs.claude.com/en/docs/about-claude/pricing'],
    ],
  },
  codex: {
    concepts: [
      ['Cached Input', 'analytics.ref.codexCache'],
      ['Reasoning', 'analytics.ref.codexReasoning'],
    ],
    links: [
      ['analytics.ref.openaiDocs', 'https://platform.openai.com/docs/pricing'],
    ],
  },
  opencode: {
    concepts: [
      ['Cost (USD)', 'analytics.ref.opencodeCost'],
    ],
    links: [
      ['analytics.ref.opencodeDocs', 'https://opencode.ai/docs'],
    ],
  },
}

const UNIT_HINT_KEYS: Record<string, string> = {
  aiu: 'analytics.unit.aiu',
  usd: 'analytics.unit.usd',
  premium_requests: 'analytics.unit.premium',
}

function fmtBillingAmount(locale: 'zh-CN' | 'en', amount: number, unit: string | undefined, t: (key: string, vars?: Record<string, string | number>) => string): string {
  switch (unit) {
    case 'aiu': return `${amount.toFixed(2)} AIU`
    case 'usd': return `$${amount.toFixed(4)}`
    case 'premium_requests': return t('analytics.premiumValue', { count: formatNumber(locale, amount) })
    default: return formatNumber(locale, Math.round(amount))
  }
}

// A bucket value is only meaningful when the agent actually reported it
// ("exact"); both "n/a" (concept doesn't exist for this agent) and missing
// data render an em dash instead of a fake 0.
function bucketText(locale: 'zh-CN' | 'en', value: number, presence?: string): string {
  return presence === 'exact' ? formatNumber(locale, value) : '—'
}

export default function AnalyticsView({ sessionId, agentType, isLive, onJumpToTurn, onJumpToTool }: Props) {
  const { locale, t } = useI18n()
  const [data, setData] = useState<AnalyticsData | null>(null)

  useEffect(() => {
    fetch(`/api/sessions/${sessionId}/analytics`)
      .then(r => r.json())
      .then(setData)
      .catch(console.error)
  }, [sessionId])

  if (!data) return (
    <div className="p-4 space-y-3">
      <div className="grid grid-cols-4 gap-3">
        {Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-16 rounded-md bg-[var(--bg-inset)] animate-pulse" />)}
      </div>
      <div className="h-[200px] rounded-lg bg-[var(--bg-inset)] animate-pulse" />
    </div>
  )

  // Cumulative token data
  let cumul = 0
  const cumulativeData = data.timeline.map(t => { cumul += t.tokens; return cumul })

  const styles = getComputedStyle(document.documentElement)
  const textColor = styles.getPropertyValue('--text-secondary').trim()
  const gridColor = styles.getPropertyValue('--border-muted').trim()
  const accentBlue = styles.getPropertyValue('--accent-blue').trim()
  const accentPurple = styles.getPropertyValue('--accent-purple').trim()

  const hasCost = data.cost_precision === 'estimated'
  const costUnit = data.billing?.billing_unit
  const costName = t('analytics.costEstimated', { unit: costUnit ?? '' })
  const accentOrange = styles.getPropertyValue('--accent-orange').trim()
  const mutedColor = styles.getPropertyValue('--text-muted').trim()

  const tokenTimeline = {
    tooltip: { trigger: 'axis' as const },
    grid: { left: 40, right: 48, top: 24, bottom: 24 },
    xAxis: { type: 'category' as const, data: data.timeline.map(t => t.rolled_back ? `R${(t.original_turn_index ?? 0) + 1}` : `T${t.turn_index}`), axisLabel: { fontSize: 11, color: textColor } },
    yAxis: [
      { type: 'value' as const, axisLabel: { fontSize: 11, color: textColor, formatter: (v: number) => formatNumber(locale, v) }, splitLine: { lineStyle: { color: gridColor } } },
      ...(hasCost ? [{ type: 'value' as const, axisLabel: { fontSize: 11, color: textColor }, splitLine: { show: false } }] : []),
    ],
    series: [{
      name: t('analytics.tokens'),
      type: 'bar',
      data: data.timeline.map(t => t.rolled_back
        ? { value: t.tokens, itemStyle: { color: mutedColor, borderRadius: [2, 2, 0, 0], opacity: 0.65 } }
        : { value: t.tokens, itemStyle: { color: accentBlue, borderRadius: [2, 2, 0, 0] } }),
      itemStyle: { color: accentBlue, borderRadius: [2, 2, 0, 0] },
    }, {
      name: t('analytics.cumulative'),
      type: 'line',
      data: cumulativeData,
      smooth: true,
      lineStyle: { color: accentPurple, width: 2 },
      itemStyle: { color: accentPurple },
      symbol: 'none',
    },
    ...(hasCost ? [{
      name: costName,
      type: 'line' as const,
      yAxisIndex: 1,
      data: data.timeline.map(t => Number((t.est_cost ?? 0).toFixed(2))),
      lineStyle: { color: accentOrange, width: 2 },
      itemStyle: { color: accentOrange },
      symbol: 'circle',
      symbolSize: 5,
    }] : [])],
    legend: { data: [t('analytics.tokens'), t('analytics.cumulative'), ...(hasCost ? [costName] : [])], textStyle: { fontSize: 11, color: textColor }, top: 0 },
  }

  const chartEvents = onJumpToTurn ? {
    click: (params: { dataIndex?: number }) => {
      if (typeof params.dataIndex === 'number') {
        onJumpToTurn(data.timeline[params.dataIndex]?.turn_index ?? params.dataIndex)
      }
    },
  } : undefined

  // Top expensive turns: by estimated cost when a bill was attributed,
  // otherwise by per-turn tokens.
  const topTurns = [...data.timeline]
    .sort((a, b) => (hasCost ? (b.est_cost ?? 0) - (a.est_cost ?? 0) : b.tokens - a.tokens))
    .filter(t => (hasCost ? (t.est_cost ?? 0) > 0 : t.tokens > 0))
    .slice(0, 3)

  const present = data.billing?.totals?.present
  const cacheKnown = present?.input === 'exact' && present?.cache_read === 'exact'
  const agentRef = agentType ? AGENT_REFERENCES[agentType] : undefined

  return (
    <div className="flex-1 overflow-auto bg-[var(--bg-surface)]">
      {/* Key metrics */}
      <div className="grid grid-cols-7 gap-3 p-4">
        {[
          [t('analytics.metric.totalTokens'), present?.input === 'exact' ? formatNumber(locale, data.total_tokens) : '—'],
          [t('analytics.metric.cacheRate'), cacheKnown ? `${data.cache_hit_rate.toFixed(1)}%` : '—'],
          [t('analytics.metric.tools'), String(data.total_tools)],
          [t('analytics.metric.anomalies'), String(data.anomaly_count)],
          [t('analytics.metric.turns'), (data.rolled_back_turn_count ?? 0) > 0 ? t('analytics.turnCounts', { active: data.turn_count, rolledBack: data.rolled_back_turn_count ?? 0 }) : String(data.turn_count)],
          [t('analytics.metric.errors'), String(data.total_errors)],
          [t('analytics.metric.avgTokens'), present?.input === 'exact' ? formatNumber(locale, Math.round(data.token_efficiency)) : '—'],
          [t('analytics.metric.contextPeak'), formatNumber(locale, data.context_peak)],
          [t('analytics.metric.pressure'), `${data.pressure_pct.toFixed(1)}%`],
          [t('analytics.metric.promptTokens'), bucketText(locale, data.prompt_tokens, present?.input)],
          [t('analytics.metric.todos'), data.todo_count > 0 ? `${data.todo_done}/${data.todo_count}` : '-'],
        ].map(([label, value]) => (
          <div key={label} className="bg-[var(--bg-inset)] rounded-md p-2.5 text-center">
            <div className="text-card text-[var(--text-primary)]">{value}</div>
            <div className="text-nav text-[var(--text-secondary)] mt-1">{label}</div>
          </div>
        ))}
      </div>

      {/* Session bill */}
      {data.billing && (
        <div className="px-4 pb-4">
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">{t('analytics.bill')}</h3>
          {data.billing.precision === 'missing' ? (
            <div className="bg-[var(--bg-inset)] rounded-lg p-3 text-body text-[var(--warning)]">
              {t('analytics.billMissing')}
            </div>
          ) : (
            <div className="bg-[var(--bg-inset)] rounded-lg p-3 space-y-2">
              <div className="flex items-baseline gap-3 flex-wrap">
                {data.billing.billing_unit && (
                  <span className="text-xl font-semibold text-[var(--text-primary)]" title={t(UNIT_HINT_KEYS[data.billing.billing_unit] ?? '')}>
                    {fmtBillingAmount(locale, data.billing.billing_amount ?? 0, data.billing.billing_unit, t)}
                  </span>
                )}
                {data.billing.precision === 'estimated' && (
                  <span className="text-nav px-1.5 py-0.5 rounded-sm bg-[var(--warning)]/20 text-[var(--warning)]">{t('analytics.estimated')}</span>
                )}
                {data.billing.totals.premium_requests > 0 && (
                  <span className="text-body text-[var(--text-secondary)]">{t('analytics.premiumRequests', { count: formatNumber(locale, data.billing.totals.premium_requests) })}</span>
                )}
                <span className="text-body text-[var(--text-secondary)]">
                  <span className="cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.input)}>{t('analytics.input')}</span>
                  {' '}{bucketText(locale, data.billing.totals.prompt_tokens, present?.input)}
                  {' · '}<span className="cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.cache_read)}>{t('analytics.cacheRead')}</span>
                  {' '}{bucketText(locale, data.billing.totals.cache_read_tokens, present?.cache_read)}
                  {' · '}<span className="cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.cache_write)}>{t('analytics.cacheWrite')}</span>
                  {' '}{bucketText(locale, data.billing.totals.cache_write_tokens, present?.cache_write)}
                  {' · '}<span className="cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.output)}>{t('analytics.output')}</span>
                  {' '}{bucketText(locale, data.billing.totals.completion_tokens, present?.output)}
                </span>
              </div>
              {data.billing.by_model && data.billing.by_model.length > 0 && (
                <table className="w-full text-body">
                  <thead>
                    <tr className="text-[var(--text-secondary)] text-left">
                      <th className="font-normal py-1">{t('analytics.model')}</th>
                      <th className="font-normal py-1 text-right cursor-help underline decoration-dotted underline-offset-2" title={t('analytics.modelRequestsHelp')}>{t('analytics.requests')}</th>
                      <th className="font-normal py-1 text-right cursor-help underline decoration-dotted underline-offset-2" title={UNIT_HINT_KEYS[data.billing.billing_unit ?? ''] ? t(UNIT_HINT_KEYS[data.billing.billing_unit ?? '']) : t('analytics.modelCostHelp')}>{t('analytics.cost')}</th>
                      <th className="font-normal py-1 text-right cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.input)}>{t('analytics.input')}</th>
                      <th className="font-normal py-1 text-right cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.cache_read)}>{t('analytics.cacheRead')}</th>
                      <th className="font-normal py-1 text-right cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.output)}>{t('analytics.output')}</th>
                      <th className="font-normal py-1 text-right cursor-help underline decoration-dotted underline-offset-2" title={t(BUCKET_HINT_KEYS.reasoning)}>{t('analytics.reasoning')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.billing.by_model.map(m => (
                      <tr key={m.model} className="border-t border-[var(--border-muted)] text-[var(--text-primary)]">
                        <td className="py-1.5">{m.model}</td>
                        <td className="py-1.5 text-right">{m.requests}</td>
                        <td className="py-1.5 text-right">{fmtBillingAmount(locale, m.billing_amount ?? 0, data.billing!.billing_unit, t)}</td>
                        <td className="py-1.5 text-right">{bucketText(locale, m.usage.prompt_tokens, m.usage.present?.input)}</td>
                        <td className="py-1.5 text-right">{bucketText(locale, m.usage.cache_read_tokens, m.usage.present?.cache_read)}</td>
                        <td className="py-1.5 text-right">{bucketText(locale, m.usage.completion_tokens, m.usage.present?.output)}</td>
                        <td className="py-1.5 text-right">{bucketText(locale, m.usage.reasoning_tokens ?? 0, m.usage.present?.reasoning)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}
        </div>
      )}

      {/* Tool frequency */}
      {Object.keys(data.tool_freq).length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">
            {t('analytics.toolUsage')}
            <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">{t('analytics.toolUsageHelp')}</span>
          </h3>
          <div className="bg-[var(--bg-inset)] rounded-lg p-2 flex flex-wrap gap-1.5">
            {Object.entries(data.tool_freq).sort((a, b) => b[1] - a[1]).map(([name, count]) => {
              const success = data.tool_success?.[name] || 0
              const total = data.tool_total?.[name] || 0
              const rate = total > 0 ? (success / total * 100) : 0
              const baseTitle = total > 0
                ? t('analytics.toolRecorded', { count, success, total })
                : t('analytics.toolUnrecorded', { count })
              return (
              <span
                key={name}
                className={`bg-[var(--bg-surface)] px-2.5 py-1 rounded-sm text-body ${onJumpToTool ? 'cursor-pointer hover:bg-[var(--bg-surface-hover)] hover:ring-1 hover:ring-[var(--accent-blue)]' : ''}`}
                title={onJumpToTool ? t('analytics.toolOpen', { base: baseTitle }) : baseTitle}
                onClick={onJumpToTool ? () => onJumpToTool(name) : undefined}
              >
                <span className="text-[var(--text-primary)] font-medium">{name}</span>
                <span className="text-[var(--text-secondary)] ml-1.5">×{count}</span>
                {total > 0 && (
                  <span className={`ml-1.5 ${rate === 100 ? 'text-[var(--success)]' : rate > 80 ? 'text-[var(--warning)]' : 'text-[var(--error)]'}`}>
                    {t('analytics.successRate', { rate: rate.toFixed(0) })}
                  </span>
                )}
              </span>
            )})}
          </div>
        </div>
      )}

      {/* Skill frequency */}
      {Object.keys(data.skill_freq).length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">{t('analytics.skills')}</h3>
          <div className="bg-[var(--bg-inset)] rounded-lg p-2 flex flex-wrap gap-1.5">
            {Object.entries(data.skill_freq).sort((a, b) => b[1] - a[1]).map(([name, count]) => (
              <span key={name} className="bg-[var(--bg-surface)] px-2.5 py-1 rounded-sm text-body">
                <span className="text-[var(--text-primary)] font-medium">{name}</span>
                <span className="text-[var(--text-secondary)] ml-1.5">×{count}</span>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Todos */}
      {data.todos && data.todos.length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">{t('analytics.todos')}</h3>
          <div className="bg-[var(--bg-inset)] rounded-lg p-2 space-y-1">
            {data.todos.map(todo => (
              <div key={todo.id} className="flex items-center gap-2 px-2 py-1 rounded-sm bg-[var(--bg-surface)]">
                <span className={`w-2 h-2 rounded-full flex-shrink-0 ${
                  todo.status === 'done' ? 'bg-[var(--success)]' :
                  todo.status === 'in_progress' ? 'bg-[var(--accent-blue)] animate-pulse' :
                  todo.status === 'blocked' ? 'bg-[var(--error)]' :
                  'bg-[var(--text-muted)]'
                }`} />
                <span className="text-body text-[var(--text-primary)] flex-1">{todo.title}</span>
                {todo.deps && todo.deps.length > 0 && (
                  <span className="text-meta text-[var(--text-muted)]">{todo.deps.length} deps</span>
                )}
                <span className="text-meta text-[var(--text-muted)]">{todo.status}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Preliminary findings (local, deterministic first layer) */}
      {data.findings && data.findings.length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">
            {t('analytics.findings')}
            <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">{t('analytics.findingsHelp')}</span>
          </h3>
          <div className="space-y-2">
            {data.findings.map((f, i) => {
              const copy = findingCopy(f, locale, t)
              const color = f.severity === 'critical' ? 'var(--error)' : f.severity === 'warn' ? 'var(--warning)' : 'var(--accent-blue)'
              const clickable = f.turn_index !== undefined && onJumpToTurn
              const Tag = clickable ? 'button' : 'div'
              return (
                <Tag
                  key={i}
                  onClick={clickable ? () => onJumpToTurn!(f.turn_index!) : undefined}
                  className={`w-full text-left bg-[var(--bg-inset)] rounded-md p-3 border-l-2 ${clickable ? 'hover:bg-[var(--bg-surface-hover)] cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]' : ''}`}
                  style={{ borderLeftColor: color }}
                >
                  <div className="text-body font-medium" style={{ color }}>{copy.title}</div>
                  <div className="text-body text-[var(--text-secondary)] mt-0.5">{copy.detail}</div>
                </Tag>
              )
            })}
          </div>
        </div>
      )}

      {/* Deep Insight (根因分析) — the model-driven second layer, shown below
          the local findings so its cause explanation reads over the raw facts. */}
      {data.findings && data.findings.length > 0 && (
        <DeepInsightSection
          sessionId={sessionId}
          agentType={agentType ?? ''}
          isLive={!!isLive}
          findingsCount={data.findings.length}
          onJumpToTurn={onJumpToTurn}
        />
      )}

      {/* Top expensive turns */}
      {topTurns.length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">
            {t('analytics.topTurns')}
            {hasCost && <span className="text-nav font-normal px-1.5 py-0.5 ml-2 rounded-sm bg-[var(--warning)]/20 text-[var(--warning)]">{t('analytics.estimated')}</span>}
            <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">
              {t(hasCost ? 'analytics.topTurnsCostHelp' : 'analytics.topTurnsTokenHelp')} · {t('analytics.jumpHelp')}
            </span>
          </h3>
          <div className="grid grid-cols-3 gap-3">
            {topTurns.map(turn => (
              <button
                key={turn.turn_index}
                onClick={() => onJumpToTurn?.(turn.turn_index)}
                className="bg-[var(--bg-inset)] rounded-md p-3 text-left hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                <div className="text-card text-[var(--text-primary)]">
                  {hasCost
                    ? fmtBillingAmount(locale, turn.est_cost ?? 0, costUnit, t)
                    : t('analytics.tokenValue', { count: formatNumber(locale, turn.tokens) })}
                </div>
                <div className="text-body text-[var(--text-secondary)] mt-1">
                  {turn.rolled_back ? t('analytics.rolledBackTurn', { turn: (turn.original_turn_index ?? 0) + 1 }) : t('analytics.turnLabel', { turn: turn.turn_index })} · {t('analytics.turnMeta', { requests: formatNumber(locale, turn.requests), tools: formatNumber(locale, turn.tool_count) })}
                  {turn.error_count > 0 && <span className="text-[var(--error)]"> · {t('analytics.errorValue', { count: formatNumber(locale, turn.error_count) })}</span>}
                </div>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Token per turn chart */}
      <div className="px-4 pb-4">
        <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">
          {t('analytics.tokenPerTurn')}
          {onJumpToTurn && <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">{t('analytics.chartJumpHelp')}</span>}
        </h3>
        <div className="bg-[var(--bg-inset)] rounded-lg p-2">
          <ReactEChartsCore echarts={echarts} option={tokenTimeline} style={{ height: 200 }} onEvents={chartEvents} />
        </div>
      </div>

      {/* Agent-specific billing reference */}
      {agentRef && (
        <div className="px-4 pb-6">
          <details className="bg-[var(--bg-inset)] rounded-lg">
            <summary className="cursor-pointer select-none px-3 py-2 text-body font-semibold text-[var(--text-primary)]">
              {t('analytics.billingReference', { agent: agentType ?? '' })}
              <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">{t('analytics.billingReferenceHelp')}</span>
            </summary>
            <div className="px-3 pb-3 space-y-1.5">
              {agentRef.concepts.map(([term, desc]) => (
                <div key={term} className="text-body">
                  <span className="text-[var(--text-primary)] font-medium">{term.startsWith('analytics.') ? t(term) : term}</span>
                  <span className="text-[var(--text-secondary)] ml-2">{t(desc)}</span>
                </div>
              ))}
              {agentRef.links.length > 0 && (
                <div className="pt-1.5 flex flex-wrap gap-3">
                  {agentRef.links.map(([label, url]) => (
                    <a key={url} href={url} target="_blank" rel="noreferrer" className="text-body text-[var(--accent-blue)] hover:underline">
                      {t(label)} ↗
                    </a>
                  ))}
                </div>
              )}
            </div>
          </details>
        </div>
      )}
    </div>
  )
}
