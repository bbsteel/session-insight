import type { SessionSummary } from './types'

export interface BookmarkChange {
  agentType: string
  sessionId: string
  bookmarked: boolean
  /** When set (including empty string), replaces bookmark_note. Cleared on unbookmark. */
  bookmarkNote?: string
}

export function applyBookmarkChange<T extends Pick<SessionSummary, 'id' | 'agent_type' | 'bookmarked' | 'bookmark_note'>>(
  sessions: T[],
  change: BookmarkChange,
): T[] {
  return sessions.map(session => {
    if (session.id !== change.sessionId || session.agent_type !== change.agentType) return session
    if (!change.bookmarked) {
      return { ...session, bookmarked: false, bookmark_note: undefined }
    }
    if (change.bookmarkNote !== undefined) {
      return { ...session, bookmarked: true, bookmark_note: change.bookmarkNote }
    }
    return { ...session, bookmarked: true }
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
  modelName: string
}

export function filterBookmarks<T extends Pick<SessionSummary, 'agent_type' | 'project' | 'model_name'>>(
  bookmarks: T[],
  filters: BookmarkFilters,
): T[] {
  return bookmarks.filter(session => {
    if (filters.agentType && session.agent_type !== filters.agentType) return false
    if (filters.project && session.project !== filters.project) return false
    if (filters.modelName && session.model_name !== filters.modelName) return false
    return true
  })
}
