import { lazy, Suspense, useCallback, useEffect, useState, useRef, useMemo, startTransition } from 'react'
import { addBookmark, fetchLiveRevision, fetchPositions, fetchSession, fetchSessionEdits, fetchSettings, openFile, removeBookmark, resolveFile, updateBookmarkNote } from '../api'
import { DEFAULT_FILE_OPEN_EXTS, extractPathsAt, parseExtList } from '../filePathDetection'
import type { EditCall, PositionsResponse, SessionDetail } from '../types'
import type { BookmarkChange } from '../bookmarkState'
import type { ScrollMetrics } from '../minimapGeometry'
import { TERMINAL_LINE_HEIGHT, type TerminalActivateMeta, type TerminalContextMenuEvent, type TerminalControl, type UserHighlightRange } from '../terminalControl'
import MiniMap, { type MiniMapControl } from './MiniMap'
import GlobalSearch from './GlobalSearch'
import AIPanel from './AIPanel'
import BookmarkNoteEditor from './BookmarkNoteEditor'
import DiffModal from './DiffModal'
import InstantTooltip from './InstantTooltip'
import OutputModal from './OutputModal'
import TerminalContextMenu, { type TerminalMenuSection } from './TerminalContextMenu'
import TerminalSearchBar from './TerminalSearchBar'
import ToolCallPanel from './ToolCallPanel'
import UserMessagePanel from './UserMessagePanel'
import { getVisibleTurnRange, isSameVisibleRange, type VisibleTurnRange } from '../scrollSync'
import { parseEditHeaderLine } from '../terminalInteractionGeometry'
import { foldKeysInTurn, foldsFromPositions } from '../terminalFolds'
import { isSessionLive, LIVE_WINDOW_MS } from '../sidebarRows'
import { getNavOpenPref } from '../navPrefs'
import { formatDate, formatNumber, useI18n, type Locale } from '../i18n'

const AnalyticsView = lazy(() => import('./AnalyticsView'))
const TerminalPanel = lazy(() => import('./TerminalPanel'))

type ReplayScrollBehavior = 'auto' | 'smooth'
type JumpTarget = 'turn' | 'user'
type ViewMode = 'terminal' | 'analytics'

interface Props {
  sessionId: string | null
  searchTarget?: { sessionId: string; agentType: string; query: string } | null
  onSelect?: (id: string, agentType?: string, focusSidebar?: boolean, searchQuery?: string) => void
  bookmarkChange?: BookmarkChange | null
  onBookmarkChange?: (change: BookmarkChange) => void
}

