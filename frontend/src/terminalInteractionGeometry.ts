export interface PointerLineInput {
  clientY: number
  screenTop: number
  cellHeight: number
  viewportY: number
  rowCount: number
}

export interface MarkerOffsetInput {
  bufferLine: number
  baseY: number
  cursorY: number
}

/** A half-open range in the JavaScript string returned for one terminal row. */
export interface TerminalTextRange {
  start: number
  end: number
}

/** A half-open range in xterm cell columns for one terminal row. */
export interface TerminalCellRange {
  start: number
  end: number
}

/** Minimal xterm buffer-cell shape needed to translate text offsets to cells. */
export interface TerminalBufferCell {
  getChars(): string
  getWidth(): number
}

/** Minimal xterm buffer-line shape needed by the range conversion helpers. */
export interface TerminalBufferLine {
  getCell(column: number): TerminalBufferCell | undefined
}

export interface EditHeaderMatch {
  toolName: string
  filePath: string
}

/**
 * Converts a JavaScript string offset into xterm's cell-column space.
 *
 * JavaScript indexes count code units while xterm columns count terminal
 * cells. The distinction matters for CJK and other wide glyphs, so hover
 * decorations must use the buffer cells rather than treating the two spaces
 * as interchangeable.
 */
export function terminalTextOffsetToCellColumn(
  bufferLine: TerminalBufferLine,
  textOffset: number,
  maxColumns: number,
): number {
  const boundedTextOffset = Math.max(0, textOffset)
  let consumedTextLength = 0
  let cellColumn = 0

  while (cellColumn < maxColumns) {
    const cell = bufferLine.getCell(cellColumn)
    if (!cell) return cellColumn

    if (boundedTextOffset <= consumedTextLength) return cellColumn

    const cellText = cell.getChars()
    const nextTextOffset = consumedTextLength + cellText.length
    if (boundedTextOffset < nextTextOffset) return cellColumn

    consumedTextLength = nextTextOffset
    cellColumn += Math.max(1, cell.getWidth())
  }

  return maxColumns
}

/** Converts a text range to a clamped, drawable xterm cell range. */
export function terminalTextRangeToCellRange(
  bufferLine: TerminalBufferLine,
  textRange: TerminalTextRange,
  textLength: number,
  maxColumns: number,
): TerminalCellRange | null {
  const startTextOffset = Math.max(0, Math.min(textLength, textRange.start))
  const endTextOffset = Math.max(0, Math.min(textLength, textRange.end))
  if (endTextOffset <= startTextOffset) return null

  const start = terminalTextOffsetToCellColumn(bufferLine, startTextOffset, maxColumns)
  const end = terminalTextOffsetToCellColumn(bufferLine, endTextOffset, maxColumns)
  if (end <= start) return null
  return { start, end }
}

export function getBufferLineFromPointer({
  clientY,
  screenTop,
  cellHeight,
  viewportY,
  rowCount,
}: PointerLineInput): number | null {
  if (cellHeight <= 0 || rowCount <= 0) return null

  const row = Math.floor((clientY - screenTop) / cellHeight)
  if (row < 0 || row >= rowCount) return null

  return viewportY + row
}

export function getBufferLineFromXtermCoords(coords: [number, number] | undefined, viewportY: number): number | null {
  if (!coords) return null
  return viewportY + coords[1] - 1
}

export function getMarkerOffsetForBufferLine({
  bufferLine,
  baseY,
  cursorY,
}: MarkerOffsetInput): number {
  return bufferLine - (baseY + cursorY)
}

export function parseEditHeaderLine(text: string): EditHeaderMatch | null {
  // Fill chars cover both box charsets: 双线 ═（默认档案）与圆角 ─（chrys 档案）。
  const match = text.match(/✏(?:\uFE0F)?\s*([^:]+):\s*(.+?)(?:\s+[═─]+.*)?$/u)
  if (!match) return null

  return {
    toolName: match[1].trim(),
    filePath: match[2].trim(),
  }
}
