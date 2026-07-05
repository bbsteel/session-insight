import { useEffect, useState } from 'react'
import ReactEChartsCore from 'echarts-for-react/lib/core'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

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
  token_efficiency: number
  timeline: {
    turn_index: number
    tokens: number
    duration_ms: number
    tool_count: number
    error_count: number
  }[]
  tool_freq: Record<string, number>
  context_window: number
  context_peak: number
  pressure_pct: number
  health_score: number
  health_grade: string
  skill_freq: Record<string, number>
  tool_success: Record<string, number>
  tool_total: Record<string, number>
}

interface Props {
  sessionId: string
  agentType?: string
}

interface AgentReference {
  concepts: [string, string][]
  links: [string, string][]
}

// Billing/token concepts differ per agent; this reference panel spares the
// user a search for what each unit on the bill actually means.
const SHARED_CONCEPTS: [string, string][] = [
  ['Input', '未命中缓存、按全价计费的输入 token（系统提示 + 对话历史 + 工具定义中新处理的部分）'],
  ['Cache Read', '命中提供商 prompt cache 的输入，价格约为全价的 1/10。长会话中体量最大，因为每次调用都会重放整个上下文'],
  ['Cache Write', '首次写入缓存的输入。Anthropic 系收溢价（5 分钟 TTL 1.25 倍 / 1 小时 2 倍）；OpenAI 系缓存自动且免费，因此显示「—」'],
  ['Output', '模型生成的内容（回复正文 + 工具调用参数），单价约为 Input 的 3~5 倍'],
  ['Reasoning', '推理模型的隐藏思考 token，是 Output 的子集（计费已含在 Output 内，单列仅供参考）'],
]

const AGENT_REFERENCES: Record<string, AgentReference> = {
  copilot: {
    concepts: [
      ['AIU', 'Copilot 的用量计费单位（AI Unit），来自会话结束事件的 totalNanoAiu（1 AIU = 10 亿 nanoAiu）。月度配额按 AIU 扣减，这是「配额去哪了」的直接答案'],
      ['Premium Requests', 'Copilot 的另一种计费口径：调用高级模型时按次计数，与 AIU 并存'],
    ],
    links: [
      ['Copilot 用量与计费文档', 'https://docs.github.com/en/copilot/managing-copilot/understanding-and-managing-copilot-usage'],
    ],
  },
  claude: {
    concepts: [
      ['USD 计价', 'Claude 按 token 直接计价，每条消息带精确 usage，因此本页数字为精确值'],
      ['Cache TTL', 'cache_write 细分 5 分钟（1.25 倍）与 1 小时（2 倍）两档溢价'],
    ],
    links: [
      ['Claude 模型定价', 'https://docs.claude.com/en/docs/about-claude/pricing'],
    ],
  },
  codex: {
    concepts: [
      ['Cached Input', 'OpenAI 的 prompt 缓存自动生效且写入免费，命中部分按约 1/10 价格计，所以没有 Cache Write 一栏'],
      ['Reasoning', 'o 系/GPT-5 系推理 token 按 Output 价格计费，已包含在 Output 内'],
    ],
    links: [
      ['OpenAI API 定价', 'https://platform.openai.com/docs/pricing'],
    ],
  },
  opencode: {
    concepts: [
      ['Cost (USD)', 'OpenCode 按底层 provider 的定价把每条消息折算成美元；订阅制 provider 记 0'],
    ],
    links: [
      ['OpenCode 文档', 'https://opencode.ai/docs'],
    ],
  },
}

function fmtNumber(n: number): string {
  return n.toLocaleString()
}

const UNIT_HINTS: Record<string, string> = {
  aiu: 'AIU = Copilot 的用量计费单位（AI Unit），月度配额按它扣减',
  usd: '按底层模型定价折算的美元成本',
  premium_requests: 'Copilot 高级模型按次计费的请求数',
}

function fmtBillingAmount(amount: number, unit?: string): string {
  switch (unit) {
    case 'aiu': return `${amount.toFixed(2)} AIU`
    case 'usd': return `$${amount.toFixed(4)}`
    case 'premium_requests': return `${amount} premium`
    default: return fmtNumber(Math.round(amount))
  }
}

// A bucket value is only meaningful when the agent actually reported it
// ("exact"); both "n/a" (concept doesn't exist for this agent) and missing
// data render an em dash instead of a fake 0.
function bucketText(value: number, presence?: string): string {
  return presence === 'exact' ? fmtNumber(value) : '—'
}

