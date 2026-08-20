import type { ScrollMetrics } from './minimapGeometry'
import type { TerminalTextRange } from './terminalInteractionGeometry'
import type { ViewportAnchor } from './viewportAnchor'

export type { ViewportAnchor }
export type { TerminalTextRange }

export const TERMINAL_LINE_HEIGHT = 14 // base; grok uses even denser via xterm lineHeight option (see TerminalPanel)

export interface TerminalFileMatch {
  path: string
  line: number | null
}

// Screen/cell context of the click that activated a matcher row, so handlers
// can anchor popovers at the cursor and inspect both coordinate spaces.
export interface TerminalActivateMeta {
  clientX: number
  clientY: number
  /** Zero-based xterm cell column from MouseService. */
  cellColumn: number | null
  /** JavaScript string offset for the clicked buffer row. */
  textOffset: number | null
  lineText: string
  /** Exact source range selected when a ranged matcher handled the click. */
  textRange?: TerminalTextRange
  /** Exact path selected by a path-bearing fold header. */
  selectedFile?: TerminalFileMatch
  /** When set, the click was on a path-bearing fold header — show 展开/收起 with open-file. */
  foldKey?: string
}

export interface TerminalLineMatch<T = unknown> {
  data: T
  /** Omitted when this match intentionally owns the complete terminal row. */
  textRange?: TerminalTextRange
}

export interface TerminalLineMatcher<T = unknown> {
  /** Each returned match owns its payload, validation identity, and optional text range. */
  match: (text: string, bufferLine?: number) => TerminalLineMatch<T>[]
  /** Static label, or a formatter that receives the matcher's match data (e.g. full URL). */
  tooltip?: string | ((data: T) => string)
  // Optional async confirmation (e.g. does this detected path actually exist).
  // Each returned match is validated independently and can be removed without
  // affecting other matches on the same buffer row.
  validate?: (lineText: string, data: T) => Promise<boolean>
  /**
   * A compact action rendered through an xterm marker decoration while this
   * matcher is hovered. Clicks are accepted only in its right-edge cells.
   */
  hoverAction?: {
    label: string
    cellWidth: number
  }
  onActivate: (bufLine: number, data: T, matchIndex: number, meta?: TerminalActivateMeta) => void
}

/** Resolve a matcher tooltip for hover display. */
export function resolveMatcherTooltip<T>(
  tooltip: TerminalLineMatcher<T>['tooltip'],
  data: T,
): string {
  if (tooltip == null) return ''
  return typeof tooltip === 'function' ? tooltip(data) : tooltip
}

