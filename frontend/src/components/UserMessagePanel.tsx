import { useEffect, useMemo, useRef, useState } from 'react'
import type { MiniMapPosition, PositionsResponse } from '../types'
import { CloseIcon, NarrowIcon, PinIcon, WidenIcon } from './icons'

// 交互消息面板:按时间序列出会话的用户消息(kind === 'user')和助手回复
// (kind === 'assistant',每个连续文本块一条),两类消息各有勾选项(默认全选,
// 可单独打开),支持文字过滤、点击跳转到终端对应行。数据全部来自 positions
// API,不需要额外请求。助手回复文本由后端截断(400 字符),展示层再做 4 行
// 折叠。以浮层形式覆盖在终端右侧,不占布局宽度。

type InteractionKind = 'user' | 'assistant'

interface InteractionEntry {
  key: string
  kind: InteractionKind
  seq: number // 全会话时序编号(1 起,两类消息统一编号),筛选后仍保持原编号
  line: number
  logical: number | null // 逻辑行(跳转首选,折叠 badge 不影响它)
  label: string
  tsMs: number | null
}

interface Props {
  positions: PositionsResponse | null
  building: boolean
  pinned?: boolean
  onPinnedChange?: (pinned: boolean) => void
  onJump: (lineStart: number, logicalStart?: number) => void
  onClose: () => void
}

