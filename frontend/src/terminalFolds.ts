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
  level: 'group' | 'tool' | 'rollback' // rollback = abandoned Codex turn segment
  headerDisplay: number // display row of the "▼ …" header line
  headerLogical: number
  displayStart: number // body extent, display rows [start, end)
  displayEnd: number
  logicalStart: number // body extent, logical lines [start, end)
  logicalEnd: number
  badgeOffset: number // UTF-16 index in the header line to splice the "(N 行)" badge at; -1 = append at end
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
    const level = pl['level'] === 'tool' ? 'tool' : pl['level'] === 'rollback' ? 'rollback' : 'group'
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
      badgeOffset: num('badge_offset') ?? -1,
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
  /**
   * Original logical line → composed logical line (collapsed bodies map to
   * their header line). Pure '\n'-count bookkeeping — unlike display rows it
   * cannot drift when the spliced fold badge makes a header soft-wrap, so
   * jump targets should resolve through this and xterm's isWrapped state.
   */
  toComposedLogical(orig: number): number
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

  // Hidden display-row count + badge splice offset per collapsed fold, keyed by
  // header logical line. The offset comes from the backend profile that rendered
  // the header (it alone knows where its tool name ends), so composition stays
  // profile-agnostic — no byte-shape sniffing here.
  const badgeByHeaderLogical = new Map<number, { hidden: number; offset: number }>()
  for (const f of active) badgeByHeaderLogical.set(f.headerLogical, { hidden: f.displayEnd - f.displayStart, offset: f.badgeOffset })

  let text = ansi
  if (active.length > 0) {
    const lines = ansi.split('\n')
    const out: string[] = []
    let cursor = 0
    const foldHeader = (line: string, hidden: number, offset: number): string => {
      // Flip ▼→▶ and show the hidden-row count. The badge (dim, self-reset) is
      // spliced at the backend-supplied UTF-16 offset — placed just past the tool
      // name so it stays on the header's first display row next to the arrow even
      // when a long untruncated summary soft-wraps. offset < 0 (e.g. group
      // headers) → append before the line's trailing reset.
      const flipped = line.replace('▼', '▶')
      const badge = `\x1b[2m (${hidden} 行)\x1b[0m`
      if (offset >= 0 && offset <= flipped.length) {
        return flipped.slice(0, offset) + badge + flipped.slice(offset)
      }
      const resetAt = flipped.lastIndexOf('\x1b[0m')
      return resetAt >= 0
        ? flipped.slice(0, resetAt + 4) + badge
        : flipped + badge
    }
    for (const f of [...active].sort((a, b) => a.logicalStart - b.logicalStart)) {
      for (let i = cursor; i < f.logicalStart && i < lines.length; i++) {
        const b = badgeByHeaderLogical.get(i)
        out.push(b !== undefined ? foldHeader(lines[i], b.hidden, b.offset) : lines[i])
      }
      cursor = Math.max(cursor, f.logicalEnd)
    }
    for (let i = cursor; i < lines.length; i++) out.push(lines[i])
    text = out.join('\n')
  }

  const ranges = active.map(f => [f.displayStart, f.displayEnd] as const)
  const hiddenTotal = ranges.reduce((s, [a, b]) => s + (b - a), 0)
  const logicalRanges = [...active].sort((a, b) => a.logicalStart - b.logicalStart)
    .map(f => ({ start: f.logicalStart, end: f.logicalEnd, header: f.headerLogical }))

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

  const toComposedLogical = (orig: number): number => {
    let hidden = 0
    for (const r of logicalRanges) {
      if (orig >= r.end) {
        hidden += r.end - r.start
        continue
      }
      if (orig >= r.start) return Math.max(0, r.header - hidden) // inside a hidden body → its header line
      break
    }
    return orig - hidden
  }

  return { text, hiddenTotal, toDisplay, toOriginal, toComposedLogical }
}
