import { useEffect, useRef, useState } from 'react'
import { Terminal, type IBuffer, type IDecoration, type IMarker } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { WebglAddon } from '@xterm/addon-webgl'
import { fetchRenderANSI } from '../api'
import { extractPathsAt } from '../filePathDetection'
import { getBufferLineFromPointer, getBufferLineFromXtermCoords, getMarkerOffsetForBufferLine } from '../terminalInteractionGeometry'
import type { ScrollMetrics } from '../minimapGeometry'
import { createFrameBatcher } from '../scrollSync'
import { TERMINAL_LINE_HEIGHT, type TerminalActivateMeta, type TerminalContextMenuEvent, type TerminalControl, type TerminalLineMatcher, type UserHighlightRange } from '../terminalControl'
import { composeFoldView, type FoldRange, type FoldView } from '../terminalFolds'
import { onBannerColorChange, terminalTheme, useIsDark } from '../terminalTheme'
import {
  getTerminalFontStack,
  useTerminalFont,
  useTerminalFontSize,
} from '../fontPrefs'
import { useI18n } from '../i18n'

// Text the backend renders for a chrys turn still in progress; the frontend
// finds this row and overlays a spinning hourglass. Keep in sync with the
// formatter's "in_progress" case.
const PROGRESS_ROW_TEXT = '推理中…'
// The live progress row sits at the transcript tail; only scan this many rows
// up from the bottom to find it (historical sessions have none).
const PROGRESS_SCAN_ROWS = 400
// Follow-mode button rendered to the right of the hourglass on the
// in-progress row (backend renders "  推理中…": 2 spaces + 6 cells of 推理中 +
// 1 cell …, so the row text ends at cell 8; the button starts one cell
// later). Shown only while live follow is on; clicking it pauses follow.
const PROGRESS_HINT_X = 10
// Cell width of PROGRESS_HINT_TEXT (CJK glyphs are double-width), plus slack
// for the button padding so the last glyph never clips.
const PROGRESS_HINT_CELLS = 32

// Diagnostic instrumentation for the intermittent "scroll → blank screen,
// recovers after a few clicks" symptom. Logs are prefixed [si-term] so they
// can be filtered in DevTools. This is observation only — no behaviour
// change — so the cause can be pinned down before fixing.
//
// The blank symptom has only been seen on Windows (chrys/opencode sessions
// there), so every debug line carries a platform tag (win32 / darwin / linux)
// and a captured log stream is self-identifying. On Windows the likely root
// causes differ from Linux/macOS: WebGL2 repaint timing after wheel/HiDPI
// scale changes behaves differently, selection styling forces a canvas
// redraw that may not re-blit glyph-atlas rows, and ResizeObserver can
// briefly report a 0×0 content rect mid-scroll. The platform tag plus the
// existing scroll/resize/resize-zero-detected/selection-change lines let a
// recorded Windows-only repro reveal which path is the trigger.
//
// Auto-enable on Windows so a repro doesn't require typing anything first;
// other platforms still need localStorage.setItem('si-term-debug','1') to
// avoid spamming Linux/macOS users. To silence the Windows auto-enable,
// localStorage.setItem('si-term-debug','0').
//
// Best-effort platform tag. navigator.userAgentData is the modern source
// but absent from this lib.dom.dts; read via a shallow cast, fall back to UA
// sniff. Only used for labelling/gating debug logs, never for behaviour.
const TEMP_PLATFORM: string = (() => {
  if (typeof navigator === 'undefined') return 'unknown'
  const ua = navigator.userAgent || ''
  const aud = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData
  const low = (aud?.platform ?? '').toLowerCase()
  if (low === 'windows' || /windows/i.test(ua)) return 'win32'
  if (low === 'macos' || /mac/i.test(ua)) return 'darwin'
  if (low === 'linux' || /linux|X11/i.test(ua)) return 'linux'
  return ua || 'unknown'
})()
const TERM_DEBUG = (() => {
  if (typeof localStorage === 'undefined') return false
  const flag = localStorage.getItem('si-term-debug')
  if (flag === '1') return true
  if (flag === '0') return false
  return TEMP_PLATFORM === 'win32'
})()
// 跳转落点高亮的闪烁次数：localStorage.setItem('si-jump-flash-pulses', 'N') 可配，默认 2 次。
function getJumpFlashPulses(): number {
  if (typeof localStorage === 'undefined') return 2
  const n = parseInt(localStorage.getItem('si-jump-flash-pulses') ?? '', 10)
  return Number.isFinite(n) && n >= 1 && n <= 10 ? n : 2
}

// Match si-jump-flash keyframes peak (app.css). Used only when the OS asks
// for reduced motion so we can approximate the pulse without CSS animation.
const JUMP_FLASH_BG = 'rgba(37, 99, 235, 0.32)'

function prefersReducedMotion(): boolean {
  if (typeof matchMedia !== 'function') return false
  try {
    return matchMedia('(prefers-reduced-motion: reduce)').matches
  } catch {
    return false
  }
}

function dbg(tag: string, info?: Record<string, unknown>) {
  if (!TERM_DEBUG) return
  const t = (performance.now() / 1000).toFixed(3)
  const payload = { platform: TEMP_PLATFORM, ...(info ?? {}) }
  console.debug('[si-term]', t, tag, payload)
}

interface Props {
  sessionId: string
  agentType?: string
  folds?: FoldRange[]
  // Comma-separated message kinds that get an HH:MM:SS prefix (backend "ts"
  // render option). Changing it re-renders the terminal from scratch.
  tsKinds?: string
  // Live follow (tail -f): when true, content refreshes always pin the
  // viewport to the bottom even if the user was scrolled up. Only meaningful
  // for active sessions; ReplayView gates the toggle on isSessionLive.
  followOutput?: boolean
  // Invoked by the follow-pause button next to the hourglass decoration so
  // the user can stop following without reaching for the header toolbar.
  onFollowDisable?: () => void
  // One-shot buffer line to land on after the first write, used when
  // revisiting a session with follow off (the position the user left at).
  // Ignored when follow is on — the tail pin wins. Read once at mount.
  initialScrollLine?: number | null
  onFoldChange?: () => void
  // Path-bearing fold headers (e.g. ◆ write … /path): open file menu instead
  // of only toggling the fold. foldKey is set so the menu can still expand.
  onFoldPathActivate?: (bufLine: number, meta: TerminalActivateMeta) => void
  onContextMenu?: (e: TerminalContextMenuEvent) => void
  onScrollMetrics?: (m: ScrollMetrics) => void
  onColsReady?: (cols: number) => void
  controlRef?: React.MutableRefObject<TerminalControl | null>
  // User-message ranges (from positions API) to highlight with a background
  // decoration. Re-applied after every buffer rewrite / fold change so the
  // highlight tracks the rows as display rows shift.
  userPositions?: UserHighlightRange[]
  // Click handler for the sticky top user-message bar: jump back to the
  // user message that has been scrolled past. Receives original render rows
  // + logical start so the jump resolves through xterm's wrap state.
  onJumpToUserMessage?: (lineStart: number, logicalStart?: number) => void
}

async function waitForTerminalFont(fontFamily: string, fontSize: number) {
  if (!document.fonts?.load) return
  try {
    await Promise.race([
      document.fonts.load(`400 ${fontSize}px ${fontFamily}`).then(() => document.fonts.ready),
      new Promise<void>((resolve) => setTimeout(resolve, 2000)),
    ])
  } catch {
    // Ignore load failures; the fallback font stack will render immediately.
  }
}

type InteractionEntry = { matcher: TerminalLineMatcher<unknown>; data: unknown; matchIndex: number }
type XtermCoreWithMouse = {
  screenElement?: HTMLElement
  _mouseService?: {
    getCoords?: (
      event: MouseEvent,
      element: HTMLElement,
      colCount: number,
      rowCount: number,
      isSelection?: boolean,
    ) => [number, number] | undefined
  }
}

