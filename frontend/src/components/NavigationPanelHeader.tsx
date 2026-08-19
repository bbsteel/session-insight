import type { ReactNode } from 'react'
import { CloseIcon, CollapseAllIcon, ExpandAllIcon, NarrowIcon, PinIcon, WidenIcon } from './icons'

interface ExpandAllAction {
  expanded: boolean
  onToggle: () => void
  expandLabel: string
  collapseLabel: string
}

interface NavigationPanelHeaderProps {
  title: string
  count: ReactNode
  pinned: boolean
  wide: boolean
  onPinnedChange?: (pinned: boolean) => void
  onToggleWide: () => void
  onClose: () => void
  pinLabel: string
  pinnedLabel: string
  pinTitle: string
  unpinTitle: string
  widenLabel: string
  standardLabel: string
  widenTitle: string
  restoreWidthTitle: string
  closeLabel: string
  expandAll?: ExpandAllAction
}

const headerButtonClass = 'inline-flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]'

/** Shared title/action row for the three replay navigation panels. */
export default function NavigationPanelHeader({
  title,
  count,
  pinned,
  wide,
  onPinnedChange,
  onToggleWide,
  onClose,
  pinLabel,
  pinnedLabel,
  pinTitle,
  unpinTitle,
  widenLabel,
  standardLabel,
  widenTitle,
  restoreWidthTitle,
  closeLabel,
  expandAll,
}: NavigationPanelHeaderProps) {
  return (
    <div className="flex h-9 min-w-0 flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-3">
      <span className="min-w-0 flex-1 truncate text-nav font-medium text-[var(--text-primary)]">{title}</span>
      <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">{count}</span>
      <div className="flex flex-shrink-0 items-center gap-1">
        <button
          type="button"
          onClick={() => onPinnedChange?.(!pinned)}
          className={`${headerButtonClass} ${
            pinned
              ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
              : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
          }`}
          title={pinned ? unpinTitle : pinTitle}
          aria-pressed={pinned}
          aria-label={pinned ? unpinTitle : pinTitle}
        >
          <PinIcon filled={pinned} />
          {pinned ? pinnedLabel : pinLabel}
        </button>
        <button
          type="button"
          onClick={onToggleWide}
          className={`${headerButtonClass} text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]`}
          title={wide ? restoreWidthTitle : widenTitle}
          aria-label={wide ? restoreWidthTitle : widenTitle}
        >
          {wide ? <NarrowIcon /> : <WidenIcon />}
          {wide ? standardLabel : widenLabel}
        </button>
        {expandAll && (
          <button
            type="button"
            onClick={expandAll.onToggle}
            className={`${headerButtonClass} text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]`}
            title={expandAll.expanded ? expandAll.collapseLabel : expandAll.expandLabel}
            aria-label={expandAll.expanded ? expandAll.collapseLabel : expandAll.expandLabel}
            aria-pressed={expandAll.expanded}
          >
            {expandAll.expanded ? <CollapseAllIcon /> : <ExpandAllIcon />}
            {expandAll.expanded ? expandAll.collapseLabel : expandAll.expandLabel}
          </button>
        )}
        <button
          type="button"
          onClick={onClose}
          className={`${headerButtonClass} h-6 w-6 justify-center px-0 text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]`}
          title={closeLabel}
          aria-label={closeLabel}
        >
          <CloseIcon />
        </button>
      </div>
    </div>
  )
}
