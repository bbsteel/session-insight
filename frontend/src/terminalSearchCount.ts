// Time-sliced match counting over an xterm buffer. Used when all-match
// decorations are off so Ctrl+F can report n/m without building DOM markers.

import type { Terminal } from '@xterm/xterm'

export interface TerminalSearchCountOptions {
  caseSensitive: boolean
  wholeWord: boolean
  regex: boolean
}

export interface TerminalSearchCountResult {
  /** 0-based index of the active (selected) match, or -1 if unknown. */
  index: number
  count: number
  capped: boolean
}

const NON_WORD_CHARS = ' ~!@#$%^&*()+`-=[]{}|\\;:"\',./<>?'

function isWholeWord(line: string, index: number, len: number): boolean {
  const beforeOk = index === 0 || NON_WORD_CHARS.includes(line[index - 1]!)
  const afterOk = index + len >= line.length || NON_WORD_CHARS.includes(line[index + len]!)
  return beforeOk && afterOk
}

/** Text of an unwrapped line including continuation wrap rows; null if row is a wrap tail. */
export function unwrappedLineText(term: Terminal, row: number): { text: string; rowCount: number } | null {
  const first = term.buffer.active.getLine(row)
  if (!first || first.isWrapped) return null
  let text = first.translateToString(true)
  let rowCount = 1
  for (;;) {
    const next = term.buffer.active.getLine(row + rowCount)
    if (!next?.isWrapped) break
    text += next.translateToString(true)
    rowCount++
  }
  return { text, rowCount }
}

export interface TerminalMatchSpan {
  offset: number
  length: number
}

export interface TerminalMatchPos {
  row: number
  col: number
  length: number
}

/** Offset+length of each match in a single unwrapped line. */
export function matchSpansInLine(
  line: string,
  term: string,
  opts: TerminalSearchCountOptions,
): TerminalMatchSpan[] {
  if (!term) return []
  const out: TerminalMatchSpan[] = []
  if (opts.regex) {
    let re: RegExp
    try {
      re = new RegExp(term, opts.caseSensitive ? 'g' : 'gi')
    } catch {
      return []
    }
    let m: RegExpExecArray | null
    while ((m = re.exec(line)) !== null) {
      if (!m[0]) {
        re.lastIndex++
        continue
      }
      if (!opts.wholeWord || isWholeWord(line, m.index, m[0].length)) {
        out.push({ offset: m.index, length: m[0].length })
      }
    }
    return out
  }
  const hay = opts.caseSensitive ? line : line.toLowerCase()
  const needle = opts.caseSensitive ? term : term.toLowerCase()
  let from = 0
  while (from <= hay.length - needle.length) {
    const i = hay.indexOf(needle, from)
    if (i < 0) break
    if (!opts.wholeWord || isWholeWord(hay, i, needle.length)) {
      out.push({ offset: i, length: needle.length })
    }
    from = i + Math.max(1, needle.length)
  }
  return out
}

/** String offsets of each match in a single unwrapped line. */
export function matchOffsetsInLine(
  line: string,
  term: string,
  opts: TerminalSearchCountOptions,
): number[] {
  return matchSpansInLine(line, term, opts).map(span => span.offset)
}

/** True when the current selection still contains a match for `query`. */
export function selectionTextMatchesQuery(
  selection: string,
  query: string,
  opts: TerminalSearchCountOptions,
): boolean {
  if (!selection || !query) return false
  return matchSpansInLine(selection, query, opts).length > 0
}

function posAtOrAfter(row: number, col: number, startRow: number, startCol: number, inclusive: boolean): boolean {
  if (row !== startRow) return row > startRow
  return inclusive ? col >= startCol : col > startCol
}

function posAtOrBefore(row: number, col: number, startRow: number, startCol: number, inclusive: boolean): boolean {
  if (row !== startRow) return row < startRow
  return inclusive ? col <= startCol : col < startCol
}

/**
 * Time-sliced first/next/previous match over the active buffer. Used instead of
 * addon-search's synchronous findNext so typing/backspace stays responsive on
 * multi-10k-line sessions.
 */
