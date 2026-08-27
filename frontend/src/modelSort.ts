// Model filter panel sort: name / session count / recent activity, with order.
// Preference is stored in localStorage so the panel reopens with the same order.
// Generic plumbing lives in sortPreference.ts.

import {
  activityMs,
  compareNames,
  compareOrdered,
  defaultSortOrder,
  readSortPref,
  writeSortPref,
  type SortPref,
} from './sortPreference.js'

export type ModelSortKey = 'name' | 'sessions' | 'recent'

/** Minimal shape the model sort needs; ModelEntry satisfies it. */
export interface ModelSortEntry {
  /** Raw model id; the catch-all 'Other' bucket always sinks to the end. */
  id: string
  /** Display label used for name ordering and tie-breaks. */
  label: string
  session_count: number
  /** ISO timestamp of the most recent session activity on this model (empty if unknown). */
  last_active: string
}

export type ModelSortPref = SortPref<ModelSortKey>

const STORAGE_KEY = 'si-model-sort'
export const DEFAULT_MODEL_SORT: ModelSortPref = { key: 'sessions', order: 'desc' }

/** Default sort order when the user switches to a sort key. */
export function defaultOrderForModelKey(key: ModelSortKey) {
  // Name: A→Z; sessions/recent: most first.
  return defaultSortOrder(key, 'name')
}

export function isModelSortKey(v: unknown): v is ModelSortKey {
  return v === 'name' || v === 'sessions' || v === 'recent'
}

export function getModelSortPref(): ModelSortPref {
  return readSortPref(STORAGE_KEY, isModelSortKey, DEFAULT_MODEL_SORT)
}

export function setModelSortPref(pref: ModelSortPref): void {
  writeSortPref(STORAGE_KEY, pref)
}

/**
 * Compare two model entries for the active sort.
 * The 'Other' bucket always sorts last; ties break by label ascending.
 */
export function compareModels(
  a: ModelSortEntry,
  b: ModelSortEntry,
  key: ModelSortKey,
  order: SortPref<ModelSortKey>['order'],
): number {
  if (a.id === 'Other' && b.id !== 'Other') return 1
  if (b.id === 'Other' && a.id !== 'Other') return -1
  let cmp = 0
  switch (key) {
    case 'name':
      cmp = compareNames(a.label, b.label)
      break
    case 'sessions':
      cmp = a.session_count - b.session_count
      break
    case 'recent':
      cmp = activityMs(a.last_active) - activityMs(b.last_active)
      break
  }
  return compareOrdered(cmp, order, compareNames(a.label, b.label))
}

export function sortModels<T extends ModelSortEntry>(
  models: readonly T[],
  key: ModelSortKey,
  order: SortPref<ModelSortKey>['order'],
): T[] {
  return [...models].sort((a, b) => compareModels(a, b, key, order))
}