export default function AnalyticsView({ sessionId, agentType }: Props) {
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

  const tokenTimeline = {
    tooltip: { trigger: 'axis' as const },
    grid: { left: 40, right: 40, top: 8, bottom: 24 },
    xAxis: { type: 'category' as const, data: data.timeline.map(t => `T${t.turn_index}`), axisLabel: { fontSize: 10, color: textColor } },
    yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, color: textColor, formatter: (v: number) => fmtNumber(v) }, splitLine: { lineStyle: { color: gridColor } } },
    series: [{
      name: 'Tokens',
      type: 'bar',
      data: data.timeline.map(t => t.tokens),
      itemStyle: { color: accentBlue, borderRadius: [2, 2, 0, 0] },
    }, {
      name: 'Cumulative',
      type: 'line',
      data: cumulativeData,
      smooth: true,
      lineStyle: { color: accentPurple, width: 2 },
      itemStyle: { color: accentPurple },
      symbol: 'none',
    }],
    legend: { data: ['Tokens', 'Cumulative'], textStyle: { fontSize: 10, color: textColor }, top: 0 },
  }

  const present = data.billing?.totals?.present
  const cacheKnown = present?.input === 'exact' && present?.cache_read === 'exact'
  const agentRef = agentType ? AGENT_REFERENCES[agentType] : undefined

  return (
    <div className="flex-1 overflow-auto bg-[var(--bg-surface)]">
      {/* Key metrics */}
      <div className="grid grid-cols-7 gap-3 p-4">
        {[
          ['Total Tokens', present?.input === 'exact' ? fmtNumber(data.total_tokens) : '—'],
          ['Cache Rate', cacheKnown ? `${data.cache_hit_rate.toFixed(1)}%` : '—'],
          ['Tools Used', String(data.total_tools)],
          ['Anomalies', String(data.anomaly_count)],
          ['Turn Count', String(data.turn_count)],
          ['Errors', String(data.total_errors)],
          ['Avg Tok/Turn', present?.input === 'exact' ? fmtNumber(Math.round(data.token_efficiency)) : '—'],
          ['Context Peak', fmtNumber(data.context_peak)],
          ['Pressure', `${data.pressure_pct.toFixed(1)}%`],
          ['Health', `${data.health_score} (${data.health_grade})`],
          ['Prompt Tok', bucketText(data.prompt_tokens, present?.input)],
['Todos', data.todo_count > 0 ? `${data.todo_done}/${data.todo_count}` : '-'],
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
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">Session Bill · 会话账单</h3>
          {data.billing.precision === 'missing' ? (
            <div className="bg-[var(--bg-inset)] rounded-lg p-3 text-body text-[var(--warning)]">
              会话未正常退出（缺少 session.shutdown），该 agent 的账单数据不可得
            </div>
          ) : (
            <div className="bg-[var(--bg-inset)] rounded-lg p-3 space-y-2">
              <div className="flex items-baseline gap-3 flex-wrap">
                {data.billing.billing_unit && (
                  <span className="text-xl font-semibold text-[var(--text-primary)]" title={UNIT_HINTS[data.billing.billing_unit] ?? ''}>
                    {fmtBillingAmount(data.billing.billing_amount ?? 0, data.billing.billing_unit)}
                  </span>
                )}
                {data.billing.precision === 'estimated' && (
                  <span className="text-nav px-1.5 py-0.5 rounded-sm bg-[var(--warning)]/20 text-[var(--warning)]">估算</span>
                )}
                {data.billing.totals.premium_requests > 0 && (
                  <span className="text-body text-[var(--text-secondary)]">{data.billing.totals.premium_requests} premium requests</span>
                )}
                <span className="text-body text-[var(--text-secondary)]">
                  input {bucketText(data.billing.totals.prompt_tokens, present?.input)}
                  {' · '}cache read {bucketText(data.billing.totals.cache_read_tokens, present?.cache_read)}
                  {' · '}cache write {bucketText(data.billing.totals.cache_write_tokens, present?.cache_write)}
                  {' · '}output {bucketText(data.billing.totals.completion_tokens, present?.output)}
                </span>
              </div>
              {data.billing.by_model && data.billing.by_model.length > 0 && (
                <table className="w-full text-body">
                  <thead>
                    <tr className="text-[var(--text-secondary)] text-left">
                      <th className="font-normal py-1">Model</th>
                      <th className="font-normal py-1 text-right">Requests</th>
                      <th className="font-normal py-1 text-right">Cost</th>
                      <th className="font-normal py-1 text-right">Input</th>
                      <th className="font-normal py-1 text-right">Cache Read</th>
                      <th className="font-normal py-1 text-right">Output</th>
                      <th className="font-normal py-1 text-right">Reasoning</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.billing.by_model.map(m => (
                      <tr key={m.model} className="border-t border-[var(--border-muted)] text-[var(--text-primary)]">
                        <td className="py-1.5">{m.model}</td>
                        <td className="py-1.5 text-right">{m.requests}</td>
                        <td className="py-1.5 text-right">{fmtBillingAmount(m.billing_amount ?? 0, data.billing!.billing_unit)}</td>
                        <td className="py-1.5 text-right">{bucketText(m.usage.prompt_tokens, m.usage.present?.input)}</td>
                        <td className="py-1.5 text-right">{bucketText(m.usage.cache_read_tokens, m.usage.present?.cache_read)}</td>
                        <td className="py-1.5 text-right">{bucketText(m.usage.completion_tokens, m.usage.present?.output)}</td>
                        <td className="py-1.5 text-right">{bucketText(m.usage.reasoning_tokens ?? 0, m.usage.present?.reasoning)}</td>
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
            Tool Usage
            <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">工具名 × 调用次数 · 成功率（按退出码统计）</span>
          </h3>
          <div className="bg-[var(--bg-inset)] rounded-lg p-2 flex flex-wrap gap-1.5">
            {Object.entries(data.tool_freq).sort((a, b) => b[1] - a[1]).map(([name, count]) => {
              const success = data.tool_success?.[name] || 0
              const total = data.tool_total?.[name] || 0
              const rate = total > 0 ? (success / total * 100) : 0
              return (
              <span key={name} className="bg-[var(--bg-surface)] px-2.5 py-1 rounded-sm text-body" title={total > 0 ? `调用 ${count} 次，${success}/${total} 次退出码为 0` : `调用 ${count} 次（该 agent 未记录退出码）`}>
                <span className="text-[var(--text-primary)] font-medium">{name}</span>
                <span className="text-[var(--text-secondary)] ml-1.5">×{count}</span>
                {total > 0 && (
                  <span className={`ml-1.5 ${rate === 100 ? 'text-[var(--success)]' : rate > 80 ? 'text-[var(--warning)]' : 'text-[var(--error)]'}`}>
                    成功 {rate.toFixed(0)}%
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
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">Skills</h3>
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
          <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">Todos</h3>
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

      {/* Token per turn chart */}
      <div className="px-4 pb-4">
        <h3 className="text-body font-semibold text-[var(--text-primary)] mb-2">Token Per Turn</h3>
        <div className="bg-[var(--bg-inset)] rounded-lg p-2">
          <ReactEChartsCore echarts={echarts} option={tokenTimeline} style={{ height: 200 }} />
        </div>
      </div>

      {/* Concept reference */}
      <div className="px-4 pb-6">
        <details className="bg-[var(--bg-inset)] rounded-lg">
          <summary className="cursor-pointer select-none px-3 py-2 text-body font-semibold text-[var(--text-primary)]">
            概念参考{agentRef ? `（${agentType}）` : ''}
            <span className="text-nav font-normal text-[var(--text-secondary)] ml-2">AIU、缓存、token 分类都是什么意思</span>
          </summary>
          <div className="px-3 pb-3 space-y-1.5">
            {(agentRef?.concepts ?? []).concat(SHARED_CONCEPTS).map(([term, desc]) => (
              <div key={term} className="text-body">
                <span className="text-[var(--text-primary)] font-medium">{term}</span>
                <span className="text-[var(--text-secondary)] ml-2">{desc}</span>
              </div>
            ))}
            {agentRef && agentRef.links.length > 0 && (
              <div className="pt-1.5 flex flex-wrap gap-3">
                {agentRef.links.map(([label, url]) => (
                  <a key={url} href={url} target="_blank" rel="noreferrer" className="text-body text-[var(--accent-blue)] hover:underline">
                    {label} ↗
                  </a>
                ))}
              </div>
            )}
          </div>
        </details>
      </div>
    </div>
  )
}