export async function findTerminalMatch(
  term: Terminal,
  query: string,
  opts: TerminalSearchCountOptions,
  spec: {
    direction: 'next' | 'prev'
    startRow: number
    startCol: number
    inclusive: boolean
    wrap: boolean
    linesPerSlice?: number
    isCancelled?: () => boolean
  },
): Promise<TerminalMatchPos | null> {
  if (!query) return null
  const cols = Math.max(1, term.cols)
  const length = term.buffer.active.length
  if (length <= 0) return null
  const isCancelled = spec.isCancelled ?? (() => false)
  const linesPerSlice = spec.linesPerSlice ?? 400
  const startRow = Math.max(0, Math.min(spec.startRow, length - 1))
  const startCol = Math.max(0, spec.startCol)

  const unwrappedStart = (row: number): number => {
    let y = row
    while (y > 0) {
      const line = term.buffer.active.getLine(y)
      if (!line?.isWrapped) break
      y--
    }
    return y
  }

  const matchesOnBlock = (blockRow: number, text: string): TerminalMatchPos[] => {
    return matchSpansInLine(text, query, opts).map(span => {
      const pos = offsetToBufferPos(blockRow, span.offset, cols)
      return { row: pos.row, col: pos.col, length: span.length }
    })
  }

  const scanForward = async (
    fromRow: number,
    fromCol: number,
    inclusive: boolean,
    stopBeforeRow: number | null,
  ): Promise<TerminalMatchPos | null> => {
    let rowsVisited = 0
    for (let y = unwrappedStart(fromRow); y < length; ) {
      if (isCancelled()) return null
      if (stopBeforeRow !== null && y >= stopBeforeRow) return null
      const block = unwrappedLineText(term, y)
      if (!block) {
        y++
        rowsVisited++
      } else {
        const hits = matchesOnBlock(y, block.text)
        const hit = hits.find(m => posAtOrAfter(m.row, m.col, fromRow, fromCol, inclusive))
        if (hit) return hit
        y += block.rowCount
        rowsVisited += block.rowCount
      }
      if (rowsVisited >= linesPerSlice) {
        rowsVisited = 0
        await new Promise<void>(resolve => setTimeout(resolve, 0))
      }
    }
    return null
  }

  const scanBackward = async (
    fromRow: number,
    fromCol: number,
    inclusive: boolean,
    stopAfterRow: number | null,
  ): Promise<TerminalMatchPos | null> => {
    let rowsVisited = 0
    for (let y = unwrappedStart(fromRow); y >= 0; ) {
      if (isCancelled()) return null
      if (stopAfterRow !== null && y < stopAfterRow) return null
      const block = unwrappedLineText(term, y)
      if (!block) {
        y--
        rowsVisited++
      } else {
        const hits = matchesOnBlock(y, block.text)
        for (let i = hits.length - 1; i >= 0; i--) {
          const hit = hits[i]!
          if (posAtOrBefore(hit.row, hit.col, fromRow, fromCol, inclusive)) return hit
        }
        y -= 1
        rowsVisited += block.rowCount
      }
      if (rowsVisited >= linesPerSlice) {
        rowsVisited = 0
        await new Promise<void>(resolve => setTimeout(resolve, 0))
      }
    }
    return null
  }

  if (spec.direction === 'next') {
    const hit = await scanForward(startRow, startCol, spec.inclusive, null)
    if (hit || !spec.wrap || isCancelled()) return hit
    return scanForward(0, 0, true, startRow + 1)
  }
  const hit = await scanBackward(startRow, startCol, spec.inclusive, null)
  if (hit || !spec.wrap || isCancelled()) return hit
  return scanBackward(length - 1, cols, true, unwrappedStart(startRow))
}

/**
 * Map a string offset in an unwrapped line to a buffer (row, col). Wide chars
 * are approximated as one cell — good enough for selection index matching.
 */
export function offsetToBufferPos(
  startRow: number,
  offset: number,
  cols: number,
): { row: number; col: number } {
  const row = startRow + Math.floor(offset / cols)
  const col = offset % cols
  return { row, col }
}

/**
 * Count matches in the active buffer, yielding every `linesPerSlice` rows so
 * the UI stays responsive. `onProgress` may fire multiple times.
 */
export async function countTerminalMatches(
  term: Terminal,
  query: string,
  opts: TerminalSearchCountOptions,
  options: {
    maxCount?: number
    linesPerSlice?: number
    isCancelled?: () => boolean
    onProgress?: (result: TerminalSearchCountResult) => void
  } = {},
): Promise<TerminalSearchCountResult> {
  const maxCount = options.maxCount ?? 9999
  const linesPerSlice = options.linesPerSlice ?? 300
  const isCancelled = options.isCancelled ?? (() => false)
  const cols = Math.max(1, term.cols)
  const length = term.buffer.active.length
  const sel = term.getSelectionPosition()
  let count = 0
  let index = -1
  let capped = false
  let rowsVisited = 0

  for (let y = 0; y < length; ) {
    if (isCancelled()) return { index, count, capped: true }

    const sliceRows = linesPerSlice
    const sliceStartVisited = rowsVisited
    while (y < length && rowsVisited - sliceStartVisited < sliceRows) {
      const block = unwrappedLineText(term, y)
      if (!block) {
        y++
        rowsVisited++
        continue
      }
      const offsets = matchOffsetsInLine(block.text, query, opts)
      for (const off of offsets) {
        const pos = offsetToBufferPos(y, off, cols)
        if (sel && index < 0) {
          if (pos.row === sel.start.y && pos.col === sel.start.x) {
            index = count
          } else if (
            pos.row < sel.start.y
            || (pos.row === sel.start.y && pos.col < sel.start.x)
          ) {
            // still before selection
          } else if (
            pos.row === sel.start.y
            && Math.abs(pos.col - sel.start.x) <= Math.max(1, query.length)
          ) {
            // near selection on same row (wide-char / wrap mapping drift)
            index = count
          }
        }
        count++
        if (count >= maxCount) {
          capped = true
          const result = { index, count, capped }
          options.onProgress?.(result)
          return result
        }
      }
      y += block.rowCount
      rowsVisited += block.rowCount
    }

    options.onProgress?.({ index, count, capped })
    if (y < length) {
      await new Promise<void>(resolve => setTimeout(resolve, 0))
    }
  }

  return { index, count, capped }
}
