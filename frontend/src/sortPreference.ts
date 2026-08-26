// Generic sort plumbing shared by the sidebar filter panels (projects, models):
// validated localStorage persistence for the sort preference, per-key default
// direction, and comparison helpers with a stable name tie-break.

export type SortOrder = 'asc' | 'desc'

export interface SortPref<K extends string> {
  key: K
  order: SortOrder
}

export function isSortOrder(value: unknown): value is SortOrder {
  return value === 'asc' || value === 'desc'
}

/** Most domains want name A→Z but counts/recency most-first. */
export function defaultSortOrder(key: string, nameKey: string): SortOrder {
  return key === nameKey ? 'asc' : 'desc'
}

export function readSortPref<K extends string>(
  storageKey: string,
  isKey: (value: unknown) => value is K,
  fallback: SortPref<K>,
): SortPref<K> {
  try {
    const raw = localStorage.getItem(storageKey)
    if (!raw) return { ...fallback }
    const parsed = JSON.parse(raw) as { key?: unknown; order?: unknown; dir?: unknown }
    // Prefer `order`; accept legacy `dir` from the first ship of this feature.
    const order = isSortOrder(parsed.order)
      ? parsed.order
      : isSortOrder(parsed.dir)
        ? parsed.dir
        : null
    if (isKey(parsed.key) && order) {
      return { key: parsed.key, order }
    }
  } catch {
    // ignore corrupt or unavailable storage
  }
  return { ...fallback }
}

export function writeSortPref<K extends string>(storageKey: string, pref: SortPref<K>): void {
  try {
    localStorage.setItem(storageKey, JSON.stringify(pref))
  } catch {
    // ignore
  }
}

/** Milliseconds for an ISO timestamp; empty/invalid counts as oldest (0). */
export function activityMs(iso: string): number {
  if (!iso) return 0
  const parsed = Date.parse(iso)
  return Number.isFinite(parsed) ? parsed : 0
}

/** Case-insensitive display-name comparison used as the stable tie-break. */
export function compareNames(a: string, b: string): number {
  return a.localeCompare(b, undefined, { sensitivity: 'base' })
}

/** Apply the sort direction to a primary comparison, falling back to a tie-break. */
export function compareOrdered(primaryCmp: number, order: SortOrder, tieBreakCmp: number): number {
  if (primaryCmp !== 0) return order === 'asc' ? primaryCmp : -primaryCmp
  return tieBreakCmp
}
