import type { ScrollMetrics } from './minimapGeometry'

export const TERMINAL_LINE_HEIGHT = 16

export interface TerminalControl {
  scrollToLine: (line: number) => void
  getMetrics: () => ScrollMetrics
}
