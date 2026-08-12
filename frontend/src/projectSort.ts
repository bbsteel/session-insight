// Project filter list sort: name / session count / recent activity, with order.
// Preference is stored in localStorage so the dropdown reopens with the same order.

export type ProjectSortKey = 'name' | 'sessions' | 'recent'
export type ProjectSortOrder = 'asc' | 'desc'

export interface ProjectEntry {
  name: string
  session_count: number
  /** ISO timestamp of the most recent session activity in this project (empty if unknown). */
  last_active: string
}

export interface ProjectSortPref {
  key: ProjectSortKey
  order: ProjectSortOrder
}

const STORAGE_KEY = 'si-project-sort'
export const DEFAULT_PROJECT_SORT: ProjectSortPref = { key: 'sessions', order: 'desc' }

/** Default sort order when the user switches to a sort key. */
export function defaultOrderForKey(key: ProjectSortKey): ProjectSortOrder {
  // Name: A→Z; sessions/recent: most first.
  return key === 'name' ? 'asc' : 'desc'
}

export function isProjectSortKey(v: unknown): v is ProjectSortKey {
  return v === 'name' || v === 'sessions' || v === 'recent'
}

export function isProjectSortOrder(v: unknown): v is ProjectSortOrder {
  return v === 'asc' || v === 'desc'
}

export function getProjectSortPref(): ProjectSortPref {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULT_PROJECT_SORT }
    const parsed = JSON.parse(raw) as { key?: unknown; order?: unknown; dir?: unknown }
    // Prefer `order`; accept legacy `dir` from the first ship of this feature.
    const order = isProjectSortOrder(parsed.order)
      ? parsed.order
      : isProjectSortOrder(parsed.dir)
        ? parsed.dir
        : null
    if (isProjectSortKey(parsed.key) && order) {
      return { key: parsed.key, order }
    }
  } catch {
    // ignore corrupt or unavailable storage
  }
  return { ...DEFAULT_PROJECT_SORT }
}

export function setProjectSortPref(pref: ProjectSortPref): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(pref))
  } catch {
    // ignore
  }
}

function activityMs(iso: string): number {
  if (!iso) return 0
  const parsed = Date.parse(iso)
  return Number.isFinite(parsed) ? parsed : 0
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
  const sign = order === 'asc' ? 1 : -1
  let cmp = 0
  switch (key) {
    case 'name':
      cmp = a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
      break
    case 'sessions':
      cmp = a.session_count - b.session_count
      break
    case 'recent':
      cmp = activityMs(a.last_active) - activityMs(b.last_active)
      break
  }
  if (cmp !== 0) return cmp * sign
  return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
}

export function sortProjects(
  projects: readonly ProjectEntry[],
  key: ProjectSortKey,
  order: ProjectSortOrder,
): ProjectEntry[] {
  return [...projects].sort((a, b) => compareProjects(a, b, key, order))
}
