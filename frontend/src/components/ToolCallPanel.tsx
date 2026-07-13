import { useEffect, useMemo, useRef, useState } from 'react'
import type { MiniMapPosition, PositionsResponse } from '../types'

// 工具调用管理面板:按时序列出会话的所有工具调用(kind === 'tool' 的
// positions),支持按工具类型筛选、文字过滤、展开查看参数、点击跳转到终端
// 对应行。数据全部来自 positions API 的 payload,不需要额外请求。
// 以浮层形式覆盖在终端右侧:不占布局宽度,开关不会引起终端列数变化。

interface ToolEntry {
  key: string
  seq: number // 全会话时序编号(1 起),筛选后仍保持原编号
  line: number
  logical: number | null // 逻辑行(跳转首选,折叠 badge 不影响它)
  turn: number
  name: string
  category: string
  summary: string
  tsMs: number | null
  durationMs: number | null
  status: string // '' | 'ok' | 'error' | 'timeout' | 'rejected'
  preview: string[]
}

interface Props {
  positions: PositionsResponse | null
  building: boolean
  // 外部筛选请求(分析页 Tool Usage chip):token 变化时应用 name 筛选。
  filterRequest?: { name: string; token: number } | null
  onJump: (lineStart: number, logicalStart?: number) => void
  onClose: () => void
}

function toEntry(p: MiniMapPosition, seq: number): ToolEntry {
  const pl = p.payload ?? {}
  return {
    key: p.position_key,
    seq,
    line: p.line_start,
    logical: typeof pl.logical_start === 'number' ? pl.logical_start : null,
    turn: p.turn_index,
    name: typeof pl.tool_name === 'string' ? pl.tool_name : p.label,
    category: typeof pl.category === 'string' ? pl.category : 'tool',
    summary: typeof pl.summary === 'string' ? pl.summary : '',
    tsMs: typeof pl.ts_ms === 'number' ? pl.ts_ms : null,
    durationMs: typeof pl.duration_ms === 'number' ? pl.duration_ms : null,
    status: typeof pl.status === 'string' ? pl.status : '',
    preview: Array.isArray(pl.input_preview) ? pl.input_preview.filter((l): l is string => typeof l === 'string') : [],
  }
}

