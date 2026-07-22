import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { MiniMapPosition, PositionsResponse } from '../types'
import AgentIcon from './AgentIcon'
import UserAvatar from './UserAvatar'
import { CloseIcon, NarrowIcon, PinIcon, WidenIcon } from './icons'
import { formatNumber, useI18n } from '../i18n'

// 交互消息面板:按时间序列出会话的用户消息(kind === 'user')和助手回复
// (kind === 'assistant',每个连续文本块一条),两类消息各有勾选项(默认全选,
// 可单独打开),支持文字过滤、点击跳转到终端对应行。用户消息用头像图标
// (可在设置中自定义),助手回复用对应 agent 图标。数据全部来自 positions
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
  agentType?: string // 助手行图标用;会话未加载时退化为通用 agent 图标
  pinned?: boolean
  onPinnedChange?: (pinned: boolean) => void
  onWidthChange?: (width: number) => void
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

const KIND_META: Record<InteractionKind, { rowCls: string; borderColor: string }> = {
  user: {
    rowCls: 'bg-[color-mix(in_srgb,var(--accent-blue)_5%,transparent)]',
    borderColor: 'var(--accent-blue)',
  },
  assistant: {
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

export default function UserMessagePanel({ positions, building, agentType, pinned = false, onPinnedChange, onWidthChange, onJump, onClose }: Props) {
  const { locale, t } = useI18n()
  const panelRef = useRef<HTMLElement>(null)
  const [activeKey, setActiveKey] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  // 宽/标准两档宽度,记忆到本地;浮层不占终端布局,加宽零成本。
  const [wide, setWide] = useState(() => localStorage.getItem(WIDE_STORAGE_KEY) === '1')
  const toggleWide = () => setWide(w => {
    localStorage.setItem(WIDE_STORAGE_KEY, w ? '0' : '1')
    return !w
  })

  // Report the rendered width rather than the nominal 420/640px so the
  // terminal search bar also handles the panel's responsive max-width.
  useLayoutEffect(() => {
    const panel = panelRef.current
    if (!panel || !onWidthChange) return
    const reportWidth = () => onWidthChange(panel.getBoundingClientRect().width)
    reportWidth()
    const observer = new ResizeObserver(reportWidth)
    observer.observe(panel)
    return () => observer.disconnect()
  }, [onWidthChange])
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
    <aside ref={panelRef} data-testid="navigation-panel" className={`absolute inset-y-0 right-0 z-10 flex max-w-[calc(100%-24px)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-[-8px_0_24px_rgba(0,0,0,0.35)] ${wide ? 'w-[640px]' : 'w-[420px]'}`}>
      <div className="flex h-9 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-3">
        <span className="text-nav font-medium text-[var(--text-primary)]">{t('replay.messages')}</span>
        <span className="text-meta text-[var(--text-muted)]">
          {filtering ? `${formatNumber(locale, visible.length)}/${formatNumber(locale, kindFiltered.length)}` : formatNumber(locale, kindFiltered.length)}
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
          title={pinned ? t('panel.unpinHelp') : t('panel.pinHelp')}
          aria-pressed={pinned}
          aria-label={pinned ? t('panel.unpinHelp') : t('panel.pinHelp')}
        >
          <PinIcon filled={pinned} />
          {pinned ? t('panel.pinned') : t('panel.pin')}
        </button>
        <button
          onClick={toggleWide}
          className="inline-flex h-6 items-center gap-1 rounded px-1.5 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title={wide ? t('panel.restoreWidth') : t('panel.widen')}
        >
          {wide ? <NarrowIcon /> : <WidenIcon />}
          {wide ? t('panel.standard') : t('panel.widen')}
        </button>
        <button
          onClick={onClose}
          className="inline-flex h-6 w-6 items-center justify-center rounded text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          title={t('common.close')}
          aria-label={t('panel.closeMessages')}
        >
          <CloseIcon />
        </button>
      </div>

      <div className="flex-shrink-0 border-b border-[var(--border-muted)] p-2">
        <input
          value={query}
          onChange={ev => setQuery(ev.target.value)}
          placeholder={t('panel.filterMessages')}
          className="h-6 w-full rounded border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-meta text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none"
          aria-label={t('panel.filterMessagesLabel')}
        />
        <div className="mt-1.5 flex items-center gap-3">
          {([['user', t('panel.userMessage')], ['assistant', t('panel.assistantReply')]] as const).map(([kind, label]) => (
            <label
              key={kind}
              className="flex cursor-pointer items-center gap-1.5 text-meta text-[var(--text-secondary)]"
            >
              <input
                type="checkbox"
                checked={kinds.includes(kind)}
                onChange={() => toggleKind(kind)}
                className="h-3.5 w-3.5 cursor-pointer accent-[var(--accent-blue)]"
                aria-label={t('panel.showKind', { kind: label })}
              />
              {label}
            </label>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {/* 列标题:与下方行用同一套固定列宽,竖线上下对齐,形成表格感。 */}
        <div className="sticky top-0 z-[1] flex items-stretch border-b border-[var(--border-muted)] bg-[var(--bg-surface)] text-meta text-[var(--text-muted)]">
          <div className="w-8 flex-shrink-0 border-r border-[var(--border-muted)] px-1 py-1 text-right">{t('panel.number')}</div>
          <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 py-1 text-center">{t('panel.time')}</div>
          <div className="min-w-0 flex-1 px-1.5 py-1">{t('panel.content')}</div>
        </div>
        {building && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('panel.indexing')}</div>
        )}
        {!building && entries.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('panel.noMessages')}</div>
        )}
        {!building && entries.length > 0 && kindFiltered.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('panel.chooseMessageKinds')}</div>
        )}
        {!building && kindFiltered.length > 0 && visible.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('panel.noMatchingMessages')}</div>
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
                title={t('panel.jumpLine', { line: e.line })}
              >
                <div className="w-8 flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-right text-meta tabular-nums text-[var(--text-muted)]">
                  {e.seq}
                </div>
                <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-center text-meta tabular-nums text-[var(--text-muted)]">
                  {e.tsMs !== null ? fmtTime(e.tsMs) : ''}
                </div>
                <div className="flex min-w-0 flex-1 items-start gap-1.5 px-1.5 py-1.5">
                  <span className="mt-px flex-shrink-0" title={e.kind === 'user' ? t('panel.userMessage') : t('panel.assistantReply')}>
                    {e.kind === 'user'
                      ? <UserAvatar size={14} />
                      : <AgentIcon agentType={agentType} size={14} />}
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
