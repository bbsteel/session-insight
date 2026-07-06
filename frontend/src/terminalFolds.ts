import type { MiniMapPosition } from './types'

// Fold composition for the terminal replay.
//
// The backend emits "fold" positions describing each collapsible tool run in
// BOTH coordinate spaces: display rows (soft wraps included — what xterm,
// the minimap and scroll math use) and logical lines (raw '\n'-split ANSI —
// what client-side slicing needs). Folding recomposes the raw ANSI by logical
// lines and shifts display rows by the backend-provided display extents, so
// the client never re-implements wrap simulation.

export interface FoldRange {
  key: string
  label: string
  headerDisplay: number // display row of the "▼ Tools (n/m)" header line
  headerLogical: number
  displayStart: number // body extent, display rows [start, end)
  displayEnd: number
  logicalStart: number // body extent, logical lines [start, end)
  logicalEnd: number
}

export function foldsFromPositions(positions: MiniMapPosition[] | undefined | null): FoldRange[] {
  if (!positions) return []
  const folds: FoldRange[] = []
  for (const p of positions) {
    if (p.kind !== 'fold' || !p.payload) continue
    const pl = p.payload as Record<string, unknown>
    const num = (k: string): number | null => (typeof pl[k] === 'number' ? (pl[k] as number) : null)
    const displayStart = num('display_start')
    const displayEnd = num('display_end')
    const logicalStart = num('logical_start')
    const logicalEnd = num('logical_end')
    const headerLogical = num('header_logical')
    if (displayStart === null || displayEnd === null || logicalStart === null || logicalEnd === null || headerLogical === null) continue
    if (displayEnd <= displayStart || logicalEnd <= logicalStart) continue
    folds.push({
      key: p.position_key,
      label: p.label,
      headerDisplay: p.line_start,
      headerLogical,
      displayStart,
      displayEnd,
      logicalStart,
      logicalEnd,
    })
  }
  return folds.sort((a, b) => a.displayStart - b.displayStart)
}

export interface FoldView {
  text: string
  hiddenTotal: number
  /** Original display row → current display row (collapsed bodies map to their header row). */
  toDisplay(orig: number): number
  /** Current display row → original display row. */
  toOriginal(display: number): number
}

export function composeFoldView(ansi: string, folds: FoldRange[], collapsed: ReadonlySet<string>): FoldView {
  const active = folds
    .filter(f => collapsed.has(f.key))
    .sort((a, b) => a.displayStart - b.displayStart)

  let text = ansi
  if (active.length > 0) {
    const lines = ansi.split('\n')
    const out: string[] = []
    let cursor = 0
    for (const f of [...active].sort((a, b) => a.logicalStart - b.logicalStart)) {
      for (let i = cursor; i < f.logicalStart && i < lines.length; i++) {
        out.push(i === f.headerLogical ? lines[i].replace('▼', '▶') : lines[i])
      }
      cursor = Math.max(cursor, f.logicalEnd)
    }
    for (let i = cursor; i < lines.length; i++) out.push(lines[i])
    text = out.join('\n')
  }

  const ranges = active.map(f => [f.displayStart, f.displayEnd] as const)
  const hiddenTotal = ranges.reduce((s, [a, b]) => s + (b - a), 0)

  const toDisplay = (orig: number): number => {
    let hidden = 0
    for (const [a, b] of ranges) {
      if (orig >= b) {
        hidden += b - a
        continue
      }
      if (orig >= a) return Math.max(0, a - 1 - hidden) // inside a hidden body → its header row
      break
    }
    return orig - hidden
  }

  const toOriginal = (display: number): number => {
    let hidden = 0
    for (const [a, b] of ranges) {
      const headerDisp = a - 1 - hidden
      if (display <= headerDisp) break
      hidden += b - a
    }
    return display + hidden
  }

  return { text, hiddenTotal, toDisplay, toOriginal }
}
