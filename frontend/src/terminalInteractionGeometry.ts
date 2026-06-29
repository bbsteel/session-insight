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

export interface EditHeaderMatch {
  toolName: string
  filePath: string
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
  const match = text.match(/✏(?:\uFE0F)?\s*([^:]+):\s*(.+?)(?:\s+═+.*)?$/u)
  if (!match) return null

  return {
    toolName: match[1].trim(),
    filePath: match[2].trim(),
  }
}
