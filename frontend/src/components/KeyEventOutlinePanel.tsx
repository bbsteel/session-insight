import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { PositionsResponse, OutlineCategory } from '../types'
import type { OutlineItem } from '../semanticOutline'
import {
  OUTLINE_CATEGORIES,
  countByCategory,
  currentHiddenReason,
  filterOutlineItems,
  loadOutlineCategories,
  outlineItemsFromPositions,
  persistOutlineCategories,
} from '../semanticOutline'
import {
  AlertTriangleIcon,
  CheckCircleIcon,
  CloseIcon,
  CrosshairIcon,
  FileEditIcon,
  LayersIcon,
  NarrowIcon,
  PinIcon,
  WidenIcon,
} from './icons'
import { formatNumber, useI18n } from '../i18n'

// 关键事件大纲面板(v0.6.1):由后端共享分类器产生的稀疏 outline positions
// 驱动,默认只包含异常、上下文边界、文件修改和关键验证结果四类。面板负责
// 类别筛选(独立 localStorage 持久化)、文本过滤、当前位置条;分类规则不在
// 前端重建。"当前位置"(视窗语义)与"选中行"(用户点击)是两个独立状态。

interface Props {
  positions: PositionsResponse | null
  building: boolean
  // 终端视窗中心对应的当前事件 key(由 ReplayView 根据 xterm viewport 计算);
  // null 表示没有可用当前位置。
  currentKey: string | null
  pinned?: boolean
  onPinnedChange?: (pinned: boolean) => void
  onWidthChange?: (width: number) => void
  onJump: (lineStart: number, logicalStart?: number) => void
  onClose: () => void
}

const WIDE_STORAGE_KEY = 'si-outline-panel-wide'

const CATEGORY_META: Record<OutlineCategory, { icon: (cls?: string) => React.ReactNode; color: string }> = {
  anomaly: { icon: cls => <AlertTriangleIcon className={cls} />, color: 'var(--accent-red, #f7768e)' },
  context: { icon: cls => <LayersIcon className={cls} />, color: 'var(--accent-yellow, #e0af68)' },
  file_change: { icon: cls => <FileEditIcon className={cls} />, color: 'var(--accent-blue)' },
  key_result: { icon: cls => <CheckCircleIcon className={cls} />, color: 'var(--accent-green)' },
}

