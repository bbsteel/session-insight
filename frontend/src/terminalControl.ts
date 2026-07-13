import type { ScrollMetrics } from './minimapGeometry'

export const TERMINAL_LINE_HEIGHT = 16

// Screen/cell context of the click that activated a matcher row, so handlers
// can anchor popovers at the cursor and inspect the exact clicked column.
export interface TerminalActivateMeta {
  clientX: number
  clientY: number
  column: number | null
  lineText: string
}

export interface TerminalLineMatcher<T = unknown> {
  match: (text: string) => T | null
  tooltip?: string
  // Optional async confirmation (e.g. does the detected path actually exist).
  // Rows failing validation lose their hover affordance and click handling;
  // results are cached per buffer row until the next rewrite.
  validate?: (lineText: string) => Promise<boolean>
  onActivate: (bufLine: number, data: T, matchIndex: number, meta?: TerminalActivateMeta) => void
}

export interface TerminalControl {
  scrollToLine: (line: number) => void
  // Programmatic jumps land the target line at the vertical center of the
  // viewport (top-anchored scrollToLine leaves it easy to miss).
  scrollToLineCentered: (line: number) => void
  getMetrics: () => ScrollMetrics
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
  hiddenLineCount: () => number
  // Batch collapse/expand fold groups in a single rewrite. anchorOriginalRow
  // (original render row, e.g. the right-clicked row) stays put on screen;
  // defaults to the top visible row when omitted.
  setFoldsCollapsed: (keys: string[], collapsed: boolean, anchorOriginalRow?: number | null) => void
  getCollapsedFoldKeys: () => string[]
  // Live tail: re-fetch the render and apply it incrementally — pure appends
  // stream into the buffer, structural changes (group counters, folds) fall
  // back to the snapshot-covered full rewrite. Follows the bottom only when
  // the viewport was already pinned there.
  refreshContent: () => Promise<'appended' | 'rewritten' | 'unchanged'>
  // In-terminal search (xterm addon-search). Searches the composed buffer,
  // so content inside collapsed tool groups is not matched until expanded.
  searchNext: (query: string, opts: TerminalSearchOptions) => boolean
  searchPrev: (query: string, opts: TerminalSearchOptions) => boolean
  searchClear: () => void
  setSearchResultsListener: (cb: ((index: number, count: number) => void) | null) => void
}

export interface TerminalSearchOptions {
  caseSensitive: boolean
  wholeWord: boolean
  regex: boolean
  // Off = only the active match is highlighted; decorations stay enabled
  // underneath (transparent) so the n/m counter keeps working.
  highlightAll: boolean
}

// Payload for the terminal context menu: where the right-click landed, in
// original render rows so it can be matched against the positions cache.
// lineText/column describe the clicked buffer row so the menu can offer
// row-content-aware actions (e.g. open the file path under the cursor).
export interface TerminalContextMenuEvent {
  clientX: number
  clientY: number
  originalRow: number | null
  column: number | null
  lineText: string
  collapsedFoldKeys: string[]
}
