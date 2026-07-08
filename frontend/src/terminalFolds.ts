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
  level: 'group' | 'tool' // group = whole "▼ Tools (n/m)" run; tool = one tool's body
  headerDisplay: number // display row of the "▼ …" header line
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
    const level = pl['level'] === 'tool' ? 'tool' : 'group'
    folds.push({
      key: p.position_key,
      label: p.label,
      level,
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

// Folds whose header falls inside the turn containing originalRow. Turn
// extents are [banner, next banner); rows before the first banner (session
// header) belong to no turn. All rows are original display rows.
export function foldKeysInTurn(
  folds: FoldRange[],
  turnStarts: number[],
  originalRow: number,
): string[] {
  const starts = [...turnStarts].sort((a, b) => a - b)
  let turnStart = -1
  let turnEnd = Infinity
  for (let i = 0; i < starts.length; i++) {
    if (starts[i] > originalRow) { turnEnd = starts[i]; break }
    turnStart = starts[i]
  }
  if (turnStart < 0) return []
  return folds
    .filter(f => f.headerDisplay >= turnStart && f.headerDisplay < turnEnd)
    .map(f => f.key)
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
  const collapsedFolds = folds.filter(f => collapsed.has(f.key))
  // De-nest: when a group is collapsed it already hides its member tool folds,
  // so drop any collapsed range fully contained in another collapsed range.
  // This keeps the active ranges disjoint — the prefix-sum row math below
  // assumes no overlap, and double-counting a nested body would corrupt it.
  const active = collapsedFolds
    .filter(f => !collapsedFolds.some(g =>
      g.key !== f.key && g.displayStart <= f.displayStart && f.displayEnd <= g.displayEnd
      && (g.displayStart < f.displayStart || f.displayEnd < g.displayEnd)))
    .sort((a, b) => a.displayStart - b.displayStart)

  // Hidden display-row count per collapsed fold, shown as "(N 行)" on its header.
  const hiddenByHeaderLogical = new Map<number, number>()
  for (const f of active) hiddenByHeaderLogical.set(f.headerLogical, f.displayEnd - f.displayStart)

  let text = ansi
  if (active.length > 0) {
    const lines = ansi.split('\n')
    const out: string[] = []
    let cursor = 0
    const foldHeader = (line: string, hidden: number): string => {
      // Flip ▼→▶ and append the hidden-row count before the line's trailing
      // reset so the badge inherits no stray color.
      const flipped = line.replace('▼', '▶')
      const badge = `\x1b[2m (${hidden} 行)\x1b[0m`
      const resetAt = flipped.lastIndexOf('\x1b[0m')
      return resetAt >= 0
        ? flipped.slice(0, resetAt + 4) + badge
        : flipped + badge
    }
    for (const f of [...active].sort((a, b) => a.logicalStart - b.logicalStart)) {
      for (let i = cursor; i < f.logicalStart && i < lines.length; i++) {
        const hidden = hiddenByHeaderLogical.get(i)
        out.push(hidden !== undefined ? foldHeader(lines[i], hidden) : lines[i])
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