function fmtTime(tsMs: number): string {
  const d = new Date(tsMs)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function fmtDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m${String(Math.floor((ms % 60_000) / 1000)).padStart(2, '0')}s`
  return `${Math.floor(ms / 3_600_000)}h${String(Math.floor((ms % 3_600_000) / 60_000)).padStart(2, '0')}m`
}

// 每种工具一个稳定专属色,形成"看颜色即知工具"的记忆点:常见工具按语义
// 固定配色(执行=绿、读取=蓝、写改=橙、搜索=紫、子代理=粉、网络=青),
// 未知工具按名字哈希取色,同名永远同色。
const TOOL_COLORS: Record<string, string> = {
  Bash: '#3fb950',
  BashOutput: '#3fb950',
  Read: '#58a6ff',
  Edit: '#d29922',
  Write: '#e3b341',
  MultiEdit: '#d29922',
  NotebookEdit: '#d29922',
  apply_patch: '#d29922',
  Grep: '#bc8cff',
  Glob: '#bc8cff',
  Task: '#f778ba',
  Agent: '#f778ba',
  WebFetch: '#39c5cf',
  WebSearch: '#39c5cf',
  TodoWrite: '#79c0ff',
}

function toolColor(name: string): string {
  const fixed = TOOL_COLORS[name]
  if (fixed) return fixed
  let h = 0
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0
  return `hsl(${h % 360}, 55%, 65%)`
}

// 展开只在能看到"摘要之外的内容"时才有意义:所有参数值都已完整出现在
// 摘要里(load_skill 这类单短参数工具)时,不提供展开入口。
function previewRedundant(e: ToolEntry): boolean {
  if (e.preview.length === 0) return true
  return e.preview.every(l => {
    const idx = l.indexOf(': ')
    const val = (idx > 0 ? l.slice(idx + 2) : l).trim()
    return val !== '' && e.summary.includes(val)
  })
}

const statusIcon: Record<string, { icon: string; className: string; title: string }> = {
  ok: { icon: '✓', className: 'text-[var(--accent-green)]', title: '成功' },
  error: { icon: '✗', className: 'text-[var(--error)]', title: '失败' },
  timeout: { icon: '⏱', className: 'text-[var(--error)]', title: '超时' },
  rejected: { icon: '⊘', className: 'text-[var(--warning,#d29922)]', title: '被拒绝' },
  '': { icon: '·', className: 'text-[var(--text-muted)]', title: '无结果记录' },
}

// 摘要不折行到需要展开才能看:最多 4 行(服务端摘要上限 200 列,面板宽度
// 下最坏 ~5 行,4 行几乎总能放下),牺牲一点密度换"不展开就能看全"。
const summaryClampStyle: React.CSSProperties = {
  display: '-webkit-box',
  WebkitLineClamp: 4,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  wordBreak: 'break-all',
}

const WIDE_STORAGE_KEY = 'si-toolpanel-wide'

export default function ToolCallPanel({ positions, building, filterRequest, onJump, onClose }: Props) {
  const panelRef = useRef<HTMLElement>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  // 宽/标准两档宽度,记忆到本地;浮层不占终端布局,加宽零成本。
  const [wide, setWide] = useState(() => localStorage.getItem(WIDE_STORAGE_KEY) === '1')
  const toggleWide = () => setWide(w => {
    localStorage.setItem(WIDE_STORAGE_KEY, w ? '0' : '1')
    return !w
  })
  const [activeKey, setActiveKey] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  // The panel is an in-terminal floating layer, so clicks outside it do not
  // bubble through a shared modal backdrop. Listen at the document level and
  // use the panel ref to keep all controls inside the panel interactive.
  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && !panelRef.current?.contains(target)) onClose()
    }
    document.addEventListener('pointerdown', handlePointerDown)
    return () => document.removeEventListener('pointerdown', handlePointerDown)
  }, [onClose])

  // 应用来自分析页的筛选请求:只按该工具筛选,并清掉文字过滤避免叠加后空结果。
  useEffect(() => {
    if (!filterRequest) return
    setSelected(new Set([filterRequest.name]))
    setQuery('')
  }, [filterRequest?.token])

  const entries = useMemo(
    () => (positions?.positions ?? []).filter(p => p.kind === 'tool').map((p, i) => toEntry(p, i + 1)),
    [positions],
  )

  // 筛选 chips:按调用次数降序;点击切换,空集 = 全部显示。
  const nameCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const e of entries) counts.set(e.name, (counts.get(e.name) ?? 0) + 1)
    return [...counts.entries()].sort((a, b) => b[1] - a[1])
  }, [entries])

  // 类型 chips 与文字过滤叠加生效;文字匹配工具名和参数摘要。
  const q = query.trim().toLowerCase()
  const visible = entries.filter(e =>
    (selected.size === 0 || selected.has(e.name)) &&
    (q === '' || e.name.toLowerCase().includes(q) || e.summary.toLowerCase().includes(q)))
  const filtering = selected.size > 0 || q !== ''

  const toggleName = (name: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      next.has(name) ? next.delete(name) : next.add(name)
      return next
    })
  }

  const toggleExpanded = (key: string) => {
    setExpanded(prev => {
      const next = new Set(prev)
      next.has(key) ? next.delete(key) : next.add(key)
      return next
    })
  }

  const jump = (e: ToolEntry) => {
    setActiveKey(e.key)
    onJump(e.line, e.logical ?? undefined)
  }

  // 全部展开/收起作用于当前筛选可见且展开有增量内容的条目。
  const expandableKeys = visible.filter(e => !previewRedundant(e)).map(e => e.key)
  const anyExpanded = expandableKeys.some(k => expanded.has(k))
  const toggleAll = () => {
    setExpanded(prev => {
      if (anyExpanded) {
        const next = new Set(prev)
        for (const k of expandableKeys) next.delete(k)
        return next
      }
      return new Set([...prev, ...expandableKeys])
    })
  }

  return (
    <aside ref={panelRef} className={`absolute inset-y-0 right-0 z-10 flex max-w-[calc(100%-24px)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-[-8px_0_24px_rgba(0,0,0,0.35)] ${wide ? 'w-[640px]' : 'w-[420px]'}`}>
      <div className="flex h-9 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-3">
        <span className="text-nav font-medium text-[var(--text-primary)]">工具调用</span>
        <span className="text-meta text-[var(--text-muted)]">
          {filtering ? `${visible.length}/${entries.length}` : entries.length}
        </span>
        <span className="flex-1" />
        <button
          onClick={toggleWide}
          className="h-6 rounded px-1.5 text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title={wide ? '恢复标准宽度' : '加宽面板'}
        >
          {wide ? '⇥ 标准' : '⇤ 加宽'}
        </button>
        {expandableKeys.length > 0 && (
          <button
            onClick={toggleAll}
            className="h-6 rounded px-1.5 text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            title={anyExpanded ? '收起所有参数' : '展开所有参数'}
          >
            {anyExpanded ? '⊟ 收起全部' : '⊞ 展开全部'}
          </button>
        )}
        <button
          onClick={onClose}
          className="h-6 w-6 rounded text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title="关闭"
          aria-label="关闭工具调用面板"
        >
          ×
        </button>
      </div>

      <div className="flex-shrink-0 border-b border-[var(--border-muted)] p-2">
        <input
          value={query}
          onChange={ev => setQuery(ev.target.value)}
          placeholder="筛选:工具名 / 参数…"
          className="h-6 w-full rounded border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-meta text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none"
          aria-label="按文字筛选工具调用"
        />
      </div>

      {nameCounts.length > 0 && (
        <div className="flex max-h-[96px] flex-shrink-0 flex-wrap gap-1 overflow-y-auto border-b border-[var(--border-muted)] p-2">
          <button
            onClick={() => setSelected(new Set())}
            className={`h-5 rounded-full border px-2 text-meta leading-none ${
              selected.size === 0
                ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]'
            }`}
          >
            全部
          </button>
          {nameCounts.map(([name, count]) => {
            const color = toolColor(name)
            const isOn = selected.has(name)
            return (
              <button
                key={name}
                onClick={() => toggleName(name)}
                className={`h-5 rounded-full border px-2 text-meta leading-none ${
                  isOn ? '' : 'border-[var(--border-default)] hover:bg-[var(--bg-surface-hover)]'
                }`}
                style={isOn
                  ? { color, borderColor: color, backgroundColor: `color-mix(in srgb, ${color} 14%, transparent)` }
                  : { color }}
                title={`${name} · ${count} 次调用`}
              >
                {name} {count}
              </button>
            )
          })}
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {/* 列标题:与下方行用同一套固定列宽,竖线上下对齐,形成表格感。 */}
        <div className="sticky top-0 z-[1] flex items-stretch border-b border-[var(--border-muted)] bg-[var(--bg-surface)] text-meta text-[var(--text-muted)]">
          <div className="w-5 flex-shrink-0 border-r border-[var(--border-muted)]" />
          <div className="w-8 flex-shrink-0 border-r border-[var(--border-muted)] px-1 py-1 text-right">#</div>
          <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 py-1 text-center">时间</div>
          <div className="min-w-0 flex-1 px-1.5 py-1">工具 · 参数</div>
          <div className="flex-shrink-0 px-1.5 py-1">耗时</div>
        </div>
        {building && (
          <div className="p-3 text-helper text-[var(--text-muted)]">位置索引构建中…</div>
        )}
        {!building && entries.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">此会话没有工具调用记录</div>
        )}
        {!building && entries.length > 0 && visible.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">没有匹配当前筛选的调用</div>
        )}
        {visible.map(e => {
          const st = statusIcon[e.status] ?? statusIcon['']
          const canExpand = !previewRedundant(e)
          const isExpanded = canExpand && expanded.has(e.key)
          const isActive = e.key === activeKey
          const color = toolColor(e.name)
          return (
            <div
              key={e.key}
              className={`border-b border-[var(--border-muted)] ${isActive ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)]' : ''}`}
              style={{ contentVisibility: 'auto', containIntrinsicSize: 'auto 48px', borderLeft: `2px solid ${color}` } as React.CSSProperties}
            >
              <div
                className="flex cursor-pointer items-stretch hover:bg-[var(--bg-surface-hover)]"
                onClick={() => jump(e)}
                title={`跳转到终端第 ${e.line} 行 · Turn ${e.turn}`}
              >
                {/* 展开三角列:悬停只高亮这一列的背景,与"点行跳转"区分。 */}
                <div
                  className={`flex w-5 flex-shrink-0 items-start justify-center border-r border-[var(--border-muted)] text-helper text-[var(--text-muted)] ${
                    canExpand
                      ? 'cursor-pointer hover:bg-[color-mix(in_srgb,var(--accent-blue)_22%,transparent)] hover:text-[var(--accent-blue)]'
                      : ''
                  }`}
                  onClick={ev => {
                    if (!canExpand) return
                    ev.stopPropagation()
                    toggleExpanded(e.key)
                  }}
                  title={!canExpand ? '参数已完整显示' : isExpanded ? '收起参数' : '展开参数'}
                  role={canExpand ? 'button' : undefined}
                  aria-label={canExpand ? (isExpanded ? '收起参数' : '展开参数') : undefined}
                >
                  <span className="pt-1.5">{canExpand ? (isExpanded ? '▾' : '▸') : ''}</span>
                </div>
                <div className="w-8 flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-right text-meta tabular-nums text-[var(--text-muted)]">
                  {e.seq}
                </div>
                <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-center text-meta tabular-nums text-[var(--text-muted)]">
                  {e.tsMs !== null ? fmtTime(e.tsMs) : ''}
                </div>
                <div className="flex min-w-0 flex-1 items-start gap-1 px-1.5 py-1.5">
                  <span className={`w-3 flex-shrink-0 text-center text-meta font-semibold ${st.className}`} title={st.title}>
                    {st.icon}
                  </span>
                  <span className="flex-shrink-0 text-meta font-medium" style={{ color }}>{e.name}</span>
                  <span
                    className="min-w-0 flex-1 font-mono text-meta text-[var(--text-secondary)]"
                    style={summaryClampStyle}
                    title={e.summary}
                  >
                    {e.summary}
                  </span>
                </div>
                {e.durationMs !== null && (
                  <div className="flex-shrink-0 whitespace-nowrap px-1.5 pt-1.5 text-meta tabular-nums text-[var(--text-muted)]">
                    {fmtDuration(e.durationMs)}
                  </div>
                )}
              </div>
              {isExpanded && e.preview.length > 0 && (
                <pre className="m-0 max-h-[180px] overflow-y-auto whitespace-pre-wrap break-all border-t border-dashed border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-1.5 font-mono text-meta leading-relaxed text-[var(--text-secondary)]">
                  {e.preview.join('\n')}
                </pre>
              )}
            </div>
          )
        })}
      </div>
    </aside>
  )
}