function fmtTokens(n: number, locale: Locale): string {
  return formatNumber(locale, n)
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${totalSeconds % 60}s`
  return `${totalSeconds}s`
}

export default function ReplayView({ sessionId, searchTarget, onSelect, bookmarkChange, onBookmarkChange }: Props) {
  const { locale, t } = useI18n()
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [viewMode, setViewMode] = useState<ViewMode>('terminal')
  const [showHelp, setShowHelp] = useState(false)
  const [showDiffModal, setShowDiffModal] = useState(false)
  const [initialDiffIdx, setInitialDiffIdx] = useState(0)
  const [terminalCols, setTerminalCols] = useState<number | null>(null)
  const [positionsData, setPositionsData] = useState<PositionsResponse | null>(null)
  const [positionsBuilding, setPositionsBuilding] = useState(false)
  const [foldVersion, setFoldVersion] = useState(0)
  const [outputModalIdx, setOutputModalIdx] = useState<number | null>(null)
  const [edits, setEdits] = useState<EditCall[]>([])
  const [showToolPanel, setShowToolPanel] = useState(false)
  const [showUserPanel, setShowUserPanel] = useState(false)
  // When pinned, click-outside does not close the nav overlay.
  const [navPinned, setNavPinned] = useState(false)
  const [navPanelWidth, setNavPanelWidth] = useState(0)
  const [showAIPanel, setShowAIPanel] = useState(false)
  // Live follow (tail -f): pin viewport to bottom on every live refresh.
  // Only offered for active sessions; cleared when the session goes idle or changes.
  const [followOutput, setFollowOutput] = useState(false)
  // Session id that already auto-engaged follow on open (null = not yet / not
  // live). Prevents detail refetches from re-enabling follow after the user
  // turned it off; reset by the session-switch effect below.
  const autoFollowSessionRef = useRef<string | null>(null)
  // Per-session view memory: follow choice + scroll position, saved when
  // switching away so switching back restores where the user left instead of
  // re-opening at the default (top / auto-follow tail).
  const sessionViewMemoryRef = useRef(new Map<string, { follow: boolean; wasLive: boolean; viewportLine: number | null }>())
  const prevSessionIdRef = useRef<string | null>(null)
  // Mirror read by the session-switch effect (it saves the outgoing
  // session's effective follow value; state setters in the same render
  // would race).
  const followOutputRef = useRef(followOutput)
  followOutputRef.current = followOutput
  // Latest session detail as a ref: the session-switch effect reads the
  // OUTGOING session's detail through it without taking session as a dep
  // (a detail-arrival re-run would clobber the restored view state).
  const sessionDetailRef = useRef<SessionDetail | null>(null)
  sessionDetailRef.current = session
  // One-shot scroll target passed to TerminalPanel when revisiting a session
  // with follow off (buffer line the user left at); null = default position.
  const [restoreScrollLine, setRestoreScrollLine] = useState<number | null>(null)
  // 时间戳前缀设置(后端 ts 渲染参数);null = 设置未加载,先不挂终端,
  // 避免渲染与 positions 用了不同的 ts 导致行号错位。
  const [tsKinds, setTsKinds] = useState<string | null>(null)
  const [bookmarkBusy, setBookmarkBusy] = useState(false)
  const [bookmarkError, setBookmarkError] = useState<string | null>(null)
  const [noteEditorOpen, setNoteEditorOpen] = useState(false)
  const termControlRef = useRef<TerminalControl | null>(null)
  const miniMapControlRef = useRef<MiniMapControl | null>(null)
  const scrollToIndexRef = useRef<((index: number, behavior?: ReplayScrollBehavior) => void) | null>(null)
  const scrollToTopRef = useRef<((top: number, behavior?: ScrollBehavior) => void) | null>(null)
  const visibleRangeRef = useRef<VisibleTurnRange>()
  const visibleRangeLabelRef = useRef<HTMLSpanElement>(null)
  const jumpBaseRef = useRef(0)
  const pollTimerRef = useRef<ReturnType<typeof setTimeout>>()
  const lastMetricsRef = useRef<ScrollMetrics>()
  // Scroll metrics are emitted only while the terminal view is active. Keep
  // their source session alongside them so a stale terminal callback cannot
  // become the saved scroll position for a different session on navigation.
  const lastMetricsSessionIdRef = useRef<string | null>(null)

  // Matcher callbacks read positions through a ref: matchers are registered
  // once (on cols-ready) but must see the latest positions and fold mapping.
  const positionsRef = useRef<PositionsResponse | null>(null)
  useEffect(() => { positionsRef.current = positionsData }, [positionsData])
  const sessionCwdRef = useRef('')
  useEffect(() => { sessionCwdRef.current = session?.cwd ?? '' }, [session])
  // Hover-time path existence results, keyed by cwd+path (rows repeat paths).
  const pathCheckCache = useRef(new Map<string, boolean>())
  useEffect(() => { pathCheckCache.current.clear() }, [sessionId])
  // "Session-relevant file" extension allowlist from settings (null = allow
  // all via '*'); rows whose tokens all fall outside it get no affordance.
  const fileExtsRef = useRef<Set<string> | null>(new Set(DEFAULT_FILE_OPEN_EXTS))
  useEffect(() => {
    let cancelled = false
    const load = () => {
      fetchSettings()
        .then(s => {
          if (cancelled) return
          fileExtsRef.current = parseExtList(s.file_open_extensions ?? '')
          setTsKinds(s.timestamp_kinds ?? '')
        })
        .catch(() => { if (!cancelled) setTsKinds('') })
    }
    load()
    // 设置面板保存后广播;时间戳选项变化会触发终端重渲染。
    window.addEventListener('si-settings-changed', load)
    return () => {
      cancelled = true
      window.removeEventListener('si-settings-changed', load)
    }
  }, [])

  // Resolve the first candidate on the row that exists on disk (cached).
  const resolveRowFile = useCallback(async (lineText: string, column: number | null): Promise<{ path: string; line: number | null } | null> => {
    const cwd = sessionCwdRef.current
    for (const cand of extractPathsAt(lineText, column, fileExtsRef.current)) {
      const key = cwd + '\0' + cand.path
      let ok = pathCheckCache.current.get(key)
      let resolved: string | null = null
      if (ok === undefined) {
        resolved = await resolveFile(cand.path, cwd).catch(() => null)
        ok = resolved !== null
        pathCheckCache.current.set(key, ok)
      } else if (ok) {
        resolved = await resolveFile(cand.path, cwd).catch(() => null)
      }
      if (ok && resolved) return { path: resolved, line: cand.line }
    }
    return null
  }, [])

  // Left-click on a row with file context opens a small action popover at the
  // cursor (editor / new tab / diff) instead of a single hard-wired action.
  // foldKey (from path-bearing tool headers) adds 展开/收起 next to open-file.
  const openFilePopover = useCallback((bufLine: number, meta: TerminalActivateMeta | undefined, editIdx: number | null) => {
    if (!meta) return
    const ctrl = termControlRef.current
    setCtxMenu({
      clientX: meta.clientX,
      clientY: meta.clientY,
      originalRow: ctrl ? ctrl.toOriginalLine(bufLine) : bufLine,
      column: meta.column,
      lineText: meta.lineText,
      collapsedFoldKeys: ctrl?.getCollapsedFoldKeys() ?? [],
      fileOnly: true,
      editIdx: editIdx ?? undefined,
      foldKey: meta.foldKey,
    })
  }, [])

  const registerMatchers = useCallback(() => {
    termControlRef.current?.setLineMatchers([
      {
        // ✏️ diff headers → file action popover (open in editor / new tab /
        // diff detail). matchIndex over the visible buffer breaks once a fold
        // hides some headers, so the click is resolved back to the original
        // row and looked up among the "edit" positions; matchIndex stays as
        // fallback when positions are unavailable.
        match: (text: string) => parseEditHeaderLine(text),
        tooltip: t('replay.fileActions'),
        onActivate: (bufLine: number, _data: unknown, matchIndex: number, meta?: TerminalActivateMeta) => {
          const ctrl = termControlRef.current
          const orig = ctrl ? ctrl.toOriginalLine(bufLine) : bufLine
          const editPositions = (positionsRef.current?.positions ?? [])
            .filter(p => p.kind === 'edit')
            .sort((a, b) => a.line_start - b.line_start)
          const idx = editPositions.findIndex(p => p.line_start === orig)
          openFilePopover(bufLine, meta, idx >= 0 ? idx : matchIndex)
        },
      },
      {
        // "[+] N 行被截断（点击展开）" lines → full output modal via the
        // "trunc" position at the same original row.
        match: (text: string) => (/\[\+\] \d+ 行被截断/.test(text) ? {} : null),
        tooltip: t('replay.expandOutput'),
        onActivate: (bufLine: number, _data: unknown, matchIndex: number) => {
          const ctrl = termControlRef.current
          const orig = ctrl ? ctrl.toOriginalLine(bufLine) : bufLine
          const pos = (positionsRef.current?.positions ?? [])
            .find(p => p.kind === 'trunc' && p.line_start === orig)
          const idx = pos && typeof pos.payload?.output_index === 'number'
            ? (pos.payload.output_index as number)
            : matchIndex
          setOutputModalIdx(idx)
        },
      },
      {
        // Rows containing an allowlisted file-path token → same file popover.
        // Registered last: edit headers and truncation lines take precedence.
        // validate: the affordance only appears when some candidate on the
        // row actually resolves to an existing file (multi-token rows check
        // every candidate, so "cd /some/dir && vim a.vue" still qualifies).
        match: (text: string) => (extractPathsAt(text, null, fileExtsRef.current).length > 0 ? {} : null),
        tooltip: t('replay.openFileMenu'),
        validate: async (lineText: string) => (await resolveRowFile(lineText, null)) !== null,
        onActivate: (bufLine: number, _data: unknown, _idx: number, meta?: TerminalActivateMeta) => {
          openFilePopover(bufLine, meta, null)
        },
      },
    ])
  }, [openFilePopover, t])

  // 列数没变时(例如「分析↔终端」来回切换导致的终端重挂载)必须保留现有
  // positions:positions 拉取 effect 的依赖不会变化、不会重拉,这里若无条件
  // 清空,工具列表/折叠/minimap 标记会永久丢失。
  const lastColsRef = useRef<number | null>(null)
  const handleColsReady = useCallback((cols: number) => {
    if (lastColsRef.current !== cols) {
      lastColsRef.current = cols
      setPositionsData(null)
      setTerminalCols(cols)
    }
    registerMatchers()
  }, [registerMatchers])

  // Fold ranges extracted from positions; TerminalPanel owns collapse state.
  const folds = useMemo(() => foldsFromPositions(positionsData?.positions), [positionsData])
  const handleFoldChange = useCallback(() => setFoldVersion(v => v + 1), [])
  const turns = useMemo(() => session?.turns ?? [], [session?.turns])
  const rolledBackTurns = useMemo(
    () => session?.rollback_groups?.flatMap(group => group.turns) ?? [],
    [session?.rollback_groups],
  )
  const userIndexes = useMemo(() => turns
    .map((turn, index) => turn.user_message ? index : -1)
    .filter(index => index >= 0), [turns])

  const jump = useCallback((direction: -1 | 1, target: JumpTarget) => {
    const barCount = turns.length
    if (barCount === 0) return
    const base = jumpBaseRef.current
    let targetIndex: number

    if (target === 'turn') {
      targetIndex = Math.max(0, Math.min(base + direction, barCount - 1))
    } else {
      const found = direction > 0
        ? userIndexes.find(i => i > base)
        : [...userIndexes].reverse().find(i => i < base)
      if (found === undefined) return
      targetIndex = found
    }

    jumpBaseRef.current = targetIndex
    scrollToIndexRef.current?.(targetIndex)
  }, [turns.length, userIndexes])

  // Terminal context menu: opened by right-click with a snapshot of the
  // collapse state so item enablement is stable while the menu is up.
  const [ctxMenu, setCtxMenu] = useState<(TerminalContextMenuEvent & { fileOnly?: boolean; editIdx?: number }) | null>(null)
  const handleTerminalContextMenu = useCallback((e: TerminalContextMenuEvent) => setCtxMenu(e), [])
  useEffect(() => { setCtxMenu(null) }, [sessionId, viewMode])

  const toggleBookmark = useCallback(async () => {
    if (!session || bookmarkBusy) return
    const nextBookmarked = !session.bookmarked
    setBookmarkBusy(true)
    setBookmarkError(null)
    try {
      if (nextBookmarked) await addBookmark(session)
      else await removeBookmark(session)
      setSession(prev => prev
        ? {
            ...prev,
            bookmarked: nextBookmarked,
            bookmark_note: nextBookmarked ? prev.bookmark_note : undefined,
          }
        : prev)
      if (!nextBookmarked) setNoteEditorOpen(false)
      onBookmarkChange?.({
        agentType: session.agent_type,
        sessionId: session.id,
        bookmarked: nextBookmarked,
        bookmarkNote: nextBookmarked ? (session.bookmark_note ?? '') : undefined,
      })
    } catch {
      setBookmarkError(nextBookmarked ? 'replay.addBookmarkFailed' : 'replay.removeBookmarkFailed')
    } finally {
      setBookmarkBusy(false)
    }
  }, [bookmarkBusy, onBookmarkChange, session])

  // Live tail: poll the stat-level revision every few seconds; on change,
  // apply the new render incrementally (append when possible) and bump
  // contentVersion so positions/detail refetch. Polling stops permanently for
  // agents without live-revision support (404 → null).
  const [contentVersion, setContentVersion] = useState(0)
  useEffect(() => {
    if (!sessionId || viewMode !== 'terminal') return
    let stopped = false
    let lastRev: number | null = null
    // Anchor expiry to the source-backed session timestamp. Starting this at
    // Date.now() makes a transcript that was already stale when opened wait a
    // second full live window before its cached progress row is redrawn.
    const sourceUpdatedAt = session?.id === sessionId
      ? Date.parse(session.updated_at)
      : Number.NaN
    let lastRevChangeAt = Number.isFinite(sourceUpdatedAt) ? sourceUpdatedAt : Date.now()
    // One-shot cleanup for the backend's "推理中…" row: that row is emitted
    // only while the session file was written within the backend live window
    // (model.LiveWindow, 5 min). A session interrupted/killed mid-turn stops
    // writing, so the revision never changes again and no poll would ever
    // redraw — this flag forces exactly one refresh after the window passes
    // so the stale row disappears without a page reload.
    let staleRowCleaned = false
    let timer: ReturnType<typeof setTimeout>
    const tick = async () => {
      if (stopped) return
      const rev = await fetchLiveRevision(sessionId).catch(() => null)
      if (stopped) return
      if (rev === null) return // unsupported → no live tail for this agent
      if (lastRev !== null && rev !== lastRev) {
        lastRevChangeAt = Date.now()
        staleRowCleaned = false
        const result = await termControlRef.current?.refreshContent().catch(() => 'unchanged' as const)
        if (!stopped && result && result !== 'unchanged') {
          setContentVersion(v => v + 1)
        }
      } else if (!staleRowCleaned && Date.now() - lastRevChangeAt >= LIVE_WINDOW_MS) {
        // TerminalPanel loads lazily. Do not consume the one-shot cleanup
        // before its control exists, or a stale row can survive forever.
        const control = termControlRef.current
        if (control) {
          const result = await control.refreshContent().catch(() => 'unchanged' as const)
          if (!stopped) {
            staleRowCleaned = true
            if (result !== 'unchanged') setContentVersion(v => v + 1)
          }
        }
      }
      lastRev = rev
      timer = setTimeout(tick, 3000)
    }
    void tick()
    return () => { stopped = true; clearTimeout(timer) }
  }, [sessionId, viewMode, session?.id, session?.updated_at])

  // Content grew: refresh the turn list / header stats (and the live badge).
  useEffect(() => {
    if (contentVersion === 0 || !sessionId) return
    fetchSession(sessionId).then(setSession).catch(() => {})
  }, [contentVersion, sessionId])

  // 活跃徽标的客户端衰减时钟（与 Sidebar 同一套 isSessionLive 判定）。
  // 只在快照仍为活跃时才 tick——停在旧会话上不必每分钟重渲染整棵树。
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    if (!session?.is_live) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [session?.is_live, session?.id])

  // Session switch: save the outgoing session's view (follow choice + scroll
  // position) and restore the incoming one's. A memory saved while the
  // session was LIVE suppresses auto-follow on return — the user's explicit
  // pause survives the round trip. Memories of non-live sessions only
  // restore the scroll position, so a session that has since gone live still
  // gets the fresh auto-follow behavior.
  useEffect(() => {
    const prevId = prevSessionIdRef.current
    if (prevId && prevId !== sessionId) {
      // sessionIsLive is id-gated and already reads false for the outgoing
      // session on this render, so compute outgoing liveness from the detail
      // itself (still the outgoing session's at this point). Fallback: an
      // engaged follow implies the session was live.
      const prevDetail = sessionDetailRef.current
      const outgoingLive = prevDetail && prevDetail.id === prevId
        ? isSessionLive(prevDetail, Date.now())
        : followOutputRef.current
      const m = lastMetricsRef.current
      sessionViewMemoryRef.current.set(prevId, {
        follow: followOutputRef.current,
        wasLive: outgoingLive,
        viewportLine: m && lastMetricsSessionIdRef.current === prevId && m.scrollHeight > m.clientHeight
          ? Math.round(m.scrollTop / TERMINAL_LINE_HEIGHT)
          : null,
      })
    }
    prevSessionIdRef.current = sessionId
    const saved = sessionId ? sessionViewMemoryRef.current.get(sessionId) : undefined
    autoFollowSessionRef.current = saved?.wasLive ? sessionId : null
    setFollowOutput(saved?.wasLive ? saved.follow : false)
    setRestoreScrollLine(saved?.viewportLine ?? null)
  }, [sessionId])
  // The id guard keeps the previous session's detail from leaking its
  // liveness into the header/follow state while the new session loads.
  const sessionIsLive = !!(session && session.id === sessionId && isSessionLive(session, now))
  // Idle expiry only applies to the session actually loaded; during the
  // stale-detail window after a switch this effect must not clear the
  // restored follow state of the incoming session.
  useEffect(() => {
    if (session && session.id === sessionId && !sessionIsLive && followOutput) setFollowOutput(false)
  }, [sessionIsLive, followOutput, session, sessionId])

  // Opening an active session auto-engages follow (tail -f): the terminal
  // lands at the tail and the 跟随 button lights up. Fires at most once per
  // session open — later detail refetches (live growth) must not re-enable it
  // after the user turned it off to browse history. The id guard skips the
  // stale detail of the previously selected session while the new one loads.
  useEffect(() => {
    if (!session || !sessionId || session.id !== sessionId) return
    if (autoFollowSessionRef.current === sessionId) return
    if (!isSessionLive(session, Date.now())) return
    autoFollowSessionRef.current = sessionId
    setFollowOutput(true)
  }, [session, sessionId])

  // On session open: optionally expand nav (user/tool) and pin it (settings).
  useEffect(() => {
    if (!sessionId) {
      setShowUserPanel(false)
      setShowToolPanel(false)
      setNavPinned(false)
      return
    }
    const pref = getNavOpenPref()
    if (pref === 'user') {
      setShowUserPanel(true)
      setShowToolPanel(false)
      setNavPinned(true)
    } else if (pref === 'tool') {
      setShowToolPanel(true)
      setShowUserPanel(false)
      setNavPinned(true)
    } else {
      setShowUserPanel(false)
      setShowToolPanel(false)
      setNavPinned(false)
    }
  }, [sessionId])

  // Ctrl+F in-terminal search. Capture phase: focus usually sits in xterm's
  // helper textarea, which stops keydown propagation before the bubble phase.
  const [searchOpen, setSearchOpen] = useState(false)
  // Bumped on every Ctrl+F so an already-open bar pulls focus back to itself.
  const [searchFocusToken, setSearchFocusToken] = useState(0)
  useEffect(() => { setSearchOpen(false) }, [sessionId, viewMode])
  useEffect(() => {
    if (!sessionId) return
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && (e.key === 'f' || e.key === 'F')) {
        if (viewMode !== 'terminal') return
        e.preventDefault()
        setSearchOpen(true)
        setSearchFocusToken(t => t + 1)
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [sessionId, viewMode])

  // Ctrl+C / Ctrl+Shift+C / Ctrl+Insert to copy terminal selection.
  // Capture phase: xterm's helper textarea stops keydown propagation before
  // the bubble phase, so we must intercept the event during capture.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (viewMode !== 'terminal') return
      const isCopy =
        ((e.ctrlKey || e.metaKey) && !e.altKey && e.key === 'c') ||
        ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'C') ||
        (e.ctrlKey && e.key === 'Insert')
      if (!isCopy) return
      const selectedText = window.getSelection()?.toString() ?? ''
      if (selectedText.length === 0) return
      e.preventDefault()
      void navigator.clipboard.writeText(selectedText)
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [viewMode])

  // "Open in editor" target for the clicked row, resolved asynchronously so
  // the item only appears for files that actually exist. Edit header rows use
  // the exact file from the edits API (plus a best-effort line via content
  // search); other rows go through path-token heuristics on the row text.
  const [fileTarget, setFileTarget] = useState<{ path: string; label: string; line: number | null; search?: string } | null>(null)
  const [openFileError, setOpenFileError] = useState<string | null>(null)
  useEffect(() => {
    setFileTarget(null)
    if (!ctxMenu) return
    let cancelled = false
    const cwd = session?.cwd ?? ''
    const show = (path: string, line: number | null, search?: string) => {
      if (cancelled) return
      setFileTarget({ path, label: path.split('/').pop() ?? path, line, search })
    }

    const editPositions = (positionsData?.positions ?? [])
      .filter(p => p.kind === 'edit')
      .sort((a, b) => a.line_start - b.line_start)
    const editIdx = ctxMenu.originalRow !== null
      ? editPositions.findIndex(p => p.line_start === ctxMenu.originalRow)
      : -1
    if (editIdx >= 0 && edits[editIdx]) {
      const e = edits[editIdx]
      const search = (e.new_string || e.old_string).split('\n').find(l => l.trim())
      void resolveFile(e.file_path, cwd).then(p => { if (p) show(p, null, search) })
    } else {
      void resolveRowFile(ctxMenu.lineText, ctxMenu.column).then(hit => {
        if (hit) show(hit.path, hit.line)
      })
    }
    return () => { cancelled = true }
  }, [ctxMenu, edits, positionsData, session, resolveRowFile])

  const ctxMenuSections = useMemo((): TerminalMenuSection[] => {
    const selectedText = window.getSelection()?.toString() ?? ''
    const copyText = (text: string) => {
      void navigator.clipboard.writeText(text)
      setCtxMenu(null)
    }
    const scrollToBottom = () => {
      const ctrl = termControlRef.current
      if (!ctrl) return
      const metrics = ctrl.getMetrics()
      ctrl.scrollToLine(Math.floor(metrics.scrollHeight / TERMINAL_LINE_HEIGHT))
      setCtxMenu(null)
    }
    const toggleFollow = () => {
      setFollowOutput(v => {
        const next = !v
        if (next) {
          // Turning on: jump to the tail immediately, like enabling tail -f.
          const ctrl = termControlRef.current
          if (ctrl) {
            const metrics = ctrl.getMetrics()
            ctrl.scrollToLine(Math.floor(metrics.scrollHeight / TERMINAL_LINE_HEIGHT))
          }
        }
        return next
      })
      setCtxMenu(null)
    }
    const sessionCwd = (session as (SessionDetail & { cwd?: string }) | null)?.cwd ?? ''

    // File actions shared by the left-click popover and the full menu.
    const openWithEditor = () => {
      if (!fileTarget) return
      void openFile({
        path: fileTarget.path,
        line: fileTarget.line ?? undefined,
        search: fileTarget.search,
      }).catch(err => {
        setOpenFileError(err instanceof Error ? err.message : t('replay.openFileFailed'))
        setTimeout(() => setOpenFileError(null), 5000)
      })
      setCtxMenu(null)
    }
    const openInNewTab = () => {
      if (!fileTarget) return
      const params = new URLSearchParams({ path: fileTarget.path, cwd: sessionCwd })
      if (fileTarget.line) params.set('line', String(fileTarget.line))
      window.open(`#/file?${params}`, '_blank')
      setCtxMenu(null)
    }
    const fileItems = () => [
      {
        label: fileTarget ? t('replay.openEditorFile', { file: fileTarget.label }) : t('replay.openEditor'),
        disabled: !fileTarget,
        onClick: openWithEditor,
      },
      {
        label: t('replay.openNewTab'),
        disabled: !fileTarget,
        onClick: openInNewTab,
      },
      ...(ctxMenu?.editIdx != null ? [{
        label: t('replay.viewDiff'),
        onClick: () => {
          setInitialDiffIdx(ctxMenu.editIdx!)
          setShowDiffModal(true)
          setCtxMenu(null)
        },
      }] : []),
    ]

    // Left-click file popover: file actions (+ fold toggle when opened from a
    // path-bearing tool header like `◆ write … /path`).
    if (ctxMenu?.fileOnly) {
      const foldKey = ctxMenu.foldKey
      const foldItems = foldKey
        ? [{
            label: ctxMenu.collapsedFoldKeys.includes(foldKey) ? t('replay.expandContent') : t('replay.collapseContent'),
            onClick: () => {
              const collapsed = ctxMenu.collapsedFoldKeys.includes(foldKey)
              termControlRef.current?.setFoldsCollapsed([foldKey], !collapsed, ctxMenu.originalRow)
              setCtxMenu(null)
            },
          }]
        : []
      return [{
        title: t('replay.fileSection'),
        items: [...foldItems, ...fileItems()],
        emptyText: t('replay.noFile'),
      }]
    }

    const sections: TerminalMenuSection[] = [
      {
        title: 'Common',
        items: [
          // Always visible; greyed out when the row has no openable file.
          ...fileItems(),
          { label: t('replay.previousUser'), onClick: () => { jump(-1, 'user'); setCtxMenu(null) } },
          { label: t('replay.nextUser'), onClick: () => { jump(1, 'user'); setCtxMenu(null) } },
          { label: t('replay.previousTurn'), onClick: () => { jump(-1, 'turn'); setCtxMenu(null) } },
          { label: t('replay.nextTurn'), onClick: () => { jump(1, 'turn'); setCtxMenu(null) } },
          { label: t('replay.toTop'), onClick: () => { termControlRef.current?.scrollToLine(0); setCtxMenu(null) } },
          { label: t('replay.toBottom'), onClick: scrollToBottom },
          ...(sessionIsLive
            ? [{ label: followOutput ? t('replay.disableFollow') : t('replay.followOutput'), onClick: toggleFollow }]
            : []),
          {
            label: t('replay.copySelection'),
            disabled: selectedText.length === 0,
            onClick: () => copyText(selectedText),
          },
          { label: t('replay.copySessionId'), onClick: () => copyText(session?.id ?? '') },
          {
            label: t('replay.copyCwd'),
            disabled: sessionCwd.length === 0,
            onClick: () => copyText(sessionCwd),
          },
          {
            label: t('replay.exportSession'),
            onClick: () => {
              if (session) window.location.href = `/api/sessions/${session.id}/export`
              setCtxMenu(null)
            },
          },
          {
            label: session?.bookmarked ? t('replay.removeBookmark') : t('replay.bookmark'),
            disabled: bookmarkBusy,
            tooltip: session?.bookmarked
              ? (session.bookmark_note?.trim()
                ? t('replay.bookmarkedWithNote', { note: session.bookmark_note.trim() })
                : t('replay.bookmarkedWithoutNote'))
              : t('replay.bookmarkHint'),
            onClick: () => {
              void toggleBookmark()
              setCtxMenu(null)
            },
          },
          ...(session?.bookmarked
            ? [{
                label: session.bookmark_note?.trim() ? t('replay.editBookmarkNote') : t('replay.addBookmarkNote'),
                disabled: bookmarkBusy,
                tooltip: session.bookmark_note?.trim() || t('replay.noBookmarkReason'),
                onClick: () => {
                  setNoteEditorOpen(true)
                  setCtxMenu(null)
                },
              }]
            : []),
        ],
        emptyText: t('replay.noActions'),
      },
    ]
    if (!ctxMenu || folds.length === 0) return sections

    const collapsed = new Set(ctxMenu.collapsedFoldKeys)
    const turnStarts = (positionsData?.positions ?? [])
      .filter(p => p.kind === 'turn')
      .map(p => p.line_start)
    const turnKeys = ctxMenu.originalRow !== null
      ? foldKeysInTurn(folds, turnStarts, ctxMenu.originalRow)
      : []
    const apply = (keys: string[], collapse: boolean) => {
      termControlRef.current?.setFoldsCollapsed(keys, collapse, ctxMenu.originalRow)
      setCtxMenu(null)
    }
    const agent = session?.agent_type ?? 'agent'
    sections.push({
      title: agent.charAt(0).toUpperCase() + agent.slice(1),
      items: [
        {
          label: t('replay.collapseAll'),
          disabled: folds.every(f => collapsed.has(f.key)),
          onClick: () => apply(folds.map(f => f.key), true),
        },
        {
          label: t('replay.expandAll'),
          disabled: collapsed.size === 0,
          onClick: () => apply(folds.map(f => f.key), false),
        },
        {
          label: t('replay.collapseTurn'),
          disabled: turnKeys.length === 0 || turnKeys.every(k => collapsed.has(k)),
          onClick: () => apply(turnKeys, true),
        },
        {
          label: t('replay.expandTurn'),
          disabled: turnKeys.length === 0 || !turnKeys.some(k => collapsed.has(k)),
          onClick: () => apply(turnKeys, false),
        },
      ],
    })
    return sections
  }, [bookmarkBusy, ctxMenu, fileTarget, folds, followOutput, jump, positionsData, session, sessionIsLive, t, toggleBookmark])

  // Positions remapped into the current (post-fold) buffer rows for the
  // minimap and scroll math. Identity while nothing is collapsed.
  const displayPositions = useMemo(() => {
    void foldVersion
    if (!positionsData) return positionsData
    const ctrl = termControlRef.current
    if (!ctrl || ctrl.hiddenLineCount() === 0) return positionsData
    return {
      ...positionsData,
      total_lines: Math.max(1, positionsData.total_lines - ctrl.hiddenLineCount()),
      positions: positionsData.positions.map(p => ({
        ...p,
        line_start: ctrl.toDisplayLine(p.line_start),
        line_end: p.line_end != null ? ctrl.toDisplayLine(p.line_end) : p.line_end,
      })),
    }
  }, [positionsData, foldVersion])

  useEffect(() => {
    if (!bookmarkChange) return
    setSession(prev => {
      if (!prev || prev.id !== bookmarkChange.sessionId || prev.agent_type !== bookmarkChange.agentType) return prev
      if (!bookmarkChange.bookmarked) {
        setNoteEditorOpen(false)
        return { ...prev, bookmarked: false, bookmark_note: undefined }
      }
      if (bookmarkChange.bookmarkNote !== undefined) {
        return { ...prev, bookmarked: true, bookmark_note: bookmarkChange.bookmarkNote }
      }
      return { ...prev, bookmarked: true }
    })
  }, [bookmarkChange])

  const saveBookmarkNote = useCallback(async (_target: { id: string; agent_type: string }, note: string) => {
    if (!session) return
    try {
      await updateBookmarkNote(session, note)
      setSession(prev => prev ? { ...prev, bookmarked: true, bookmark_note: note } : prev)
      onBookmarkChange?.({
        agentType: session.agent_type,
        sessionId: session.id,
        bookmarked: true,
        bookmarkNote: note,
      })
    } catch {
      const key = 'bookmark.noteSaveFailed'
      setBookmarkError(key)
      throw new Error(t(key))
    }
  }, [onBookmarkChange, session, t])

  // Wire scrollToIndexRef and scrollToTopRef to the terminal control.
  // When analytics is shown the terminal is unmounted so these become no-ops.
  useEffect(() => {
    if (scrollToIndexRef) {
      scrollToIndexRef.current = (index: number) => {
        const ctrl = termControlRef.current
        if (!ctrl) return
        // Prefer the exact banner line from the positions cache; the ratio
        // estimate below drifts badly on sessions with uneven turn lengths.
        const turnPos = positionsData?.positions.find(p => p.kind === 'turn' && p.turn_index === index)
        const jumpDbg = (info: Record<string, unknown>) => {
          if (localStorage.getItem('si-term-debug') === '1') console.log('[si-jump]', JSON.stringify(info))
        }
        if (turnPos) {
          const jumpToPosition = () => {
          // logical_start resolves through xterm's own wrap state and stays
          // exact when collapsed-fold badges shift display rows; line_start
          // is the fallback for position caches built by older binaries.
          const logical = turnPos.payload?.logical_start
          const line = typeof logical === 'number'
            ? ctrl.logicalToDisplayLine(logical)
            : Math.max(0, ctrl.toDisplayLine(turnPos.line_start))
          jumpDbg({ index, via: typeof logical === 'number' ? 'logical' : 'positions', lineStart: turnPos.line_start, logical, line, hidden: ctrl.hiddenLineCount(), scrollHeight: ctrl.getMetrics().scrollHeight })
          ctrl.scrollToLineCentered(line)
          ctrl.flashLines(line, 2)
          }
          if (index < 0) {
            const rollbackFold = folds.find(f =>
              f.level === 'rollback' && turnPos.line_start >= f.displayStart && turnPos.line_start < f.displayEnd)
            if (rollbackFold && ctrl.getCollapsedFoldKeys().includes(rollbackFold.key)) {
              ctrl.setFoldsCollapsed([rollbackFold.key], false, rollbackFold.headerDisplay)
              requestAnimationFrame(() => requestAnimationFrame(jumpToPosition))
              return
            }
          }
          jumpToPosition()
          return
        }
        const metrics = ctrl.getMetrics()
        const totalLines = Math.floor(metrics.scrollHeight / TERMINAL_LINE_HEIGHT)
        const barCount = turns.length
        const ratio = barCount > 1 ? index / (barCount - 1) : 0
        const line = Math.floor(ratio * Math.max(0, totalLines - 1))
        jumpDbg({ index, via: 'fallback', line, totalLines, hidden: ctrl.hiddenLineCount() })
        ctrl.scrollToLineCentered(line)
        ctrl.flashLines(line, 2)
      }
    }
    if (scrollToTopRef) {
      scrollToTopRef.current = (top: number) => {
        if (localStorage.getItem('si-term-debug') === '1') console.log('[si-scroll-top]', JSON.stringify({ top, line: Math.floor(top / TERMINAL_LINE_HEIGHT) }))
        termControlRef.current?.scrollToLine(Math.floor(top / TERMINAL_LINE_HEIGHT))
      }
    }
  }, [scrollToIndexRef, scrollToTopRef, turns, positionsData, folds])

  // Jump requested from AnalyticsView while the terminal was unmounted.
  // The terminal re-renders its content asynchronously after remount: first an
  // (often empty) compose, then the real render arrives and a 1-3s fold
  // rewrite replaces the whole buffer. A jump fired against any intermediate
  // buffer lands pages away, and no single readiness signal covers all the
  // intermediate states — so fire on each stable height and keep watching:
  // if the buffer height changes after a fire, the landing was on a stale
  // buffer and the jump re-fires against the new one. Done when the height is
  // stable and the last fire happened at that height.
  const pendingJumpTurnRef = useRef<number | null>(null)
  useEffect(() => {
    if (viewMode !== 'terminal' || pendingJumpTurnRef.current == null) return
    const needsFolds = folds.some(f => f.level === 'tool')
    let prevHeight = -1
    let firedAtHeight = -1
    let tries = 0
    const timer = setInterval(() => {
      tries++
      const ctrl = termControlRef.current
      if (ctrl) {
        const h = ctrl.getMetrics().scrollHeight
        const foldsApplied = !needsFolds || ctrl.hiddenLineCount() > 0
        // The transitional buffer between remount and the real render is a
        // few dozen rows; positions total_lines minus hidden rows says how
        // many to expect. The 0.5 factor tolerates cols drift between the
        // cached positions and the live terminal — this only needs to tell
        // "placeholder" from "content", not be exact.
        const expectedRows = positionsRef.current
          ? positionsRef.current.total_lines - ctrl.hiddenLineCount()
          : 0
        const bufferReady = expectedRows <= 0 || h / TERMINAL_LINE_HEIGHT >= expectedRows * 0.5
        if (h > 0 && h === prevHeight && foldsApplied && bufferReady) {
          if (h === firedAtHeight) {
            pendingJumpTurnRef.current = null
            clearInterval(timer)
            return
          }
          const idx = pendingJumpTurnRef.current
          if (idx != null) scrollToIndexRef.current?.(idx)
          firedAtHeight = h
        }
        prevHeight = h
      }
      // Generous bail: a cold positions rebuild for a new cols plus a 2-3s
      // fold rewrite can exceed 10s; giving up early strands the jump on the
      // transitional buffer (the "flash lands pages away" bug).
      if (tries > 150) {
        clearInterval(timer)
        pendingJumpTurnRef.current = null
      }
    }, 200)
    return () => clearInterval(timer)
  }, [viewMode, folds])

  const handleJumpToTurn = useCallback((index: number) => {
    pendingJumpTurnRef.current = index
    setViewMode('terminal')
  }, [])

  // Global-search results carry the original query. Once the new terminal is
  // ready, locate that exact text in xterm and flash the matched buffer row.
  useEffect(() => {
    if (!searchTarget || searchTarget.sessionId !== sessionId) return
    setViewMode('terminal')
    let attempts = 0
    const timer = window.setInterval(() => {
      attempts++
      const ctrl = termControlRef.current
      if (attempts === 5 && ctrl) {
        const rollbackKeys = folds.filter(f => f.level === 'rollback').map(f => f.key)
        if (rollbackKeys.length > 0) ctrl.setFoldsCollapsed(rollbackKeys, false)
      }
      if (ctrl?.flashSearchMatch(searchTarget.query) || attempts >= 30) {
        window.clearInterval(timer)
      }
    }, 100)
    return () => window.clearInterval(timer)
  }, [searchTarget, sessionId, folds])

  // 面板点击跳转:优先逻辑行(折叠 badge 不会让它漂移),旧缓存回退显示行。
  // 工具面板和交互消息面板共用同一套动效。
  const handlePanelJump = useCallback((lineStart: number, logicalStart?: number) => {
    // jumpToPosition defers when the live buffer hasn't caught up to the
    // positions snapshot yet, instead of clamping onto the wrong row.
    termControlRef.current?.jumpToPosition(lineStart, logicalStart)
  }, [])

  const toolCallCount = useMemo(
    () => (positionsData?.positions ?? []).filter(p => p.kind === 'tool').length,
    [positionsData],
  )

  const interactionCount = useMemo(
    () => (positionsData?.positions ?? []).filter(p => p.kind === 'user' || p.kind === 'assistant').length,
    [positionsData],
  )

  // User-message ranges for the terminal: highlight decoration + sticky top
  // bar. Mapped from positions (kind === 'user') to the shape TerminalPanel
  // consumes. line_end / logical_end come from the backend (set when the user
  // prompt body is fully written); they let the highlight paint exactly the
  // prompt rows, not the trailing blank separator.
  const userHighlightRanges = useMemo<UserHighlightRange[]>(
    () => (positionsData?.positions ?? [])
      .filter(p => p.kind === 'user')
      .map((p, i) => {
        const pl = p.payload ?? {}
        const lineEnd = typeof p.line_end === 'number' ? p.line_end : undefined
        const logicalStart = typeof pl.logical_start === 'number' ? pl.logical_start : undefined
        const logicalEnd = typeof pl.logical_end === 'number' ? pl.logical_end : undefined
        const text = typeof pl.text === 'string' ? pl.text : p.label
        const tsMs = typeof pl.ts_ms === 'number' ? pl.ts_ms : null
        return {
          key: p.position_key,
          lineStart: p.line_start,
          lineEnd,
          logicalStart,
          logicalEnd,
          text,
          tsMs,
          seq: i + 1,
        }
      }),
    [positionsData],
  )

  // 分析页 Tool Usage chip → 切回终端、打开工具面板并按该工具筛选。
  // token 递增让重复点击同一工具也能重新触发筛选。
  const [toolFilterRequest, setToolFilterRequest] = useState<{ name: string; token: number } | null>(null)
  const handleJumpToTool = useCallback((name: string) => {
    setToolFilterRequest(prev => ({ name, token: (prev?.token ?? 0) + 1 }))
    setShowToolPanel(true)
    setShowUserPanel(false)
    setNavPinned(true)
    setViewMode('terminal')
  }, [])

  // Keyboard navigation
  useEffect(() => {
    if (!sessionId || !session?.turns?.length) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (
        e.target instanceof HTMLInputElement ||
        e.target instanceof HTMLTextAreaElement ||
        e.target instanceof HTMLSelectElement ||
        e.target instanceof HTMLButtonElement
      ) return
      if (e.key === 'j' || e.key === 'ArrowDown') { e.preventDefault(); jump(1, 'turn') }
      else if (e.key === 'k' || e.key === 'ArrowUp') { e.preventDefault(); jump(-1, 'turn') }
      else if (e.key === '?' && !e.shiftKey && !e.metaKey && !e.ctrlKey) { e.preventDefault(); setShowHelp(h => !h) }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [jump, session?.turns?.length, sessionId])

  useEffect(() => {
    if (!sessionId) { setSession(null); setEdits([]); return }
    visibleRangeRef.current = undefined
    jumpBaseRef.current = 0
    lastColsRef.current = null
    setTerminalCols(null)
    setPositionsData(null)
    setPositionsBuilding(false)
    clearTimeout(pollTimerRef.current)
    setLoading(true)
    fetchSession(sessionId)
      .then(data => { setSession(data) })
      .catch(console.error)
      .finally(() => setLoading(false))
  }, [sessionId])

  useEffect(() => {
    if (!sessionId) return
    fetchSessionEdits(sessionId)
      .then(setEdits)
      .catch(() => setEdits([]))
  }, [sessionId])

  // Fetch positions once terminal cols are stable. Polls on 202 until ready.
  useEffect(() => {
    if (!sessionId || terminalCols === null) return
    let cancelled = false

    const poll = () => {
      fetchPositions(sessionId, terminalCols, tsKinds ?? '')
        .then(result => {
          if (cancelled) return
          if (result.status === 'building') {
            setPositionsBuilding(true)
            pollTimerRef.current = setTimeout(poll, 1000)
          } else {
            setPositionsBuilding(false)
            setPositionsData(result.data)
          }
        })
        .catch(() => {
          if (!cancelled) {
            setPositionsBuilding(false)
            setPositionsData(null) // error → turn-index fallback
          }
        })
    }

    poll()
    return () => {
      cancelled = true
      clearTimeout(pollTimerRef.current)
    }
    // contentVersion: live tail grew the render → position cache rebuilds
    // under the new revision, so refetch (202-polling included).
    // tsKinds: 时间戳选项改变布局,渲染与 positions 必须成对刷新。
  }, [sessionId, terminalCols, contentVersion, tsKinds])

  // Translate terminal scroll events into ScrollMetrics + visibleRange so that
  // MiniMap stays in sync even though there's no DOM scroller to observe.
  const handleTerminalScrollMetrics = useCallback((metrics: ScrollMetrics) => {
    lastMetricsRef.current = metrics
    lastMetricsSessionIdRef.current = sessionDetailRef.current?.id ?? null
    const range = getVisibleTurnRange(metrics, turns.length)
    miniMapControlRef?.current?.updateViewport(metrics, range)
    if (range && !isSameVisibleRange(visibleRangeRef.current, range)) {
      visibleRangeRef.current = range
      jumpBaseRef.current = range.start
      if (visibleRangeLabelRef.current) {
        visibleRangeLabelRef.current.textContent = `Turn ${range.start + 1}-${range.end + 1}/${turns.length}`
      }
    }
  }, [turns, miniMapControlRef])

  useEffect(() => {
    const metrics = lastMetricsRef.current ?? termControlRef.current?.getMetrics()
    if (!metrics) return

    const range = getVisibleTurnRange(metrics, turns.length)
    const frame = window.requestAnimationFrame(() => {
      miniMapControlRef.current?.updateViewport(metrics, range)
    })
    return () => window.cancelAnimationFrame(frame)
  }, [turns.length, positionsData, positionsBuilding, viewMode])

  if (!sessionId) return (
    <main className="flex-1 flex flex-col min-w-[360px] bg-[var(--bg-surface)]">
      <GlobalSearch onSelect={onSelect} />
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center px-6">
          <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--bg-inset)] text-nav text-[var(--text-muted)]">SI</div>
          <h3 className="text-body font-medium text-[var(--text-primary)]">{t('replay.noSelection')}</h3>
          <p className="text-helper text-[var(--text-muted)] mt-1">{t('replay.selectHint')}</p>
        </div>
      </div>
    </main>
  )

  if (loading) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)]">
      <GlobalSearch onSelect={onSelect} />
      <div className="p-4 space-y-3">{Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="rounded-lg border border-[var(--border-muted)] bg-[var(--bg-surface)] p-3">
          <div className="h-5 w-44 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
          <div className="mt-3 h-3 w-3/4 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
          <div className="mt-2 h-3 w-1/2 bg-[var(--bg-surface-hover)] rounded-sm animate-pulse" />
        </div>
      ))}</div>
    </main>
  )

  // turns may be null from older backends / nil Go slices — never touch .length bare.
  if (!session || !(session.turns?.length)) return (
    <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)] flex flex-col">
      <GlobalSearch onSelect={onSelect} />
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center px-6">
          <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--bg-inset)] text-nav text-[var(--text-muted)]">MSG</div>
          <h3 className="text-body font-medium text-[var(--text-primary)]">
            {session ? t('replay.noReplay') : t('replay.noSessions')}
          </h3>
          <p className="text-helper text-[var(--text-muted)] mt-1">
            {session
              ? t('replay.createdNoTurns')
              : t('replay.agentHint')}
          </p>
        </div>
      </div>
    </main>
  )

  // Chrys reports exact usage only at session level, so its per-turn buckets
  // are empty. Prefer the session bill when present and keep the turn sum as
  // the fallback for readers that only expose per-turn usage.
  const totalTokens = session.billing?.totals
    ? session.billing.totals.prompt_tokens + session.billing.totals.completion_tokens
    : [...session.turns, ...rolledBackTurns].reduce(
      (sum, t) => sum + t.token_usage.prompt_tokens + t.token_usage.completion_tokens,
      0,
    )
  const modelName = session.model_name || session.agent_type || 'unknown'
  const sessionDuration = formatDuration(session.turns.reduce((sum, t) => sum + t.duration_ms, 0))

  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden relative">
      <GlobalSearch onSelect={onSelect} />
      <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-3" style={{ height: '40px' }}>
        <div className="flex items-center gap-2">
          <InstantTooltip
            text={
              session.bookmarked
                ? (session.bookmark_note?.trim()
                  ? t('replay.bookmarkedWithNote', { note: session.bookmark_note.trim() })
                  : t('replay.bookmarkedWithoutNote'))
                : t('replay.bookmarkSession')
            }
            placement="bottom"
          >
            <button
              onClick={toggleBookmark}
              disabled={bookmarkBusy}
              className={`h-7 rounded-md px-2 inline-flex items-center justify-center text-nav ${
                session.bookmarked ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10' : 'text-[var(--text-secondary)]'
              } hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
              aria-label={session.bookmarked ? t('replay.removeBookmark') : t('replay.bookmark')}
            >
              {session.bookmarked ? t('replay.removeBookmark') : t('replay.bookmark')}
            </button>
          </InstantTooltip>
          {session.bookmarked && (
            <InstantTooltip
              text={session.bookmark_note?.trim() || t('replay.noBookmarkReason')}
              placement="bottom"
            >
              <button
                onClick={() => setNoteEditorOpen(true)}
                disabled={bookmarkBusy}
                className="h-7 rounded-md px-2 inline-flex items-center justify-center text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                aria-label={session.bookmark_note?.trim() ? t('replay.editBookmarkNote') : t('replay.addBookmarkNote')}
              >
                {session.bookmark_note?.trim() ? t('replay.note') : t('replay.addBookmarkNote')}
              </button>
            </InstantTooltip>
          )}
          {bookmarkError && (
            <span className="text-meta text-[var(--error)]" role="status">
              {t(bookmarkError)}
            </span>
          )}
          {openFileError && (
            <span className="text-meta text-[var(--error)]" role="status">
              {openFileError}
            </span>
          )}
          <span className="text-[var(--border-default)]">|</span>
          <button
            onClick={() => startTransition(() => setViewMode(v => v === 'analytics' ? 'terminal' : 'analytics'))}
            className={`h-7 rounded-md px-2 text-nav ${viewMode === 'analytics' ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
          >
            {t('replay.analytics')}
          </button>
          <span className="text-[var(--border-default)]">|</span>
          <button
            onClick={() => {
              setFollowOutput(v => {
                const next = !v
                if (next) {
                  const ctrl = termControlRef.current
                  if (ctrl) {
                    const metrics = ctrl.getMetrics()
                    ctrl.scrollToLine(Math.floor(metrics.scrollHeight / TERMINAL_LINE_HEIGHT))
                  }
                }
                return next
              })
            }}
            disabled={!sessionIsLive}
            className={`h-7 rounded-md px-2 inline-flex items-center gap-1 text-nav ${followOutput ? 'text-[var(--accent-green)] bg-[color-mix(in_srgb,var(--accent-green)_15%,transparent)]' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-transparent disabled:hover:text-[var(--text-secondary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={
              !sessionIsLive
                ? t('replay.followUnavailable')
                : followOutput
                  ? t('replay.followOn')
                  : t('replay.followOff')
            }
            aria-pressed={followOutput}
            aria-label={followOutput ? t('replay.followOn') : t('replay.followOff')}
          >
            {followOutput && (
              <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--accent-green)]" />
            )}
            {t('replay.follow')}
          </button>
          <span className="text-[var(--border-default)]">|</span>
          <a href={`/api/sessions/${session.id}/export`} className="h-7 rounded-md px-2 inline-flex items-center text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] no-underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">{t('replay.export')}</a>
          <span className="text-[var(--border-default)]">|</span>
          <button
            onClick={() => setShowAIPanel(true)}
            className={`h-7 rounded-md border px-2 text-nav font-medium ${
              showAIPanel
                ? 'border-[var(--accent-blue)] bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] text-[var(--accent-blue)]'
                : 'border-[color-mix(in_srgb,var(--accent-blue)_45%,transparent)] text-[var(--accent-blue)]'
            } hover:bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.aiPanel')}
          >
            ✨ AI
          </button>
        </div>
        <span className="flex-1 text-center text-helper text-[var(--text-secondary)] truncate px-2">
          {isSessionLive(session, now) && (
            <span className="mr-1.5 inline-flex items-center gap-1 rounded-sm bg-[color-mix(in_srgb,var(--accent-green)_15%,transparent)] px-1.5 text-meta font-medium text-[var(--accent-green)]" aria-label={t('replay.active')}>
              <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--accent-green)]" />{t('replay.active')}
            </span>
          )}
          {session.agent_type || 'agent'} · {modelName} · {fmtTokens(totalTokens, locale)} {t('replay.tokens')} · {formatNumber(locale, session.turn_count)} {t('replay.turns')}
          {(session.rolled_back_turn_count ?? 0) > 0 && (
            <span className="text-[var(--warning)]"> · +{formatNumber(locale, session.rolled_back_turn_count ?? 0)} {t('replay.rolledBack')}</span>
          )}
          {' · '}{sessionDuration}
          {session.repository && <span className="text-[var(--text-muted)]"> · {session.repository.split('/').pop()}</span>}
          {session.branch && <span className="text-[var(--text-muted)]">@{session.branch}</span>}
          {session.created_at && (
            <span className="text-[var(--text-muted)] ml-1 text-meta">
              {formatDate(locale, session.created_at)}
            </span>
          )}
          {session.todos && session.todos.length > 0 && (
            <span className="ml-1 text-[var(--accent-green)]">{session.todos.filter(t => t.status === 'done').length}/{session.todos.length} done</span>
          )}
        </span>
        <span className="text-[var(--border-default)] mx-1">|</span>
        <div className="flex items-center gap-2 mr-4">
          <span className="text-nav text-[var(--text-muted)]">{t('replay.navigation')}</span>
          <button
            onClick={() => setShowUserPanel(v => {
              const next = !v
              if (next) {
                setShowToolPanel(false)
                // Manual open keeps current pin; first open starts unpinned
                // unless auto-open already pinned this session.
              } else {
                setNavPinned(false)
              }
              return next
            })}
            className={`h-7 rounded-md border px-2 text-nav ${
              showUserPanel
                ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            } focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.messages')}
          >
            {t('replay.messages')}{interactionCount > 0 ? ` ${formatNumber(locale, interactionCount)}` : ''}
          </button>
          <button
            onClick={() => setShowToolPanel(v => {
              const next = !v
              if (next) {
                setShowUserPanel(false)
              } else {
                setToolFilterRequest(null)
                setNavPinned(false)
              }
              return next
            })}
            className={`h-7 rounded-md border px-2 text-nav ${
              showToolPanel
                ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            } focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.toolCalls')}
          >
            {t('replay.toolCalls')}{toolCallCount > 0 ? ` ${formatNumber(locale, toolCallCount)}` : ''}
          </button>
        </div>
        <span ref={visibleRangeLabelRef} className="flex-shrink-0 text-meta text-[var(--text-muted)]">
          Turn ?/{session.turn_count}
        </span>
      </header>

      {showHelp && (
        <div className="absolute inset-0 z-20 flex items-center justify-center bg-[rgba(0,0,0,var(--opacity-overlay))]" onClick={() => setShowHelp(false)}>
          <div className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-lg p-6 max-w-sm" onClick={e => e.stopPropagation()}>
            <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-3">{t('replay.shortcuts')}</h3>
            <div className="space-y-2 text-helper">
              {[
                ['j / ↓', t('replay.shortcutNext')],
                ['k / ↑', t('replay.shortcutPrevious')],
                ['?', t('replay.shortcutHelp')],
              ].map(([key, desc]) => (
                <div key={key} className="flex items-center gap-3">
                  <kbd className="bg-[var(--bg-inset)] px-1.5 py-0.5 rounded-sm border border-[var(--border-default)] text-meta text-[var(--text-primary)] min-w-[60px] text-center">{key}</kbd>
                  <span className="text-[var(--text-secondary)]">{desc}</span>
                </div>
              ))}
            </div>
            <button onClick={() => setShowHelp(false)} className="mt-4 text-meta text-[var(--accent-blue)] hover:underline">{t('common.close')}</button>
          </div>
        </div>
      )}

      {showDiffModal && session && (
        <DiffModal sessionId={session.id} onClose={() => setShowDiffModal(false)} initialIdx={initialDiffIdx} />
      )}

      {noteEditorOpen && session?.bookmarked && (
        <BookmarkNoteEditor
          session={session}
          onSave={saveBookmarkNote}
          onClose={() => setNoteEditorOpen(false)}
        />
      )}

      {outputModalIdx !== null && session && (
        <OutputModal sessionId={session.id} outputIndex={outputModalIdx} onClose={() => setOutputModalIdx(null)} />
      )}

      {showAIPanel && session && (
        <AIPanel
          sessionId={session.id}
          agentType={session.agent_type}
          sessionName={session.name || session.id}
          onClose={() => setShowAIPanel(false)}
          onTitleApplied={title => {
            // Apply: reflect the new display name immediately. Remove: the
            // original name only lives in the agent log, so refetch.
            if (title !== null) setSession(prev => prev ? { ...prev, name: title } : prev)
            else void fetchSession(session.id).then(d => setSession(prev => (prev && prev.id === d.id ? { ...prev, name: d.name } : prev))).catch(() => {})
          }}
        />
      )}

      {ctxMenu && (
        <TerminalContextMenu
          x={ctxMenu.clientX}
          y={ctxMenu.clientY}
          sections={ctxMenuSections}
          onClose={() => setCtxMenu(null)}
        />
      )}

      <div className="flex min-h-0 flex-1 overflow-hidden">
        <div className="relative flex min-w-0 flex-1 overflow-hidden">
          {viewMode === 'terminal' && searchOpen && (
            <TerminalSearchBar
              controlRef={termControlRef}
              refreshToken={foldVersion}
              focusToken={searchFocusToken}
              rightInset={navPinned && (showUserPanel || showToolPanel) ? navPanelWidth : 0}
              onClose={() => setSearchOpen(false)}
            />
          )}
          {viewMode === 'analytics' ? (
            <Suspense fallback={<AnalyticsSkeleton />}>
              <AnalyticsView sessionId={session.id} agentType={session.agent_type} isLive={session.is_live} onJumpToTurn={handleJumpToTurn} onJumpToTool={handleJumpToTool} />
            </Suspense>
          ) : tsKinds !== null && (
            <Suspense fallback={<div className="flex-1 bg-[#1a1b26]" />}>
              <TerminalPanel
                sessionId={session.id}
                agentType={session.agent_type}
                folds={folds}
                tsKinds={tsKinds}
                followOutput={followOutput && sessionIsLive}
                onFollowDisable={() => setFollowOutput(false)}
                initialScrollLine={restoreScrollLine}
                onFoldChange={handleFoldChange}
                onFoldPathActivate={(bufLine, meta) => openFilePopover(bufLine, meta, null)}
                onContextMenu={handleTerminalContextMenu}
                onScrollMetrics={handleTerminalScrollMetrics}
                onColsReady={handleColsReady}
                controlRef={termControlRef}
                userPositions={userHighlightRanges}
                onJumpToUserMessage={handlePanelJump}
              />
            </Suspense>
          )}
          {viewMode === 'terminal' && showUserPanel && (
            <UserMessagePanel
              positions={positionsData}
              building={positionsBuilding}
              agentType={session.agent_type}
              pinned={navPinned}
              onPinnedChange={setNavPinned}
              onWidthChange={setNavPanelWidth}
              onJump={handlePanelJump}
              onClose={() => {
                setShowUserPanel(false)
                setNavPinned(false)
              }}
            />
          )}
          {/* 浮层覆盖在终端右侧:不改变终端布局宽度,开关面板不会触发
              列数变化 → 整屏重渲染 → minimap 闪烁。 */}
          {viewMode === 'terminal' && showToolPanel && (
            <ToolCallPanel
              positions={positionsData}
              building={positionsBuilding}
              pinned={navPinned}
              onPinnedChange={setNavPinned}
              onWidthChange={setNavPanelWidth}
              filterRequest={toolFilterRequest}
              onJump={handlePanelJump}
              onClose={() => {
                setShowToolPanel(false)
                setNavPinned(false)
                // 面板生命周期结束,清掉分析页带来的筛选请求,
                // 避免下次手动打开时又套用旧筛选。
                setToolFilterRequest(null)
              }}
            />
          )}
        </div>
        <MiniMap
          turns={turns}
          positions={positionsBuilding ? null : displayPositions}
          billing={session?.billing}
          controlRef={miniMapControlRef}
          scrollToIndexRef={scrollToIndexRef}
          scrollToTopRef={scrollToTopRef}
        />
      </div>
    </main>
  )
}

function AnalyticsSkeleton() {
  return (
    <div className="p-4 space-y-3">
      <div className="grid grid-cols-4 gap-3">
        {Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-16 rounded-md bg-[var(--bg-inset)] animate-pulse" />)}
      </div>
      <div className="h-[200px] rounded-lg bg-[var(--bg-inset)] animate-pulse" />
    </div>
  )
}
