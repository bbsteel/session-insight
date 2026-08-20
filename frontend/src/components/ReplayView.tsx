import { lazy, Suspense, useCallback, useEffect, useState, useRef, useMemo, startTransition } from 'react'
import { addBookmark, APIError, createSnippet, fetchAgents, fetchCollaborationDetail, fetchLiveRevision, fetchPositions, fetchSession, fetchSessionEdits, fetchSettings, openFile, removeBookmark, resolveFile, updateBookmarkNote, watchSessionsChanged } from '../api'
import { DEFAULT_FILE_OPEN_EXTS, extractPathMatches, extractPathsAt, parseExtList, type PathMatch } from '../filePathDetection'
import { extractTerminalUrlMatches, type TerminalUrlMatch } from '../terminalUrlDetection'
import type { AgentInfo, EditCall, PositionsResponse, SessionDetail } from '../types'
import { sessionCapabilityHeaderHint, sessionTokenHeaderDisplay } from '../capabilityPresentation'
import { presentFromSession, recordStatusLabel, toneClass } from '../recordStatusPresentation'
import AgentIcon from './AgentIcon'
import SessionCapabilityPanel from './SessionCapabilityPanel'
import RecordStatusPanel from './RecordStatusPanel'
import AgentCapabilityCompareDialog from './AgentCapabilityCompareDialog'
import CollaborationDock, {
  DEFAULT_DOCK_HEIGHT_PX,
  MIN_DOCK_HEIGHT_PX,
  type CollaborationDockStatus,
} from './CollaborationDock'
import { isGraphEmpty } from '../collaboration/dockState'
import { resolveAnchorJump } from '../collaboration/jumpTargets'
import type { SourceAnchorDTO } from '../collaboration/types'
import type { BookmarkChange } from '../bookmarkState'
import type { ScrollMetrics } from '../minimapGeometry'
import { TERMINAL_LINE_HEIGHT, type TerminalActivateMeta, type TerminalContextMenuEvent, type TerminalControl, type TerminalFileMatch, type UserHighlightRange, type ViewportAnchor } from '../terminalControl'
import MiniMap, { type MiniMapControl } from './MiniMap'
import GlobalSearch from './GlobalSearch'
import GitEvidencePanel from './GitEvidencePanel'
import AIPanel from './AIPanel'
import BookmarkNoteEditor from './BookmarkNoteEditor'
import DiffModal from './DiffModal'
import OutputModal from './OutputModal'
import TerminalContextMenu, { type TerminalMenuSection } from './TerminalContextMenu'
import TerminalSearchBar from './TerminalSearchBar'
import ToolCallPanel from './ToolCallPanel'
import UserMessagePanel from './UserMessagePanel'
import KeyEventOutlinePanel from './KeyEventOutlinePanel'
import { getVisibleTurnRange, isSameVisibleRange, type VisibleTurnRange } from '../scrollSync'
import { nearestOutlineKey, outlineItemsFromPositions } from '../semanticOutline'
import { parseEditHeaderLine } from '../terminalInteractionGeometry'
import { foldKeysContainingTarget, foldKeysInTurn, foldsFromPositions } from '../terminalFolds'
import { editIndexForRow, editPositionForIndex, editPositionsSorted } from '../editPositionMatch'
import { isSessionLive, LIVE_WINDOW_MS, getAgentLabel } from '../sidebarRows'
import { getNavOpenPref } from '../navPrefs'
import { formatTokenCount } from '../formatTokenCount'
import {
  getTokenDisplayMode,
  onTokenDisplayModeChange,
  type TokenDisplayMode,
} from '../tokenDisplayPrefs'
import { formatDate, formatNumber, useI18n, type Locale } from '../i18n'
import { openOnModifiedClick } from '../sessionLink'
import ResumeTerminalControl from './ResumeTerminalControl'
import { SearchIcon } from './icons'

const AnalyticsView = lazy(() => import('./AnalyticsView'))
const TerminalPanel = lazy(() => import('./TerminalPanel'))

type ReplayScrollBehavior = 'auto' | 'smooth'
type JumpTarget = 'turn' | 'user'
type ViewMode = 'terminal' | 'analytics'

// The terminal keeps at least this much height below the expanded dock
// (design §10.4 / §16: usable at 1280×720).
const MIN_TERMINAL_HEIGHT_PX = 240

const COLLAB_DOCK_PREFS_KEY = 'si-collab-dock'

type CollabDockMode = 'closed' | 'collapsed' | 'expanded'