export default function TerminalPanel({ sessionId, agentType, folds, tsKinds = '', followOutput = false, onFollowDisable, initialScrollLine = null, onFoldChange, onFoldPathActivate, onContextMenu, onScrollMetrics, onColsReady, controlRef, userPositions, onJumpToUserMessage }: Props) {
  const { t } = useI18n()
  const translatorRef = useRef(t)
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const onScrollMetricsRef = useRef(onScrollMetrics)
  onScrollMetricsRef.current = onScrollMetrics
  const onColsReadyRef = useRef(onColsReady)
  onColsReadyRef.current = onColsReady
  const onJumpToUserMessageRef = useRef(onJumpToUserMessage)
  onJumpToUserMessageRef.current = onJumpToUserMessage
  // userPositions is read inside the mount effect's closures (which run once
  // per sessionId) so a ref keeps the latest ranges without re-mounting.
  // A version token bumps whenever the value changes so the mount effect can
  // re-apply highlight decorations without a full buffer rewrite.
  const userPositionsRef = useRef<UserHighlightRange[]>(userPositions ?? [])
  const userPositionsVersionRef = useRef(0)
  const userPositionsPrevRef = useRef<UserHighlightRange[] | undefined>(userPositions)
  if (userPositions !== userPositionsPrevRef.current) {
    userPositionsPrevRef.current = userPositions
    userPositionsRef.current = userPositions ?? []
    userPositionsVersionRef.current++
  }
  const isDark = useIsDark()
  const terminalFont = useTerminalFont()
  const terminalFontSize = useTerminalFontSize()
  const terminalFontFamily = getTerminalFontStack(terminalFont)
  const isDarkRef = useRef(isDark)
  isDarkRef.current = isDark
  // WebGL renderer degraded to the DOM fallback (no hardware acceleration /
  // WebGL2 unavailable / context lost). The DOM renderer can't hold CJK
  // box-drawing borders in alignment (the terminal font has no CJK glyphs, so
  // Chinese falls back to a system font whose advance isn't exactly 2 cells),
  // so we surface a dismissible hint. WebGL users never see it.
  const [webglDegraded, setWebglDegraded] = useState(false)
  const [warnDismissed, setWarnDismissed] = useState(
    () => localStorage.getItem('si-webgl-warn-dismissed') === '1',
  )
  const agentTypeRef = useRef(agentType)
  agentTypeRef.current = agentType
  const foldsRef = useRef<FoldRange[]>(folds ?? [])
  foldsRef.current = folds ?? []
  const onFoldChangeRef = useRef(onFoldChange)
  onFoldChangeRef.current = onFoldChange
  const onContextMenuRef = useRef(onContextMenu)
  onContextMenuRef.current = onContextMenu
  const onFoldPathActivateRef = useRef(onFoldPathActivate)
  onFoldPathActivateRef.current = onFoldPathActivate
  const onFollowDisableRef = useRef(onFollowDisable)
  onFollowDisableRef.current = onFollowDisable
  // Read once at mount (revisit restore); not a mount-effect dep.
  const initialScrollLineRef = useRef(initialScrollLine)
  initialScrollLineRef.current = initialScrollLine
  // Live follow is read from a ref inside refreshContent so toggling does not
  // remount the terminal; only the next live-tail poll needs the new value.
  const followOutputRef = useRef(followOutput)
  followOutputRef.current = followOutput
  // Assigned inside the mount effect; invoked on either follow edge: the
  // rising edge leaves open-at-top mode and jumps to the tail, and both
  // edges re-apply the follow-pause chip next to the hourglass (created on
  // rise, disposed on fall).
  const followWakeRef = useRef<((on: boolean) => void) | null>(null)
  const prevFollowRef = useRef(followOutput)
  useEffect(() => {
    if (followOutput !== prevFollowRef.current) followWakeRef.current?.(followOutput)
    prevFollowRef.current = followOutput
  }, [followOutput])
  // Assigned inside the mount effect once the terminal is live; the folds
  // prop effect below routes updated fold ranges into that closure.
  const applyFoldsRef = useRef<((folds: FoldRange[]) => void) | null>(null)
  // Same pattern for user-message highlights: the mount effect owns the
  // decoration closures; this ref lets the userPositions effect re-apply
  // them without re-mounting the terminal.
  const applyUserHighlightsRef = useRef<(() => void) | null>(null)
  const relocalizeTerminalRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const agent = agentTypeRef.current || ""
    // Grok native TUI is very compact; use denser line height only for it
    // (global changes to row height affect all agents, so keep agent-specific).
    const lineH = agent === "grok" ? 1.05 : 1.2
    const term = new Terminal({
      theme: terminalTheme(isDarkRef.current, agentTypeRef.current),
      fontFamily: terminalFontFamily,
      fontSize: terminalFontSize,
      lineHeight: lineH,
      allowProposedApi: true,
      scrollback: 20000,
      convertEol: true,
      disableStdin: true,
      screenReaderMode: false,
      drawBoldTextInBrightColors: false,
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    const searchAddon = new SearchAddon()
    term.loadAddon(searchAddon)
    termRef.current = term

    let disposeOnScroll: { dispose(): void } | null = null
    let disposeOnSelectionChange: { dispose(): void } | null = null
    // DPR-change watcher is created inside the font-ready then() but torn
    // down in the effect cleanup, so the handles live at effect scope.
    let dprWatcher: MediaQueryList | null = null
    let onDprChange: (() => void) | null = null
    let observer: ResizeObserver | null = null
    let metricsBatcher: ReturnType<typeof createFrameBatcher<ScrollMetrics>> | null = null
    let disposed = false
    let currentCols = 0
    let resizeDebounce: ReturnType<typeof setTimeout> | null = null
    // Safety-net timer for post-rewrite snapshot drop + force-repaint; cleared
    // on unmount so a disposed terminal is never stick/refreshed after switch.
    let finishPaintTimeout: ReturnType<typeof setTimeout> | null = null
    // Bumps on every writeComposed so a lagging rAF/timeout from rewrite A
    // cannot drop rewrite B's snapshot or scroll a disposed/newer terminal.
    let repaintGeneration = 0

    // Line interaction state
    let lineMatchers: TerminalLineMatcher<unknown>[] = []
    const interactionMap = new Map<number, InteractionEntry>()
    // Per-row async validation results ('pending' while in flight); cleared
    // on every buffer rescan since row numbers shift across rewrites.
    const rowValidity = new Map<number, 'pending' | boolean>()

    // Fold state: raw ANSI is kept so collapsed tool-group bodies can be
    // recomposed out of the buffer (xterm has no hide-rows primitive; a fold
    // toggle is a full reset+rewrite, same as the initial load path).
    let rawAnsi = ''
    let foldRanges: FoldRange[] = foldsRef.current
    const collapsedKeys = new Set<string>()
    // Folds default to collapsed: group folds ("▼ Tools (n/m)") show just the
    // group header, tool folds hide each tool's input/output boxes behind the
    // compact header. defaultedKeys records which we've already auto-collapsed
    // so a user's manual expand sticks while folds that first appear (session
    // grew) still start collapsed.
    const defaultedKeys = new Set<string>()
    let foldView: FoldView | null = null
    const toDisplayLine = (n: number) => (foldView ? foldView.toDisplay(n) : n)
    const toOriginalLine = (n: number) => (foldView ? foldView.toOriginal(n) : n)
    // Buffer row of each composed logical line, rebuilt after every rewrite
    // from xterm's own isWrapped flags. Jump targets resolve through logical
    // lines: display-row prediction drifts once the spliced fold badge makes
    // collapsed headers soft-wrap, logical lines cannot.
    let logicalRows: number[] = []
    const logicalToDisplayLine = (orig: number): number => {
      const composed = foldView ? foldView.toComposedLogical(orig) : orig
      if (logicalRows.length === 0) return composed
      return logicalRows[Math.max(0, Math.min(composed, logicalRows.length - 1))]
    }
    let tooltipEl: HTMLDivElement | null = null
    let hoverDecoration: IDecoration | null = null
    let hoverMarker: IMarker | null = null
    let activeHoverLine: number | null = null

    // Jump-flash state (hoisted so the effect cleanup can dispose it)
    let flashMarkers: IMarker[] = []
    let flashDecorations: IDecoration[] = []
    let flashTimer: ReturnType<typeof setTimeout> | null = null
    // Reduced-motion path schedules hold/gap timers per decoration; clear with flash.
    let flashPulseTimers: ReturnType<typeof setTimeout>[] = []
    const clearFlash = () => {
      if (flashTimer) { clearTimeout(flashTimer); flashTimer = null }
      for (const t of flashPulseTimers) clearTimeout(t)
      flashPulseTimers = []
      flashDecorations.forEach(d => d.dispose())
      flashMarkers.forEach(m => m.dispose())
      flashDecorations = []
      flashMarkers = []
    }

    // Spinning hourglass over a "turn in progress" row (chrys in-flight
    // checkpoint). One decoration, re-applied after every rewrite so it tracks
    // the row as the buffer changes; disposed when the marker text is gone.
    // A second decoration to its right carries the follow-pause button.
    // NOTE: term.reset() (every writeComposed) discards the buffer WITHOUT
    // disposing its markers, so any decoration anchored to a stale marker
    // keeps rendering over the rewritten content at its old row until
    // disposed — always clear these before a rewrite, not just after.
    let progressDecoration: IDecoration | null = null
    let progressHintDecoration: IDecoration | null = null
    let progressHintElement: HTMLElement | null = null
    let progressMarker: IMarker | null = null
    const disposeProgressHint = () => {
      progressHintDecoration?.dispose()
      progressHintDecoration = null
      progressHintElement = null
    }
    const clearProgress = () => {
      progressDecoration?.dispose()
      disposeProgressHint()
      progressMarker?.dispose()
      progressDecoration = null
      progressMarker = null
    }
    // Follow-pause chip anchored to the progress marker, to the right of the
    // hourglass. Created only while follow is on and disposed the moment it
    // turns off (no visibility hacks — xterm fully owns the element), so the
    // chip can never linger over unrelated rows.
    const applyProgressHint = () => {
      disposeProgressHint()
      if (!followOutputRef.current || !progressMarker || progressMarker.isDisposed) return
      let hintDecoration: IDecoration | undefined
      try {
        hintDecoration = term.registerDecoration({ marker: progressMarker, x: PROGRESS_HINT_X, width: PROGRESS_HINT_CELLS, height: 1, layer: 'top' })
      } catch {
        hintDecoration = undefined
      }
      if (!hintDecoration) return
      hintDecoration.onRender(element => {
        progressHintElement = element
        if (element.dataset.siFollowHint) return // onRender fires per xterm paint
        element.dataset.siFollowHint = '1'
        const bgIdle = 'color-mix(in srgb, var(--accent-green) 15%, transparent)'
        const bgHover = 'color-mix(in srgb, var(--accent-green) 30%, transparent)'
        element.style.pointerEvents = 'auto'
        element.style.cursor = 'pointer'
        element.style.whiteSpace = 'nowrap'
        element.style.borderRadius = '3px'
        element.style.padding = '0 4px'
        element.style.color = 'var(--accent-green)'
        element.style.background = bgIdle
        element.textContent = translatorRef.current('terminal.followingHint')
        element.title = translatorRef.current('terminal.pauseFollow')
        element.addEventListener('mouseenter', () => { element.style.background = bgHover })
        element.addEventListener('mouseleave', () => { element.style.background = bgIdle })
        element.addEventListener('click', e => {
          // Keep the click off the terminal's matcher hit-testing
          // (xterm MouseService would resolve this row/column).
          e.preventDefault()
          e.stopPropagation()
          // Instant feedback — React state (and the follow edge hook, which
          // disposes again) catches up within a frame.
          disposeProgressHint()
          onFollowDisableRef.current?.()
        })
      })
      progressHintDecoration = hintDecoration
    }

    // User-message highlight decorations: one per row of each user prompt
    // range. Re-applied after every rewrite (display rows shift when folds
    // toggle) and whenever userPositions changes (new positions poll).
    let userDecorations: IDecoration[] = []
    let userMarkers: IMarker[] = []
    const clearUserHighlights = () => {
      userDecorations.forEach(d => d.dispose())
      userMarkers.forEach(m => m.dispose())
      userDecorations = []
      userMarkers = []
    }
    // Resolve a user-message position to a buffer row. Prefer logical_start
    // (exact under fold badge wrap drift) but only when the logical line is
    // actually present in the xterm buffer — sessions longer than scrollback
    // (20000 rows) will have logicalRows shorter than the render's logical
    // line count, and logicalToDisplayLine clamps out-of-range indices to
    // the last entry, making every out-of-range highlight pile up on the
    // same row. Fall back to display-line mapping (toDisplayLine) which at
    // worst places the marker off-screen rather than on the wrong row.
    const resolveUserRow = (origLine: number, logical?: number): number => {
      if (typeof logical === 'number' && logical < logicalRows.length) {
        return logicalToDisplayLine(logical)
      }
      return toDisplayLine(origLine)
    }
    // Resolve the last buffer row of a user-message range. For the exclusive
    // logical_end supplied by the backend, the final wrapped row is the row
    // immediately before the next logical line (or the buffer tail if this is
    // the last logical line). Falls back to display-line mapping when logical
    // coordinates are unavailable or out of range.
    const resolveUserEndRow = (origLine: number, logical: number | undefined, buf: IBuffer): number => {
      if (typeof logical === 'number') {
        const composed = foldView ? foldView.toComposedLogical(logical) : logical
        if (composed < logicalRows.length) {
          return Math.max(0, logicalRows[composed] - 1)
        }
        return Math.max(0, buf.length - 1)
      }
      return toDisplayLine(origLine)
    }
    // Apply background decorations for every user-message range. Called after
    // scanBuffer/injectFoldRows in every rewrite path and on prop change.
    const injectUserHighlights = () => {
      clearUserHighlights()
      const ranges = userPositionsRef.current
      if (ranges.length === 0) return
      const buf = term.buffer.active
      for (const r of ranges) {
        const startRow = resolveUserRow(r.lineStart, r.logicalStart)
        // logical_end is exclusive on the backend; resolve to the final wrapped
        // buffer row of the user message. Fall back to a single-row highlight
        // when logical coordinates are unavailable.
        const endOrig = typeof r.lineEnd === 'number' ? r.lineEnd : r.lineStart
        const endLogical = typeof r.logicalEnd === 'number' ? r.logicalEnd : r.logicalStart
        const endRow = resolveUserEndRow(endOrig, endLogical, buf)
        const first = Math.max(0, Math.min(startRow, endRow))
        const last = Math.max(startRow, endRow)
        // Skip ranges entirely outside the buffer (e.g. scrolled out of
        // scrollback) so we don't paint a stray highlight at the buffer edge.
        if (first >= buf.length && last >= buf.length) continue
        for (let row = first; row <= last; row++) {
          if (row < 0 || row >= buf.length) continue
          const offset = getMarkerOffsetForBufferLine({
            bufferLine: row,
            baseY: buf.baseY,
            cursorY: buf.cursorY,
          })
          const marker = term.registerMarker(offset)
          if (!marker) continue
          let decoration: IDecoration | undefined
          try {
            decoration = term.registerDecoration({ marker, width: term.cols, height: 1, layer: 'top' })
          } catch {
            marker.dispose()
            continue
          }
          if (!decoration) { marker.dispose(); continue }
          decoration.onRender(element => {
            element.style.pointerEvents = 'none'
            element.style.left = '0'
            element.style.width = '100%'
            element.style.boxSizing = 'border-box'
            // Set background directly (not just via CSS class) so it survives
            // xterm's between-paint element resets. Semi-transparent so the
            // terminal text stays readable through the overlay.
            const bg = isDarkRef.current ? 'rgba(130, 130, 140, 0.18)' : 'rgba(100, 110, 130, 0.22)'
            element.style.background = bg
            element.classList.add('si-user-msg-highlight')
          })
          userMarkers.push(marker)
          userDecorations.push(decoration)
        }
      }
    }
    // Compute the most recent user message that has been fully scrolled past
    // the current viewport top. Returns null when none qualifies. Used by the
    // scroll handler to update the sticky top bar without re-rendering on
    // every pixel scroll — only when the resolved message key changes.
    const computeStickyUserMsg = (): UserHighlightRange | null => {
      const ranges = userPositionsRef.current
      if (ranges.length === 0) return null
      const buf = term.buffer.active
      const viewportTop = buf.viewportY
      // Find the last user range whose last row is strictly above viewportTop.
      // Equal-to-top means the message is still visible — don't sticky it.
      let sticky: UserHighlightRange | null = null
      for (const r of ranges) {
        const endOrig = typeof r.lineEnd === 'number' ? r.lineEnd : r.lineStart
        const endLogical = typeof r.logicalEnd === 'number' ? r.logicalEnd : r.logicalStart
        const endRow = resolveUserEndRow(endOrig, endLogical, buf)
        if (endRow < viewportTop) sticky = r
        else break
      }
      return sticky
    }
    // Sticky top bar: imperative DOM overlay at the top of the terminal
    // viewport showing the most recent user message scrolled past. Managed
    // imperatively (like tooltipEl) so it never conflicts with xterm's own
    // DOM children under container. Clicking it jumps back to that message.
    let stickyBarEl: HTMLDivElement | null = null
    let stickyLabelEl: HTMLSpanElement | null = null
    let stickyTextEl: HTMLSpanElement | null = null
    let currentStickyRange: UserHighlightRange | null = null
    const updateStickyUserMsg = () => {
      if (disposed || !stickyBarEl) return
      const next = computeStickyUserMsg()
      if ((next?.key ?? null) === (currentStickyRange?.key ?? null)) return
      currentStickyRange = next
      if (!next) {
        stickyBarEl.style.display = 'none'
        return
      }
      stickyBarEl.style.display = 'flex'
      if (stickyLabelEl) {
        stickyLabelEl.textContent = `↑ ${translatorRef.current('terminal.userMessage')}${typeof next.seq === 'number' ? ` #${next.seq}` : ''}`
      }
      if (stickyTextEl) {
        const text = next.text || ''
        stickyTextEl.textContent = text
        stickyTextEl.title = text
      }
    }
    const onStickyClick = () => {
      const r = currentStickyRange
      if (!r) return
      onJumpToUserMessageRef.current?.(r.lineStart, r.logicalStart)
    }
    let onStickyKeyDown: (e: KeyboardEvent) => void = () => {}

    let onMouseMove: ((e: MouseEvent) => void) | null = null
    let onMouseLeave: (() => void) | null = null
    let onClick: ((e: MouseEvent) => void) | null = null
    let onCtxMenu: ((e: MouseEvent) => void) | null = null

    // Anti-flicker for fold rewrites: reset+write repaints over several frames,
    // so a static snapshot of the current screen covers the terminal until the
    // rewrite (and its anchor scroll) has painted.
    let removeSnapshot: (() => void) | null = null
    let hasWrittenOnce = false
    // Opening a session should land at the start (top). Cleared once the user
    // scrolls away, jumps, or live-follow pins the viewport to the bottom.
    // When follow is already on at mount (auto-follow for an active session),
    // start pinned to the tail instead; when revisiting with a saved scroll
    // position (follow off), restore that line after the first write.
    let pendingInitialScrollLine = followOutputRef.current ? null : initialScrollLineRef.current
    let openAtTop = !followOutputRef.current && pendingInitialScrollLine == null
    // While rewriting, xterm briefly parks at the bottom; ignore those scrolls
    // so they don't cancel open-at-top before we re-anchor to line 0.
    let writingContent = false
    // Brief grace after each write: xterm/async fold rewrites can emit lagging
    // bottom-scroll events that would otherwise clear openAtTop too early.
    let openAtTopGraceUntil = 0
    let disposeSearchResultsRef: { dispose(): void } | null = null

    waitForTerminalFont(terminalFontFamily, terminalFontSize).then(() => {
      if (disposed) return

      term.open(container)

      // WebGL renderer: rasterizes every glyph into a fixed grid cell, so CJK
      // (wide) characters occupy exactly the two cells xterm allocates them.
      // The default DOM renderer instead lays glyphs out as text and leans on
      // the font for cell width — but our terminal font (JetBrains Mono) has no
      // CJK glyphs, so Chinese falls back to a system font whose advance width
      // isn't exactly double, drifting box-drawing borders out of alignment on
      // rows with Chinese. Must load after open(). Any failure path (WebGL2
      // unavailable, addon throws, or a later context loss) reverts to the DOM
      // renderer and flags the degraded state so the hint banner can show.
      let webglOk = false
      // Kept so post-rewrite repaint can clearTextureAtlas / reattach when
      // refresh alone leaves a blank canvas on Windows (wheel scroll still worked).
      let webglAddon: WebglAddon | null = null
      try {
        webglOk = !!document.createElement('canvas').getContext('webgl2')
      } catch {
        // keep webglOk false when WebGL2 probe throws
      }
      if (webglOk) {
        // preserveDrawingBuffer: true so the anti-flicker snapshot
        // (snapshotTerminal → drawImage of the live canvas) can read real
        // pixels. Without it WebGL clears the buffer outside its render loop,
        // the fold-rewrite cover snapshot comes out blank, and toggling a
        // fold flickers the terminal blank for a couple of frames.
        // attachWebgl is defined below after open() helpers; inline first load.
        try {
          const webgl = new WebglAddon(true)
          webgl.onContextLoss(() => {
            dbg('webgl-context-loss')
            webgl.dispose()
            if (webglAddon === webgl) webglAddon = null
            setWebglDegraded(true)
          })
          term.loadAddon(webgl)
          webglAddon = webgl
          dbg('webgl-loaded', { cols: term.cols, rows: term.rows })
        } catch {
          webglOk = false
        }
      }
      if (!webglOk) setWebglDegraded(true)

      fitAddon.fit()
      currentCols = term.cols
      dbg('initial-fit', { cols: term.cols, rows: term.rows, containerW: container.clientWidth, containerH: container.clientHeight })

      const xtermCanvas = container.querySelector<HTMLCanvasElement>('.xterm-screen canvas')
      const dpr = window.devicePixelRatio || 1
      const cellHeight = xtermCanvas && term.rows > 0
        ? xtermCanvas.height / dpr / term.rows
        : TERMINAL_LINE_HEIGHT
      dbg('open-done', { webglDetected: !!xtermCanvas, canvasW: xtermCanvas?.width, canvasH: xtermCanvas?.height, cellHeight })

      container.style.position = 'relative'

      tooltipEl = document.createElement('div')
      tooltipEl.style.cssText = [
        'position:fixed', 'padding:3px 10px',
        'background:rgba(30,30,46,0.96)', 'color:#cdd6f4',
        'border:1px solid rgba(124,58,237,0.55)', 'border-radius:4px',
        'font-size:12px', 'font-family:system-ui,-apple-system,sans-serif',
        'pointer-events:none', 'z-index:9999', 'display:none',
        'white-space:nowrap', 'box-shadow:0 2px 10px rgba(0,0,0,0.45)',
      ].join(';')
      document.body.appendChild(tooltipEl)

      // Sticky top user-message bar: imperative DOM overlay at the top of
      // the terminal viewport. Shows the most recent user message that has
      // been scrolled past; click jumps back to it. Created here (not via
      // React JSX) so it never conflicts with xterm's own children under
      // container — React reconciliation would otherwise risk removing
      // xterm's elements on re-render.
      stickyBarEl = document.createElement('div')
      stickyBarEl.className = 'si-sticky-user-msg'
      stickyBarEl.style.cssText = [
        'position:absolute', 'top:0', 'left:0', 'right:0',
        'display:none', 'align-items:center', 'gap:6px',
        'padding:4px 12px', 'z-index:10', 'cursor:pointer',
        `font-family:${terminalFontFamily}`,
        `font-size:${terminalFontSize}px`, 'line-height:1.4',
        'white-space:nowrap', 'overflow:hidden',
        'border-bottom:1px solid var(--border-default)',
      ].join(';')
      stickyBarEl.title = translatorRef.current('terminal.returnUserMessage')
      stickyBarEl.setAttribute('role', 'button')
      stickyBarEl.setAttribute('tabindex', '0')
      stickyBarEl.setAttribute('aria-label', translatorRef.current('terminal.returnUserMessageLabel'))
      onStickyKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onStickyClick()
        }
      }
      stickyBarEl.addEventListener('click', onStickyClick)
      stickyBarEl.addEventListener('keydown', onStickyKeyDown)
      stickyLabelEl = document.createElement('span')
      stickyLabelEl.textContent = `↑ ${translatorRef.current('terminal.userMessage')}`
      stickyLabelEl.style.cssText = [
        'flex-shrink:0', 'font-weight:600',
        'color:var(--accent-blue)',
      ].join(';')
      stickyTextEl = document.createElement('span')
      stickyTextEl.style.cssText = [
        'min-width:0', 'flex:1', 'overflow:hidden',
        'text-overflow:ellipsis', 'white-space:nowrap',
      ].join(';')
      stickyBarEl.appendChild(stickyLabelEl)
      stickyBarEl.appendChild(stickyTextEl)
      container.appendChild(stickyBarEl)

      const xtermScreen = container.querySelector<HTMLElement>('.xterm-screen')
      const eventTarget = xtermScreen ?? container

      const getScreenRect = () => (xtermScreen ?? container).getBoundingClientRect()

      const getBufLine = (e: MouseEvent): number | null => {
        // Keep hit-testing in xterm's coordinate system. Hand-rolled
        // rect/cellHeight math drifts by 1-2 rows because xterm accounts for
        // screen padding, renderService cell dimensions, ceil/clamp behavior,
        // and viewport state internally.
        const core = (term as unknown as { _core?: XtermCoreWithMouse })._core
        const screenElement = core?.screenElement ?? xtermScreen ?? container
        const coords = core?._mouseService?.getCoords?.(e, screenElement, term.cols, term.rows, false)
        const xtermLine = getBufferLineFromXtermCoords(coords, term.buffer.active.viewportY)
        if (xtermLine !== null) return xtermLine

        const screenRect = screenElement.getBoundingClientRect?.() ?? getScreenRect()
        return getBufferLineFromPointer({
          clientY: e.clientY,
          screenTop: screenRect.top,
          cellHeight,
          viewportY: term.buffer.active.viewportY,
          rowCount: term.rows,
        })
      }

      const clearHoverDecoration = () => {
        hoverDecoration?.dispose()
        hoverMarker?.dispose()
        hoverDecoration = null
        hoverMarker = null
        activeHoverLine = null
      }

      const flashLines = (startLine: number, count = 2) => {
        clearFlash()
        const pulses = getJumpFlashPulses()
        const pulseMs = 900
        // Windows "Animation effects" off maps to prefers-reduced-motion.
        // Keep the original CSS keyframe path when motion is allowed so the
        // open-animation look is unchanged; only approximate when reduced.
        const reduceMotion = prefersReducedMotion()
        dbg('flash-start', { startLine, count, pulses, pulseMs, reduceMotion })
        // Keyframes hold solid through 40% then ease out; mirror hold/gap for JS.
        const holdMs = Math.round(pulseMs * 0.4)
        const gapMs = pulseMs - holdMs
        for (let i = 0; i < count; i++) {
          const offset = getMarkerOffsetForBufferLine({
            bufferLine: startLine + i,
            baseY: term.buffer.active.baseY,
            cursorY: term.buffer.active.cursorY,
          })
          const marker = term.registerMarker(offset)
          dbg('flash-marker', { startLine, i, offset, baseY: term.buffer.active.baseY, cursorY: term.buffer.active.cursorY, markerLine: marker?.line, disposed: marker?.isDisposed })
          if (!marker) continue
          let decoration: IDecoration | undefined
          try {
            decoration = term.registerDecoration({ marker, width: term.cols, height: 1, layer: 'top' })
          } catch (e) {
            dbg('flash-decoration-throw', { msg: String(e) })
            marker.dispose()
            continue
          }
          dbg('flash-decoration', { created: !!decoration })
          if (!decoration) { marker.dispose(); continue }
          decoration.onRender(element => {
            // onRender fires every xterm paint; init once so CSS animation is
            // not restarted every frame (would freeze the pulse at 0%).
            if (element.dataset.siFlash === '1') return
            element.dataset.siFlash = '1'
            dbg('flash-render', { y: element.getBoundingClientRect().top, reduceMotion })
            element.style.pointerEvents = 'none'
            element.style.left = '0'
            element.style.width = '100%'
            element.style.boxSizing = 'border-box'
            if (!reduceMotion) {
              // Original path — identical to pre-change behavior.
              element.style.animation = `si-jump-flash ${pulseMs}ms ease-out ${pulses}`
              return
            }
            // Reduced motion: timed solid / clear pulses (no CSS animation).
            const runPulse = (n: number) => {
              if (n >= pulses || element.dataset.siFlash !== '1') return
              element.style.backgroundColor = JUMP_FLASH_BG
              const tHold = setTimeout(() => {
                if (element.dataset.siFlash !== '1') return
                element.style.backgroundColor = 'transparent'
                const tGap = setTimeout(() => runPulse(n + 1), gapMs)
                flashPulseTimers.push(tGap)
              }, holdMs)
              flashPulseTimers.push(tHold)
            }
            runPulse(0)
          })
          flashMarkers.push(marker)
          flashDecorations.push(decoration)
        }
        flashTimer = setTimeout(clearFlash, pulses * pulseMs + 200)
      }

      const hideHover = () => {
        clearHoverDecoration()
        if (tooltipEl) tooltipEl.style.display = 'none'
        if (xtermScreen) xtermScreen.style.cursor = ''
      }

      const showHoverDecoration = (bufLine: number) => {
        if (activeHoverLine === bufLine && hoverDecoration && !hoverDecoration.isDisposed) return
        clearHoverDecoration()

        const offset = getMarkerOffsetForBufferLine({
          bufferLine: bufLine,
          baseY: term.buffer.active.baseY,
          cursorY: term.buffer.active.cursorY,
        })
        const marker = term.registerMarker(offset)
        if (!marker) return

        let decoration: IDecoration | undefined
        try {
          decoration = term.registerDecoration({
            marker,
            width: term.cols,
            height: 1,
            layer: 'top',
          })
        } catch {
          marker.dispose()
          return
        }
        if (!decoration) {
          marker.dispose()
          return
        }

        decoration.onRender(element => {
          element.style.pointerEvents = 'none'
          element.style.left = '0'
          element.style.right = '0'
          element.style.width = '100%'
          element.style.boxSizing = 'border-box'
          element.style.borderBottom = '1.5px solid rgba(124,58,237,0.65)'
          element.style.background = 'rgba(124,58,237,0.07)'
        })

        hoverMarker = marker
        hoverDecoration = decoration
        activeHoverLine = bufLine
      }

      // Deferred jump target: set when a panel jump arrives before the buffer
      // contains the target logical line (live positions race the render).
      // Flushed after every buffer scan; on expiry it falls back to the legacy
      // clamping resolution so the click is never silently dropped. The timer
      // guarantees the fallback fires even when no further buffer scan occurs.
      let pendingJump: { lineStart: number; logicalStart?: number; deadline: number } | null = null
      let pendingJumpTimer: ReturnType<typeof setTimeout> | null = null
      const clearPendingJump = () => {
        pendingJump = null
        if (pendingJumpTimer) {
          clearTimeout(pendingJumpTimer)
          pendingJumpTimer = null
        }
      }
      const execJump = (lineStart: number, logicalStart: number | undefined, allowClamp: boolean): boolean => {
        let line: number
        if (typeof logicalStart === 'number') {
          // logicalStart is in ORIGINAL logical-line space, but logicalRows
          // indexes the COMPOSED buffer (collapsed folds drop lines) — the
          // bounds check must compare in composed space, or a target past a
          // collapsed fold defers forever and expires into a wrong-row clamp.
          const composed = foldView ? foldView.toComposedLogical(logicalStart) : logicalStart
          if (!allowClamp && (logicalRows.length === 0 || composed >= logicalRows.length)) return false
          line = logicalToDisplayLine(logicalStart)
        } else {
          line = Math.max(0, toDisplayLine(lineStart))
        }
        openAtTop = false
        term.scrollToLine(Math.max(0, line - Math.floor(term.rows / 2)))
        flashLines(line, 1)
        return true
      }
      const flushPendingJump = () => {
        if (!pendingJump) return
        const j = pendingJump
        if (Date.now() > j.deadline) {
          clearPendingJump()
          execJump(j.lineStart, j.logicalStart, true)
          return
        }
        if (execJump(j.lineStart, j.logicalStart, false)) clearPendingJump()
      }

      // Scan the xterm.js buffer for all registered matchers and populate interactionMap.
      // Called after every render so buffer lines are the source of truth (no Go lineStart dependency).
      const scanBuffer = () => {
        {
          const buf = term.buffer.active
          const rows: number[] = []
          for (let i = 0; i < buf.length; i++) {
            if (!buf.getLine(i)?.isWrapped) rows.push(i)
          }
          logicalRows = rows
        }
        flushPendingJump()
        interactionMap.clear()
        rowValidity.clear()
        if (lineMatchers.length === 0) return
        const buf = term.buffer.active
        const matchCounts = new Map<TerminalLineMatcher<unknown>, number>()
        for (let i = 0; i < buf.length; i++) {
          const line = buf.getLine(i)
          if (!line) continue
          const text = line.translateToString(true)
          for (const matcher of lineMatchers) {
            const data = matcher.match(text)
            if (data !== null) {
              const idx = matchCounts.get(matcher) ?? 0
              matchCounts.set(matcher, idx + 1)
              interactionMap.set(i, { matcher, data, matchIndex: idx })
              break
            }
          }
        }
      }

      // Register collapsed/expanded tool-group headers as clickable rows.
      // Injected after every scanBuffer so a rewrite can't leave stale rows.
      //
      // Path-bearing headers (`◆ write (N 行) /path/file.md`) open the file
      // menu instead of only toggling the fold. Soft-wrapped rows are joined
      // so a long path split across display lines still counts.
      const joinWrappedLineText = (startRow: number): string => {
        const buf = term.buffer.active
        let text = buf.getLine(startRow)?.translateToString(true) ?? ''
        for (let r = startRow + 1; r < buf.length; r++) {
          const bl = buf.getLine(r)
          if (!bl || !bl.isWrapped) break
          text += bl.translateToString(true)
        }
        return text
      }
      // Walk back to the first non-wrapped row of a soft-wrap group.
      const wrapGroupStart = (row: number): number => {
        const buf = term.buffer.active
        let r = row
        while (r > 0) {
          const bl = buf.getLine(r)
          if (!bl?.isWrapped) break
          r--
        }
        return r
      }
      const makeFoldMatcher = (): TerminalLineMatcher<FoldRange> => ({
        match: () => null, // rows come from fold geometry, not text scanning
        tooltip: translatorRef.current('terminal.toggleContent'),
        onActivate: (bufLine, fold, _matchIndex, meta) => {
          const groupStart = wrapGroupStart(bufLine)
          const joined = joinWrappedLineText(groupStart)
          // Any path-like token on the (joined) header → file menu with fold
          // toggle, not silent expand-only. Matches write/read/edit summaries.
          const hasPath = extractPathsAt(joined, null, null).length > 0
          if (hasPath && meta && onFoldPathActivateRef.current) {
            onFoldPathActivateRef.current(bufLine, {
              clientX: meta.clientX,
              clientY: meta.clientY,
              column: meta.column,
              lineText: joined,
              foldKey: fold.key,
            })
            return
          }
          toggleFold(fold)
        },
      })
      const injectFoldRows = () => {
        if (!foldRanges.length) return
        // Inject group folds last: when a group is collapsed, every tool
        // fold inside it also maps its header to the group header row (fold
        // bodies collapse to their header). If a tool fold were injected
        // after the group fold it would overwrite the interactionMap entry,
        // making the group header untoggable.
        const sorted = [...foldRanges].sort((a, b) =>
          a.level === 'group' ? 1 : b.level === 'group' ? -1 : 0)
        const buf = term.buffer.active
        const foldMatcher = makeFoldMatcher() as TerminalLineMatcher<unknown>
        for (const f of sorted) {
          const row = logicalToDisplayLine(f.headerLogical)
          const joined = joinWrappedLineText(row)
          const hasPath = extractPathsAt(joined, null, null).length > 0
          // Tooltip reflects the dual action when a path is present.
          const matcher: TerminalLineMatcher<unknown> = hasPath
            ? {
                ...foldMatcher,
                tooltip: translatorRef.current('terminal.fileMenuHint'),
              }
            : foldMatcher
          const entry = { matcher, data: f, matchIndex: 0 }
          interactionMap.set(row, entry)
          // An untruncated header ("▶ • Name  <full summary>") can soft-wrap over
          // several display rows. Make every one clickable so a click on the
          // wrapped summary/badge toggles the fold too. isWrapped rides xterm's
          // own wrap state (AGENTS.md: no hand-rolled row math), so it also
          // covers the extra row the badge may add.
          for (let r = row + 1; r < buf.length; r++) {
            const bl = buf.getLine(r)
            if (!bl || !bl.isWrapped) break
            interactionMap.set(r, entry)
          }
        }
      }

      // Overlay a spinning hourglass on chrys's "推理中…" in-progress row. The
      // marker rides xterm's own viewport math (AGENTS.md: no hand-rolled DOM
      // row coordinates); the row sits near the tail, so scan from the bottom.
      const injectProgressRow = () => {
        clearProgress()
        const buf = term.buffer.active
        // The in-progress "推理中…" row only exists for a live session and sits
        // at the tail. Historical sessions have none, so scanning the whole
        // buffer bottom-to-top just to find nothing is pure waste — cap the scan
        // to the last rows.
        const scanFloor = Math.max(0, buf.length - PROGRESS_SCAN_ROWS)
        for (let i = buf.length - 1; i >= scanFloor; i--) {
          const line = buf.getLine(i)
          if (!line) continue
          if (!line.translateToString(true).includes(PROGRESS_ROW_TEXT)) continue
          const offset = getMarkerOffsetForBufferLine({ bufferLine: i, baseY: buf.baseY, cursorY: buf.cursorY })
          const marker = term.registerMarker(offset)
          if (!marker) break
          let decoration: IDecoration | undefined
          try {
            decoration = term.registerDecoration({ marker, x: 0, width: 2, height: 1, layer: 'top' })
          } catch {
            marker.dispose()
            break
          }
          if (!decoration) { marker.dispose(); break }
          decoration.onRender(element => {
            if (element.dataset.siProgress === '1') return // onRender fires per xterm paint
            element.dataset.siProgress = '1'
            element.style.pointerEvents = 'none'
            element.style.display = 'flex'
            element.style.alignItems = 'center'
            element.style.justifyContent = 'center'
            element.textContent = '⏳'
            element.animate(
              [{ transform: 'rotate(0deg)' }, { transform: 'rotate(360deg)' }],
              { duration: 1600, iterations: Infinity, easing: 'linear' },
            )
          })
          progressMarker = marker
          progressDecoration = decoration
          // Follow-pause chip rides the same marker (only while follow is on).
          applyProgressHint()
          break
        }
      }

const snapshotTerminal = () => {
      removeSnapshot?.()
      removeSnapshot = null
      const screen = container.querySelector<HTMLElement>('.xterm')
      if (!screen) { dbg('snapshot-skip-no-screen'); return }
      const snapshotAppliedAt = performance.now()
      let snap: HTMLElement
      try {
        snap = screen.cloneNode(true) as HTMLElement
        // Canvas pixels (canvas/webgl renderers) don't survive cloneNode;
        // copy them explicitly. No-op under the DOM renderer.
        const srcCanvases = screen.querySelectorAll('canvas')
        const dstCanvases = snap.querySelectorAll('canvas')
        srcCanvases.forEach((src, i) => {
          const dst = dstCanvases[i]
          if (!dst) return
          dst.width = src.width
          dst.height = src.height
          dst.getContext('2d')?.drawImage(src, 0, 0)
        })
      } catch (e) {
        dbg('snapshot-clone-error', { msg: String(e) })
        return
      }
      const wrapper = document.createElement('div')
      const bg = term.options.theme?.background ?? '#1a1b26'
      wrapper.style.cssText = `position:absolute;inset:0;overflow:hidden;z-index:5;pointer-events:none;background:${bg}`
      wrapper.appendChild(snap)
      container.appendChild(wrapper)
      dbg('snapshot-applied', { appliedAt: snapshotAppliedAt })
      removeSnapshot = () => {
        const removedAt = performance.now()
        wrapper.remove()
        removeSnapshot = null
        dbg('snapshot-removed', { holdMs: Math.round(removedAt - snapshotAppliedAt) })
      }
    }

      const stickOpenAtTop = () => {
        if (!openAtTop || followOutputRef.current) return
        term.scrollToLine(0)
      }

      const attachWebgl = (): boolean => {
        try {
          const webgl = new WebglAddon(true)
          webgl.onContextLoss(() => {
            dbg('webgl-context-loss')
            webgl.dispose()
            if (webglAddon === webgl) webglAddon = null
            setWebglDegraded(true)
          })
          term.loadAddon(webgl)
          webglAddon = webgl
          return true
        } catch {
          webglAddon = null
          return false
        }
      }

      // Windows WebGL often leaves a cleared canvas after reset+large write
      // even though the buffer is full. term.refresh alone and even atlas
      // clear were not enough (logs: force-repaint with viewportY:0 still
      // blank; wheel scroll paints). Open-at-top keeps viewportY at 0, so a
      // scroll-only nudge is easy to miss when maxScroll is 0 (folded short
      // view).
      //
      // Hard path (finishPaint once per rewrite settle) uses ONE recovery
      // technique so open/switch does not flash twice:
      // - Windows + reattach: dispose/reload WebGL + fit + refresh
      // - else: resize bounce + atlas clear + refresh
      // Soft path: atlas clear + refresh only.
      const forceViewportRepaint = (
        reason: string,
        opts?: { reattachWebgl?: boolean; soft?: boolean },
      ) => {
        if (disposed || term.rows <= 0) return
        const soft = !!opts?.soft
        let resized = false
        let reattached = false
        try {
          if (soft) {
            webglAddon?.clearTextureAtlas()
            term.refresh(0, Math.max(0, term.rows - 1))
          } else if (opts?.reattachWebgl && webglAddon) {
            // Single hard step on Windows: reattach rebuilds the renderer.
            // Do not also resize-bounce or micro-scroll (that was a second flash).
            try {
              webglAddon.dispose()
            } catch {
              // dispose may throw if already torn down; still try reload
            }
            webglAddon = null
            if (attachWebgl()) {
              reattached = true
              fitAddon.fit()
              stickOpenAtTop()
              // Fresh addon has an empty atlas; refresh is enough.
              term.refresh(0, Math.max(0, term.rows - 1))
            } else {
              setWebglDegraded(true)
              // Fall back to resize bounce if reattach failed.
              const cols = term.cols
              const rows = term.rows
              if (cols > 0 && rows > 1) {
                term.resize(cols, rows - 1)
                term.resize(cols, rows)
                resized = true
              } else {
                fitAddon.fit()
                resized = true
              }
              term.refresh(0, Math.max(0, term.rows - 1))
            }
          } else {
            // Non-Windows / no-reattach hard path: one resize bounce.
            const cols = term.cols
            const rows = term.rows
            if (cols > 0 && rows > 1) {
              term.resize(cols, rows - 1)
              term.resize(cols, rows)
              resized = true
            } else {
              fitAddon.fit()
              resized = true
            }
            webglAddon?.clearTextureAtlas()
            stickOpenAtTop()
            term.refresh(0, Math.max(0, term.rows - 1))
          }
        } catch (e) {
          dbg('force-repaint-error', { reason, msg: String(e) })
          return
        }
        const canvas = container.querySelector<HTMLCanvasElement>('.xterm-screen canvas')
        dbg('force-repaint', {
          reason,
          soft,
          rows: term.rows,
          cols: term.cols,
          viewportY: term.buffer.active.viewportY,
          bufLen: term.buffer.active.length,
          maxScroll: Math.max(0, term.buffer.active.length - term.rows),
          resized,
          reattached,
          webgl: !!webglAddon,
          canvasW: canvas?.width,
          canvasH: canvas?.height,
        })
      }

      // Follow edge hook: on the rising edge leave open-at-top mode and jump
      // to the tail (follow already on at mount takes the openAtTop=false
      // init above and parks at the bottom on the initial write instead);
      // both edges create/dispose the follow-pause chip.
      followWakeRef.current = (on: boolean) => {
        if (on) {
          openAtTop = false
          if (hasWrittenOnce) term.scrollToBottom()
        }
        applyProgressHint()
      }
      // Replay a rising edge that fired while waitForTerminalFont was still
      // pending: the ref was null then, so the followOutput effect dropped
      // it and openAtTop would stay stuck true.
      if (followOutputRef.current) followWakeRef.current(true)

      const writeComposed = (afterWrite?: () => void) => {
        const generation = ++repaintGeneration
        // Supersede any pending finishPaint from a prior rewrite before we
        // install a new snapshot (stale finishPaint must not remove it).
        if (finishPaintTimeout) {
          clearTimeout(finishPaintTimeout)
          finishPaintTimeout = null
        }
        if (hasWrittenOnce) snapshotTerminal()
        clearHoverDecoration()
        // term.reset() below discards the buffer without disposing its
        // markers (xterm BufferSet.reset swaps in a fresh Buffer), leaving
        // decorations anchored to stale rows painted over the new content
        // until the post-write re-inject disposes them. Clear them up front
        // so the rewrite window never shows ghost overlays.
        clearProgress()
        clearUserHighlights()
        const rewriteStart = performance.now()
        const wroteBytes = (foldView?.text ?? rawAnsi).length
        writingContent = true
        term.reset()
        term.write('\x1b[3J') // clear accumulated scrollback so buffer lines start at 0
        term.write(foldView?.text ?? rawAnsi, () => {
          // A newer writeComposed may have already reset+written; do not
          // inject/scroll/finishPaint for this stale completion.
          if (disposed || generation !== repaintGeneration) return
          hasWrittenOnce = true
          scanBuffer()
          injectFoldRows()
          injectProgressRow()
          injectUserHighlights()
          updateStickyUserMsg()
          queueMetrics()
          if (afterWrite) afterWrite()
          // Revisit restore (first write only): land on the line the user
          // left at. Runs while writingContent is still true and before the
          // grace window, so the scroll handlers don't fight it.
          if (pendingInitialScrollLine != null) {
            openAtTop = false
            term.scrollToLine(pendingInitialScrollLine)
            pendingInitialScrollLine = null
          }
          // Always re-assert top when openAtTop is active (afterWrite may only
          // notify folds / restore an anchor). xterm parks at the bottom after
          // large writes; without this, session open lands at the end.
          stickOpenAtTop()
          // Soft refresh only: hard repaint (resize bounce / reattach) flashes
          // visibly; reserve it for finishPaint after the snapshot drops.
          // Still marks the viewport dirty so the first write seeds canvas
          // pixels for the next write's anti-flicker snapshot.
          forceViewportRepaint('rewrite-sync', { soft: true })
          openAtTopGraceUntil = performance.now() + 800
          writingContent = false
          // Two frames: drop the anti-flicker snapshot past xterm's paint,
          // re-stick open-at-top, then one hard repaint so Windows does not
          // leave a cleared canvas after snapshot-removed.
          let finishedInRaf = false
          const finishPaint = (reason: string) => {
            // Stale generation: pure no-op (do not clear the newer rewrite's
            // timeout or call its removeSnapshot).
            if (finishedInRaf || generation !== repaintGeneration) return
            finishedInRaf = true
            if (finishPaintTimeout) {
              clearTimeout(finishPaintTimeout)
              finishPaintTimeout = null
            }
            // Unmount / session switch can land between write and this rAF or
            // the 250ms safety net; forceViewportRepaint already no-ops, but
            // stickOpenAtTop would still call scrollToLine on a disposed term.
            if (disposed) {
              removeSnapshot?.()
              return
            }
            removeSnapshot?.()
            stickOpenAtTop()
            // One hard recovery paint (reattach XOR resize). A second soft
            // atlas-clear pass was still a visible flash on some drivers.
            forceViewportRepaint(reason, { reattachWebgl: TEMP_PLATFORM === 'win32' })
            // Light present only: no atlas clear / resize / reattach.
            requestAnimationFrame(() => {
              if (disposed || generation !== repaintGeneration || term.rows <= 0) return
              try {
                term.refresh(0, term.rows - 1)
              } catch {
                // disposed mid-frame
              }
            })
          }
          requestAnimationFrame(() => {
            if (disposed || generation !== repaintGeneration) return
            stickOpenAtTop()
            requestAnimationFrame(() => finishPaint('post-rewrite'))
          })
          // Safety net: if the double-RAF never fires (tab backgrounded,
          // rAF throttled by 0Hz display), drop the snapshot by the next
          // macrotask so it can't permanently mask the terminal.
          finishPaintTimeout = setTimeout(() => {
            finishPaintTimeout = null
            if (!finishedInRaf) {
              if (removeSnapshot) dbg('snapshot-timeout-remove')
              finishPaint('post-rewrite-timeout')
            }
          }, 250)
          dbg('rewrite-done', { ms: Math.round(performance.now() - rewriteStart), bytes: wroteBytes, bufLen: term.buffer.active.length })
        })
      }

      const recompose = (afterWrite?: () => void) => {
        foldView = composeFoldView(rawAnsi, foldRanges, collapsedKeys, translatorRef.current('terminal.lines'))
        writeComposed(() => {
          afterWrite?.()
          onFoldChangeRef.current?.()
        })
      }

      relocalizeTerminalRef.current = () => {
        if (progressHintElement) {
          progressHintElement.textContent = translatorRef.current('terminal.followingHint')
          progressHintElement.title = translatorRef.current('terminal.pauseFollow')
        }
        if (stickyBarEl) {
          stickyBarEl.title = translatorRef.current('terminal.returnUserMessage')
          stickyBarEl.setAttribute('aria-label', translatorRef.current('terminal.returnUserMessageLabel'))
        }
        if (stickyLabelEl) {
          const seq = currentStickyRange?.seq
          stickyLabelEl.textContent = `↑ ${translatorRef.current('terminal.userMessage')}${typeof seq === 'number' ? ` #${seq}` : ''}`
        }
        if (foldView) {
          const anchorOriginal = toOriginalLine(term.buffer.active.viewportY)
          const viewportOffset = toDisplayLine(anchorOriginal) - term.buffer.active.viewportY
          foldView = composeFoldView(rawAnsi, foldRanges, collapsedKeys, translatorRef.current('terminal.lines'))
          writeComposed(() => {
            term.scrollToLine(Math.max(0, toDisplayLine(anchorOriginal) - viewportOffset))
            onFoldChangeRef.current?.()
          })
        } else {
          scanBuffer()
          injectFoldRows()
        }
      }

      const toggleFold = (fold: FoldRange) => {
        if (!rawAnsi) return
        openAtTop = false
        // Keep the toggled header at the same on-screen row across the rewrite.
        const beforeRow = toDisplayLine(fold.headerDisplay)
        const viewportOffset = beforeRow - term.buffer.active.viewportY
        if (collapsedKeys.has(fold.key)) collapsedKeys.delete(fold.key)
        else collapsedKeys.add(fold.key)
        recompose(() => {
          const afterRow = toDisplayLine(fold.headerDisplay)
          term.scrollToLine(Math.max(0, afterRow - viewportOffset))
        })
      }

      const setFoldsCollapsed = (keys: string[], collapsed: boolean, anchorOriginalRow?: number | null) => {
        if (!rawAnsi || !foldRanges.length) return
        const valid = new Set(foldRanges.map(f => f.key))
        let changed = false
        for (const k of keys) {
          if (!valid.has(k)) continue
          if (collapsed && !collapsedKeys.has(k)) { collapsedKeys.add(k); changed = true }
          if (!collapsed && collapsedKeys.has(k)) { collapsedKeys.delete(k); changed = true }
        }
        if (!changed) return
        openAtTop = false
        const anchor = anchorOriginalRow ?? toOriginalLine(term.buffer.active.viewportY)
        const viewportOffset = toDisplayLine(anchor) - term.buffer.active.viewportY
        recompose(() => {
          term.scrollToLine(Math.max(0, toDisplayLine(anchor) - viewportOffset))
        })
      }

      applyFoldsRef.current = (next: FoldRange[]) => {
        foldRanges = next
        // Drop collapsed state for folds that no longer exist (session grew).
        const valid = new Set(next.map(f => f.key))
        let dropped = false
        for (const k of [...collapsedKeys]) {
          if (!valid.has(k)) { collapsedKeys.delete(k); dropped = true }
        }
        for (const k of [...defaultedKeys]) {
          if (!valid.has(k)) defaultedKeys.delete(k)
        }
        // Default every newly-seen fold to collapsed (group, tool, rollback).
        let addedDefault = false
        for (const f of next) {
          if (!defaultedKeys.has(f.key)) {
            defaultedKeys.add(f.key)
            collapsedKeys.add(f.key)
            addedDefault = true
          }
        }
        // Open race: mount applies folds before loadRender fills rawAnsi.
        // Rewriting an empty buffer here forces a hard repaint, then the
        // real content write hard-paints again (double flash). Keep fold
        // state only; loadRender composes with collapsedKeys on first write.
        if (!rawAnsi) return
        if (collapsedKeys.size > 0 || dropped || addedDefault || foldView) recompose()
        else { injectFoldRows(); injectProgressRow(); injectUserHighlights() }
      }

      // Re-apply user-message highlights when the prop changes without a
      // rewrite (e.g. positions poll landed while folds stayed the same).
      // Also refreshes the sticky bar in case the resolved message changed.
      applyUserHighlightsRef.current = () => {
        if (!hasWrittenOnce) return
        injectUserHighlights()
        updateStickyUserMsg()
      }

      const showHoverFor = (bl: number, entry: InteractionEntry, clientX: number, clientY: number) => {
        showHoverDecoration(bl)
        if (tooltipEl) {
          const tip = entry.matcher.tooltip ?? ''
          tooltipEl.textContent = tip
          tooltipEl.style.display = tip ? 'block' : 'none'
          tooltipEl.style.left = (clientX + 14) + 'px'
          tooltipEl.style.top = (clientY - 38) + 'px'
        }
        if (xtermScreen) xtermScreen.style.cursor = 'pointer'
      }

      let lastHover: { bl: number; clientX: number; clientY: number } | null = null

      onMouseMove = (e: MouseEvent) => {
        if (!interactionMap.size) { hideHover(); return }
        const bl = getBufLine(e)
        if (bl === null) { hideHover(); lastHover = null; return }
        const entry = interactionMap.get(bl)
        lastHover = { bl, clientX: e.clientX, clientY: e.clientY }
        if (!entry) { hideHover(); return }

        // Rows with a validator only get the affordance once it confirms
        // (e.g. the detected path really exists). The mouse may sit still
        // while the check resolves, so completion re-shows from lastHover.
        if (entry.matcher.validate) {
          const state = rowValidity.get(bl)
          if (state === undefined) {
            rowValidity.set(bl, 'pending')
            hideHover()
            const text = term.buffer.active.getLine(bl)?.translateToString(true) ?? ''
            entry.matcher.validate(text).then(ok => {
              if (disposed || rowValidity.get(bl) !== 'pending') return
              rowValidity.set(bl, ok)
              if (!ok) {
                interactionMap.delete(bl)
                return
              }
              if (lastHover?.bl === bl) showHoverFor(bl, entry, lastHover.clientX, lastHover.clientY)
            })
            return
          }
          if (state !== true) { hideHover(); return }
        }
        showHoverFor(bl, entry, e.clientX, e.clientY)
      }

      onMouseLeave = () => hideHover()

      onClick = (e: MouseEvent) => {
        const bl = getBufLine(e)
        if (bl === null) return
        const entry = interactionMap.get(bl)
        if (entry) {
          // Validated matchers only activate after their check confirmed.
          if (entry.matcher.validate && rowValidity.get(bl) !== true) return
          e.preventDefault()
          e.stopPropagation()
          const core = (term as unknown as { _core?: XtermCoreWithMouse })._core
          const screenElement = core?.screenElement ?? xtermScreen ?? container
          const coords = core?._mouseService?.getCoords?.(e, screenElement, term.cols, term.rows, false)
          entry.matcher.onActivate(bl, entry.data, entry.matchIndex, {
            clientX: e.clientX,
            clientY: e.clientY,
            column: coords ? coords[0] - 1 : null,
            lineText: term.buffer.active.getLine(bl)?.translateToString(true) ?? '',
          })
        }
      }

      onCtxMenu = (e: MouseEvent) => {
        e.preventDefault()
        const bl = getBufLine(e)
        // Column comes from the same xterm MouseService call as the row
        // (coords are 1-based); needed to pick the path token under the cursor.
        const core = (term as unknown as { _core?: XtermCoreWithMouse })._core
        const screenElement = core?.screenElement ?? xtermScreen ?? container
        const coords = core?._mouseService?.getCoords?.(e, screenElement, term.cols, term.rows, false)
        // Join soft-wrapped rows so right-click on a long path (write header)
        // still resolves the full token for "打开文件".
        let lineText = ''
        if (bl !== null) {
          const start = wrapGroupStart(bl)
          lineText = joinWrappedLineText(start)
        }
        onContextMenuRef.current?.({
          clientX: e.clientX,
          clientY: e.clientY,
          originalRow: bl !== null ? toOriginalLine(bl) : null,
          column: coords ? coords[0] - 1 : null,
          lineText,
          collapsedFoldKeys: [...collapsedKeys],
        })
      }

      eventTarget.addEventListener('mousemove', onMouseMove)
      eventTarget.addEventListener('mouseleave', onMouseLeave)
      eventTarget.addEventListener('click', onClick)
      // The custom menu replaces the browser one across the whole terminal
      // area (container, not just the text screen).
      container.addEventListener('contextmenu', onCtxMenu)

      const getMetrics = (): ScrollMetrics => ({
        scrollTop: term.buffer.active.viewportY * TERMINAL_LINE_HEIGHT,
        scrollHeight: term.buffer.active.length * TERMINAL_LINE_HEIGHT,
        clientHeight: term.rows * TERMINAL_LINE_HEIGHT,
      })
      let lastMetrics: ScrollMetrics | undefined
      metricsBatcher = createFrameBatcher(metrics => {
        if (
          lastMetrics
          && lastMetrics.scrollTop === metrics.scrollTop
          && lastMetrics.scrollHeight === metrics.scrollHeight
          && lastMetrics.clientHeight === metrics.clientHeight
        ) {
          return
        }
        lastMetrics = metrics
        onScrollMetricsRef.current?.(metrics)
      })
      const queueMetrics = () => metricsBatcher?.push(getMetrics())

      const loadRender = (cols: number) => {
        fetchRenderANSI(sessionId, cols, tsKinds)
          .then(ansi => {
            if (disposed) return
            rawAnsi = ansi
            foldView = collapsedKeys.size > 0 ? composeFoldView(rawAnsi, foldRanges, collapsedKeys, translatorRef.current('terminal.lines')) : null
            writeComposed(() => {
              if (foldView) onFoldChangeRef.current?.()
            })
          })
          .catch(err => { term.write(`\x1b[31mError loading render: ${err.message}\x1b[0m`) })
      }

      // Search: decorations are always enabled (they drive the n/m counter);
      // visual styling lives in app.css on the addon's decoration classes
      // (the DOM renderer ignores decoration background options). "Highlight
      // all" off = container class that blanks the all-match layer via CSS.
      const buildSearchOptions = (o: { caseSensitive: boolean; wholeWord: boolean; regex: boolean; highlightAll: boolean }) => {
        container.classList.toggle('si-search-active-only', !o.highlightAll)
        return {
          caseSensitive: o.caseSensitive,
          wholeWord: o.wholeWord,
          regex: o.regex,
          decorations: {
            matchOverviewRuler: o.highlightAll ? '#facc15' : '#00000000',
            // The inline outline this sets is the only DOM marker of the
            // active match in addon-search 0.16 (see app.css); the visible
            // outline itself is suppressed there in favor of a background.
            activeMatchBorder: '#f59e0b',
            activeMatchColorOverviewRuler: '#f59e0b',
          },
        }
      }
      // addon-search 0.16 overwrites lastSearchOptions before diffing them,
      // so an option change alone never refreshes the all-match highlight;
      // clearing the cached term forces the next find to re-highlight.
      let lastSearchOptsKey = ''
      const invalidateSearchOnOptionChange = (o: { caseSensitive: boolean; wholeWord: boolean; regex: boolean; highlightAll: boolean }) => {
        const key = `${o.caseSensitive}|${o.wholeWord}|${o.regex}|${o.highlightAll}`
        if (key !== lastSearchOptsKey) {
          lastSearchOptsKey = key
          searchAddon.clearDecorations()
        }
      }
      let searchResultsCb: ((index: number, count: number) => void) | null = null
      const disposeSearchResults = searchAddon.onDidChangeResults(r => {
        searchResultsCb?.(r.resultIndex, r.resultCount)
      })
      disposeSearchResultsRef = disposeSearchResults

      if (controlRef) {
        controlRef.current = {
          scrollToLine: (line) => {
            if (line > 0) openAtTop = false
            term.scrollToLine(line)
          },
          scrollToLineCentered: (line) => {
            openAtTop = false
            term.scrollToLine(Math.max(0, line - Math.floor(term.rows / 2)))
          },
          getMetrics,
          setLineMatchers: (matchers) => {
            lineMatchers = matchers
            if (term.buffer.active.length > 1) {
              scanBuffer()
              injectFoldRows()
              injectProgressRow()
              injectUserHighlights()
              updateStickyUserMsg()
            }
          },
          flashLines,
          flashSearchMatch: (query) => {
            if (!query || !hasWrittenOnce) return false
            try {
              const found = searchAddon.findNext(query, buildSearchOptions({
                caseSensitive: false,
                wholeWord: false,
                regex: false,
                highlightAll: false,
              }))
              const selection = term.getSelectionPosition()
              if (found && selection) {
                const line = selection.start.y
                term.scrollToLine(Math.max(0, line - Math.floor(term.rows / 2)))
                flashLines(line, 1)
              }
              return found
            } catch {
              return false
            }
          },
          toDisplayLine,
          logicalToDisplayLine,
          toOriginalLine,
          jumpToPosition: (lineStart, logicalStart) => {
            if (execJump(lineStart, logicalStart, false)) {
              // Immediate success supersedes any older deferred jump — without
              // this the stale request would fire on the next buffer scan and
              // drag the user away from where they just landed.
              clearPendingJump()
              return
            }
            clearPendingJump()
            pendingJump = { lineStart, logicalStart, deadline: Date.now() + 15000 }
            pendingJumpTimer = setTimeout(() => {
              pendingJumpTimer = null
              if (disposed || !pendingJump) return
              const j = pendingJump
              pendingJump = null
              execJump(j.lineStart, j.logicalStart, true)
            }, 15000)
          },
          hiddenLineCount: () => foldView?.hiddenTotal ?? 0,
          setFoldsCollapsed,
          getCollapsedFoldKeys: () => [...collapsedKeys],
          searchNext: (query, opts) => {
            invalidateSearchOnOptionChange(opts)
            try {
              return searchAddon.findNext(query, buildSearchOptions(opts))
            } catch { return false } // invalid regex mid-typing
          },
          searchPrev: (query, opts) => {
            invalidateSearchOnOptionChange(opts)
            try {
              return searchAddon.findPrevious(query, buildSearchOptions(opts))
            } catch { return false }
          },
          searchClear: () => {
            searchAddon.clearDecorations()
            term.clearSelection() // the active match is selection-backed
          },
          refreshContent: async () => {
            if (!hasWrittenOnce || currentCols === 0) return 'unchanged'
            const ansi = await fetchRenderANSI(sessionId, currentCols, tsKinds)
            if (disposed || ansi === rawAnsi) return 'unchanged'
            const buf = term.buffer.active
            // Pin to bottom when already there, or when live-follow is on
            // (explicit tail -f mode for active sessions).
            // Explicit follow-output always pins bottom. The openAtTop guard
            // only blocks the "already at bottom" heuristic so a post-write
            // park does not defeat open-at-top on first load.
            const pinBottom = followOutputRef.current || (!openAtTop && buf.viewportY >= buf.baseY)
            if (pinBottom) openAtTop = false

            // Pure append: rendering is deterministic, so as long as nothing
            // structural changed upstream (an in-progress group header's n/m
            // counter, a collapsed fold), the old ANSI is a strict prefix and
            // the delta streams straight into the buffer.
            if (collapsedKeys.size === 0 && !foldView && ansi.startsWith(rawAnsi)) {
              const suffix = ansi.slice(rawAnsi.length)
              rawAnsi = ansi
              await new Promise<void>(resolve => term.write(suffix, () => {
                scanBuffer()
                injectFoldRows()
                injectProgressRow()
                injectUserHighlights()
                updateStickyUserMsg()
                queueMetrics()
                resolve()
              }))
              if (pinBottom) term.scrollToBottom()
              else if (openAtTop) term.scrollToLine(0)
              return 'appended'
            }

            const keepRow = buf.viewportY
            rawAnsi = ansi
            foldView = collapsedKeys.size > 0 ? composeFoldView(rawAnsi, foldRanges, collapsedKeys, translatorRef.current('terminal.lines')) : null
            await new Promise<void>(resolve => writeComposed(() => {
              if (pinBottom) {
                openAtTop = false
                term.scrollToBottom()
              } else if (openAtTop) {
                term.scrollToLine(0)
              } else {
                term.scrollToLine(keepRow)
              }
              resolve()
            }))
            if (foldView) onFoldChangeRef.current?.()
            return 'rewritten'
          },
          setSearchResultsListener: (cb) => { searchResultsCb = cb },
        }
      }

      disposeOnScroll = term.onScroll(() => {
        // User (or intentional programmatic) scroll away from the top ends
        // open-at-top mode. Ignore mid-rewrite scrolls and the short grace
        // after writes (xterm's bottom park / lagging scroll events).
        if (
          !writingContent
          && openAtTop
          && performance.now() >= openAtTopGraceUntil
          && term.buffer.active.viewportY > 0
        ) {
          openAtTop = false
        } else if (
          openAtTop
          && !followOutputRef.current
          && term.buffer.active.viewportY > 0
        ) {
          // During rewrite/grace, pull back to the top if something nudged us.
          term.scrollToLine(0)
        }
        clearHoverDecoration()
        if (tooltipEl) tooltipEl.style.display = 'none'
        if (xtermScreen) xtermScreen.style.cursor = ''
        queueMetrics()
        updateStickyUserMsg()
        // Diagnose scroll→blank: coalesce leading-edge samples into a
        // single debug line per animation frame so we don't spam.
        if (TERM_DEBUG) {
          const buf = term.buffer.active
          const c = container.querySelector<HTMLElement>('.xterm-screen canvas')
          dbg('scroll', {
            viewportY: buf.viewportY, baseY: buf.baseY, bufLen: buf.length, rows: term.rows,
            canvasW: c?.clientWidth, canvasH: c?.clientHeight,
            snapshotActive: removeSnapshot !== null,
          })
        }
      })

      disposeOnSelectionChange = term.onSelectionChange(() => {
        // Selecting text is another reported trigger for the white-screen
        // symptom. xterm redraws the viewport glpyhs to overlay selection
        // styling; if the WebGL renderer's glyph atlas is stale or its
        // canvas was concurrently resized, this redraw may paint the buffer
        // clear and only a later click (force-repaint) restores it. Log the
        // selection's buffer span plus the live canvas dimensions so a
        // capture of logs around a real blank reveal what the renderer
        // thinks the screen looks like at that instant.
        if (!TERM_DEBUG) return
        const sel = term.getSelection() // returns '' when selection cleared
        const c = container.querySelector<HTMLElement>('.xterm-screen canvas')
        dbg('selection-change', {
          len: sel.length, snapshotActive: removeSnapshot !== null,
          canvasW: c?.clientWidth, canvasH: c?.clientHeight,
          cols: term.cols, rows: term.rows, webglDegraded,
        })
      })

      observer = new ResizeObserver(entries => {
        // 0×0 or out-of-view containers are the most likely scroll→blank
        // trigger: xterm's fit() can clamp cols/rows to 0 while a parent
        // scroll container briefly reports zero height, after which the
        // WebGL renderer holds a cleared buffer until something forces a
        // repaint (a click → focus does). Log the observed content rect so
        // we can confirm or rule it out.
        const er = entries[0]?.contentRect
        dbg('resize', { w: er?.width, h: er?.height, containerW: container.clientWidth, containerH: container.clientHeight })
        fitAddon.fit()
        queueMetrics()
        const newCols = term.cols
        if (er && (er.width === 0 || er.height === 0)) dbg('resize-zero-detected', { colsAfterFit: term.cols, rowsAfterFit: term.rows })
        if (newCols !== currentCols) {
          currentCols = newCols
          if (resizeDebounce) clearTimeout(resizeDebounce)
          resizeDebounce = setTimeout(() => {
            if (disposed) return
            loadRender(newCols)
            onColsReadyRef.current?.(newCols)
          }, 500)
        }
      })
      observer.observe(container)

      // Windows-only signal: devicePixelRatio changes when the session
      // window is dragged between monitors of different HiDPI scale (very
      // common on Windows multi-monitor setups) or when the OS scale slider
      // moves. xterm's WebGL renderer reallocates its atlas on DPR change
      // and a mid-scroll change can leave the canvas cleared; this is a
      // prime suspect for the Windows-only blank. matchMedia('(resolution:
      // …dppx)') change fires reliably across DPR transitions.
      let dprOld = window.devicePixelRatio || 1
      if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
        dprWatcher = window.matchMedia(`(resolution: ${dprOld}dppx)`)
        onDprChange = () => {
          const now = window.devicePixelRatio || 1
          dbg('dpr-change', { from: dprOld, to: now, snapshotActive: removeSnapshot !== null, webglDegraded })
          dprOld = now
        }
        // addEventListener is the modern path; older Safari needs the
        // legacy addListener fallback.
        if (typeof dprWatcher.addEventListener === 'function') {
          dprWatcher.addEventListener('change', onDprChange)
        } else if (typeof dprWatcher.addListener === 'function') {
          dprWatcher.addListener(onDprChange)
        }
      }

      loadRender(currentCols)
      onColsReadyRef.current?.(currentCols)
      if (foldsRef.current.length) applyFoldsRef.current?.(foldsRef.current)
    })

    return () => {
      disposed = true
      if (resizeDebounce) clearTimeout(resizeDebounce)
      if (finishPaintTimeout) clearTimeout(finishPaintTimeout)
      observer?.disconnect()
      disposeOnScroll?.dispose()
      disposeOnSelectionChange?.dispose()
      if (dprWatcher && onDprChange) {
        dprWatcher.removeEventListener('change', onDprChange)
      }
      metricsBatcher?.cancel()
      const eventTarget = container.querySelector<HTMLElement>('.xterm-screen') ?? container
      if (onMouseMove) eventTarget.removeEventListener('mousemove', onMouseMove)
      if (onMouseLeave) eventTarget.removeEventListener('mouseleave', onMouseLeave)
      if (onClick) eventTarget.removeEventListener('click', onClick)
      if (onCtxMenu) container.removeEventListener('contextmenu', onCtxMenu)
      disposeSearchResultsRef?.dispose()
      removeSnapshot?.()
      hoverDecoration?.dispose()
      hoverMarker?.dispose()
      clearFlash()
      clearProgress()
      clearUserHighlights()
      tooltipEl?.remove()
      if (stickyBarEl) {
        stickyBarEl.removeEventListener('click', onStickyClick)
        stickyBarEl.removeEventListener('keydown', onStickyKeyDown)
        stickyBarEl.remove()
        stickyBarEl = null
        stickyLabelEl = null
        stickyTextEl = null
      }
      if (controlRef) controlRef.current = null
      applyFoldsRef.current = null
      applyUserHighlightsRef.current = null
      followWakeRef.current = null
      relocalizeTerminalRef.current = null
      termRef.current = null
      term.dispose()
    }
  }, [sessionId, tsKinds, terminalFont, terminalFontFamily, terminalFontSize])

  useEffect(() => {
    translatorRef.current = t
    relocalizeTerminalRef.current?.()
  }, [t])

  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.theme = terminalTheme(isDark, agentType)
    }
  }, [isDark, agentType])

  useEffect(() => {
    return onBannerColorChange(() => {
      if (termRef.current) {
        termRef.current.options.theme = terminalTheme(isDark, agentType)
      }
    })
  }, [isDark, agentType])

  useEffect(() => {
    applyFoldsRef.current?.(folds ?? [])
  }, [folds])

  // Re-apply user-message highlight decorations when the prop changes. The
  // mount effect owns the decoration closures; this just routes the new
  // ranges in via the ref. Version token avoids a no-op call when the
  // mount effect itself already refreshed (e.g. after a rewrite).
  useEffect(() => {
    void userPositionsVersionRef.current
    applyUserHighlightsRef.current?.()
  }, [userPositions])

  const dismissWebglWarn = () => {
    localStorage.setItem('si-webgl-warn-dismissed', '1')
    setWarnDismissed(true)
  }

  return (
    <div style={{ flex: 1, overflow: 'hidden', background: terminalTheme(isDark).background, display: 'flex', flexDirection: 'column' }}>
      {webglDegraded && !warnDismissed && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '5px 10px',
            fontSize: 12,
            lineHeight: 1.4,
            fontFamily: 'system-ui, -apple-system, sans-serif',
            color: isDark ? '#fde68a' : '#854d0e',
            background: isDark ? 'rgba(234,179,8,0.14)' : '#fef9c3',
            borderBottom: `1px solid ${isDark ? 'rgba(234,179,8,0.30)' : '#fde68a'}`,
          }}
        >
          <span style={{ flex: 1 }}>
            ⚠ {t('terminal.webglWarning')}
          </span>
          <button
            onClick={dismissWebglWarn}
            title={t('terminal.dismissWarning')}
            style={{
              flexShrink: 0,
              border: 'none',
              background: 'transparent',
              color: 'inherit',
              cursor: 'pointer',
              fontSize: 15,
              lineHeight: 1,
              padding: '0 2px',
              opacity: 0.7,
            }}
          >
            ×
          </button>
        </div>
      )}
      <div ref={containerRef} style={{ flex: 1, overflow: 'hidden' }} />
    </div>
  )
}
