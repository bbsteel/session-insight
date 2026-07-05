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
}
