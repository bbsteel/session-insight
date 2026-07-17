import { useEffect, useMemo, useRef, useState } from 'react'
import type { MiniMapPosition, PositionsResponse } from '../types'

// 用户消息管理面板:按时间序列出会话的所有用户消息(kind === 'user' 的
// positions),支持文字过滤、点击跳转到终端对应行。数据全部来自 positions
// API,不需要额外请求。以浮层形式覆盖在终端右侧,不占布局宽度。

interface UserMessageEntry {
  key: string
  seq: number // 全会话时序编号(1 起),筛选后仍保持原编号
  line: number
  logical: number | null // 逻辑行(跳转首选,折叠 badge 不影响它)
  label: string
  tsMs: number | null
}

interface Props {
  positions: PositionsResponse | null
  building: boolean
  onJump: (lineStart: number, logicalStart?: number) => void
  onClose: () => void
}

function toEntry(p: MiniMapPosition, seq: number): UserMessageEntry {
  const pl = p.payload ?? {}
  return {
    key: p.position_key,
    seq,
    line: p.line_start,
    logical: typeof pl.logical_start === 'number' ? pl.logical_start : null,
    label: typeof pl.text === 'string' && pl.text !== '' ? pl.text : p.label,
    tsMs: typeof pl.ts_ms === 'number' ? pl.ts_ms : null,
  }
}

function fmtTime(tsMs: number): string {
  const d = new Date(tsMs)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

// 消息内容最多显示 4 行,避免面板行高过高。
const summaryClampStyle: React.CSSProperties = {
  display: '-webkit-box',
  WebkitLineClamp: 4,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  wordBreak: 'break-all',
}

const WIDE_STORAGE_KEY = 'si-userpanel-wide'

export default function UserMessagePanel({ positions, building, onJump, onClose }: Props) {
  const panelRef = useRef<HTMLElement>(null)
  const [activeKey, setActiveKey] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  // 宽/标准两档宽度,记忆到本地;浮层不占终端布局,加宽零成本。
  const [wide, setWide] = useState(() => localStorage.getItem(WIDE_STORAGE_KEY) === '1')
  const toggleWide = () => setWide(w => {
    localStorage.setItem(WIDE_STORAGE_KEY, w ? '0' : '1')
    return !w
  })

  // 面板是覆盖在终端上的浮层,点击外部关闭,同时保留面板内部交互。
  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && !panelRef.current?.contains(target)) onClose()
    }
    document.addEventListener('pointerdown', handlePointerDown)
    return () => document.removeEventListener('pointerdown', handlePointerDown)
  }, [onClose])

  const entries = useMemo(
    () => (positions?.positions ?? []).filter(p => p.kind === 'user').map((p, i) => toEntry(p, i + 1)),
    [positions],
  )

  const q = query.trim().toLowerCase()
  const visible = entries.filter(e =>
    q === '' || e.label.toLowerCase().includes(q)
  )
  const filtering = q !== ''

  const jump = (e: UserMessageEntry) => {
    setActiveKey(e.key)
    onJump(e.line, e.logical ?? undefined)
  }

  return (
    <aside ref={panelRef} className={`absolute inset-y-0 right-0 z-10 flex max-w-[calc(100%-24px)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-[-8px_0_24px_rgba(0,0,0,0.35)] ${wide ? 'w-[640px]' : 'w-[420px]'}`}>
      <div className="flex h-9 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-3">
        <span className="text-nav font-medium text-[var(--text-primary)]">用户消息</span>
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
        <button
          onClick={onClose}
          className="h-6 w-6 rounded text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title="关闭"
          aria-label="关闭用户消息面板"
        >
          ×
        </button>
      </div>

      <div className="flex-shrink-0 border-b border-[var(--border-muted)] p-2">
        <input
          value={query}
          onChange={ev => setQuery(ev.target.value)}
          placeholder="筛选消息内容…"
          className="h-6 w-full rounded border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-meta text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none"
          aria-label="按文字筛选用户消息"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {/* 列标题:与下方行用同一套固定列宽,竖线上下对齐,形成表格感。 */}
        <div className="sticky top-0 z-[1] flex items-stretch border-b border-[var(--border-muted)] bg-[var(--bg-surface)] text-meta text-[var(--text-muted)]">
          <div className="w-8 flex-shrink-0 border-r border-[var(--border-muted)] px-1 py-1 text-right">#</div>
          <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 py-1 text-center">时间</div>
          <div className="min-w-0 flex-1 px-1.5 py-1">内容</div>
        </div>
        {building && (
          <div className="p-3 text-helper text-[var(--text-muted)]">位置索引构建中…</div>
        )}
        {!building && entries.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">此会话没有用户消息记录</div>
        )}
        {!building && entries.length > 0 && visible.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">没有匹配当前筛选的消息</div>
        )}
        {visible.map(e => {
          const isActive = e.key === activeKey
          return (
            <div
              key={e.key}
              className={`border-b border-[var(--border-muted)] ${isActive ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)]' : ''}`}
              style={{ contentVisibility: 'auto', containIntrinsicSize: 'auto 40px', borderLeft: '2px solid var(--accent-blue)' } as React.CSSProperties}
            >
              <div
                className="flex cursor-pointer items-stretch hover:bg-[var(--bg-surface-hover)]"
                onClick={() => jump(e)}
                title={`跳转到终端第 ${e.line} 行`}
              >
                <div className="w-8 flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-right text-meta tabular-nums text-[var(--text-muted)]">
                  {e.seq}
                </div>
                <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-center text-meta tabular-nums text-[var(--text-muted)]">
                  {e.tsMs !== null ? fmtTime(e.tsMs) : ''}
                </div>
                <div className="flex min-w-0 flex-1 items-start px-1.5 py-1.5">
                  <span className="min-w-0 flex-1 font-mono text-helper text-[var(--text-secondary)]" style={summaryClampStyle} title={e.label}>
                    {e.label}
                  </span>
                </div>
              </div>
            </div>
          )
        })}
      </div>
    </aside>
  )
}