function toEntry(p: MiniMapPosition, kind: InteractionKind, seq: number): InteractionEntry {
  const pl = p.payload ?? {}
  return {
    key: p.position_key,
    kind,
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

// 消息内容最多显示 4 行,避免面板行高过高(助手回复已在后端截断)。
const summaryClampStyle: React.CSSProperties = {
  display: '-webkit-box',
  WebkitLineClamp: 4,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  wordBreak: 'break-all',
}

const WIDE_STORAGE_KEY = 'si-userpanel-wide'
const KINDS_STORAGE_KEY = 'si-interaction-kinds'
const ALL_KINDS: InteractionKind[] = ['user', 'assistant']

const KIND_META: Record<InteractionKind, { label: string; chipCls: string; rowCls: string; borderColor: string }> = {
  user: {
    label: '用户',
    chipCls: 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]',
    rowCls: 'bg-[color-mix(in_srgb,var(--accent-blue)_5%,transparent)]',
    borderColor: 'var(--accent-blue)',
  },
  assistant: {
    label: '助手',
    chipCls: 'bg-[var(--accent-green)]/10 text-[var(--accent-green)]',
    rowCls: 'bg-[color-mix(in_srgb,var(--accent-green)_6%,transparent)]',
    borderColor: 'var(--accent-green)',
  },
}

function loadKinds(): InteractionKind[] {
  try {
    const raw = localStorage.getItem(KINDS_STORAGE_KEY)
    if (raw === null) return ALL_KINDS
    const parsed: unknown = JSON.parse(raw)
    if (Array.isArray(parsed)) {
      return parsed.filter((k): k is InteractionKind => k === 'user' || k === 'assistant')
    }
  } catch { /* 损坏的本地存储按默认处理 */ }
  return ALL_KINDS
}

export default function UserMessagePanel({ positions, building, pinned = false, onPinnedChange, onJump, onClose }: Props) {
  const panelRef = useRef<HTMLElement>(null)
  const [activeKey, setActiveKey] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  // 宽/标准两档宽度,记忆到本地;浮层不占终端布局,加宽零成本。
  const [wide, setWide] = useState(() => localStorage.getItem(WIDE_STORAGE_KEY) === '1')
  const toggleWide = () => setWide(w => {
    localStorage.setItem(WIDE_STORAGE_KEY, w ? '0' : '1')
    return !w
  })
  // 用户消息 / 助手回复两个勾选项,默认全选,可单独打开,记忆到本地。
  const [kinds, setKinds] = useState<InteractionKind[]>(loadKinds)
  const toggleKind = (kind: InteractionKind) => setKinds(prev => {
    const next = prev.includes(kind) ? prev.filter(k => k !== kind) : [...prev, kind]
    localStorage.setItem(KINDS_STORAGE_KEY, JSON.stringify(next))
    return next
  })

  // 面板是覆盖在终端上的浮层;未钉住时点击外部关闭,钉住后保持打开。
  useEffect(() => {
    if (pinned) return
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && !panelRef.current?.contains(target)) onClose()
    }
    document.addEventListener('pointerdown', handlePointerDown)
    return () => document.removeEventListener('pointerdown', handlePointerDown)
  }, [onClose, pinned])

  const entries = useMemo(
    () => (positions?.positions ?? [])
      .filter((p): p is MiniMapPosition & { kind: InteractionKind } => p.kind === 'user' || p.kind === 'assistant')
      .map((p, i) => toEntry(p, p.kind, i + 1)),
    [positions],
  )

  const kindFiltered = entries.filter(e => kinds.includes(e.kind))
  const q = query.trim().toLowerCase()
  const visible = kindFiltered.filter(e =>
    q === '' || e.label.toLowerCase().includes(q)
  )
  const filtering = q !== ''

  const jump = (e: InteractionEntry) => {
    setActiveKey(e.key)
    onJump(e.line, e.logical ?? undefined)
  }

  return (
    <aside ref={panelRef} className={`absolute inset-y-0 right-0 z-10 flex max-w-[calc(100%-24px)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-[-8px_0_24px_rgba(0,0,0,0.35)] ${wide ? 'w-[640px]' : 'w-[420px]'}`}>
      <div className="flex h-9 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-3">
        <span className="text-nav font-medium text-[var(--text-primary)]">交互消息</span>
        <span className="text-meta text-[var(--text-muted)]">
          {filtering ? `${visible.length}/${kindFiltered.length}` : kindFiltered.length}
        </span>
        <span className="flex-1" />
        {/* Header actions: text-nav + currentColor SVG (no emoji/symbol fallbacks). */}
        <button
          onClick={() => onPinnedChange?.(!pinned)}
          className={`inline-flex h-6 items-center gap-1 rounded px-1.5 text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
            pinned
              ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
              : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
          }`}
          title={pinned ? '取消钉住（点击外部可关闭）' : '钉住面板（点击外部不关闭）'}
          aria-pressed={pinned}
          aria-label={pinned ? '取消钉住导航面板' : '钉住导航面板'}
        >
          <PinIcon filled={pinned} />
          {pinned ? '已钉住' : '钉住'}
        </button>
        <button
          onClick={toggleWide}
          className="inline-flex h-6 items-center gap-1 rounded px-1.5 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title={wide ? '恢复标准宽度' : '加宽面板'}
        >
          {wide ? <NarrowIcon /> : <WidenIcon />}
          {wide ? '标准' : '加宽'}
        </button>
        <button
          onClick={onClose}
          className="inline-flex h-6 w-6 items-center justify-center rounded text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title="关闭"
          aria-label="关闭交互消息面板"
        >
          <CloseIcon />
        </button>
      </div>

      <div className="flex-shrink-0 border-b border-[var(--border-muted)] p-2">
        <input
          value={query}
          onChange={ev => setQuery(ev.target.value)}
          placeholder="筛选消息内容…"
          className="h-6 w-full rounded border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-meta text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none"
          aria-label="按文字筛选交互消息"
        />
        <div className="mt-1.5 flex items-center gap-3">
          {([['user', '用户消息'], ['assistant', '助手回复']] as const).map(([kind, label]) => (
            <label
              key={kind}
              className="flex cursor-pointer items-center gap-1.5 text-meta text-[var(--text-secondary)]"
            >
              <input
                type="checkbox"
                checked={kinds.includes(kind)}
                onChange={() => toggleKind(kind)}
                className="h-3.5 w-3.5 cursor-pointer accent-[var(--accent-blue)]"
                aria-label={`显示${label}`}
              />
              {label}
            </label>
          ))}
        </div>
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
          <div className="p-3 text-helper text-[var(--text-muted)]">此会话没有交互消息记录</div>
        )}
        {!building && entries.length > 0 && kindFiltered.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">请在上方勾选要显示的消息类型</div>
        )}
        {!building && kindFiltered.length > 0 && visible.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">没有匹配当前筛选的消息</div>
        )}
        {visible.map(e => {
          const isActive = e.key === activeKey
          const meta = KIND_META[e.kind]
          return (
            <div
              key={e.key}
              className={`border-b border-[var(--border-muted)] ${isActive ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)]' : meta.rowCls}`}
              style={{ contentVisibility: 'auto', containIntrinsicSize: 'auto 40px', borderLeft: `2px solid ${meta.borderColor}` } as React.CSSProperties}
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
                <div className="flex min-w-0 flex-1 items-start gap-1.5 px-1.5 py-1.5">
                  <span className={`mt-px flex-shrink-0 rounded px-1 text-meta leading-4 ${meta.chipCls}`}>
                    {meta.label}
                  </span>
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
