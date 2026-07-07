import type { ScrollMetrics } from './minimapGeometry'

export const TERMINAL_LINE_HEIGHT = 16

export interface TerminalLineMatcher<T = unknown> {
  match: (text: string) => T | null
  tooltip?: string
  onActivate: (bufLine: number, data: T, matchIndex: number) => void
}

export interface TerminalControl {
  scrollToLine: (line: number) => void
  getMetrics: () => ScrollMetrics
  setLineMatchers: (matchers: TerminalLineMatcher<unknown>[]) => void
  // Briefly highlight buffer lines after a programmatic jump so the user can
  // see where they landed. Rendered via xterm marker/decoration (AGENTS.md:
  // no hand-rolled DOM coordinate math for terminal rows).
  flashLines: (startLine: number, count?: number) => void
  // Fold mapping between original render rows (what the positions API uses)
  // and current buffer rows (after collapsed tool groups are hidden).
  // Identity when nothing is collapsed.
  toDisplayLine: (origLine: number) => number
  toOriginalLine: (displayLine: number) => number
  hiddenLineCount: () => number
  // Batch collapse/expand fold groups in a single rewrite. anchorOriginalRow
  // (original render row, e.g. the right-clicked row) stays put on screen;
  // defaults to the top visible row when omitted.
  setFoldsCollapsed: (keys: string[], collapsed: boolean, anchorOriginalRow?: number | null) => void
  getCollapsedFoldKeys: () => string[]
}

// Payload for the terminal context menu: where the right-click landed, in
// original render rows so it can be matched against the positions cache.
export interface TerminalContextMenuEvent {
  clientX: number
  clientY: number
  originalRow: number | null
  collapsedFoldKeys: string[]
}