function readCollabDockPrefs(): { mode: CollabDockMode; height: number; autoFit: boolean } {
  const fallback = { mode: 'collapsed' as CollabDockMode, height: DEFAULT_DOCK_HEIGHT_PX, autoFit: true }
  try {
    const raw = window.localStorage.getItem(COLLAB_DOCK_PREFS_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as { mode?: unknown; height?: unknown; autoFit?: unknown }
    const mode: CollabDockMode =
      parsed.mode === 'closed' || parsed.mode === 'collapsed' || parsed.mode === 'expanded' ? parsed.mode : fallback.mode
    const height =
      typeof parsed.height === 'number' && Number.isFinite(parsed.height)
        ? Math.max(MIN_DOCK_HEIGHT_PX, Math.min(600, parsed.height))
        : fallback.height
    // Preferences written before auto-fit existed may contain the height from
    // the short-lived automatic expansion. Treat them as auto-fit so stale
    // space is recovered; a manual resize opts out and is persisted below.
    return { mode, height, autoFit: parsed.autoFit !== false }
  } catch {
    return fallback
  }
}

function writeCollabDockPrefs(mode: CollabDockMode, height: number, autoFit: boolean) {
  try {
    window.localStorage.setItem(COLLAB_DOCK_PREFS_KEY, JSON.stringify({ mode, height, autoFit }))
  } catch {
    // localStorage unavailable — prefs are best-effort.
  }
}

// Typed contract failures that will fail identically on every retry; a live
// ping surfacing one replaces the graph instead of showing it forever.
const COLLAB_TERMINAL_CODES = new Set(['session_not_found', 'collaboration_not_indexed', 'collaboration_unsupported'])

interface Props {
  sessionId: string | null
  searchTarget?: { sessionId: string; agentType: string; query: string } | null
  // Root ancestor of a subagent session opened from global search: drives the
  // same back-to-parent breadcrumb as dock navigation.
  searchRootRef?: { sessionId: string; childAgentType: string; root: { id: string; agentType: string; name: string } } | null
  onSelect?: (id: string, agentType?: string, focusSidebar?: boolean, searchQuery?: string) => void
  onOpenCodingQuotas?: () => void
  bookmarkChange?: BookmarkChange | null
  onBookmarkChange?: (change: BookmarkChange) => void
}

function fmtTokens(
  tokenCount: number,
  locale: Locale,
  mode: TokenDisplayMode,
  units: { tenThousand: string; hundredMillion: string },
): string {
  return formatTokenCount(locale, tokenCount, mode, units)
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${totalSeconds % 60}s`
  return `${totalSeconds}s`
}

export default function ReplayView({ sessionId, searchTarget, searchRootRef, onSelect, onOpenCodingQuotas, bookmarkChange, onBookmarkChange }: Props) {
  const { locale, t } = useI18n()
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [capPanelOpen, setCapPanelOpen] = useState(false)
  const [recordPanelOpen, setRecordPanelOpen] = useState(false)
  const [gitPanelOpen, setGitPanelOpen] = useState(false)
  const [degradedBannerDismissed, setDegradedBannerDismissed] = useState(false)
  const [tokenDisplayMode, setTokenDisplayModeState] = useState<TokenDisplayMode>(getTokenDisplayMode)
  useEffect(() => onTokenDisplayModeChange(setTokenDisplayModeState), [])
  useEffect(() => {
    setDegradedBannerDismissed(false)
    setRecordPanelOpen(false)
    setGitPanelOpen(false)
  }, [sessionId])
  const [capCompareOpen, setCapCompareOpen] = useState(false)
  const [agentsCatalog, setAgentsCatalog] = useState<AgentInfo[]>([])
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
  // Ref mirror for the edit-header matcher: it is registered once per session
  // but must resolve rows against the latest edits payload.
  const editsRef = useRef(edits)
  editsRef.current = edits
  const [showToolPanel, setShowToolPanel] = useState(false)
  const [showUserPanel, setShowUserPanel] = useState(false)
  const [showOutlinePanel, setShowOutlinePanel] = useState(false)
  // Terminal viewport anchor (original render row at the viewport center) for
  // the outline's current-position tracking; updated from scroll metrics.
  const [outlineAnchor, setOutlineAnchor] = useState<number | null>(null)
  // When pinned, click-outside does not close the nav overlay.
  // Auto-open+pin only when settings "open on session" is not off.
  const [navPinned, setNavPinned] = useState(false)
  const [navPanelWidth, setNavPanelWidth] = useState(0)
  // Analytics Tool Usage chip → tool panel filter; cleared on session switch /
  // when Messages hides the tool panel so it does not stick to a later open.
  const [toolFilterRequest, setToolFilterRequest] = useState<{ name: string; token: number } | null>(null)
  const [showAIPanel, setShowAIPanel] = useState(false)

  // ---- Collaboration dock (horizontal strip above the terminal) ----
  // ReplayView owns the detail fetch (the entry button is gated on confirmed
  // non-empty data), the persisted open/collapsed mode and height, and the
  // parent-session context for backing-child navigation. The dock itself only
  // changes the terminal container's height, so xterm cols never change and
  // no replacement /render?cols= request is triggered by toggling it.
  const [collabStatus, setCollabStatus] = useState<CollaborationDockStatus>({ kind: 'loading' })
  const collabEtagRef = useRef<string | null>(null)
  // Generation counter: the last-started request always wins, so an
  // out-of-order live ping can never clobber newer data.
  const collabGenRef = useRef(0)
  const [collabMode, setCollabMode] = useState<'closed' | 'collapsed' | 'expanded'>(readCollabDockPrefs().mode)
  const [collabHeight, setCollabHeight] = useState<number>(readCollabDockPrefs().height)
  const [collabAutoFit, setCollabAutoFit] = useState<boolean>(readCollabDockPrefs().autoFit)
  const [collabContentHeight, setCollabContentHeight] = useState<number | null>(null)
  useEffect(() => {
    setCollabContentHeight(null)
  }, [sessionId])
  const collabEntryRef = useRef<HTMLButtonElement | null>(null)
  const wasCollabDockOpenRef = useRef(false)
  const terminalColumnRef = useRef<HTMLDivElement | null>(null)
  const [terminalColumnHeight, setTerminalColumnHeight] = useState(0)
  // Set when a backing child session was opened from the dock; drives the
  // breadcrumb chip and Escape-back until the user leaves the child session.
  const [childContext, setChildContext] = useState<{
    childId: string
    childAgentType: string
    parentId: string
    parentAgentType: string
    parentLabel: string
  } | null>(null)
  const collabDockOpen = collabMode !== 'closed'
  const collabAvailable = collabStatus.kind === 'ready' && !isGraphEmpty(collabStatus.detail)
  // True once the current session has shown confirmed collaboration data: a
  // later live transition (delete → session_not_found, re-index → zero-child)
  // then surfaces the typed state inside the open dock instead of silently
  // dropping it, while sessions that never had data keep the dock hidden.
  const collabHadDataRef = useRef(false)
  // Expanded height clamp: min 120px, max min(40vh, column height minus the
  // 240px the terminal must keep).
  const collabMaxHeight = Math.max(
    MIN_DOCK_HEIGHT_PX,
    Math.min(Math.floor(window.innerHeight * 0.4), terminalColumnHeight - MIN_TERMINAL_HEIGHT_PX),
  )
  const collabRequestedHeight = collabAutoFit && collabContentHeight !== null ? collabContentHeight : collabHeight
  const collabEffectiveHeight = Math.max(MIN_DOCK_HEIGHT_PX, Math.min(collabMaxHeight, collabRequestedHeight))
  // Live follow (tail -f): pin viewport to bottom on every live refresh.
  // Only offered for active sessions; cleared when the session goes idle or changes.
  const [followOutput, setFollowOutput] = useState(false)
  // Session id that already auto-engaged follow on open (null = not yet / not
  // live). Prevents detail refetches from re-enabling follow after the user
  // turned it off; reset by the session-switch effect below.
  const autoFollowSessionRef = useRef<string | null>(null)
  // Per-session view memory: follow choice + scroll position, saved when
  // switching away so switching back restores where the user left instead of
  // re-opening at the default (top / auto-follow tail). The content-stable
  // viewportAnchor (original logical line) supersedes the legacy display-row
  // viewportLine, which drifts with fold state.
  const sessionViewMemoryRef = useRef(new Map<string, { follow: boolean; wasLive: boolean; viewportLine: number | null; viewportAnchor?: ViewportAnchor | null }>())
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
  // One-shot content anchor for the same purpose; survives fold-state and
  // re-wrap differences between save and restore. Also refreshed by the
  // terminal's cleanup report (analytics detour, mid-session unmount).
  const [restoreViewportAnchor, setRestoreViewportAnchor] = useState<ViewportAnchor | null>(null)
  // Initial render still streaming: drives the header status chip's 加载中 state.
  const [terminalLoading, setTerminalLoading] = useState(true)
  // Current session id as a ref so the terminal's cleanup-time anchor report
  // (which echoes its own sessionId) can tell a same-session unmount from a
  // switch-away.
  const sessionIdRef = useRef(sessionId)
  sessionIdRef.current = sessionId
  // TerminalPanel cleanup reports the final reading position here. Passive
  // effect destroys all run before any create in the same commit, so on a
  // session switch this lands under the OUTGOING id before the switch effect
  // merges it into that session's memory entry.
  const handleSaveViewportAnchor = useCallback((anchorSessionId: string, anchor: ViewportAnchor) => {
    const memory = sessionViewMemoryRef.current
    const prior = memory.get(anchorSessionId)
    memory.set(anchorSessionId, {
      follow: prior?.follow ?? false,
      wasLive: prior?.wasLive ?? false,
      viewportLine: prior?.viewportLine ?? null,
      viewportAnchor: anchor,
    })
    if (sessionIdRef.current === anchorSessionId) setRestoreViewportAnchor(anchor)
  }, [])
  // 时间戳前缀设置(后端 ts 渲染参数);null = 设置未加载,先不挂终端,
  // 避免渲染与 positions 用了不同的 ts 导致行号错位。
  const [tsKinds, setTsKinds] = useState<string | null>(null)
  const [bookmarkBusy, setBookmarkBusy] = useState(false)
  const [bookmarkError, setBookmarkError] = useState<string | null>(null)
  const [noteEditorOpen, setNoteEditorOpen] = useState(false)
  const [snippetSaving, setSnippetSaving] = useState(false)
  const [snippetNotice, setSnippetNotice] = useState<string | null>(null)
  const termControlRef = useRef<TerminalControl | null>(null)
  const saveSnippet = useCallback(async (content: string, sourceKind: 'selection' | 'assistant', turnIndex?: number) => {
    if (!session || snippetSaving || !content.trim()) return
    setSnippetSaving(true)
    setSnippetNotice(null)
    try {
      await createSnippet({
        content,
        agent_type: session.agent_type,
        session_id: session.id,
        session_name: session.name,
        source_kind: sourceKind,
        ...(turnIndex === undefined ? {} : { turn_index: turnIndex }),
      })
      setSnippetNotice('snippets.saved')
    } catch {
      setSnippetNotice('snippets.saveFailed')
    } finally {
      setSnippetSaving(false)
    }
  }, [session, snippetSaving])
  const handleToggleFollow = useCallback(() => {
    setFollowOutput(currentlyFollowing => {
      const shouldFollow = !currentlyFollowing
      if (shouldFollow) {
        const terminal = termControlRef.current
        if (terminal) {
          const metrics = terminal.getMetrics()
          terminal.scrollToLine(Math.floor(metrics.scrollHeight / TERMINAL_LINE_HEIGHT))
        }
      }
      return shouldFollow
    })
  }, [])
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

  // Resolve one path candidate on disk (cached). Keeping this per candidate
  // lets multiple path matches on one row validate independently.
  const resolveFileCandidate = useCallback(async (candidate: TerminalFileMatch): Promise<TerminalFileMatch | null> => {
    const cwd = sessionCwdRef.current
    const key = cwd + '\0' + candidate.path
    let ok = pathCheckCache.current.get(key)
    let resolved: string | null = null
    if (ok === undefined) {
      resolved = await resolveFile(candidate.path, cwd).catch(() => null)
      ok = resolved !== null
      pathCheckCache.current.set(key, ok)
    } else if (ok) {
      resolved = await resolveFile(candidate.path, cwd).catch(() => null)
    }
    return ok && resolved ? { path: resolved, line: candidate.line } : null
  }, [])

  // Resolve the first candidate on the row that exists on disk (cached).
  // textOffset is a JavaScript string offset, never an xterm cell column.
  const resolveRowFile = useCallback(async (lineText: string, textOffset: number | null): Promise<TerminalFileMatch | null> => {
    for (const candidate of extractPathsAt(lineText, textOffset, fileExtsRef.current)) {
      const resolved = await resolveFileCandidate(candidate)
      if (resolved) return resolved
    }
    return null
  }, [resolveFileCandidate])

  // Left-click on a row with file context opens a small action popover at the
  // cursor (editor / new tab / diff) instead of a single hard-wired action.
  // foldKey (from path-bearing tool headers) adds 展开/收起 next to open-file.
  const openFilePopover = useCallback((bufLine: number, meta: TerminalActivateMeta | undefined, editIdx: number | null, selectedFile?: TerminalFileMatch) => {
    if (!meta) return
    const ctrl = termControlRef.current
    setCtxMenu({
      clientX: meta.clientX,
      clientY: meta.clientY,
      originalRow: ctrl ? ctrl.toOriginalLine(bufLine) : bufLine,
      cellColumn: meta.cellColumn,
      textOffset: meta.textOffset,
      lineText: meta.lineText,
      selectionText: ctrl?.getSelectionText() ?? '',
      collapsedFoldKeys: ctrl?.getCollapsedFoldKeys() ?? [],
      selectedFile: selectedFile ?? meta.selectedFile,
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
        match: (text: string) => {
          const data = parseEditHeaderLine(text)
          return data ? [{ data }] : []
        },
        tooltip: t('replay.fileActions'),
        onActivate: (bufLine: number, _data: unknown, matchIndex: number, meta?: TerminalActivateMeta) => {
          const ctrl = termControlRef.current
          const orig = ctrl ? ctrl.toOriginalLine(bufLine) : bufLine
          const editPositions = editPositionsSorted(positionsRef.current?.positions ?? [])
          const idx = editIndexForRow(editsRef.current, editPositions, orig)
          openFilePopover(bufLine, meta, idx >= 0 ? idx : matchIndex)
        },
      },
      {
        // "[+] N 行被截断（点击展开）" lines → full output modal via the
        // "trunc" position at the same original row.
        match: (text: string) => (/\[\+\] \d+ 行被截断/.test(text) ? [{ data: {} }] : []),
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
        // Rendered Markdown links retain their destination in parentheses;
        // open only http(s) URLs in a new tab. URL matching comes before the
        // file matcher because URL path fragments are not local files.
        // Tooltip includes the full destination so truncated terminal rows
        // still show where a click will navigate.
        match: (text: string) => extractTerminalUrlMatches(text).map((urlMatch: TerminalUrlMatch) => ({
          data: urlMatch,
          textRange: { start: urlMatch.start, end: urlMatch.end },
        })),
        tooltip: (data) => t('replay.openLink', { url: (data as TerminalUrlMatch).value }),
        onActivate: (_bufLine: number, url: unknown) => {
          const urlMatch = url as TerminalUrlMatch
          window.open(urlMatch.value, '_blank', 'noopener,noreferrer')
        },
      },
      {
        // Rows containing an allowlisted file-path token → same file popover.
        // Registered last: edit headers and truncation lines take precedence.
        // validate: the affordance only appears when some candidate on the
        // row actually resolves to an existing file (multi-token rows check
        // every candidate, so "cd /some/dir && vim a.vue" still qualifies).
        match: (text: string) => extractPathMatches(text, fileExtsRef.current).map((pathMatch: PathMatch) => ({
          data: pathMatch,
          textRange: { start: pathMatch.start, end: pathMatch.end },
        })),
        tooltip: t('replay.openFileMenu'),
        validate: async (_lineText: string, data: unknown) => (await resolveFileCandidate(data as PathMatch)) !== null,
        onActivate: (bufLine: number, data: unknown, _idx: number, meta?: TerminalActivateMeta) => {
          openFilePopover(bufLine, meta, null, data as PathMatch)
        },
      },
    ])
  }, [openFilePopover, resolveFileCandidate, t])

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

  // Return focus to the Collaboration entry button when the dock closes.
  useEffect(() => {
    if (!collabDockOpen && wasCollabDockOpenRef.current) {
      collabEntryRef.current?.focus()
    }
    wasCollabDockOpenRef.current = collabDockOpen
  }, [collabDockOpen])

  // Collaboration detail per session. The entry button and the dock are both
  // gated on this: unsupported agents and exact zero-child graphs never show
  // a dead entry (design §10.4 — hidden without confirmed data).
  // Guard against the outgoing session's stale detail surviving into the
  // render for a new sessionId (fetchSession hasn't resolved yet) — the same
  // staleness pattern sessionIsLive already guards below.
  const collabAgentType = session && session.id === sessionId ? session.agent_type : null
  useEffect(() => {
    const gen = ++collabGenRef.current
    collabEtagRef.current = null
    if (!sessionId || !collabAgentType) {
      setCollabStatus({ kind: 'loading' })
      return
    }
    const ctrl = new AbortController()
    setCollabStatus({ kind: 'loading' })
    fetchCollaborationDetail(sessionId, collabAgentType, { signal: ctrl.signal })
      .then((res) => {
        if (gen !== collabGenRef.current || res === 'not-modified') return
        collabEtagRef.current = res.etag
        setCollabStatus({ kind: 'ready', detail: res.detail })
      })
      .catch((err: unknown) => {
        if (gen !== collabGenRef.current || ctrl.signal.aborted) return
        setCollabStatus({ kind: 'error', code: err instanceof APIError ? err.code : 'request_failed' })
      })
    return () => ctrl.abort()
  }, [sessionId, collabAgentType])

  // Conditional live refetch on the backend session-change ping. A 304 keeps
  // the mounted graph untouched; a terminal typed failure replaces it (the
  // user learns the graph is gone) while a transient failure keeps the
  // current state for the next ping.
  useEffect(() => {
    if (!sessionId || !collabAgentType) return
    return watchSessionsChanged(() => {
      const gen = ++collabGenRef.current
      fetchCollaborationDetail(sessionId, collabAgentType, { etag: collabEtagRef.current })
        .then((res) => {
          if (gen !== collabGenRef.current || res === 'not-modified') return
          collabEtagRef.current = res.etag
          setCollabStatus({ kind: 'ready', detail: res.detail })
        })
        .catch((err: unknown) => {
          if (gen !== collabGenRef.current) return
          if (err instanceof APIError && COLLAB_TERMINAL_CODES.has(err.code)) {
            setCollabStatus({ kind: 'error', code: err.code })
          }
        })
    })
  }, [sessionId, collabAgentType])

  // Track the terminal column height for the dock's max-height clamp. Runs
  // again when the session detail arrives: the column only mounts after the
  // early-return branches, so keying on sessionId alone would observe null.
  useEffect(() => {
    const column = terminalColumnRef.current
    if (!column) return
    const measure = () => setTerminalColumnHeight(column.clientHeight)
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(column)
    return () => ro.disconnect()
  }, [sessionId, session?.id])

  useEffect(() => {
    collabHadDataRef.current = false
  }, [sessionId])
  useEffect(() => {
    if (collabAvailable) collabHadDataRef.current = true
  }, [collabAvailable])

  // Dock mode/height changes alter only the terminal container's height, so
  // the buffer never reflows; preserve the logical top line across the
  // resulting fit anyway (xterm clamps scroll when rows shrink).
  const preserveTerminalPosition = useCallback((mutate: () => void) => {
    const ctrl = termControlRef.current
    const topLine = ctrl?.getViewportTopLine()
    mutate()
    if (topLine == null || !ctrl) return
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        if (!followOutputRef.current) ctrl.scrollToLine(topLine)
      })
    })
  }, [])

  const setCollabModePersist = useCallback(
    (mode: CollabDockMode) => {
      preserveTerminalPosition(() => setCollabMode(mode))
    },
    [preserveTerminalPosition],
  )

  // Persist open/collapsed mode immediately; the drag height is persisted on
  // drag end (pointermove updates would otherwise write per frame).
  const collabHeightRef = useRef(collabHeight)
  useEffect(() => {
    collabHeightRef.current = collabHeight
  }, [collabHeight])
  useEffect(() => {
    writeCollabDockPrefs(collabMode, collabHeightRef.current, collabAutoFit)
  }, [collabMode, collabAutoFit])
  const persistCollabHeight = useCallback(() => {
    writeCollabDockPrefs(collabMode, collabHeightRef.current, collabAutoFit)
  }, [collabMode, collabAutoFit])

  const openBackingSession = useCallback(
    (id: string, agentType: string) => {
      const parent = sessionDetailRef.current
      if (parent) {
        setChildContext({
          childId: id,
          childAgentType: agentType,
          parentId: parent.id,
          parentAgentType: parent.agent_type,
          parentLabel: parent.name || parent.id,
        })
      }
      onSelect?.(id, agentType)
    },
    [onSelect],
  )

  const returnToParentSession = useCallback(() => {
    setChildContext((ctx) => {
      if (ctx) onSelect?.(ctx.parentId, ctx.parentAgentType)
      return null
    })
  }, [onSelect])

  // Global-search landing on a subagent session reuses the dock breadcrumb:
  // the main view shows the child transcript while the chip offers a jump
  // back to the root parent (which is where the sidebar focus landed).
  useEffect(() => {
    if (searchRootRef && sessionId === searchRootRef.sessionId) {
      setChildContext({
        childId: searchRootRef.sessionId,
        childAgentType: searchRootRef.childAgentType,
        parentId: searchRootRef.root.id,
        parentAgentType: searchRootRef.root.agentType,
        parentLabel: searchRootRef.root.name || searchRootRef.root.id,
      })
    }
  }, [searchRootRef, sessionId])

  // Jump actions resolve the frozen source anchors against the existing
  // replay positions (exact event_id → tool_call_id → turn → timestamp).
  // Launch and result use distinct coordinates even though both share one
  // native tool_call_id.
  const jumpToCollabAnchor = useCallback(
    (anchor: SourceAnchorDTO | null, targetKind: 'launch' | 'result') => {
      const positions = positionsData?.positions
      if (!positions) return
      const target = resolveAnchorJump(anchor, positions, targetKind)
      const control = termControlRef.current
      if (!target || !control) return

      // A Chrys sub-agent call lives inside the parent Tools fold. Resolve in
      // the original coordinate space, open every collapsed ancestor first,
      // then jump only after xterm has recomposed the buffer. Otherwise the
      // logical target is remapped to a collapsed group header and appears to
      // be a no-op (or lands at an imprecise location after manual expansion).
      const ancestorFoldKeys = foldKeysContainingTarget(folds, target.lineStart, target.logicalStart)
      const collapsed = new Set(control.getCollapsedFoldKeys())
      const toOpen = ancestorFoldKeys.filter((key) => collapsed.has(key))
      const jump = () => control.jumpToPosition(target.lineStart, target.logicalStart)
      if (toOpen.length > 0) {
        control.setFoldsCollapsed(toOpen, false, target.lineStart, jump)
      } else {
        jump()
      }
    },
    [folds, positionsData],
  )

  // Locate a Diff-modal edit in the terminal: same contract as the collab
  // jump — open collapsed ancestor folds first, then resolve the position
  // through its logical line so the landing row survives fold-badge wraps.
  const locateEditInTerminal = useCallback(
    (editIdx: number) => {
      const positions = positionsData?.positions
      const control = termControlRef.current
      if (!positions || !control) return
      // The edits array (raw event order) and the edit positions (render
      // order) can diverge on nested Chrys events; match via tool_call_id.
      const target = editPositionForIndex(edits, editPositionsSorted(positions), editIdx)
      if (!target) return
      const payloadLogical = target.payload?.['logical_start']
      const logicalStart = typeof payloadLogical === 'number' ? payloadLogical : undefined
      setShowDiffModal(false)
      const ancestorFoldKeys = foldKeysContainingTarget(folds, target.line_start, logicalStart)
      const collapsed = new Set(control.getCollapsedFoldKeys())
      const toOpen = ancestorFoldKeys.filter((key) => collapsed.has(key))
      const jump = () => control.jumpToPosition(target.line_start, logicalStart)
      if (toOpen.length > 0) {
        control.setFoldsCollapsed(toOpen, false, target.line_start, jump)
      } else {
        jump()
      }
    },
    [folds, positionsData, edits],
  )

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
      // Merge, don't replace: the terminal's cleanup already stored the
      // content anchor for this outgoing session earlier in the same commit.
      const priorMemory = sessionViewMemoryRef.current.get(prevId)
      sessionViewMemoryRef.current.set(prevId, {
        follow: followOutputRef.current,
        wasLive: outgoingLive,
        viewportLine: m && lastMetricsSessionIdRef.current === prevId && m.scrollHeight > m.clientHeight
          ? Math.round(m.scrollTop / TERMINAL_LINE_HEIGHT)
          : null,
        viewportAnchor: priorMemory?.viewportAnchor ?? null,
      })
    }
    prevSessionIdRef.current = sessionId
    const saved = sessionId ? sessionViewMemoryRef.current.get(sessionId) : undefined
    autoFollowSessionRef.current = saved?.wasLive ? sessionId : null
    setFollowOutput(saved?.wasLive ? saved.follow : false)
    setRestoreScrollLine(saved?.viewportLine ?? null)
    setRestoreViewportAnchor(saved?.viewportAnchor ?? null)
    setTerminalLoading(true)
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

  // On session open: optionally expand nav (user/tool) and pin it when the
  // settings preference is enabled. Default is off — no auto-open, no auto-pin.
  useEffect(() => {
    // Drop analytics-driven tool filters when leaving a session so they do not
    // stick to a later manual open of the tool panel.
    setToolFilterRequest(null)
    setOutlineAnchor(null)
    if (!sessionId) {
      setShowUserPanel(false)
      setShowToolPanel(false)
      setShowOutlinePanel(false)
      setNavPinned(false)
      return
    }
    const pref = getNavOpenPref()
    if (pref === 'user') {
      setShowUserPanel(true)
      setShowToolPanel(false)
      setShowOutlinePanel(false)
      setNavPinned(true)
    } else if (pref === 'tool') {
      setShowToolPanel(true)
      setShowUserPanel(false)
      setShowOutlinePanel(false)
      setNavPinned(true)
    } else {
      setShowUserPanel(false)
      setShowToolPanel(false)
      setShowOutlinePanel(false)
      setNavPinned(false)
    }
  }, [sessionId])

  // In-terminal search (toolbar Find or Ctrl/Cmd+F). Capture phase: focus
  // usually sits in xterm's helper textarea, which stops keydown before bubble.
  const [searchOpen, setSearchOpen] = useState(false)
  // Bumped on every open so an already-open bar pulls focus back to itself.
  const [searchFocusToken, setSearchFocusToken] = useState(0)
  const openTerminalSearch = useCallback(() => {
    setViewMode('terminal')
    setSearchOpen(true)
    setSearchFocusToken(t => t + 1)
  }, [])
  const toggleTerminalSearch = useCallback(() => {
    if (viewMode === 'terminal' && searchOpen) {
      setSearchOpen(false)
      return
    }
    openTerminalSearch()
  }, [viewMode, searchOpen, openTerminalSearch])
  useEffect(() => { setSearchOpen(false) }, [sessionId])
  useEffect(() => {
    if (viewMode !== 'terminal') setSearchOpen(false)
  }, [viewMode])
  useEffect(() => {
    if (!sessionId) return
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && (e.key === 'f' || e.key === 'F')) {
        const target = e.target
        // Keep Ctrl+F when focus is in the find bar or xterm's helper textarea.
        const inTerminalFind = target instanceof HTMLElement && !!target.closest(
          '[data-testid="terminal-search-bar"], .xterm',
        )
        if (
          !inTerminalFind
          && (target instanceof HTMLInputElement
            || target instanceof HTMLTextAreaElement
            || (target instanceof HTMLElement && target.isContentEditable))
        ) return
        e.preventDefault()
        openTerminalSearch()
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [sessionId, openTerminalSearch])

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
      const selectedText = termControlRef.current?.getSelectionText() ?? ''
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

    const editPositions = editPositionsSorted(positionsData?.positions ?? [])
    const editIdx = ctxMenu.originalRow !== null
      ? editIndexForRow(edits, editPositions, ctxMenu.originalRow)
      : -1
    if (editIdx >= 0 && edits[editIdx]) {
      const e = edits[editIdx]
      const search = (e.new_string || e.old_string).split('\n').find(l => l.trim())
      void resolveFile(e.file_path, cwd).then(p => { if (p) show(p, null, search) })
    } else if (ctxMenu.selectedFile) {
      void resolveFileCandidate(ctxMenu.selectedFile).then(hit => {
        if (hit) show(hit.path, hit.line)
      })
    } else {
      void resolveRowFile(ctxMenu.lineText, ctxMenu.textOffset).then(hit => {
        if (hit) show(hit.path, hit.line)
      })
    }
    return () => { cancelled = true }
  }, [ctxMenu, edits, positionsData, session, resolveFileCandidate, resolveRowFile])

  const ctxMenuSections = useMemo((): TerminalMenuSection[] => {
    const selectedText = ctxMenu?.selectionText ?? termControlRef.current?.getSelectionText() ?? ''
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
      handleToggleFollow()
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
          {
            label: t('snippets.saveSelection'),
            disabled: selectedText.length === 0 || snippetSaving,
            onClick: () => {
              void saveSnippet(selectedText, 'selection')
              setCtxMenu(null)
            },
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
  }, [bookmarkBusy, ctxMenu, fileTarget, folds, followOutput, handleToggleFollow, jump, positionsData, saveSnippet, session, sessionIsLive, snippetSaving, t, toggleBookmark])

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

  const outlineItems = useMemo(
    () => outlineItemsFromPositions(positionsData?.positions),
    [positionsData],
  )

  // Current position = outline event nearest the viewport center. Computed
  // against ALL outline items — the panel decides afterwards whether the
  // active filters hide it, so disabling a category never silently retargets
  // "current" onto a different event.
  const currentOutlineKey = useMemo(
    () => (outlineAnchor === null ? null : nearestOutlineKey(outlineItems, outlineAnchor)),
    [outlineItems, outlineAnchor],
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
  const handleJumpToTool = useCallback((name: string) => {
    setToolFilterRequest(prev => ({ name, token: (prev?.token ?? 0) + 1 }))
    setShowToolPanel(true)
    setShowUserPanel(false)
    setShowOutlinePanel(false)
    setNavPinned(false)
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
      else if (e.key === 'Escape' && childContext && sessionId === childContext.childId) { e.preventDefault(); returnToParentSession() }
      else if (e.key === '?' && !e.shiftKey && !e.metaKey && !e.ctrlKey) { e.preventDefault(); setShowHelp(h => !h) }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [jump, session?.turns?.length, sessionId, childContext, returnToParentSession])

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
    // Viewport anchor for the outline's current-position tracking. The
    // metrics callback already rides TerminalPanel's rAF-batched
    // scroll/resize/render lifecycle; only commit state on change.
    const anchor = termControlRef.current?.getViewportAnchor() ?? null
    setOutlineAnchor(prev => (prev === anchor ? prev : anchor))
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
      <GlobalSearch onSelect={onSelect} onOpenCodingQuotas={onOpenCodingQuotas} />
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
      <GlobalSearch onSelect={onSelect} onOpenCodingQuotas={onOpenCodingQuotas} />
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
  // Non-replayable record states get a dedicated empty state (never “0 turns success”).
  if (!session || !(session.turns?.length)) {
    const rec = session ? presentFromSession(session) : null
    const emptyKey = rec?.emptyStateKey
    const emptyCapHint = session ? sessionCapabilityHeaderHint(session.agent_capabilities) : null
    const emptyAgentTitle = session
      ? (agentsCatalog.find(a => a.type === session.agent_type)?.display_name || session.agent_type || 'agent')
      : ''
    return (
      <main className="flex-1 min-w-[360px] bg-[var(--bg-surface)] flex flex-col">
        <GlobalSearch onSelect={onSelect} onOpenCodingQuotas={onOpenCodingQuotas} />
        {session && (
          <header className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center gap-2 px-3" style={{ height: '40px' }}>
            <button
              type="button"
              onClick={() => {
                setCapPanelOpen(true)
                if (agentsCatalog.length === 0) {
                  void fetchAgents().then(setAgentsCatalog).catch(() => setAgentsCatalog([]))
                }
              }}
              className="h-7 max-w-[8rem] rounded-md border border-[var(--border-default)] px-1.5 inline-flex items-center gap-1 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]"
              aria-label={t('capability.session.openButton')}
              title={emptyAgentTitle}
              data-testid="session-agent-capability-button"
            >
              <AgentIcon agentType={session.agent_type} size={16} />
              {emptyCapHint?.kind === 'calm' && session.agent_capabilities && (
                <span
                  className="shrink-0 text-meta font-medium text-[var(--accent-green)]"
                  title={t('capability.session.hintCalm')}
                  aria-label={t('capability.session.hintCalm')}
                  data-testid="session-cap-hint-calm"
                >
                  ✓
                </span>
              )}
              {emptyCapHint?.kind === 'missing' && (
                <span className="shrink-0 text-meta text-[var(--warning)]">
                  {t('capability.session.hintMissing', { n: emptyCapHint.count })}
                </span>
              )}
              {emptyCapHint?.kind === 'estimated' && (
                <span className="shrink-0 text-meta text-[var(--accent-blue)]">
                  {t('capability.session.hintEstimated')}
                </span>
              )}
              {emptyCapHint?.kind === 'unsupported' && (
                <span className="shrink-0 text-meta text-[var(--error)]">
                  {t('capability.session.hintUnsupported', { n: emptyCapHint.count })}
                </span>
              )}
            </button>
            {rec && (
              <button
                type="button"
                onClick={() => setRecordPanelOpen(true)}
                className={`h-7 rounded-md border px-2 inline-flex items-center gap-1.5 text-nav ${toneClass(rec.tone)}`}
                aria-label={t('record.pill.open')}
                title={recordStatusLabel(rec, t)}
                data-testid="session-record-status-button"
              >
                <span className="truncate">{t('record.pill.label')}</span>
              </button>
            )}
            <button
              type="button"
              onClick={() => setGitPanelOpen(true)}
              className="h-7 rounded-md border border-[var(--border-default)] px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]"
              data-testid="session-git-evidence-button"
            >
              {t('git.panel.open')}
            </button>
          </header>
        )}
        <div className="flex-1 flex items-center justify-center">
          <div className="text-center px-6 max-w-md" data-testid="record-empty-state">
            <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--bg-inset)] text-nav text-[var(--text-muted)]">MSG</div>
            <h3 className="text-body font-medium text-[var(--text-primary)]">
              {emptyKey && rec
                ? recordStatusLabel(rec, t)
                : session
                  ? t('replay.noReplay')
                  : t('replay.noSessions')}
            </h3>
            <p className="text-helper text-[var(--text-muted)] mt-1">
              {emptyKey
                ? t(emptyKey)
                : session
                  ? t('replay.createdNoTurns')
                  : t('replay.agentHint')}
            </p>
            {emptyKey && (
              <p className="text-meta text-[var(--text-muted)] mt-2">{t('record.empty.notSuccess')}</p>
            )}
            {session && emptyKey && (
              <button
                type="button"
                className="mt-3 rounded-md border border-[var(--border-default)] px-3 py-1.5 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]"
                onClick={() => setRecordPanelOpen(true)}
              >
                {t('record.pill.open')}
              </button>
            )}
          </div>
        </div>
        {session && (
          <>
            <SessionCapabilityPanel
              open={capPanelOpen}
              session={session}
              agentInfo={agentsCatalog.find(a => a.type === session.agent_type) ?? null}
              onClose={() => setCapPanelOpen(false)}
            />
            <RecordStatusPanel
              open={recordPanelOpen}
              session={session}
              onClose={() => setRecordPanelOpen(false)}
              onRemovedFromIndex={() => onSelect?.('')}
            />
            {gitPanelOpen && (
              <GitEvidencePanel
                session={session}
                onClose={() => setGitPanelOpen(false)}
                onSelectSession={onSelect}
              />
            )}
          </>
        )}
      </main>
    )
  }

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
  const agentInfo = agentsCatalog.find(a => a.type === session.agent_type) ?? null
  const capHint = sessionCapabilityHeaderHint(session.agent_capabilities)
  const agentDisplayName = agentInfo?.display_name || session.agent_type || 'agent'
  const tokenHeader = sessionTokenHeaderDisplay(
    session.agent_capabilities?.status?.tokens?.state,
    totalTokens,
  )
  const tokenUnits = {
    tenThousand: t('token.unit.tenThousand'),
    hundredMillion: t('token.unit.hundredMillion'),
  }
  const uaPlatform = typeof navigator !== 'undefined'
    ? ((navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform
      || navigator.userAgent
      || navigator.platform
      || '')
    : ''
  const findShortcut = /Mac|iPhone|iPad|iPod/i.test(uaPlatform) ? '⌘F' : 'Ctrl+F'
  const tokenExactFull =
    tokenHeader.kind === 'value' ? formatTokenCount(locale, tokenHeader.total, 'full') : ''
  const tokenHeaderText =
    tokenHeader.kind === 'missing'
      ? t('capability.session.tokensMissing')
      : tokenHeader.kind === 'unsupported'
        ? t('capability.session.tokensUnsupported')
        : tokenHeader.kind === 'not_applicable'
          ? t('capability.session.tokensNA')
          : tokenHeader.kind === 'unknown'
            ? t('capability.session.tokensUnknown')
            : `${fmtTokens(tokenHeader.total, locale, tokenDisplayMode, tokenUnits)} ${t('replay.tokens')}`

  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden relative">
      <GlobalSearch onSelect={onSelect} onOpenCodingQuotas={onOpenCodingQuotas} />
      <header className="relative flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-3" style={{ height: '40px', zIndex: 'var(--z-sticky)' }} data-testid="session-toolbar">
        <div className="flex items-center gap-2">
          <ResumeTerminalControl session={session} />
          <button
            type="button"
            onClick={() => {
              setCapPanelOpen(true)
              if (agentsCatalog.length === 0) {
                void fetchAgents().then(setAgentsCatalog).catch(() => setAgentsCatalog([]))
              }
            }}
            className="h-7 max-w-[8rem] rounded-md border border-[var(--border-default)] px-1.5 inline-flex items-center gap-1 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            aria-label={t('capability.session.openButton')}
            title={agentDisplayName}
            data-testid="session-agent-capability-button"
          >
            <AgentIcon agentType={session.agent_type} size={16} />
            {capHint.kind === 'calm' && session.agent_capabilities && (
              <span
                className="shrink-0 text-meta font-medium text-[var(--accent-green)]"
                title={t('capability.session.hintCalm')}
                aria-label={t('capability.session.hintCalm')}
                data-testid="session-cap-hint-calm"
              >
                ✓
              </span>
            )}
            {capHint.kind === 'missing' && (
              <span className="shrink-0 text-meta text-[var(--warning)]">
                {t('capability.session.hintMissing', { n: capHint.count })}
              </span>
            )}
            {capHint.kind === 'estimated' && (
              <span className="shrink-0 text-meta text-[var(--accent-blue)]">
                {t('capability.session.hintEstimated')}
              </span>
            )}
            {capHint.kind === 'unsupported' && (
              <span className="shrink-0 text-meta text-[var(--error)]">
                {t('capability.session.hintUnsupported', { n: capHint.count })}
              </span>
            )}
          </button>
          {session && (() => {
            const rec = presentFromSession(session)
            return (
              <button
                type="button"
                onClick={() => setRecordPanelOpen(true)}
                className={`h-7 rounded-md border px-2 inline-flex items-center gap-1.5 text-nav hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${toneClass(rec.tone)}`}
                aria-label={t('record.pill.open')}
                title={recordStatusLabel(rec, t)}
                data-testid="session-record-status-button"
              >
                <span className="truncate">{t('record.pill.label')}</span>
              </button>
            )
          })()}
          <button
            type="button"
            onClick={() => setGitPanelOpen(true)}
            className={`h-7 rounded-md border px-2 text-nav ${
              gitPanelOpen
                ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            } focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            aria-expanded={gitPanelOpen}
            data-testid="session-git-evidence-button"
          >
            {t('git.panel.open')}
          </button>
          {bookmarkError && (
            <span className="text-meta text-[var(--error)]" role="status">
              {t(bookmarkError)}
            </span>
          )}
          {snippetNotice && (
            <span className={`text-meta ${snippetNotice === 'snippets.saved' ? 'text-[var(--success)]' : 'text-[var(--error)]'}`} role="status">
              {t(snippetNotice)}
            </span>
          )}
          {openFileError && (
            <span className="text-meta text-[var(--error)]" role="status">
              {openFileError}
            </span>
          )}
          <span className="text-[var(--border-default)]">|</span>
          <button
            type="button"
            onClick={toggleTerminalSearch}
            className={`h-7 rounded-md px-2 inline-flex items-center gap-1 text-nav ${
              viewMode === 'terminal' && searchOpen
                ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10'
                : 'text-[var(--text-secondary)]'
            } hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.findTitle', { shortcut: findShortcut })}
            aria-pressed={viewMode === 'terminal' && searchOpen}
            data-testid="session-terminal-find-button"
          >
            <SearchIcon className="h-3.5 w-3.5" />
            {t('replay.find')}
          </button>
          <button
            onClick={() => startTransition(() => setViewMode(v => v === 'analytics' ? 'terminal' : 'analytics'))}
            className={`h-7 rounded-md px-2 text-nav ${viewMode === 'analytics' ? 'text-[var(--accent-blue)] bg-[var(--accent-blue)]/10' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
          >
            {t('replay.analytics')}
          </button>
          <span className="text-[var(--border-default)]">|</span>
          <a href={`/api/sessions/${session.id}/export`} className="h-7 rounded-md px-2 inline-flex items-center text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] no-underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">{t('replay.export')}</a>
          <span className="text-[var(--border-default)]">|</span>
          <button
            onClick={() => setShowAIPanel(true)}
            className={`h-7 rounded-md px-2 text-nav ${
              showAIPanel
                ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'text-[var(--text-secondary)]'
            } hover:bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.aiPanel')}
            aria-expanded={showAIPanel}
            aria-controls="session-assistant-panel"
          >
            {t('replay.sessionAssistant')}
          </button>
          {collabAvailable && (
            <button
              type="button"
              ref={collabEntryRef}
              onClick={() => setCollabModePersist(collabMode === 'closed' ? 'expanded' : 'closed')}
              className={`h-7 rounded-md px-2 text-nav ${
                collabDockOpen
                  ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                  : 'text-[var(--text-secondary)]'
              } hover:bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
              title={t('collaboration.dock.open')}
              aria-expanded={collabDockOpen}
              aria-controls="collaboration-dock"
              data-testid="collaboration-entry-button"
            >
              {t('collaboration.dock.open')}
            </button>
          )}
        </div>
        <span className="flex-1 text-center text-helper text-[var(--text-secondary)] truncate px-2">
          {/*
            Explicit live-state indicator: the four states the replay can be
            in. 加载中 wins while the initial render streams; for a live
            session the follow toggle decides 追尾中 vs 已暂停; a session
            whose source stopped writing shows 来源已停止 instead of silently
            losing the badge. Deliberately chromeless (no pill background, no
            hover, default cursor): it sits inside the meta text run as a
            colored-dot status readout, not a button.
          */}
          {viewMode === 'terminal' && terminalLoading ? (
            <span className="mr-1.5 inline-flex cursor-default items-center gap-1 text-meta text-[var(--accent-blue)]" data-testid="replay-status" data-state="loading">
              <span className="inline-block h-1.5 w-1.5 rounded-full bg-[var(--accent-blue)]" />{t('replay.statusLoading')}
            </span>
          ) : sessionIsLive ? (
            followOutput ? (
              <span className="mr-1.5 inline-flex cursor-default items-center gap-1 text-meta text-[var(--accent-green)]" data-testid="replay-status" data-state="following">
                <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--accent-green)]" />{t('replay.statusFollowing')}
              </span>
            ) : (
              <span className="mr-1.5 inline-flex cursor-default items-center gap-1 text-meta text-[var(--accent-amber)]" data-testid="replay-status" data-state="paused">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-[var(--accent-amber)]" />{t('replay.statusPaused')}
              </span>
            )
          ) : (
            <span className="mr-1.5 inline-flex cursor-default items-center gap-1 text-meta text-[var(--text-muted)]" data-testid="replay-status" data-state="ended">
              <span className="inline-block h-1.5 w-1.5 rounded-full bg-[var(--text-muted)]" />{t('replay.statusEnded')}
            </span>
          )}
          {session.import_info && (
            <span
              className="mr-1.5 inline-flex items-center gap-1 rounded-sm bg-[color-mix(in_srgb,var(--accent-blue)_15%,transparent)] px-1.5 text-meta font-medium text-[var(--accent-blue)]"
              title={t('replay.importedReadOnly')}
            >
              {t('replay.importedFrom', { agent: getAgentLabel(session.import_info.original_agent_type), host: session.import_info.origin_host })}
            </span>
          )}
          <span
            data-testid="session-token-header"
            title={tokenExactFull ? `${tokenExactFull} ${t('replay.tokens')}` : undefined}
          >
            {modelName} · {tokenHeaderText} · {formatNumber(locale, session.turn_count)} {t('replay.turns')}
          </span>
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
                setShowOutlinePanel(false)
                setToolFilterRequest(null)
              } else {
                setNavPinned(false)
              }
              return next
            })}
            className={`h-7 whitespace-nowrap rounded-md border px-2 text-nav ${
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
                setShowOutlinePanel(false)
              } else {
                setToolFilterRequest(null)
                setNavPinned(false)
              }
              return next
            })}
            className={`h-7 whitespace-nowrap rounded-md border px-2 text-nav ${
              showToolPanel
                ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            } focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.toolCalls')}
          >
            {t('replay.toolCalls')}{toolCallCount > 0 ? ` ${formatNumber(locale, toolCallCount)}` : ''}
          </button>
          <button
            onClick={() => setShowOutlinePanel(v => {
              const next = !v
              if (next) {
                setShowUserPanel(false)
                setShowToolPanel(false)
                setToolFilterRequest(null)
              } else {
                setNavPinned(false)
              }
              return next
            })}
            className={`h-7 whitespace-nowrap rounded-md border px-2 text-nav ${
              showOutlinePanel
                ? 'border-[var(--accent-blue)] bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                : 'border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
            } focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]`}
            title={t('replay.outline')}
          >
            {t('replay.outline')}{outlineItems.length > 0 ? ` ${formatNumber(locale, outlineItems.length)}` : ''}
          </button>
        </div>
        <span ref={visibleRangeLabelRef} className="flex-shrink-0 text-meta text-[var(--text-muted)]">
          Turn ?/{session.turn_count}
        </span>
      </header>

      {presentFromSession(session).state === 'degraded' && !degradedBannerDismissed && (
        <div
          className="flex-shrink-0 flex items-center justify-between gap-2 border-b border-[var(--warning)]/30 bg-[var(--warning)]/10 px-3 py-1.5 text-meta text-[var(--warning)]"
          data-testid="record-degraded-banner"
          role="status"
        >
          <span>{t('record.banner.degraded')}</span>
          <div className="flex items-center gap-2">
            <button type="button" className="underline" onClick={() => setRecordPanelOpen(true)}>
              {t('record.pill.open')}
            </button>
            <button type="button" className="underline" onClick={() => setDegradedBannerDismissed(true)}>
              {t('record.banner.dismiss')}
            </button>
          </div>
        </div>
      )}
      {presentFromSession(session).state === 'degraded' && degradedBannerDismissed && (
        <button
          type="button"
          className="flex-shrink-0 self-start px-3 py-0.5 text-meta text-[var(--warning)] underline"
          onClick={() => setDegradedBannerDismissed(false)}
        >
          {t('record.banner.reopen')}
        </button>
      )}

      {showHelp && (
        <div className="absolute inset-0 z-20 flex items-center justify-center bg-[rgba(0,0,0,var(--opacity-overlay))]" onClick={() => setShowHelp(false)}>
          <div className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-lg p-6 max-w-sm" onClick={e => e.stopPropagation()}>
            <h3 className="text-nav font-semibold text-[var(--text-primary)] mb-3">{t('replay.shortcuts')}</h3>
            <div className="space-y-2 text-helper">
              {[
                ['j / ↓', t('replay.shortcutNext')],
                ['k / ↑', t('replay.shortcutPrevious')],
                [findShortcut, t('replay.shortcutFind')],
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
        <DiffModal sessionId={session.id} onClose={() => setShowDiffModal(false)} initialIdx={initialDiffIdx} onLocateInTerminal={locateEditInTerminal} />
      )}

      {gitPanelOpen && session && (
        <GitEvidencePanel
          session={session}
          onClose={() => setGitPanelOpen(false)}
          onSelectSession={onSelect}
        />
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
        <div ref={terminalColumnRef} className="flex min-w-0 flex-1 flex-col overflow-hidden">
          {childContext && sessionId === childContext.childId && (
            <div
              className="flex flex-shrink-0 items-center gap-2 border-b border-[var(--border-default)] bg-[var(--bg-surface)] px-3 py-1"
              data-testid="collaboration-child-context"
            >
              <span className="min-w-0 flex-1 truncate text-meta text-[var(--text-muted)]">
                {t('collaboration.backing.childContext', { label: childContext.parentLabel })}
              </span>
              <button
                type="button"
                onClick={(e) => { if (!openOnModifiedClick(e, childContext.parentAgentType, childContext.parentId)) returnToParentSession() }}
                onAuxClick={(e) => { openOnModifiedClick(e, childContext.parentAgentType, childContext.parentId) }}
                className="h-6 flex-shrink-0 rounded-md px-2 text-meta text-[var(--accent-blue)] hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                data-testid="collaboration-return-parent"
              >
                ← {t('collaboration.backing.back')}
              </button>
            </div>
          )}
          {collabDockOpen && (collabAvailable || collabHadDataRef.current) && (
            <CollaborationDock
              // Remount per session so the previous session's lanes and
              // selection never flash into the next session.
              key={sessionId}
              status={collabStatus}
              mode={collabMode === 'collapsed' ? 'collapsed' : 'expanded'}
              heightPx={collabEffectiveHeight}
              maxHeightPx={collabMaxHeight}
              onExpand={() => setCollabModePersist('expanded')}
              onCollapse={() => setCollabModePersist('collapsed')}
              onClose={() => setCollabModePersist('closed')}
              onResize={(heightPx) => {
                setCollabAutoFit(false)
                setCollabHeight(heightPx)
              }}
              onResizeEnd={persistCollabHeight}
              onContentHeightChange={setCollabContentHeight}
              onOpenSession={openBackingSession}
              onJumpToLaunch={(_id, anchor) => jumpToCollabAnchor(anchor, 'launch')}
              onJumpToResult={(_id, anchor) => jumpToCollabAnchor(anchor, 'result')}
              returnToParentActive={Boolean(childContext && sessionId === childContext.childId)}
            />
          )}
          <div className="relative flex min-w-0 flex-1 overflow-hidden">
          {viewMode === 'terminal' && searchOpen && (
            <TerminalSearchBar
              controlRef={termControlRef}
              refreshToken={foldVersion}
              focusToken={searchFocusToken}
              rightInset={navPinned && (showUserPanel || showToolPanel || showOutlinePanel) ? navPanelWidth : 0}
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
                initialViewportAnchor={restoreViewportAnchor}
                onSaveViewportAnchor={handleSaveViewportAnchor}
                onLoadingChange={setTerminalLoading}
                onFoldChange={handleFoldChange}
                onFoldPathActivate={(bufLine, meta) => openFilePopover(bufLine, meta, null)}
                onContextMenu={handleTerminalContextMenu}
                onScrollMetrics={handleTerminalScrollMetrics}
                onColsReady={handleColsReady}
                controlRef={termControlRef}
                expectedLines={positionsData?.total_lines}
                userPositions={userHighlightRanges}
                onJumpToUserMessage={handlePanelJump}
              />
            </Suspense>
          )}
          {sessionIsLive && viewMode === 'terminal' && (
            <button
              type="button"
              data-testid="follow-fab"
              onClick={handleToggleFollow}
              className={`absolute bottom-3 z-[var(--z-sticky)] h-9 rounded-full border px-3.5 inline-flex items-center gap-1.5 text-nav shadow-md backdrop-blur-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                followOutput
                  ? 'border-[var(--accent-green)]/40 bg-[color-mix(in_srgb,var(--accent-green)_18%,var(--bg-surface))] text-[var(--accent-green)]'
                  : 'border-[var(--border-default)] bg-[var(--bg-surface)]/95 text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
              }`}
              style={{
                right: 12 + (navPinned && (showUserPanel || showToolPanel) ? navPanelWidth : 0),
              }}
              title={followOutput ? t('replay.followFabOn') : t('replay.followFabOff')}
              aria-pressed={followOutput}
              aria-label={followOutput ? t('replay.followFabOn') : t('replay.followFabOff')}
            >
              {followOutput && (
                <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--accent-green)]" />
              )}
              {t('replay.follow')}
            </button>
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
              savingSnippet={snippetSaving}
              onSaveAssistantSnippet={(turnIndex, fallbackContent) => {
                const assistantContent = session.turns.find(turn => turn.turn_index === turnIndex)?.assistant_message || fallbackContent
                void saveSnippet(assistantContent, 'assistant', turnIndex)
              }}
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
          {viewMode === 'terminal' && showOutlinePanel && (
            <KeyEventOutlinePanel
              positions={positionsData}
              building={positionsBuilding}
              currentKey={currentOutlineKey}
              pinned={navPinned}
              onPinnedChange={setNavPinned}
              onWidthChange={setNavPanelWidth}
              onJump={handlePanelJump}
              onClose={() => {
                setShowOutlinePanel(false)
                setNavPinned(false)
              }}
            />
          )}
        </div>
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

      <SessionCapabilityPanel
        open={capPanelOpen}
        session={session}
        agentInfo={agentInfo}
        onClose={() => setCapPanelOpen(false)}
        onOpenCompare={() => {
          setCapPanelOpen(false)
          setCapCompareOpen(true)
          if (agentsCatalog.length === 0) {
            void fetchAgents().then(setAgentsCatalog).catch(() => setAgentsCatalog([]))
          }
        }}
      />
      <RecordStatusPanel
        open={recordPanelOpen}
        session={session}
        onClose={() => setRecordPanelOpen(false)}
        onRemovedFromIndex={() => onSelect?.('')}
      />
      <AgentCapabilityCompareDialog
        open={capCompareOpen}
        agents={agentsCatalog}
        onClose={() => setCapCompareOpen(false)}
      />
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
