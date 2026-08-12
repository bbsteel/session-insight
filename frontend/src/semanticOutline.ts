// Pure logic for the semantic key-event outline (v0.6.1): payload validation,
// category counts, text filtering, and viewport-anchored current-position
// selection. React components consume these helpers; they never re-implement
// the rules. Categories/codes are stable backend machine codes — display copy
// lives in i18n, keyed by code.

import type { MiniMapPosition, OutlineCategory, OutlinePayload } from './types'

export const OUTLINE_CATEGORIES: OutlineCategory[] = ['anomaly', 'context', 'file_change', 'key_result']

export interface OutlineItem {
  key: string
  category: OutlineCategory
  code: string
  precision: 'exact' | 'estimated'
  severity: string
  turnIndex: number
  line: number // original render row (positions line_start space)
  lineEnd: number | null
  logical: number | null // original logical line; preferred jump target when present
  summary: string
  toolName: string
  filePath: string
  tsMs: number | null
  durationMs: number | null
}

const KNOWN_CODES = new Set([
  'tool_rejected', 'tool_timeout', 'tool_failed', 'verification_failed',
  'duration_spike', 'missing_shutdown',
  'compaction', 'rollback',
  'file_created', 'file_modified', 'file_deleted', 'file_renamed', 'file_change_unverified',
  'tests_passed', 'build_succeeded', 'lint_passed', 'typecheck_passed', 'checks_passed',
])

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

// toOutlineItem validates one position's payload. Unknown codes degrade to a
// per-category fallback ('_other') so a newer backend never crashes an older
// frontend; the item stays countable and jumpable.
export function toOutlineItem(p: MiniMapPosition): OutlineItem | null {
  if (p.kind !== 'outline' || !p.payload) return null
  const pl = p.payload as Record<string, unknown>
  const category = pl.category
  if (category !== 'anomaly' && category !== 'context' && category !== 'file_change' && category !== 'key_result') {
    return null
  }
  const rawCode = str(pl.code)
  const code = KNOWN_CODES.has(rawCode) ? rawCode : '_other'
  const precision = pl.precision === 'estimated' ? 'estimated' : 'exact'
  return {
    key: p.position_key,
    category,
    code,
    precision,
    severity: str(pl.severity),
    turnIndex: p.turn_index,
    line: p.line_start,
    lineEnd: typeof p.line_end === 'number' ? p.line_end : null,
    logical: num(pl.logical_start),
    summary: str(pl.summary) || p.label || '',
    toolName: str(pl.tool_name),
    filePath: str(pl.file_path),
    tsMs: num(pl.ts_ms),
    durationMs: num(pl.duration_ms),
  }
}

export function outlineItemsFromPositions(positions: MiniMapPosition[] | undefined | null): OutlineItem[] {
  if (!positions) return []
  const items: OutlineItem[] = []
  for (const p of positions) {
    const item = toOutlineItem(p)
    if (item) items.push(item)
  }
  // Backend order is already line/key stable; keep it.
  return items
}

export interface CategoryCount {
  visible: number
  total: number
}

// countByCategory returns per-category totals against the FULL item set —
// counts never shrink just because the user toggled a category off.
export function countByCategory(items: OutlineItem[], visible: OutlineItem[]): Record<OutlineCategory, CategoryCount> {
  const out: Record<OutlineCategory, CategoryCount> = {
    anomaly: { visible: 0, total: 0 },
    context: { visible: 0, total: 0 },
    file_change: { visible: 0, total: 0 },
    key_result: { visible: 0, total: 0 },
  }
  for (const i of items) out[i.category].total++
  const visibleKeys = new Set(visible.map(i => i.key))
  for (const i of items) if (visibleKeys.has(i.key)) out[i.category].visible++
  return out
}

// matchesOutlineQuery matches the stable code, localized title, raw summary,
// path, and tool name. It deliberately never touches unloaded full
// stdout/stderr.
export function matchesOutlineQuery(item: OutlineItem, localizedTitle: string, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (q === '') return true
  return (
    item.code.toLowerCase().includes(q) ||
    localizedTitle.toLowerCase().includes(q) ||
    item.summary.toLowerCase().includes(q) ||
    item.filePath.toLowerCase().includes(q) ||
    item.toolName.toLowerCase().includes(q)
  )
}