function fmtTime(tsMs: number): string {
  const d = new Date(tsMs)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export default function KeyEventOutlinePanel({
  positions,
  building,
  currentKey,
  pinned = false,
  onPinnedChange,
  onWidthChange,
  onJump,
  onClose,
}: Props) {
  const { locale, t } = useI18n()
  const panelRef = useRef<HTMLElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [wide, setWide] = useState(() => localStorage.getItem(WIDE_STORAGE_KEY) === '1')
  const toggleWide = () => setWide(w => {
    localStorage.setItem(WIDE_STORAGE_KEY, w ? '0' : '1')
    return !w
  })

  useLayoutEffect(() => {
    const panel = panelRef.current
    if (!panel || !onWidthChange) return
    const reportWidth = () => onWidthChange(panel.getBoundingClientRect().width)
    reportWidth()
    const observer = new ResizeObserver(reportWidth)
    observer.observe(panel)
    return () => observer.disconnect()
  }, [onWidthChange])

  const [categories, setCategories] = useState<OutlineCategory[]>(() => loadOutlineCategories(localStorage))
  const toggleCategory = (cat: OutlineCategory) => setCategories(prev => {
    const next = prev.includes(cat) ? prev.filter(c => c !== cat) : [...prev, cat]
    persistOutlineCategories(localStorage, next)
    return next
  })

  // Floating overlay: close on outside click unless pinned.
  useEffect(() => {
    if (pinned) return
    let remove: (() => void) | undefined
    const timer = window.setTimeout(() => {
      const handlePointerDown = (event: PointerEvent) => {
        const target = event.target
        if (!(target instanceof Element)) return
        if (panelRef.current?.contains(target)) return
        onClose()
      }
      document.addEventListener('pointerdown', handlePointerDown)
      remove = () => document.removeEventListener('pointerdown', handlePointerDown)
    }, 0)
    return () => {
      window.clearTimeout(timer)
      remove?.()
    }
  }, [onClose, pinned])

  const titleFor = (item: OutlineItem) => t(`outline.code.${item.code}`)
  const items = useMemo(() => outlineItemsFromPositions(positions?.positions), [positions])
  const visible = useMemo(
    () => filterOutlineItems(items, categories, query, titleFor),
    // titleFor is stable per locale; t identity changes with locale.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [items, categories, query, locale],
  )
  const counts = useMemo(() => countByCategory(items, visible), [items, visible])

  const hiddenReason = currentHiddenReason(currentKey, items, categories, query, titleFor)
  const currentItem = currentKey ? items.find(i => i.key === currentKey) ?? null : null

  // "在大纲中定位":只滚动列表,不改变终端位置。被类别隐藏时一键开启类别,
  // 被文本筛选隐藏时一键清除筛选。
  const locateCurrent = () => {
    if (!currentKey) return
    if (currentItem && !categories.includes(currentItem.category)) {
      setCategories(prev => {
        const next = [...prev, currentItem.category]
        persistOutlineCategories(localStorage, next)
        return next
      })
    }
    if (query !== '') setQuery('')
    requestAnimationFrame(() => {
      listRef.current
        ?.querySelector(`[data-outline-key="${CSS.escape(currentKey)}"]`)
        ?.scrollIntoView({ block: 'nearest' })
    })
  }

  const jump = (item: OutlineItem) => {
    setSelectedKey(item.key)
    onJump(item.line, item.logical ?? undefined)
  }

  return (
    <aside ref={panelRef} data-testid="navigation-panel" className={`absolute inset-y-0 right-0 z-10 flex max-w-[calc(100%-24px)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-[-8px_0_24px_rgba(0,0,0,0.35)] ${wide ? 'w-[640px]' : 'w-[420px]'}`}>
      <div className="flex h-9 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-3">
        <span className="text-nav font-medium text-[var(--text-primary)]">{t('replay.outline')}</span>
        <span className="text-meta text-[var(--text-muted)]">
          {visible.length === items.length
            ? formatNumber(locale, items.length)
            : `${formatNumber(locale, visible.length)}/${formatNumber(locale, items.length)}`}
        </span>
        <span className="flex-1" />
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
          aria-label={t('outline.close')}
        >
          <CloseIcon />
        </button>
      </div>

      <div className="flex-shrink-0 border-b border-[var(--border-muted)] p-2">
        <input
          value={query}
          onChange={ev => setQuery(ev.target.value)}
          placeholder={t('outline.filter')}
          className="h-6 w-full rounded border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-meta text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none"
          aria-label={t('outline.filterLabel')}
        />
        <div className="mt-1.5 flex flex-wrap items-center gap-3">
          {OUTLINE_CATEGORIES.map(cat => (
            <label
              key={cat}
              className="flex cursor-pointer items-center gap-1.5 text-meta text-[var(--text-secondary)]"
            >
              <input
                type="checkbox"
                checked={categories.includes(cat)}
                onChange={() => toggleCategory(cat)}
                className="h-3.5 w-3.5 cursor-pointer accent-[var(--accent-blue)]"
                aria-label={t('outline.showKind', { kind: t(`outline.category.${cat}`) })}
              />
              <span style={{ color: CATEGORY_META[cat].color }}>{CATEGORY_META[cat].icon('h-3 w-3 shrink-0')}</span>
              {t(`outline.category.${cat}`)}
              <span className="text-[var(--text-muted)]">
                {counts[cat].visible === counts[cat].total
                  ? formatNumber(locale, counts[cat].total)
                  : `${formatNumber(locale, counts[cat].visible)}/${formatNumber(locale, counts[cat].total)}`}
              </span>
            </label>
          ))}
        </div>
      </div>

      {/* 当前位置条:由 xterm 视窗驱动,与用户选中行独立。 */}
      {currentItem && (
        <div className="flex flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-1.5">
          <span className="min-w-0 flex-1 truncate text-meta text-[var(--text-secondary)]" title={currentItem.summary}>
            {t('outline.current', { title: titleFor(currentItem) })}
            {hiddenReason === 'hidden_by_category' && (
              <span className="ml-1 text-[var(--warning)]">{t('outline.hiddenByCategory')}</span>
            )}
            {hiddenReason === 'hidden_by_search' && (
              <span className="ml-1 text-[var(--warning)]">{t('outline.hiddenBySearch')}</span>
            )}
          </span>
          {hiddenReason === 'hidden_by_category' && (
            <button
              onClick={() => toggleCategory(currentItem.category)}
              className="flex-shrink-0 rounded border border-[var(--border-default)] px-1.5 py-0.5 text-meta text-[var(--accent-blue)] hover:bg-[var(--bg-surface-hover)]"
            >
              {t('outline.showCategory', { category: t(`outline.category.${currentItem.category}`) })}
            </button>
          )}
          {hiddenReason === 'hidden_by_search' && (
            <button
              onClick={() => setQuery('')}
              className="flex-shrink-0 rounded border border-[var(--border-default)] px-1.5 py-0.5 text-meta text-[var(--accent-blue)] hover:bg-[var(--bg-surface-hover)]"
            >
              {t('outline.clearSearch')}
            </button>
          )}
          <button
            onClick={locateCurrent}
            className="inline-flex h-5 w-5 flex-shrink-0 items-center justify-center rounded text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            title={t('outline.locateCurrent')}
            aria-label={t('outline.locateCurrent')}
          >
            <CrosshairIcon />
          </button>
        </div>
      )}

      <div ref={listRef} className="min-h-0 flex-1 overflow-y-auto">
        {building && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('panel.indexing')}</div>
        )}
        {!building && items.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('outline.noEvents')}</div>
        )}
        {!building && items.length > 0 && categories.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('outline.allCategoriesOff')}</div>
        )}
        {!building && items.length > 0 && categories.length > 0 && visible.length === 0 && (
          <div className="p-3 text-helper text-[var(--text-muted)]">{t('outline.noMatching')}</div>
        )}
        {visible.map(item => {
          const isCurrent = item.key === currentKey
          const isSelected = item.key === selectedKey
          const meta = CATEGORY_META[item.category]
          return (
            <div
              key={item.key}
              data-outline-key={item.key}
              className={`border-b border-[var(--border-muted)] ${
                isSelected ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)]' : ''
              }`}
              style={{ contentVisibility: 'auto', containIntrinsicSize: 'auto 36px', borderLeft: `2px solid ${meta.color}` } as React.CSSProperties}
            >
              <div
                className="flex cursor-pointer items-stretch hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--accent-blue)]"
                onClick={() => jump(item)}
                role="button"
                tabIndex={0}
                onKeyDown={ev => {
                  if (ev.key === 'Enter' || ev.key === ' ') {
                    ev.preventDefault()
                    jump(item)
                  }
                }}
                title={t('panel.jumpLineTurn', { line: item.line, turn: item.turnIndex + 1 })}
              >
                <div className="w-[58px] flex-shrink-0 border-r border-[var(--border-muted)] px-1 pt-1.5 text-center text-meta tabular-nums text-[var(--text-muted)]">
                  {item.tsMs !== null ? fmtTime(item.tsMs) : ''}
                </div>
                <div className="flex min-w-0 flex-1 items-start gap-1.5 px-1.5 py-1.5">
                  <span className="mt-px flex-shrink-0" style={{ color: meta.color }} title={t(`outline.category.${item.category}`)}>
                    {meta.icon('h-3.5 w-3.5 shrink-0')}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1.5">
                      <span className={`truncate text-helper ${isCurrent ? 'font-semibold text-[var(--accent-blue)]' : 'text-[var(--text-primary)]'}`}>
                        {titleFor(item)}
                      </span>
                      {item.precision === 'estimated' && (
                        <span className="flex-shrink-0 rounded bg-[var(--warning)]/15 px-1 text-[10px] leading-4 text-[var(--warning)]">
                          {t('outline.estimated')}
                        </span>
                      )}
                      {isCurrent && (
                        <span className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-[var(--accent-blue)]" aria-hidden />
                      )}
                    </span>
                    {item.summary && item.summary !== titleFor(item) && (
                      <span className="block truncate font-mono text-meta text-[var(--text-muted)]" title={item.summary}>
                        {item.summary}
                      </span>
                    )}
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