export interface TerminalControl {
  scrollToLine: (line: number) => void
  // Programmatic jumps land the target line at the vertical center of the
  // viewport (top-anchored scrollToLine leaves it easy to miss).
  scrollToLineCentered: (line: number) => void
  getMetrics: () => ScrollMetrics
  /** Current xterm buffer selection, or an empty string when none exists. */
  getSelectionText: () => string
  // Top visible buffer line via xterm's own buffer state (exact regardless of
  // line-height variants); used to preserve the logical visible position
  // across layout changes that only alter the container height.
  getViewportTopLine: () => number
  // Original render row (positions line_start space) at the viewport center,
  // resolved through xterm's own buffer state + the fold mapping. Drives the
  // key-event outline's current-position tracking; null when no terminal is
  // attached yet.
  getViewportAnchor: () => number | null
  setLineMatchers: (matchers: TerminalLineMatcher<unknown>[]) => void
  // Briefly highlight buffer lines after a programmatic jump so the user can
  // see where they landed. Rendered via xterm marker/decoration (AGENTS.md:
  // no hand-rolled DOM coordinate math for terminal rows).
  flashLines: (startLine: number, count?: number) => void
  // Find the first matching terminal row and flash it after a global search
  // opens a session. Returns false while the terminal content is not ready.
  flashSearchMatch: (query: string) => boolean
  // Fold mapping between original render rows (what the positions API uses)
  // and current buffer rows (after collapsed tool groups are hidden).
  // Identity when nothing is collapsed.
  toDisplayLine: (origLine: number) => number
  toOriginalLine: (displayLine: number) => number
  // Original logical line ('\n'-split render) → current buffer row, resolved
  // through xterm's own isWrapped state. Exact even when collapsed-fold
  // badges change header wrap counts; prefer this for jump targets whenever
  // the position carries payload.logical_start.
  logicalToDisplayLine: (origLogical: number) => number
  // Jump (scroll-centered + flash) to a positions-API anchor. When the
  // logical line is not in the buffer yet — live positions can race ahead of
  // the terminal render — the jump is deferred and fired after the next
  // rewrite that contains it, instead of clamping to the buffer tail and
  // flashing the wrong row.
  jumpToPosition: (lineStart: number, logicalStart?: number) => void
  // Capture the current reading position as a content-stable anchor (original
  // logical line + wrap offset) that survives rewrites and remounts. Null
  // while no content has been written.
  captureViewportAnchor: () => ViewportAnchor | null
  hiddenLineCount: () => number
  // Batch collapse/expand fold groups in a single rewrite. anchorOriginalRow
  // (original render row, e.g. the right-clicked row) stays put on screen;
  // defaults to the top visible row when omitted.
  setFoldsCollapsed: (
    keys: string[],
    collapsed: boolean,
    anchorOriginalRow?: number | null,
    afterApply?: () => void,
  ) => void
  getCollapsedFoldKeys: () => string[]
  // Live tail: re-fetch the render and apply it incrementally — pure appends
  // stream into the buffer, structural changes (group counters, folds) fall
  // back to the snapshot-covered full rewrite. Pins the viewport to the
  // bottom when it was already there, or when live-follow (tail -f) is on.
  refreshContent: () => Promise<'appended' | 'rewritten' | 'unchanged'>
  // In-terminal search. Matches the composed buffer, so collapsed tool groups
  // are not found until expanded. Navigation is time-sliced (does not block
  // typing/backspace on multi-10k-line buffers). All-match highlights and
  // n/m counting stay deferred/async.
  searchNext: (query: string, opts: TerminalSearchOptions) => boolean
  searchPrev: (query: string, opts: TerminalSearchOptions) => boolean
  /** Jump to the first (topmost) match. */
  searchFirst: (query: string, opts: TerminalSearchOptions) => boolean
  /** Jump to the last (bottommost) match. */
  searchLast: (query: string, opts: TerminalSearchOptions) => boolean
  searchClear: () => void
  /**
   * Toggle all-match highlights. When turning on with an active query, rebuilds
   * decorations asynchronously; when off, drops decorations and keeps selection.
   */
  setSearchHighlightAll: (on: boolean) => void
  setSearchResultsListener: (cb: ((index: number, count: number) => void) | null) => void
}

export interface TerminalSearchOptions {
  caseSensitive: boolean
  wholeWord: boolean
  regex: boolean
  // On = paint all matches (capped) via addon decorations. Off = selection-only
  // active match; n/m comes from a time-sliced counter (no DOM markers).
  highlightAll: boolean
}

// Payload for the terminal context menu: where the right-click landed, in
// original render rows so it can be matched against the positions cache.
// lineText/textOffset describe the clicked buffer row so the menu can offer
// row-content-aware actions (e.g. open the file path under the cursor). The
// cell coordinate remains available for terminal-only consumers.
export interface TerminalContextMenuEvent {
  clientX: number
  clientY: number
  originalRow: number | null
  cellColumn: number | null
  textOffset: number | null
  lineText: string
  /** Selection frozen from xterm at the context-menu event boundary. */
  selectionText: string
  collapsedFoldKeys: string[]
  /** Exact path selected by a ranged matcher or path-bearing fold header. */
  selectedFile?: TerminalFileMatch
  /** Path-bearing fold header: offer 展开/收起 alongside open-file in fileOnly menu. */
  foldKey?: string
}

// A user-message range to highlight in the terminal buffer. lineStart/lineEnd
// are original render rows (positions API); logicalStart/logicalEnd are the
// '\n'-split source rows that stay exact when collapsed-fold badges shift
// display rows. text is the user's prompt text for the sticky top bar.
export interface UserHighlightRange {
  key: string
  lineStart: number
  lineEnd?: number
  logicalStart?: number
  logicalEnd?: number
  text: string
  tsMs?: number | null
  seq?: number
}
