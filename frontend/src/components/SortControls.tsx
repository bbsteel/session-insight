import { useI18n } from '../i18n'
import { defaultSortOrder, type SortOrder, type SortPref } from '../sortPreference'

interface SortControlsProps<K extends string> {
  /** Sort keys rendered as the segmented control, in display order. */
  keys: readonly K[]
  /** The key that sorts ascending by default (typically 'name'). */
  nameKey: K
  currentSortPref: SortPref<K>
  onSortPrefChange: (pref: SortPref<K>) => void
  /** Accessible label for the whole control group, e.g. t('filter.sort.label'). */
  groupLabel: string
}

/**
 * Segmented sort-key picker plus ascending/descending toggle, shared by the
 * sidebar filter panels (projects, models). Copy comes from the generic
 * `filter.sort.*` i18n keys.
 */
export default function SortControls<K extends string>({
  keys,
  nameKey,
  currentSortPref,
  onSortPrefChange,
  groupLabel,
}: SortControlsProps<K>) {
  const { t } = useI18n()

  const selectSortKey = (key: K) => {
    if (key === currentSortPref.key) return
    onSortPrefChange({ key, order: defaultSortOrder(key, nameKey) })
  }

  const toggleOrder = () => {
    const order: SortOrder = currentSortPref.order === 'asc' ? 'desc' : 'asc'
    onSortPrefChange({ ...currentSortPref, order })
  }

  const orderLabel = currentSortPref.order === 'asc' ? t('filter.sort.asc') : t('filter.sort.desc')
  const orderAria = t('filter.sort.toggleOrder', { order: orderLabel })

  return (
    <div
      className="px-1.5 py-1.5 border-b border-[var(--border-default)] flex items-center gap-1"
      role="group"
      aria-label={groupLabel}
    >
      <div className="flex flex-1 min-w-0 rounded border border-[var(--border-default)] overflow-hidden">
        {keys.map(key => {
          const active = currentSortPref.key === key
          return (
            <button
              key={key}
              type="button"
              onClick={() => selectSortKey(key)}
              aria-pressed={active}
              title={t(`filter.sort.${key}`)}
              className={`flex-1 min-w-0 h-6 px-1 text-[10px] leading-none truncate transition-colors duration-fast ${
                active
                  ? 'bg-[var(--bg-surface-selected)] text-[var(--text-primary)] font-medium'
                  : 'bg-[var(--bg-inset)] text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
              }`}
            >
              {t(`filter.sort.${key}`)}
            </button>
          )
        })}
      </div>
      <button
        type="button"
        onClick={toggleOrder}
        aria-label={orderAria}
        title={orderAria}
        className="flex-shrink-0 h-6 w-6 inline-flex items-center justify-center rounded border border-[var(--border-default)] bg-[var(--bg-inset)] text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] transition-colors duration-fast"
      >
        {currentSortPref.order === 'asc' ? (
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <line x1="12" y1="19" x2="12" y2="5" />
            <polyline points="5 12 12 5 19 12" />
          </svg>
        ) : (
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <line x1="12" y1="5" x2="12" y2="19" />
            <polyline points="19 12 12 19 5 12" />
          </svg>
        )}
      </button>
    </div>
  )
}
