// Project filter list sort: name / session count / recent activity, with order.
// Preference is stored in localStorage so the panel reopens with the same order.
// Generic plumbing lives in sortPreference.ts.

import {
  activityMs,
  compareNames,
  compareOrdered,
  defaultSortOrder,
  isSortOrder,
  readSortPref,
  writeSortPref,
  type SortOrder,
  type SortPref,
} from './sortPreference.js'

export type ProjectSortKey = 'name' | 'sessions' | 'recent'
export type ProjectSortOrder = SortOrder

export interface ProjectEntry {
  name: string
  session_count: number
  /** ISO timestamp of the most recent session activity in this project (empty if unknown). */
  last_active: string
}

export type ProjectSortPref = SortPref<ProjectSortKey>

const STORAGE_KEY = 'si-project-sort'
export const DEFAULT_PROJECT_SORT: ProjectSortPref = { key: 'sessions', order: 'desc' }

/** Default sort order when the user switches to a sort key. */
export function defaultOrderForKey(key: ProjectSortKey): ProjectSortOrder {
  // Name: A→Z; sessions/recent: most first.
  return defaultSortOrder(key, 'name')
}

export function isProjectSortKey(v: unknown): v is ProjectSortKey {
  return v === 'name' || v === 'sessions' || v === 'recent'
}

export function isProjectSortOrder(v: unknown): v is ProjectSortOrder {
  return isSortOrder(v)
}

export function getProjectSortPref(): ProjectSortPref {
  return readSortPref(STORAGE_KEY, isProjectSortKey, DEFAULT_PROJECT_SORT)
}

export function setProjectSortPref(pref: ProjectSortPref): void {
  writeSortPref(STORAGE_KEY, pref)
}

/**
 * Compare two project entries for the active sort.
 * Secondary key is always name ascending for stable, scannable ties.
 */
export function compareProjects(
  a: ProjectEntry,
  b: ProjectEntry,
  key: ProjectSortKey,
  order: ProjectSortOrder,
): number {
  let cmp = 0
  switch (key) {
    case 'name':
      cmp = compareNames(a.name, b.name)
      break
    case 'sessions':
      cmp = a.session_count - b.session_count
      break
    case 'recent':
      cmp = activityMs(a.last_active) - activityMs(b.last_active)
      break
  }
  return compareOrdered(cmp, order, compareNames(a.name, b.name))
}

export function sortProjects(
  projects: readonly ProjectEntry[],
  key: ProjectSortKey,
  order: ProjectSortOrder,
): ProjectEntry[] {
  return [...projects].sort((a, b) => compareProjects(a, b, key, order))
}
