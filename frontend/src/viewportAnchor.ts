// Viewport anchor: a rewrite-proof record of the user's reading position.
//
// Buffer display rows are unstable — folding hides bodies, a cols/font change
// re-wraps every line, and a live re-render shifts row counts — so saving
// "the user was at row N" cannot survive any rewrite. The coordinate space
// that survives all of these is the ORIGINAL logical line ('\n'-split of the
// full render) plus the soft-wrap offset below that line's first buffer row.
// A rewrite captures the anchor from the outgoing buffer and resolves it
// against the freshly scanned one, keeping the reading position stable across
// collapse/expand, zoom, panel resize, live rewrites, and full remounts.

export interface ViewportAnchor {
  /** Original logical line ('\n'-split full render) containing the viewport top row. */
  originalLogical: number
  /** Soft-wrapped buffer rows the viewport top sits below that logical line's first row. */
  offsetRows: number
}

/**
 * Capture the anchor for the buffer row at the viewport top.
 *
 * @param composedLogicalBufferRows buffer row of each composed logical line
 *   (ascending; xterm's non-wrapped rows), as rebuilt by the buffer scan.
 * @param viewportRow top visible buffer row (xterm viewportY).
 * @param toOriginalLogical composed logical index → original logical line
 *   (FoldView.toOriginalLogical; identity when nothing is collapsed).
 */
export function captureViewportAnchor(
  composedLogicalBufferRows: readonly number[],
  viewportRow: number,
  toOriginalLogical: (composedLogical: number) => number,
): ViewportAnchor {
  // Largest index whose buffer row is at/above the viewport top: the composed
  // logical line the top row belongs to.
  let lo = 0
  let hi = composedLogicalBufferRows.length - 1
  let index = 0
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (composedLogicalBufferRows[mid] <= viewportRow) {
      index = mid
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return {
    originalLogical: toOriginalLogical(index),
    offsetRows: Math.max(0, viewportRow - composedLogicalBufferRows[index]),
  }
}

/**
 * Resolve a captured anchor to a buffer row in the freshly scanned buffer.
 * The wrap offset is clamped to the logical line's current extent so a cols
 * change that shortened the wrap group cannot overshoot into the next line.
 *
 * @param toComposedLogical original logical line → composed logical index
 *   (FoldView.toComposedLogical; identity when nothing is collapsed).
 */
export function resolveViewportAnchor(
  anchor: ViewportAnchor,
  composedLogicalBufferRows: readonly number[],
  bufferLength: number,
  toComposedLogical: (originalLogical: number) => number,
): number {
  if (composedLogicalBufferRows.length === 0) return 0
  const composed = Math.max(
    0,
    Math.min(toComposedLogical(anchor.originalLogical), composedLogicalBufferRows.length - 1),
  )
  const firstRow = composedLogicalBufferRows[composed]
  const nextLineRow = composed + 1 < composedLogicalBufferRows.length
    ? composedLogicalBufferRows[composed + 1]
    : bufferLength
  const maxOffset = Math.max(0, nextLineRow - firstRow - 1)
  return firstRow + Math.min(anchor.offsetRows, maxOffset)
}
