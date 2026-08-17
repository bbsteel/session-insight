import type { EditCall, MiniMapPosition } from './types'

// Matching between the Diff modal's edit list and the terminal's "edit"
// positions. The session-edits API preserves raw event order, while the
// render pipeline re-pairs nested tool events (Chrys subagent calls) before
// assigning positions — so "the Nth edit" can point at different entries in
// the two lists. Both sides carry the invocation's tool_call_id: match on
// it, disambiguating multi-file invocations (apply_patch emits several
// edits per id) by the per-id occurrence ordinal, and fall back to plain
// indexing only when ids are absent.

/** Edit positions in render order (ascending original row). */
export function editPositionsSorted(positions: MiniMapPosition[]): MiniMapPosition[] {
  return positions
    .filter((p) => p.kind === 'edit')
    .sort((a, b) => a.line_start - b.line_start)
}

function positionEditId(position: MiniMapPosition): string {
  const id = position.payload?.['tool_call_id']
  return typeof id === 'string' ? id : ''
}

// Occurrence index of items[index] among same-id predecessors: the first
// Edit call of invocation X has ordinal 0, the second (apply_patch's next
// file) ordinal 1, and so on.
function perIdOrdinal<T>(items: readonly T[], idOf: (item: T) => string, index: number): number {
  const id = idOf(items[index])
  let ordinal = 0
  for (let i = 0; i < index; i++) {
    if (idOf(items[i]) === id) ordinal++
  }
  return ordinal
}

/** Terminal edit position for the modal's editIdx-th edit. */
export function editPositionForIndex(
  edits: readonly EditCall[],
  sortedEditPositions: readonly MiniMapPosition[],
  editIdx: number,
): MiniMapPosition | undefined {
  const edit = edits[editIdx]
  if (!edit) return undefined
  if (edit.tool_call_id) {
    const ordinal = perIdOrdinal(edits, (e) => e.tool_call_id ?? '', editIdx)
    const candidates = sortedEditPositions.filter((p) => positionEditId(p) === edit.tool_call_id)
    if (candidates.length > 0) return candidates[Math.min(ordinal, candidates.length - 1)]
  }
  return sortedEditPositions[editIdx]
}

/** Modal edit index owning an original render row; -1 when no edit starts there. */
export function editIndexForRow(
  edits: readonly EditCall[],
  sortedEditPositions: readonly MiniMapPosition[],
  originalRow: number,
): number {
  const posIdx = sortedEditPositions.findIndex((p) => p.line_start === originalRow)
  if (posIdx < 0) return -1
  const id = positionEditId(sortedEditPositions[posIdx])
  if (!id) return posIdx
  const ordinal = perIdOrdinal(sortedEditPositions, positionEditId, posIdx)
  let seen = 0
  for (let i = 0; i < edits.length; i++) {
    if ((edits[i].tool_call_id ?? '') === id) {
      if (seen === ordinal) return i
      seen++
    }
  }
  // Ids present on positions but missing from the edits payload (older
  // backend): plain indexing is the best remaining correspondence.
  return posIdx < edits.length ? posIdx : -1
}
