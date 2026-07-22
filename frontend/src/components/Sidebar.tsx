import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { addBookmark, fetchAgents, fetchSearch, fetchSessions, fetchVersion, removeBookmark, updateBookmarkNote, watchSessionsChanged } from '../api'
import type { AgentInfo, SessionSummary } from '../types'
import { applyBookmarkChange, type BookmarkChange } from '../bookmarkState'
import AgentFilter from './AgentFilter'
import ProjectFilter, { type ProjectEntry } from './ProjectFilter'
import ModelFilter, { type ModelEntry } from './ModelFilter'
import AgentIcon from './AgentIcon'
import BookmarkNoteEditor, { type BookmarkNoteAnchor, type BookmarkNoteTarget } from './BookmarkNoteEditor'
import DeleteSessionDialog from './DeleteSessionDialog'
import InstantTooltip from './InstantTooltip'
import StarIcon from './StarIcon'
import { InfoIcon } from './icons'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { formatRelativeTime, getAgentLabel, isSessionLive } from '../sidebarRows'
import { getResumeCommandOptions, getResumePreferenceKey, isWindowsSession, type ResumeCommandMode, type ResumeShell } from '../resumeCommands'
import { modelMeta } from '../modelMeta'
import { formatDate, formatNumber, useI18n } from '../i18n'

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

function sessionModelKey(s: Pick<SessionSummary, 'model_name' | 'model_provider'>): string {
  const meta = modelMeta(s.model_name, s.model_provider)
  return `model\x00${meta.label}`
}

function sessionModelProviderKey(s: Pick<SessionSummary, 'model_name' | 'model_provider'>): string {
  const meta = modelMeta(s.model_name, s.model_provider)
  return `provider\x00${meta.label}\x00${meta.providerKey}`
}

function sessionMatchesModelFilter(s: Pick<SessionSummary, 'model_name' | 'model_provider'>, filter: string): boolean {
  if (filter.startsWith('provider\x00')) return sessionModelProviderKey(s) === filter
  if (filter.startsWith('model\x00')) return sessionModelKey(s) === filter
  return sessionModelProviderKey(s) === filter || sessionModelKey(s) === filter
}

function hostIsWindows(): boolean {
  return /Windows/i.test(navigator.userAgent) || /Win/i.test(navigator.platform)
}

interface ContextMenuState {
  x: number
  y: number
  session: SessionSummary
}

interface SidebarProps {
  selectedId: string | null
  selectedAgentType?: string | null
  focusTarget?: { id: string; agentType: string } | null
  onSelect: (id: string, agentType?: string, focusSidebar?: boolean, searchQuery?: string) => void
  drawer?: boolean
  onClose?: () => void
  bookmarkChange?: BookmarkChange | null
  onBookmarkChange?: (change: BookmarkChange) => void
  onSessionDeleted?: (session: SessionSummary) => void
}

