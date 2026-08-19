import type { ButtonHTMLAttributes, ReactNode } from 'react'

export interface NavigationFilterPillProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  active: boolean
  accentColor?: string
  children: ReactNode
}

/**
 * Shared multi-select control for navigation-panel filters. The same 24px
 * capsule geometry is used for selection actions and individual filters; an
 * accent color may be supplied for tool/category-specific meaning.
 */
export function NavigationFilterPill({
  active,
  accentColor,
  children,
  className = '',
  style,
  ...rest
}: NavigationFilterPillProps) {
  const accentStyle = accentColor
    ? {
        color: accentColor,
        ...(active
          ? {
              borderColor: accentColor,
              backgroundColor: `color-mix(in srgb, ${accentColor} 14%, transparent)`,
            }
          : {}),
      }
    : undefined

  return (
    <button
      type="button"
      data-testid="navigation-filter-pill"
      {...rest}
      aria-pressed={active}
      className={`inline-flex h-6 shrink-0 items-center justify-center gap-1 rounded-full border px-2 text-helper leading-none transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
        active && !accentColor
          ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
          : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
      } ${className}`}
      style={{ ...accentStyle, ...style }}
    >
      {children}
    </button>
  )
}

interface NavigationFilterSelectionActionsProps {
  allSelected: boolean
  noneSelected: boolean
  allLabel: string
  noneLabel: string
  onSelectAll: () => void
  onSelectNone: () => void
}

export function NavigationFilterSelectionActions({
  allSelected,
  noneSelected,
  allLabel,
  noneLabel,
  onSelectAll,
  onSelectNone,
}: NavigationFilterSelectionActionsProps) {
  return (
    <>
      <NavigationFilterPill
        active={allSelected}
        onClick={onSelectAll}
        data-testid="navigation-filter-select-all"
        aria-label={allLabel}
      >
        {allLabel}
      </NavigationFilterPill>
      <NavigationFilterPill
        active={noneSelected}
        onClick={onSelectNone}
        data-testid="navigation-filter-select-none"
        aria-label={noneLabel}
      >
        {noneLabel}
      </NavigationFilterPill>
      <span className="mx-0.5 h-4 w-px shrink-0 bg-[var(--border-muted)]" aria-hidden="true" />
    </>
  )
}
