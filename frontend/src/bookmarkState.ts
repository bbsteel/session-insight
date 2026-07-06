import type { SessionSummary } from './types'

export interface BookmarkChange {
  agentType: string
  sessionId: string
  bookmarked: boolean
}

export function applyBookmarkChange<T extends Pick<SessionSummary, 'id' | 'agent_type' | 'bookmarked'>>(
  sessions: T[],
  change: BookmarkChange,
): T[] {
  return sessions.map(session => {
    if (session.id !== change.sessionId || session.agent_type !== change.agentType) return session
    return { ...session, bookmarked: change.bookmarked }
  })
}

export function removeBookmarkFromList<T extends Pick<SessionSummary, 'id' | 'agent_type'>>(
  bookmarks: T[],
  change: BookmarkChange,
): T[] {
  if (change.bookmarked) return bookmarks
  return bookmarks.filter(session => session.id !== change.sessionId || session.agent_type !== change.agentType)
}

export interface BookmarkFilters {
  agentType: string
  project: string
}

export function filterBookmarks<T extends Pick<SessionSummary, 'agent_type' | 'project'>>(
  bookmarks: T[],
  filters: BookmarkFilters,
): T[] {
  return bookmarks.filter(session => {
    if (filters.agentType && session.agent_type !== filters.agentType) return false
    if (filters.project && session.project !== filters.project) return false
    return true
  })
}
