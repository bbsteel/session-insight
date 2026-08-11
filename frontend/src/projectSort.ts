// Project filter list sort: name / session count / recent activity, with direction.
// Preference is stored in localStorage so the dropdown reopens with the same order.

export type ProjectSortKey = 'name' | 'sessions' | 'recent'
export type ProjectSortDir = 'asc' | 'desc'

export interface ProjectEntry {
  name: string
  session_count: number
  /** ISO timestamp of the most recent session activity in this project (empty if unknown). */
  last_active: string
}

export interface ProjectSortPref {
  key: ProjectSortKey
  dir: ProjectSortDir
}

const STORAGE_KEY = 'si-project-sort'
export const DEFAULT_PROJECT_SORT: ProjectSortPref = { key: 'sessions', dir: 'desc' }

/** Default direction when the user switches to a sort key. */
export function defaultDirForKey(key: ProjectSortKey): ProjectSortDir {
  // Name: A→Z; sessions/recent: most first.
  return key === 'name' ? 'asc' : 'desc'
}

export function isProjectSortKey(v: unknown): v is ProjectSortKey {
  return v === 'name' || v === 'sessions' || v === 'recent'
}

export function isProjectSortDir(v: unknown): v is ProjectSortDir {
  return v === 'asc' || v === 'desc'
}

export function getProjectSortPref(): ProjectSortPref {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULT_PROJECT_SORT }
    const parsed = JSON.parse(raw) as { key?: unknown; dir?: unknown }
    if (isProjectSortKey(parsed.key) && isProjectSortDir(parsed.dir)) {
      return { key: parsed.key, dir: parsed.dir }
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
  const t = Date.parse(iso)
  return Number.isFinite(t) ? t : 0
}

/**
 * Compare two project entries for the active sort.
 * Secondary key is always name ascending for stable, scannable ties.
 */
export function compareProjects(
  a: ProjectEntry,
  b: ProjectEntry,
  key: ProjectSortKey,
  dir: ProjectSortDir,
): number {
  const sign = dir === 'asc' ? 1 : -1
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
  dir: ProjectSortDir,
): ProjectEntry[] {
  return [...projects].sort((a, b) => compareProjects(a, b, key, dir))
}