export function filterOutlineItems(
  items: OutlineItem[],
  enabled: OutlineCategory[],
  query: string,
  titleFor: (item: OutlineItem) => string,
): OutlineItem[] {
  return items.filter(i => enabled.includes(i.category) && matchesOutlineQuery(i, titleFor(i), query))
}

// nearestOutlineKey picks the current-position item for a viewport anchor
// (original render row at the viewport center):
//   1. an item whose [line, lineEnd] contains the anchor — narrowest range wins;
//   2. otherwise the item with the smallest |line - anchor|;
//   3. ties prefer the earlier event;
//   4. no items → null.
// Items and anchor share one unit (original render rows); no mixed-unit math.
export function nearestOutlineKey(items: OutlineItem[], anchor: number): string | null {
  if (items.length === 0) return null
  let best: OutlineItem | null = null
  let bestSpan = Infinity
  for (const i of items) {
    const end = i.lineEnd ?? i.line
    if (anchor >= i.line && anchor <= end) {
      const span = end - i.line
      if (span < bestSpan) {
        best = i
        bestSpan = span
      }
    }
  }
  if (best) return best.key

  // Binary-search the nearest by start line (items are line-ordered).
  let lo = 0
  let hi = items.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (items[mid].line < anchor) lo = mid + 1
    else hi = mid
  }
  // lo is the first item with line >= anchor; the previous item is the other
  // candidate. Equal distance prefers the previous (earlier) event.
  const after = items[lo]
  const before = lo > 0 ? items[lo - 1] : null
  if (!before) return after.key
  const dBefore = anchor - before.line
  const dAfter = after.line - anchor
  return dBefore <= dAfter ? before.key : after.key
}

export type CurrentVisibility = 'visible' | 'hidden_by_category' | 'hidden_by_search'

// currentHiddenReason explains where the current-position item went under the
// active filters. Callers must compute the current key against ALL items
// first, then ask this — never filter first, or disabling a category would
// silently retarget "current" to a different event.
export function currentHiddenReason(
  currentKey: string | null,
  items: OutlineItem[],
  enabled: OutlineCategory[],
  query: string,
  titleFor: (item: OutlineItem) => string,
): 'hidden_by_category' | 'hidden_by_search' | null {
  if (!currentKey) return null
  const item = items.find(i => i.key === currentKey)
  if (!item) return null
  if (!enabled.includes(item.category)) return 'hidden_by_category'
  if (!matchesOutlineQuery(item, titleFor(item), query)) return 'hidden_by_search'
  return null
}

// loadOutlineCategories / persistOutlineCategories: user filter choice,
// independent localStorage key, all-four-on fallback for missing/corrupt
// state (including legacy formats).
const CATEGORIES_STORAGE_KEY = 'si-outline-categories'

export function loadOutlineCategories(storage: Pick<Storage, 'getItem'>): OutlineCategory[] {
  try {
    const raw = storage.getItem(CATEGORIES_STORAGE_KEY)
    if (raw === null) return [...OUTLINE_CATEGORIES]
    const parsed: unknown = JSON.parse(raw)
    if (Array.isArray(parsed)) {
      const valid = parsed.filter((c): c is OutlineCategory =>
        typeof c === 'string' && (OUTLINE_CATEGORIES as string[]).includes(c))
      if (valid.length > 0) return valid
    }
  } catch { /* corrupt local state falls back to defaults */ }
  return [...OUTLINE_CATEGORIES]
}

export function persistOutlineCategories(storage: Pick<Storage, 'setItem'>, cats: OutlineCategory[]): void {
  try {
    storage.setItem(CATEGORIES_STORAGE_KEY, JSON.stringify(cats))
  } catch { /* persistence is best-effort */ }
}

export const OUTLINE_CATEGORIES_KEY = CATEGORIES_STORAGE_KEY

// Re-export the payload type for component props without a second import path.
export type { OutlinePayload }
