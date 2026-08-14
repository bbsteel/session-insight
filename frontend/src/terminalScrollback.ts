export const DEFAULT_TERMINAL_SCROLLBACK = 20_000

export interface TerminalScrollbackTarget {
  rows: number
  options: { scrollback?: number }
}

// xterm keeps viewport rows plus `scrollback` history. Capacity is therefore
// `rows + scrollback`; lines past that are dropped from the top of the buffer.
export function terminalBufferCapacity(scrollback: number, viewportRows: number): number {
  return Math.max(0, scrollback) + Math.max(1, viewportRows)
}

// Smallest scrollback option that can hold `bufferLines` after the viewport.
export function requiredTerminalScrollback(bufferLines: number, viewportRows: number): number {
  const rows = Math.max(1, viewportRows)
  const needed = Math.max(0, Math.ceil(bufferLines) - rows)
  return Math.max(DEFAULT_TERMINAL_SCROLLBACK, needed)
}

// Grow-only: never shrink mid-session (a later rewrite may still need the
// larger ring). Double when raising so streamed chunks do not reallocate
// xterm's CircularList on every write.
export function nextTerminalScrollback(
  current: number,
  neededBufferLines: number,
  viewportRows: number,
): number {
  const required = requiredTerminalScrollback(neededBufferLines, viewportRows)
  if (required <= current) return current
  return Math.max(required, current * 2)
}

// Conservative visible-row estimate used to size scrollback before xterm
// wraps. Escape sequences inflate the count slightly; over-estimate is safe.
export function estimateRenderedLineCount(text: string, cols: number): number {
  if (!text) return 0
  const width = Math.max(1, cols)
  let lines = 1
  let col = 0
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i)
    if (code === 10) {
      lines++
      col = 0
    } else if (code === 13) {
      col = 0
    } else {
      if (col >= width) {
        lines++
        col = 0
      }
      col++
    }
  }
  return lines
}

export function ensureTerminalScrollback(
  term: TerminalScrollbackTarget,
  neededBufferLines: number,
): number {
  const current = term.options.scrollback ?? DEFAULT_TERMINAL_SCROLLBACK
  const next = nextTerminalScrollback(current, neededBufferLines, term.rows)
  if (next !== current) {
    term.options.scrollback = next
  }
  return next
}
