import { useEffect, useState } from 'react'
import ReactEChartsCore from 'echarts-for-react/lib/core'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([BarChart, LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

interface AnalyticsData {
  total_tokens: number
  todo_count: number
  todo_done: number
  todos: { id: string; title: string; description: string; status: string; deps?: string[] }[]
  prompt_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cache_hit_rate: number
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
}

function fmtK(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

export default function AnalyticsView({ sessionId }: Props) {
  const [data, setData] = useState<AnalyticsData | null>(null)

  useEffect(() => {
    fetch(`/api/sessions/${sessionId}/analytics`)
      .then(r => r.json())
      .then(setData)
      .catch(console.error)
  }, [sessionId])

  if (!data) return <div className="p-4 text-helper text-[var(--text-muted)]">Loading analytics...</div>

  // Cumulative token data
  let cumul = 0
  const cumulativeData = data.timeline.map(t => { cumul += t.tokens; return cumul })

  const isDark = document.documentElement.classList.contains('dark')
  const textColor = isDark ? '#9ea5b4' : '#555b6e'
  const gridColor = isDark ? '#232330' : '#e4e6ec'

  const tokenTimeline = {
    tooltip: { trigger: 'axis' as const },
    grid: { left: 40, right: 40, top: 8, bottom: 24 },
    xAxis: { type: 'category' as const, data: data.timeline.map(t => `T${t.turn_index}`), axisLabel: { fontSize: 10, color: textColor } },
    yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, color: textColor, formatter: (v: number) => fmtK(v) }, splitLine: { lineStyle: { color: gridColor } } },
    series: [{
      name: 'Tokens',
      type: 'bar',
      data: data.timeline.map(t => t.tokens),
      itemStyle: { color: 'var(--accent-blue)', borderRadius: [2, 2, 0, 0] },
    }, {
      name: 'Cumulative',
      type: 'line',
      data: cumulativeData,
      smooth: true,
      lineStyle: { color: 'var(--accent-purple)', width: 2 },
      itemStyle: { color: 'var(--accent-purple)' },
      symbol: 'none',
    }],
    legend: { data: ['Tokens', 'Cumulative'], textStyle: { fontSize: 10, color: textColor }, top: 0 },
  }

  return (
    <div className="flex-1 overflow-auto bg-[var(--bg-surface)]">
      {/* Key metrics */}
      <div className="grid grid-cols-7 gap-3 p-4">
        {[
          ['Total Tokens', fmtK(data.total_tokens)],
          ['Cache Rate', `${data.cache_hit_rate.toFixed(1)}%`],
          ['Tools Used', String(data.total_tools)],
          ['Anomalies', String(data.anomaly_count)],
          ['Turn Count', String(data.turn_count)],
          ['Errors', String(data.total_errors)],
          ['Avg Tok/Turn', fmtK(Math.round(data.token_efficiency))],
          ['Context Peak', fmtK(data.context_peak)],
          ['Pressure', `${data.pressure_pct.toFixed(1)}%`],
          ['Health', `${data.health_score} (${data.health_grade})`],
          ['Prompt Tok', fmtK(data.prompt_tokens)],
['Todos', data.todo_count > 0 ? `${data.todo_done}/${data.todo_count}` : '-'],
        ].map(([label, value]) => (
          <div key={label} className="bg-[var(--bg-inset)] rounded-md p-2 text-center">
            <div className="text-card text-[var(--text-primary)]">{value}</div>
            <div className="text-meta text-[var(--text-muted)] mt-0.5">{label}</div>
          </div>
        ))}
      </div>

      {/* Tool frequency */}
      {Object.keys(data.tool_freq).length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-2">Tool Usage</h3>
          <div className="bg-[var(--bg-inset)] rounded-lg p-2 flex flex-wrap gap-1.5">
            {Object.entries(data.tool_freq).sort((a, b) => b[1] - a[1]).map(([name, count]) => {
              const success = data.tool_success?.[name] || 0
              const total = data.tool_total?.[name] || count
              const rate = total > 0 ? (success / total * 100) : 100
              return (
              <span key={name} className="bg-[var(--bg-surface)] px-2 py-1 rounded-sm text-meta">
                <span className="text-[var(--text-primary)] font-medium">{count}</span>
                <span className="text-[var(--text-muted)] ml-1">{name}</span>
                {total > 0 && (
                  <span className={`ml-1 ${rate === 100 ? 'text-[var(--success)]' : rate > 80 ? 'text-[var(--warning)]' : 'text-[var(--error)]'}`}>
                    {rate.toFixed(0)}%
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
          <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-2">Skills</h3>
          <div className="bg-[var(--bg-inset)] rounded-lg p-2 flex flex-wrap gap-1.5">
            {Object.entries(data.skill_freq).sort((a, b) => b[1] - a[1]).map(([name, count]) => (
              <span key={name} className="bg-[var(--bg-surface)] px-2 py-1 rounded-sm text-meta">
                <span className="text-[var(--text-primary)] font-medium">{count}</span>
                <span className="text-[var(--text-muted)] ml-1">{name}</span>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Todos */}
      {data.todos && data.todos.length > 0 && (
        <div className="px-4 pb-4">
          <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-2">Todos</h3>
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
        <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-2">Token Per Turn</h3>
        <div className="bg-[var(--bg-inset)] rounded-lg p-2">
          <ReactEChartsCore echarts={echarts} option={tokenTimeline} style={{ height: 200 }} />
        </div>
      </div>
    </div>
  )
}