export default function Sidebar({ selectedId, selectedAgentType, focusTarget, onSelect, drawer, onClose, bookmarkChange, onBookmarkChange, onSessionDeleted }: SidebarProps) {
  const { locale, t } = useI18n()
  const [now, setNow] = useState(Date.now())
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [disconnected, setDisconnected] = useState(false)
  const [query, setQuery] = useState('')
  // 消息内容命中的会话集合（服务端 FTS）。列表 payload 不再携带 preview
  // 文本，内容匹配交给已有的 /api/search；null 表示无内容搜索结果可用，
  // 过滤时只按本地元数据字段匹配。
  const [contentHits, setContentHits] = useState<Set<string> | null>(null)
  const [version, setVersion] = useState('')

  // 版本号只取一次（进程生命周期内不变），失败时 footer 留空不打扰。
  useEffect(() => {
    let cancelled = false
    void fetchVersion().then(info => {
      if (!cancelled) setVersion(info.version)
    })
    return () => {
      cancelled = true
    }
  }, [])

  // 输入防抖 250ms 后调 FTS，把内容命中并进过滤结果；失败静默降级为
  // 仅本地字段匹配（搜索框不该因后台接口抖动而报错）。
  useEffect(() => {
    const q = query.trim()
    if (!q) {
      setContentHits(null)
      return
    }
    let cancelled = false
    const timer = setTimeout(() => {
      fetchSearch(q)
        .then(results => {
          if (cancelled) return
          setContentHits(new Set(results.map(r => `${r.agent_type}\x00${r.session_id}`)))
        })
        .catch(() => {
          if (!cancelled) setContentHits(null)
        })
    }, 250)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [query])

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [])
  const [width, setWidth] = useState(() => {
    const stored = Number(readStorage(SIDEBAR_WIDTH_KEY))
    return Number.isFinite(stored) && stored >= 160 && stored <= 400 ? stored : 260
  })
  const [toast, setToast] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout>>()
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null)
  const [noteEditorSession, setNoteEditorSession] = useState<SessionSummary | null>(null)
  const [notePopover, setNotePopover] = useState<{ session: BookmarkNoteTarget; anchor: BookmarkNoteAnchor } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SessionSummary | null>(null)
  // 活跃会话点「复制恢复命令」不直接复制，先弹窗确认——对同一会话开第二个
  // CLI 实例可能与正在写入的进程双写冲突。
  const [resumeConfirm, setResumeConfirm] = useState<{ session: SessionSummary; shell: ResumeShell; mode: ResumeCommandMode } | null>(null)
  const [isMobile, setIsMobile] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches
  )
  const [agents, setAgents] = useState<AgentInfo[] | null>(null)
  const [agentsReady, setAgentsReady] = useState(false)
  const [agentFilter, setAgentFilter] = useState<string>('')
  const [projectFilter, setProjectFilter] = useState<string>('')
  const [modelFilter, setModelFilter] = useState<string>('')

  const searchRef = useRef<HTMLInputElement>(null)
  const asideRef = useRef<HTMLElement>(null)
  const listRef = useRef<VirtuosoHandle>(null)

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

  // Esc to close drawer on mobile or context menu.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (contextMenu) {
          setContextMenu(null)
        } else if (isMobile && onClose) {
          onClose()
        }
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [isMobile, onClose, contextMenu])

  // Load all sessions once on mount — agent/search filters are client-side
  useEffect(() => {
    fetchSessions()
      .then(data => { setSessions(data); setError(null) })
      .catch(err => setError(err instanceof Error ? err.message : t('sidebar.loadFailed')))
      .finally(() => setLoading(false))
  }, [t])

  // Live refresh: the backend pings over SSE after an index round lands;
  // refetch the list, throttled to one request per 2s. In-flight guard: a
  // slow response must never overlap with the next refetch, or requests pile
  // up server-side and amplify the slowness — pings that arrive mid-request
  // coalesce into one catch-up refetch after it settles. Failed background
  // refetches keep the current list (no error banner: the user didn't ask
  // for this fetch).
  useEffect(() => {
    let disposed = false
    let inFlight = false
    let pendingPing = false
    let lastFetch = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const refetch = () => {
      lastFetch = Date.now()
      inFlight = true
      fetchSessions()
        .then(data => {
          if (disposed) return
          setSessions(data)
          setError(null)
        })
        .catch(() => {})
        .finally(() => {
          inFlight = false
          if (disposed) return
          if (pendingPing) {
            pendingPing = false
            schedule()
          }
        })
    }
    const schedule = () => {
      if (timer) return
      const wait = Math.max(0, lastFetch + 2000 - Date.now())
      timer = setTimeout(() => {
        timer = undefined
        if (inFlight) {
          pendingPing = true
          return
        }
        refetch()
      }, wait)
    }
    // 断连期间后端的 ping 会整段丢失，重连成功后必须补拉一次，否则
    // 断线窗口里的所有变化（包括活跃徽标该熄灭）都不会反映到列表。
    let everDisconnected = false
    const dispose = watchSessionsChanged(() => {
      if (inFlight) {
        pendingPing = true
        return
      }
      schedule()
    }, connected => {
      if (disposed) return
      setDisconnected(!connected)
      if (!connected) {
        everDisconnected = true
        return
      }
      if (everDisconnected) {
        everDisconnected = false
        if (inFlight) {
          pendingPing = true
        } else {
          schedule()
        }
      }
    })
    return () => {
      disposed = true
      if (timer) clearTimeout(timer)
      dispose()
    }
  }, [])

  // Instant rename when the AI panel applies a title override: patch local
  // state directly instead of waiting for the SSE ping + throttled refetch.
  // Restoring (title=null) needs the original name we no longer have, so
  // that path refetches immediately.
  useEffect(() => {
    const handler = (e: Event) => {
      const { agentType, sessionId, title } =
        (e as CustomEvent).detail as { agentType: string; sessionId: string; title: string | null }
      if (title) {
        setSessions(list =>
          list.map(s => (s.agent_type === agentType && s.id === sessionId ? { ...s, name: title } : s)),
        )
      } else {
        fetchSessions().then(setSessions).catch(() => {})
      }
    }
    window.addEventListener('si-title-override', handler)
    return () => window.removeEventListener('si-title-override', handler)
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
    setNoteEditorSession(prev => {
      if (!prev) return prev
      if (prev.id !== bookmarkChange.sessionId || prev.agent_type !== bookmarkChange.agentType) return prev
      const [next] = applyBookmarkChange([prev], bookmarkChange)
      return next.bookmarked ? next : null
    })
  }, [bookmarkChange])

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
      showToast(t('sidebar.copiedSessionId'))
    } catch {
      showToast(t('sidebar.copyFailed'))
    }
  }, [showToast, t])

  const copyAgentAndId = useCallback(async (session: SessionSummary) => {
    const id = session.resume_id || session.id
    try {
      await navigator.clipboard.writeText(`agent: ${getAgentLabel(session.agent_type)}, id: ${id}`)
      showToast(t('sidebar.copiedAgentAndId'))
    } catch {
      showToast(t('sidebar.copyFailed'))
    }
  }, [showToast, t])

  const copyResumeCmd = useCallback(async (session: SessionSummary, shell: ResumeShell, mode: ResumeCommandMode) => {
    const option = getResumeCommandOptions(session).find(item => item.shell === shell && item.mode === mode)
    if (!option) return
    try {
      await navigator.clipboard.writeText(option.command)
      writeStorage(getResumePreferenceKey(session), shell)
      showToast(t('sidebar.copiedResume', { shell: shell === 'powershell' ? 'PowerShell' : 'Git Bash' }))
    } catch {
      showToast(t('sidebar.copyFailed'))
    }
  }, [showToast, t])

  const openContextMenu = useCallback((e: React.MouseEvent, session: SessionSummary) => {
    e.preventDefault()
    e.stopPropagation()
    setContextMenu({ x: e.clientX, y: e.clientY, session })
  }, [])

  const toggleSessionBookmark = useCallback(async (session: SessionSummary): Promise<boolean> => {
    const bookmarked = !session.bookmarked
    try {
      if (bookmarked) await addBookmark(session)
      else await removeBookmark(session)
      const change: BookmarkChange = {
        agentType: session.agent_type,
        sessionId: session.id,
        bookmarked,
        bookmarkNote: bookmarked ? (session.bookmark_note ?? '') : undefined,
      }
      setSessions(prev => applyBookmarkChange(prev, change))
      onBookmarkChange?.(change)
      showToast(bookmarked ? t('replay.bookmarked') : t('replay.bookmarkRemoved'))
      return true
    } catch {
      showToast(bookmarked ? t('replay.addBookmarkFailed') : t('replay.removeBookmarkFailed'))
      return false
    }
  }, [onBookmarkChange, showToast, t])

  const bookmarkFromRow = useCallback(async (event: React.MouseEvent<HTMLButtonElement>, session: SessionSummary) => {
    event.preventDefault()
    event.stopPropagation()
    const changed = await toggleSessionBookmark(session)
    if (!changed || session.bookmarked) return

    setNoteEditorSession(null)
    setNotePopover({
      session: { id: session.id, agent_type: session.agent_type },
      anchor: {
        x: event.clientX,
        y: event.clientY,
        placement: event.clientY > window.innerHeight - 240 ? 'above' : 'below',
      },
    })
  }, [toggleSessionBookmark])

  const saveBookmarkNote = useCallback(async (session: BookmarkNoteTarget, note: string) => {
    try {
      await updateBookmarkNote(session, note)
      const change: BookmarkChange = {
        agentType: session.agent_type,
        sessionId: session.id,
        bookmarked: true,
        bookmarkNote: note,
      }
      setSessions(prev => applyBookmarkChange(prev, change))
      onBookmarkChange?.(change)
      showToast(note ? t('bookmark.noteSaved') : t('bookmark.noteCleared'))
    } catch {
      const message = t('bookmark.noteSaveFailed')
      showToast(message)
      throw new Error(message)
    }
  }, [onBookmarkChange, showToast, t])

  const filtered = useMemo(() => sessions.filter(s => {
    if (agentFilter && s.agent_type !== agentFilter) return false
    if (projectFilter && s.project !== projectFilter) return false
    if (modelFilter && !sessionMatchesModelFilter(s, modelFilter)) return false
    if (query.trim()) {
      const q = query.toLowerCase()
      const name = getSessionName(s).toLowerCase()
      const repo = (s.repository || '').toLowerCase()
      const branch = (s.branch || '').toLowerCase()
      const agent = (s.agent_type || '').toLowerCase()
      const sid = (s.id || '').toLowerCase()
      const contentHit = contentHits?.has(`${s.agent_type}\x00${s.id}`) ?? false
      if (!name.includes(q) && !repo.includes(q) && !branch.includes(q) && !agent.includes(q) && !sid.includes(q) && !contentHit) return false
    }
    return true
  }).sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()), [sessions, query, agentFilter, projectFilter, modelFilter, contentHits])

  const hasActiveFilters = Boolean(query || agentFilter || projectFilter || modelFilter)

  // A global-search result can be hidden by the sidebar's current filters and
  // by virtual scrolling. Reveal it, then move keyboard focus to its row.
  useEffect(() => {
    if (!focusTarget) return
    setAgentFilter(focusTarget.agentType)
    setProjectFilter('')
    setModelFilter('')
    setQuery('')
  }, [focusTarget])

  useEffect(() => {
    if (!focusTarget) return
    const index = filtered.findIndex(s => s.id === focusTarget.id && s.agent_type === focusTarget.agentType)
    if (index < 0) return

    listRef.current?.scrollIntoView({ index, align: 'center', behavior: 'auto' })
    let frame = 0
    const focusRow = () => {
      const row = asideRef.current?.querySelectorAll<HTMLButtonElement>('button[data-session-id]')
      const button = [...(row ?? [])].find(item => item.dataset.sessionId === focusTarget.id && item.dataset.agentType === focusTarget.agentType)
      if (button) {
        button.focus({ preventScroll: true })
      } else if (frame++ < 3) {
        requestAnimationFrame(focusRow)
      }
    }
    requestAnimationFrame(focusRow)
  }, [filtered, focusTarget])

  // 只有后端 reader 声明了删除能力（can_delete）的 agent 才显示删除入口。
  const deletableAgents = useMemo(
    () => new Set((agents ?? []).filter(a => a.can_delete).map(a => a.type)),
    [agents],
  )

  const handleSessionDeleted = useCallback((session: SessionSummary) => {
    setDeleteTarget(null)
    setSessions(list =>
      list.filter(s => !(s.id === session.id && s.agent_type === session.agent_type)),
    )
    onSessionDeleted?.(session)
    showToast(t('sidebar.deleted'))
  }, [onSessionDeleted, showToast, t])

  const liveCount = useMemo(() => sessions.filter(s => isSessionLive(s, now)).length, [sessions, now])

  const liveCountByAgent = useMemo(() => {
    const map = new Map<string, number>()
    for (const s of sessions) {
      if (isSessionLive(s, now) && s.agent_type) map.set(s.agent_type, (map.get(s.agent_type) ?? 0) + 1)
    }
    return map
  }, [sessions, now])

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

  const modelEntries = useMemo<ModelEntry[]>(() => {
    const counts = new Map<string, {
      meta: ReturnType<typeof modelMeta>
      modelName: string
      sessionCount: number
      providers: Map<string, { provider: string; providerKey: string; sessionCount: number }>
    }>()
    for (const s of sessions) {
      if (!s.model_name) continue
      if (agentFilter && s.agent_type !== agentFilter) continue
      if (projectFilter && s.project !== projectFilter) continue
      const meta = modelMeta(s.model_name, s.model_provider)
      const key = sessionModelKey(s)
      const entry = counts.get(key)
      const providerKey = sessionModelProviderKey(s)
      if (entry) {
        entry.sessionCount++
        const provider = entry.providers.get(providerKey)
        if (provider) provider.sessionCount++
        else entry.providers.set(providerKey, { provider: meta.provider, providerKey: meta.providerKey, sessionCount: 1 })
      } else {
        counts.set(key, {
          meta,
          modelName: s.model_name,
          sessionCount: 1,
          providers: new Map([[providerKey, { provider: meta.provider, providerKey: meta.providerKey, sessionCount: 1 }]]),
        })
      }
    }
    return [...counts.entries()]
      .map(([key, entry]) => {
        const providers = [...entry.providers.entries()]
          .map(([providerKey, provider]) => ({
            key: providerKey,
            provider: provider.provider,
            providerKey: provider.providerKey,
            session_count: provider.sessionCount,
          }))
          .sort((a, b) => b.session_count - a.session_count || a.provider.localeCompare(b.provider))
        return {
          key,
          name: entry.modelName,
          session_count: entry.sessionCount,
          ...entry.meta,
          providerSummary: providers.length === 1 ? providers[0].provider : `${providers.length} Providers`,
          providers,
        }
      })
      .sort((a, b) => b.session_count - a.session_count || a.label.localeCompare(b.label))
  }, [sessions, agentFilter, projectFilter])

  // Reset project filter when the selected project disappears from the list
  // (e.g. after switching agent filter).
  useEffect(() => {
    if (projectFilter && !projectEntries.some(p => p.name === projectFilter)) {
      setProjectFilter('')
    }
  }, [projectFilter, projectEntries])

  useEffect(() => {
    if (modelFilter && !modelEntries.some(m => m.key === modelFilter || m.providers.some(p => p.key === modelFilter))) {
      setModelFilter('')
    }
  }, [modelFilter, modelEntries])

  const SessionRow = ({ session }: { session: SessionSummary }) => {
    const selected = session.id === selectedId && (!selectedAgentType || session.agent_type === selectedAgentType)
    const repo = session.repository || ''
    const branch = session.branch || ''

    const parts: string[] = []
    if (repo) {
      const shortRepo = repo.split('/').slice(-2).join('/')
      parts.push(shortRepo)
    }
    if (branch) parts.push(branch)
    if ((session.rolled_back_turn_count ?? 0) > 0) {
      parts.push(`${formatNumber(locale, session.turn_count)} ${t('sidebar.turns')} · +${formatNumber(locale, session.rolled_back_turn_count ?? 0)} ${t('replay.rolledBack')}`)
    } else {
      parts.push(`${formatNumber(locale, session.message_count || session.turn_count)} ${t('sidebar.messages')}`)
    }
    const live = isSessionLive(session, now)
    if (live) parts.push(t('sidebar.live'))
    const metadata = parts.join(' · ')
    const relativeTime = formatRelativeTime(session.updated_at, now, locale)

    return (
      <div className="group relative">
        <button
          data-session-id={session.id}
          data-agent-type={session.agent_type}
          onClick={() => onSelect(session.id, session.agent_type)}
          onContextMenu={(e) => openContextMenu(e, session)}
          title={t('sidebar.openMenu')}
          className={`relative w-full text-left pl-2.5 pr-20 rounded-md cursor-pointer transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)] ${
            selected ? 'bg-[var(--bg-surface-hover)]' : 'hover:bg-[var(--bg-surface-hover)]'
          }`}
          style={{ paddingTop: '0.375rem', paddingBottom: '0.375rem' }}
        >
          {selected && <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-[var(--accent-blue)]" />}
          <div className="flex items-start gap-1.5">
            <AgentIcon agentType={session.agent_type} size={20} className="mt-0.5" />
            <div className="min-w-0 flex-1">
              <div className="text-body text-[var(--text-primary)] truncate flex items-center gap-1.5">
                {live && <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] flex-shrink-0 animate-pulse" title={t('sidebar.live')} aria-label={t('sidebar.live')} />}
                <span className="truncate">{getSessionName(session)}</span>
              </div>
              <div className="text-helper text-[var(--text-secondary)] mt-0.5 flex items-center gap-2">
                <span className="truncate min-w-0">{metadata}</span>
                <time className="ml-auto flex-shrink-0 tabular-nums" dateTime={session.updated_at} title={formatDate(locale, session.updated_at, { dateStyle: 'medium', timeStyle: 'short' })}>
                  {relativeTime}
                </time>
              </div>
            </div>
          </div>
        </button>
        {session.bookmarked && session.bookmark_note?.trim() && (
          <InstantTooltip
            text={t('bookmark.noteWithValue', { note: session.bookmark_note.trim() })}
            placement="top"
            className="absolute right-[3.25rem] top-1.5 flex h-5 w-5 items-center justify-center text-[var(--text-secondary)]"
          >
            <button
              type="button"
              onClick={event => { event.stopPropagation(); setNoteEditorSession(session) }}
              className="flex h-5 w-5 items-center justify-center rounded-sm hover:bg-[var(--bg-inset)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              aria-label={t('replay.editBookmarkNote')}
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M5 3.75h10.5L20 8.25v12H5z" />
                <path d="M15.5 3.75v4.5H20" />
                <path d="M8.5 13h7M8.5 16.5h5" />
              </svg>
            </button>
          </InstantTooltip>
        )}
        <InstantTooltip
          text={session.bookmarked ? t('replay.removeBookmark') : t('replay.bookmark')}
          placement="top"
          className="absolute right-7 top-1.5"
        >
          <button
            type="button"
            onClick={event => { void bookmarkFromRow(event, session) }}
            className={`flex h-5 w-5 items-center justify-center rounded-sm transition-opacity duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
              session.bookmarked
                ? 'text-[var(--accent-blue)] opacity-100 hover:bg-[var(--bg-inset)]'
                : 'text-[var(--text-muted)] opacity-0 hover:bg-[var(--bg-inset)] hover:text-[var(--warning)] group-hover:opacity-100 group-focus-within:opacity-100 max-md:opacity-100'
            }`}
            aria-label={session.bookmarked ? t('replay.removeBookmark') : t('replay.bookmark')}
          >
            <StarIcon size={14} filled={session.bookmarked} strokeWidth={1.75} />
          </button>
        </InstantTooltip>
        <button
          onClick={(e) => { e.stopPropagation(); copyId(session) }}
          tabIndex={-1}
          className="absolute right-1 top-1.5 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-inset)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 max-md:opacity-100 transition-opacity duration-fast"
          aria-hidden="true"
          title={t('sidebar.copySessionId')}
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
          <h2 className="text-nav font-semibold text-[var(--text-primary)]">{t('sidebar.sessions')}</h2>
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
      aria-label={t('sidebar.sessions')}
    >
      {/* Header */}
      <div className="p-4 flex items-center justify-between flex-shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <h2 className="text-nav font-semibold text-[var(--text-primary)] truncate">{t('sidebar.sessions')}</h2>
          <span className="text-helper text-[var(--text-muted)] flex-shrink-0">{formatNumber(locale, sessions.length)} {t('sidebar.total')}</span>
          {liveCount > 0 && (
            <span className="text-helper text-[var(--success)] flex-shrink-0 flex items-center gap-1">
              <span className="w-1.5 h-1.5 rounded-full bg-[var(--success)] animate-pulse" />
              {formatNumber(locale, liveCount)} {t('sidebar.live')}
            </span>
          )}
          {disconnected && (
            <span className="text-helper text-[var(--warning)] flex-shrink-0" title={t('sidebar.disconnectedHelp')}>
              {t('sidebar.disconnected')}
            </span>
          )}
        </div>
        {isMobile && onClose && (
          <button
            onClick={onClose}
            aria-label={t('sidebar.close')}
            className="ml-1 w-6 h-6 flex items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        )}
      </div>

      {/* Filter Bar */}
      <div className="px-4 pb-2 flex-shrink-0">
        <div className="relative min-w-0 flex-1">
          <svg className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[var(--text-muted)] pointer-events-none" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            ref={searchRef}
            type="text"
            placeholder={t('sidebar.filter')}
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="w-full h-[34px] rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] pl-8 pr-7 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20 focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)]"
          />
          {query && (
            <button
              onClick={() => setQuery('')}
              className="absolute right-1 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center rounded-sm text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              aria-label={t('sidebar.clearFilter')}
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

      <ModelFilter
        models={modelEntries}
        selected={modelFilter}
        onSelect={setModelFilter}
      />

      {/* Session List */}
      <div className="px-2 flex-1 min-h-0">
        {error ? (
          <div className="px-3 py-8 text-center">
            <div className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-md bg-[var(--bg-inset)] text-helper text-[var(--warning)]">!</div>
            <div className="text-nav font-medium text-[var(--text-primary)]">{t('sidebar.backendUnavailable')}</div>
            <div className="mt-1 text-helper text-[var(--text-muted)]">{t('sidebar.backendUnavailableHelp')}</div>
          </div>
        ) : filtered.length === 0 ? (
          <div className="px-3 py-8 text-center">
            <div className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-md bg-[var(--bg-inset)] text-helper text-[var(--text-muted)]">⌕</div>
            <div className="text-nav font-medium text-[var(--text-primary)]">{t('sidebar.noMatches')}</div>
            <div className="mt-1 text-helper text-[var(--text-muted)]">{t('sidebar.tryAnother')}</div>
            {hasActiveFilters && (
              <button
                onClick={() => {
                  setQuery('')
                  setAgentFilter('')
                  setProjectFilter('')
                  setModelFilter('')
                }}
                className="mt-3 h-7 px-3 rounded-md border border-[var(--border-default)] text-meta text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                {t('sidebar.clearFilters')}
              </button>
            )}
          </div>
        ) : (
          <Virtuoso
            ref={listRef}
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

      {/* Footer: 常驻版本号，点击 ⓘ 打开设置「关于」 */}
      <div className="flex flex-shrink-0 items-center justify-between border-t border-[var(--border-muted)] px-4 py-1.5">
        <span className="truncate text-meta text-[var(--text-muted)]" data-testid="sidebar-version">
          Session Insight{version ? ` ${version}` : ''}
        </span>
        <InstantTooltip text={t('sidebar.about')} placement="top">
          <button
            type="button"
            aria-label={t('sidebar.about')}
            onClick={() =>
              window.dispatchEvent(new CustomEvent('si-open-settings', { detail: { tab: 'about' } }))
            }
            className="ml-2 flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            <InfoIcon className="h-3.5 w-3.5" />
          </button>
        </InstantTooltip>
      </div>

      {/* Resize handle (desktop only) */}
      {!isMobile && (
        <div
          onPointerDown={beginResize}
          className="absolute right-0 top-0 h-full w-2 cursor-col-resize hover:bg-[var(--accent-blue)]/30 active:cursor-col-resize"
          title={t('sidebar.resize')}
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
            className="fixed z-[var(--z-toast,50)] w-60 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-lg py-1 text-body"
            style={{ left: contextMenu.x, top: contextMenu.y }}
          >
            <InstantTooltip
              text={
                contextMenu.session.bookmarked
                  ? (contextMenu.session.bookmark_note?.trim()
                    ? t('replay.bookmarkedWithNote', { note: contextMenu.session.bookmark_note.trim() })
                    : t('replay.bookmarkedWithoutNote'))
                  : t('replay.bookmarkHint')
              }
              placement="cursor"
              className="block w-full"
            >
              <button
                className="w-full text-left px-3 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
                onClick={() => { void toggleSessionBookmark(contextMenu.session); setContextMenu(null) }}
              >
                {contextMenu.session.bookmarked ? t('replay.removeBookmark') : t('replay.bookmark')}
              </button>
            </InstantTooltip>
            {contextMenu.session.bookmarked && (
              <InstantTooltip
                text={contextMenu.session.bookmark_note?.trim() || t('replay.noBookmarkReason')}
                placement="cursor"
                className="block w-full"
              >
                <button
                  className="w-full text-left px-3 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
                  onClick={() => {
                    setNotePopover(null)
                    setNoteEditorSession(contextMenu.session)
                    setContextMenu(null)
                  }}
                >
                  {contextMenu.session.bookmark_note?.trim() ? t('replay.editBookmarkNote') : t('replay.addBookmarkNote')}
                </button>
              </InstantTooltip>
            )}
            <button
              className="w-full text-left px-3 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              onClick={() => { copyId(contextMenu.session); setContextMenu(null) }}
            >
              {t('sidebar.copySessionId')}
            </button>
            <button
              className="w-full text-left px-3 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
              onClick={() => { void copyAgentAndId(contextMenu.session); setContextMenu(null) }}
            >
              {t('sidebar.copyAgentAndId')}
            </button>
            {(() => {
              const session = contextMenu.session
              const stored = readStorage(getResumePreferenceKey(session))
              const preferred = stored === 'powershell' || stored === 'git-bash' ? stored : null
              const options = getResumeCommandOptions(session, preferred)
              const isLive = isSessionLive(contextMenu.session, now)
              const windows = isWindowsSession(session, hostIsWindows())
              const visibleOptions = windows ? options : options.filter(option => option.shell === 'git-bash')
              const disabled = visibleOptions.length === 0
              if (disabled) return <button className="w-full text-left px-3 py-1.5 text-[var(--text-muted)] cursor-not-allowed" disabled title={t('sidebar.resumeUnsupported')}>{t('sidebar.copyResume')}</button>
              const rollbackInfo = (session.rolled_back_turn_count ?? 0) > 0
                ? t('sidebar.rollbackInfo', { count: session.rolled_back_turn_count ?? 0, turn: session.turn_count })
                : ''
              return <>
                {rollbackInfo && (
                  <div className="px-3 py-1.5 text-meta text-[var(--warning)] border-y border-[var(--border-muted)]">
                    ↩ {rollbackInfo}
                  </div>
                )}
                {visibleOptions.map(option => {
                const shellLabel = windows ? option.shell === 'powershell' ? 'PowerShell' : 'Git Bash' : ''
                const reason = option.reason === 'last-used' ? t('sidebar.lastUsed') : windows && option.reason === 'recommended' ? t('sidebar.recommended') : ''
                const dangerous = option.mode === 'skip-permissions'
                return (
                  <button
                    key={`${option.shell}:${option.mode}`}
                    className="w-full text-left px-3 py-1.5 text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
                    title={dangerous ? t('sidebar.unsafeHint', { rollback: rollbackInfo ? `; ${rollbackInfo}` : '' }) : rollbackInfo || undefined}
                    onClick={() => {
                      if (isLive) setResumeConfirm({ session, shell: option.shell, mode: option.mode })
                      else void copyResumeCmd(session, option.shell, option.mode)
                      setContextMenu(null)
                    }}
                  >
                    {dangerous ? t('sidebar.copyResumeUnsafe') : t('sidebar.copyResume')}
                    {(shellLabel || dangerous) && (
                      <> ({[shellLabel, reason].filter(Boolean).join(', ')}{(shellLabel || reason) && dangerous ? ', ' : ''}{dangerous && <span className="text-[var(--error)]">{t('sidebar.unsafe')}</span>})</>
                    )}
                  </button>
                )
                })}
              </>
            })()}
            {deletableAgents.has(contextMenu.session.agent_type) && (
              <>
                <div className="my-1 border-t border-[var(--border-default)]" />
                <button
                  className="w-full text-left px-3 py-1.5 text-[var(--error)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast"
                  onClick={() => { setDeleteTarget(contextMenu.session); setContextMenu(null) }}
                >
                  {t('sidebar.deleteSession')}
                </button>
              </>
            )}
          </div>
        </>,
        document.body
      )}

      {noteEditorSession && (
        <BookmarkNoteEditor
          session={noteEditorSession}
          onSave={saveBookmarkNote}
          onClose={() => setNoteEditorSession(null)}
        />
      )}

      {notePopover && (
        <BookmarkNoteEditor
          session={notePopover.session}
          anchor={notePopover.anchor}
          title={t('bookmark.addNoteTitle')}
          onSave={saveBookmarkNote}
          onClose={() => setNotePopover(null)}
        />
      )}

      {/* Delete confirmation dialog */}
      {deleteTarget && (
        <DeleteSessionDialog
          session={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={handleSessionDeleted}
        />
      )}

      {/* 活跃会话复制恢复命令的再确认弹窗 */}
      {resumeConfirm && createPortal(
        <>
          <div
            className="fixed inset-0 z-[calc(var(--z-toast,50)+1)] bg-[rgba(0,0,0,var(--opacity-overlay,0.4))]"
            onClick={() => setResumeConfirm(null)}
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-label={t('sidebar.resumeTitle')}
            className="fixed left-1/2 top-1/3 z-[calc(var(--z-toast,50)+2)] w-[420px] -translate-x-1/2 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-4 shadow-lg"
          >
            <h3 className="text-nav font-semibold text-[var(--text-primary)]">{t('sidebar.resumeTitle')}</h3>
            <div className="mt-2 text-body text-[var(--text-secondary)] break-all">
              <span className="text-[var(--text-primary)]">{getSessionName(resumeConfirm.session)}</span>
              <span className="ml-1.5 text-meta text-[var(--text-muted)]">{getAgentLabel(resumeConfirm.session.agent_type)}</span>
            </div>
            <p className="mt-3 text-body text-[var(--text-secondary)]">
              {t('sidebar.resumeLiveWarning')}
            </p>
            {(resumeConfirm.session.rolled_back_turn_count ?? 0) > 0 && (
              <p className="mt-2 text-body text-[var(--warning)]">
                {t('sidebar.resumeRollback', { count: resumeConfirm.session.rolled_back_turn_count ?? 0, turn: resumeConfirm.session.turn_count })}
              </p>
            )}
            <div className="mt-4 flex items-center justify-end gap-2">
              <button
                onClick={() => setResumeConfirm(null)}
                className="h-7 px-3 rounded-md border border-[var(--border-default)] text-nav text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)] transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                {t('common.cancel')}
              </button>
              <button
                onClick={() => {
                  const target = resumeConfirm
                  setResumeConfirm(null)
                  void copyResumeCmd(target.session, target.shell, target.mode)
                }}
                className="h-7 px-3 rounded-md bg-[var(--accent-blue)] text-nav text-white hover:opacity-90 transition-opacity duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                {t('sidebar.copyAnyway')}
              </button>
            </div>
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
