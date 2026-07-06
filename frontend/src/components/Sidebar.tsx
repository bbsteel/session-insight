import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { fetchAgents, fetchBookmarks, fetchSessions, removeBookmark } from '../api'
import type { AgentInfo, SessionSummary } from '../types'
import { applyBookmarkChange, filterBookmarks, removeBookmarkFromList, type BookmarkChange } from '../bookmarkState'
import AgentFilter from './AgentFilter'
import ProjectFilter, { type ProjectEntry } from './ProjectFilter'
import AgentIcon from './AgentIcon'
import { Virtuoso } from 'react-virtuoso'
import { getAgentLabel } from '../sidebarRows'

function formatDateTime(dateStr: string): string {
  const date = new Date(dateStr)
  if (Number.isNaN(date.getTime())) return dateStr
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day} ${hour}:${minute}`
}

const SIDEBAR_WIDTH_KEY = 'sidebar-width'

function readStorage(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Storage is optional; keep the in-memory UI state working.
  }
}

function getSessionName(s: SessionSummary): string {
  if (s.name) return s.name
  if (s.repository) return s.repository
  return s.id.slice(0, 8)
}

function getResumeCommand(session: SessionSummary): string | null {
  const at = session.agent_type.toLowerCase()
  const resumeId = session.resume_id || session.id
  let agentCmd = ''
  if (at.includes('claude')) agentCmd = `claude --resume ${resumeId}`
  else if (at.includes('codex')) agentCmd = `codex resume ${resumeId}`
  else if (at.includes('opencode')) agentCmd = `opencode -s ${resumeId}`
  else return null

  if (session.cwd) {
    const dir = /\s/.test(session.cwd) ? `"${session.cwd}"` : session.cwd
    return `cd ${dir} && ${agentCmd}`
  }
  return agentCmd
}

interface ContextMenuState {
  x: number
  y: number
  session: SessionSummary
}

interface SidebarProps {
  selectedId: string | null
  onSelect: (id: string) => void
  drawer?: boolean
  onClose?: () => void
  bookmarkChange?: BookmarkChange | null
  onBookmarkChange?: (change: BookmarkChange) => void
}

export default function Sidebar({ selectedId, onSelect, drawer, onClose, bookmarkChange, onBookmarkChange }: SidebarProps) {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [bookmarks, setBookmarks] = useState<SessionSummary[]>([])
  const [bookmarksOpen, setBookmarksOpen] = useState(false)
  const [bookmarksLoading, setBookmarksLoading] = useState(false)
  const [bookmarksError, setBookmarksError] = useState<string | null>(null)
  const [bookmarkAgentFilter, setBookmarkAgentFilter] = useState('')
  const [bookmarkProjectFilter, setBookmarkProjectFilter] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [width, setWidth] = useState(() => {
    const stored = Number(readStorage(SIDEBAR_WIDTH_KEY))
    return Number.isFinite(stored) && stored >= 160 && stored <= 400 ? stored : 260
  })
  const [toast, setToast] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout>>()
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null)
  const [isMobile, setIsMobile] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches
  )
  const [agents, setAgents] = useState<AgentInfo[] | null>(null)
  const [agentsReady, setAgentsReady] = useState(false)
  const [agentFilter, setAgentFilter] = useState<string>('')
  const [projectFilter, setProjectFilter] = useState<string>('')

  const searchRef = useRef<HTMLInputElement>(null)
  const asideRef = useRef<HTMLElement>(null)
  const bookmarksDropdownRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)')
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [])

  // Auto-focus search when drawer opens on mobile
  useEffect(() => {
    if (isMobile && drawer) {
      const timer = setTimeout(() => searchRef.current?.focus(), 100)
      return () => clearTimeout(timer)
    }
  }, [isMobile, drawer])

  // Esc to close drawer on mobile, context menu, or bookmarks dropdown.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (bookmarksOpen) {
          setBookmarksOpen(false)
        } else if (contextMenu) {
          setContextMenu(null)
        } else if (isMobile && onClose) {
          onClose()
        }
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [isMobile, onClose, contextMenu, bookmarksOpen])

  // Load all sessions once on mount — agent/search filters are client-side
  useEffect(() => {
    fetchSessions()
      .then(data => { setSessions(data); setError(null) })
      .catch(err => setError(err instanceof Error ? err.message : '会话列表加载失败'))
      .finally(() => setLoading(false))
  }, [])

  // Fetch agents on mount
  useEffect(() => {
    fetchAgents()
      .then(data => {
        setAgents(data)
        setAgentsReady(true)
      })
      .catch(() => setAgents([]))
  }, [])

  useEffect(() => {
    if (!bookmarkChange) return
    setSessions(prev => applyBookmarkChange(prev, bookmarkChange))
    setBookmarks(prev => {
      const updated = applyBookmarkChange(prev, bookmarkChange)
      return removeBookmarkFromList(updated, bookmarkChange)
    })
  }, [bookmarkChange])

  useEffect(() => {
    if (!bookmarksOpen) return
    const handler = (e: MouseEvent) => {
      if (bookmarksDropdownRef.current?.contains(e.target as Node)) return
      setBookmarksOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [bookmarksOpen])

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(null), 2000)
  }, [])

  const beginResize = (e: React.PointerEvent) => {
    if (isMobile) return
    const startX = e.clientX
    const startWidth = width
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)

    const move = (event: PointerEvent) => {
      const next = Math.min(400, Math.max(160, startWidth + event.clientX - startX))
      setWidth(next)
      writeStorage(SIDEBAR_WIDTH_KEY, String(Math.round(next)))
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  const copyId = useCallback(async (session: SessionSummary) => {
    const id = session.resume_id || session.id
    try {
      await navigator.clipboard.writeText(id)
      showToast('已复制会话 ID')
    } catch {
      showToast('复制失败')
    }
  }, [showToast])

  const copyResumeCmd = useCallback(async (session: SessionSummary) => {
    const cmd = getResumeCommand(session)
    if (!cmd) return
    try {
      await navigator.clipboard.writeText(cmd)
      showToast('已复制恢复命令')
    } catch {
      showToast('复制失败')
    }
  }, [showToast])

  const openContextMenu = useCallback((e: React.MouseEvent, session: SessionSummary) => {
    e.preventDefault()
    e.stopPropagation()
    setContextMenu({ x: e.clientX, y: e.clientY, session })
  }, [])

  const openBookmarks = useCallback(() => {
    setBookmarksOpen(v => !v)
    if (bookmarksOpen) return
    setBookmarksLoading(true)
    setBookmarksError(null)
    fetchBookmarks()
      .then(data => setBookmarks(data))
      .catch(err => setBookmarksError(err instanceof Error ? err.message : '收藏加载失败'))
      .finally(() => setBookmarksLoading(false))
  }, [bookmarksOpen])

  const removeSessionBookmark = useCallback(async (session: SessionSummary) => {
    try {
      await removeBookmark(session)
      const change = { agentType: session.agent_type, sessionId: session.id, bookmarked: false }
      setSessions(prev => applyBookmarkChange(prev, change))
      setBookmarks(prev => removeBookmarkFromList(prev, change))
      onBookmarkChange?.(change)
      showToast('已取消收藏')
    } catch {
      showToast('取消收藏失败')
    }
  }, [onBookmarkChange, showToast])

  const filtered = useMemo(() => sessions.filter(s => {
    if (agentFilter && s.agent_type !== agentFilter) return false
    if (projectFilter && s.project !== projectFilter) return false
    if (query.trim()) {
      const q = query.toLowerCase()
      const name = getSessionName(s).toLowerCase()
      const repo = (s.repository || '').toLowerCase()
      const branch = (s.branch || '').toLowerCase()
      const agent = (s.agent_type || '').toLowerCase()
      const preview = (s.preview_text || '').toLowerCase()
      if (!name.includes(q) && !repo.includes(q) && !branch.includes(q) && !agent.includes(q) && !preview.includes(q)) return false
    }
    return true
  }).sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()), [sessions, query, agentFilter, projectFilter])

  const bookmarkAgentOptions = useMemo(() => {
    return [...new Set(bookmarks.map(s => s.agent_type).filter(Boolean))].sort()
  }, [bookmarks])

  const bookmarkProjectOptions = useMemo(() => {
    return [...new Set(bookmarks
      .filter(s => !bookmarkAgentFilter || s.agent_type === bookmarkAgentFilter)
      .map(s => s.project)
      .filter(Boolean))]
      .sort((a, b) => a.localeCompare(b))
  }, [bookmarks, bookmarkAgentFilter])

  useEffect(() => {
    if (bookmarkProjectFilter && !bookmarkProjectOptions.includes(bookmarkProjectFilter)) {
      setBookmarkProjectFilter('')
    }
  }, [bookmarkProjectFilter, bookmarkProjectOptions])

  const visibleBookmarks = useMemo(
    () => filterBookmarks(bookmarks, { agentType: bookmarkAgentFilter, project: bookmarkProjectFilter }),
    [bookmarks, bookmarkAgentFilter, bookmarkProjectFilter],
  )

  const liveCount = useMemo(() => sessions.filter(s => s.is_live).length, [sessions])

  const liveCountByAgent = useMemo(() => {
    const map = new Map<string, number>()
    for (const s of sessions) {
      if (s.is_live && s.agent_type) map.set(s.agent_type, (map.get(s.agent_type) ?? 0) + 1)
    }
    return map
  }, [sessions])

  const effectiveAgents = useMemo<AgentInfo[]>(() => {
    if (agents && agents.length > 0) {
      return agents.map(a => ({ ...a, live_count: liveCountByAgent.get(a.type) ?? 0 }))
    }

    const counts = new Map<string, number>()
    for (const session of sessions) {
      if (!session.agent_type) continue
      counts.set(session.agent_type, (counts.get(session.agent_type) ?? 0) + 1)
    }
    return [...counts.entries()].map(([type, session_count]) => ({
      type,
      display_name: getAgentLabel(type),
      session_count,
      live_count: liveCountByAgent.get(type) ?? 0,
    }))
  }, [agents, sessions, liveCountByAgent])

  useEffect(() => {
    if (!agentsReady || !agentFilter) return
    if (!effectiveAgents.some(agent => agent.type === agentFilter)) {
      setAgentFilter('')
    }
  }, [agentFilter, agentsReady, effectiveAgents])

  const projectEntries = useMemo<ProjectEntry[]>(() => {
    const counts = new Map<string, number>()
    for (const s of sessions) {
      if (!s.project) continue
      // When agent filter is active, only count sessions of that agent.
      if (agentFilter && s.agent_type !== agentFilter) continue
      counts.set(s.project, (counts.get(s.project) ?? 0) + 1)
    }
    return [...counts.entries()]
      .map(([name, session_count]) => ({ name, session_count }))
      .sort((a, b) => b.session_count - a.session_count || a.name.localeCompare(b.name))
  }, [sessions, agentFilter])

  // Reset project filter when the selected project disappears from the list
  // (e.g. after switching agent filter).
  useEffect(() => {
    if (projectFilter && !projectEntries.some(p => p.name === projectFilter)) {
      setProjectFilter('')
    }
  }, [projectFilter, projectEntries])

  const SessionRow = ({ session }: { session: SessionSummary }) => {
    const selected = session.id === selectedId
    const repo = session.repository || ''
    const branch = session.branch || ''

    const parts: string[] = []
    if (repo) {
      const shortRepo = repo.split('/').slice(-2).join('/')
      parts.push(shortRepo)
    }
    if (branch) parts.push(branch)
    parts.push(`${session.message_count || session.turn_count} msgs`)
    if (session.is_live) parts.push('活跃中')
    const metadata = parts.join(' · ')
    const dateTime = formatDateTime(session.updated_at)

    return (
      <div className="group relative">
        <button
          onClick={() => onSelect(session.id)}
          onContextMenu={(e) => openContextMenu(e, session)}
          title="右键打开菜单"
          className={`relative w-full text-left pl-2.5 pr-8 rounded-md cursor-pointer transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)] ${
            selected ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
          }`}
          style={{ paddingTop: '0.375rem', paddingBottom: '0.375rem' }}
        >
          {selected && <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-[var(--accent-blue)]" />}
          <div className="flex items-start gap-1.5">
            <AgentIcon agentType={session.agent_type} size={20} className="mt-0.5" />
            <div className="min-w-0 flex-1">
              <div className="text-body text-[var(--text-primary)] truncate flex items-center gap-1.5">
                {session.is_live && <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] flex-shrink-0 animate-pulse" title="活跃中" aria-label="活跃中" />}
                {session.bookmarked && (
                  <span className="text-meta text-[var(--accent-blue)] flex-shrink-0" title="已收藏" aria-label="已收藏">
                    已收藏
                  </span>
                )}
                <span className="truncate">{getSessionName(session)}</span>
              </div>
              <div className="text-helper text-[var(--text-secondary)] mt-0.5 flex items-center gap-2">
                <span className="truncate min-w-0">{metadata}</span>
                <time className="ml-auto flex-shrink-0 tabular-nums" dateTime={session.updated_at}>
                  {dateTime}
                </time>
              </div>
            </div>
          </div>
        </button>
        <button
          onClick={(e) => { e.stopPropagation(); copyId(session) }}
          tabIndex={-1}
          className="absolute right-1 top-1.5 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-inset)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 max-md:opacity-100 transition-opacity duration-fast"
          aria-hidden="true"
          title="复制会话 ID"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        </button>
      </div>
    )
  }

  const effectiveWidth = isMobile ? 'min(86vw, 340px)' : `${width}px`

  if (loading) {
    return (
      <aside
        className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)]"
        style={{ width: effectiveWidth }}
      >
        <div className="p-4">
          <h2 className="text-nav font-semibold text-[var(--text-primary)]">Sessions</h2>
          <div className="mt-3 space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="h-7 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
            ))}
          </div>
        </div>
      </aside>
    )
  }

  return (
    <aside
      ref={asideRef}
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)] relative flex flex-col"
      style={{ width: effectiveWidth }}
      role={isMobile ? 'dialog' : undefined}
      aria-label="会话列表"
    >
      {/* Header */}
      <div className="p-4 flex items-center justify-between flex-shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <h2 className="text-nav font-semibold text-[var(--text-primary)] truncate">Sessions</h2>
          <span className="text-helper text-[var(--text-muted)] flex-shrink-0">{sessions.length} total</span>
          {liveCount > 0 && (
            <span className="text-helper text-[var(--success)] flex-shrink-0 flex items-center gap-1">
              <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] animate-pulse" />
              {liveCount} 活跃中
            </span>
          )}
        </div>
        <div ref={bookmarksDropdownRef} className="relative flex items-center gap-1 flex-shrink-0">
          <button
            onClick={openBookmarks}
            aria-label="收藏列表"
            title="收藏列表"
            className="h-6 px-2 flex items-center justify-center rounded-md text-nav text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            收藏列表
          </button>
          {bookmarksOpen && (
            <div className="absolute left-full top-0 ml-2 z-[var(--z-dropdown)] w-[520px] max-h-[70vh] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg flex flex-col">
              <div className="h-10 flex items-center justify-between border-b border-[var(--border-default)] px-3">
                <h3 className="text-nav font-semibold text-[var(--text-primary)]">收藏列表</h3>
                <button
                  onClick={() => setBookmarksOpen(false)}
                  aria-label="关闭收藏列表"
                  className="h-7 px-2 rounded-md text-nav text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                >
                  关闭
                </button>
              </div>
              <div className="grid grid-cols-2 gap-2 border-b border-[var(--border-default)] p-2">
                <label className="min-w-0 text-meta text-[var(--text-secondary)]">
                  <span className="mb-1 block">Agent</span>
                  <select
                    value={bookmarkAgentFilter}
                    onChange={e => setBookmarkAgentFilter(e.target.value)}
                    className="h-7 w-full rounded-md border border-[var(--border-muted)] bg-[var(--bg-surface)] px-1.5 text-nav text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                  >
                    <option value="">全部</option>
                    {bookmarkAgentOptions.map(agent => (
                      <option key={agent} value={agent}>{getAgentLabel(agent)}</option>
                    ))}
                  </select>
                </label>
                <label className="min-w-0 text-meta text-[var(--text-secondary)]">
                  <span className="mb-1 block">项目</span>
                  <select
                    value={bookmarkProjectFilter}
                    onChange={e => setBookmarkProjectFilter(e.target.value)}
                    className="h-7 w-full rounded-md border border-[var(--border-muted)] bg-[var(--bg-surface)] px-1.5 text-nav text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                  >
                    <option value="">全部</option>
                    {bookmarkProjectOptions.map(project => (
                      <option key={project} value={project}>{project}</option>
                    ))}
                  </select>
                </label>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto p-2">
                {bookmarksLoading ? (
                  <div className="px-3 py-8 text-center text-helper text-[var(--text-muted)]">加载中...</div>
                ) : bookmarksError ? (
                  <div className="px-3 py-8 text-center text-helper text-[var(--error)]">{bookmarksError}</div>
                ) : visibleBookmarks.length === 0 ? (
                  <div className="px-3 py-8 text-center text-helper text-[var(--text-muted)]">暂无收藏</div>
                ) : (
                  <div className="space-y-1">
                    {visibleBookmarks.map(session => {
                      const repo = session.repository ? session.repository.split('/').slice(-2).join('/') : ''
                      const project = session.project || repo || '未知项目'
                      const model = session.model_name || 'unknown model'
                      return (
                        <div key={`${session.agent_type}-${session.id}`} className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-[var(--bg-surface-hover)]">
                          <button
                            onClick={() => { onSelect(session.id); setBookmarksOpen(false) }}
                            className="min-w-0 flex-1 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] rounded-sm"
                          >
                            <span className="block truncate text-body text-[var(--text-primary)]">{getSessionName(session)}</span>
                            <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-1.5 text-meta text-[var(--text-secondary)]">
                              <span className="rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5">{getAgentLabel(session.agent_type)}</span>
                              <span className="max-w-[150px] truncate rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5">{model}</span>
                              <span className="max-w-[150px] truncate rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5">{project}</span>
                            </span>
                            <span className="mt-0.5 block truncate text-helper text-[var(--text-muted)]">
                              {formatDateTime(session.updated_at)}
                            </span>
                          </button>
                          <button
                            onClick={() => removeSessionBookmark(session)}
                            className="h-7 px-2 rounded-md text-nav text-[var(--text-muted)] hover:bg-[var(--bg-inset)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                            title="取消收藏"
                            aria-label="取消收藏"
                          >
                            取消收藏
                          </button>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            </div>
          )}
          {isMobile && onClose && (
            <button
              onClick={onClose}
              aria-label="关闭会话列表"
              className="ml-1 w-6 h-6 flex items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          )}
        </div>
      </div>

      {/* Filter Bar */}
      <div className="px-4 pb-2 flex-shrink-0">
        <div className="relative">
          <svg className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[var(--text-muted)] pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            ref={searchRef}
            type="text"
            placeholder="过滤会话..."
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="w-full h-[34px] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] pl-8 pr-7 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)]"
          />
          {query && (
            <button
              onClick={() => setQuery('')}
              className="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              aria-label="清除搜索"
            >
              &times;
            </button>
          )}
        </div>
      </div>

      {/* Agent Filter */}
      <AgentFilter
        agents={effectiveAgents}
        selected={agentFilter}
        onSelect={setAgentFilter}
      />

      {/* Project Filter */}
      <ProjectFilter
        projects={projectEntries}
        selected={projectFilter}
        onSelect={setProjectFilter}
      />

      {/* Session List */}
      <div className="px-2 flex-1 min-h-0">
        {error ? (
          <div className="px-3 py-8 text-center">
            <div className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-md bg-[var(--bg-inset)] text-helper text-[var(--warning)]">!</div>
            <div className="text-nav font-medium text-[var(--text-primary)]">后端未连接</div>
            <div className="mt-1 text-helper text-[var(--text-muted)]">启动 Go 服务后会显示本地会话。</div>
          </div>
        ) : filtered.length === 0 ? (
          <div className="px-3 py-8 text-center">
            <div className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-md bg-[var(--bg-inset)] text-helper text-[var(--text-muted)]">⌕</div>
            <div className="text-nav font-medium text-[var(--text-primary)]">未找到匹配的会话</div>
            <div className="mt-1 text-helper text-[var(--text-muted)]">尝试其他关键词或清除筛选条件</div>
            {query && (
              <button
                onClick={() => setQuery('')}
                className="mt-3 h-7 px-3 rounded-md border border-[var(--border-default)] text-meta text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                清除筛选
              </button>
            )}
          </div>
        ) : (
          <Virtuoso
            className="h-full"
            data={filtered}
            computeItemKey={(_, s) => s.id}
            components={{ Footer: () => <div className="h-4" /> }}
            itemContent={(_, s) => (
              <div className="py-0.5">
                <SessionRow session={s} />
              </div>
            )}
          />
        )}
      </div>

      {/* Resize handle (desktop only) */}
      {!isMobile && (
        <div
          onPointerDown={beginResize}
          className="absolute right-0 top-0 h-full w-1 cursor-grab hover:bg-[var(--accent-blue)]/30 active:cursor-grabbing"
          title="拖拽调整侧栏宽度"
        />
      )}

      {/* Context Menu — portaled to body so the backdrop covers the full viewport */}
      {contextMenu && createPortal(
        <>
          <div
            className="fixed inset-0 z-[calc(var(--z-toast,50)-1)]"
            onClick={() => setContextMenu(null)}
            onContextMenu={(e) => { e.preventDefault(); setContextMenu(null) }}
          />
          <div
            className="fixed z-[var(--z-toast,50)] min-w-[180px] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg py-1 text-body"
            style={{ left: contextMenu.x, top: contextMenu.y }}
          >
            <button
              className="w-full text-left px-3 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              onClick={() => { copyId(contextMenu.session); setContextMenu(null) }}
            >
              复制会话 ID
            </button>
            {(() => {
              const cmd = getResumeCommand(contextMenu.session)
              const isLive = contextMenu.session.is_live
              const disabled = isLive || !cmd
              const tooltip = isLive ? '活跃中会话不可恢复' : !cmd ? '此 Agent 暂不支持命令行恢复' : undefined
              return (
                <button
                  className={`w-full text-left px-3 py-1.5 transition-colors duration-fast ${
                    disabled
                      ? 'text-[var(--text-muted)] cursor-not-allowed'
                      : 'text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]'
                  }`}
                  disabled={disabled}
                  title={tooltip}
                  onClick={() => {
                    if (!disabled) { copyResumeCmd(contextMenu.session); setContextMenu(null) }
                  }}
                >
                  复制会话恢复命令
                </button>
              )
            })()}
          </div>
        </>,
        document.body
      )}

      {/* Toast */}
      {toast && (
        <div className="absolute bottom-4 left-1/2 -translate-x-1/2 z-[var(--z-toast)] px-3 py-1.5 rounded-md bg-[var(--text-primary)] text-[var(--text-inverse)] text-meta shadow-lg animate-pulse">
          {toast}
        </div>
      )}
    </aside>
  )
}
