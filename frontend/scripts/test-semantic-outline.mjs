// Unit tests for src/semanticOutline.ts (pure outline logic).
// Run via: npm run test:semantic-outline (compiles the module first).
import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const mod = await import(pathToFileURL('/tmp/session-insight-semantic-outline/semanticOutline.js').href)
const {
  toOutlineItem,
  outlineItemsFromPositions,
  countByCategory,
  matchesOutlineQuery,
  filterOutlineItems,
  nearestOutlineKey,
  currentHiddenReason,
  loadOutlineCategories,
  persistOutlineCategories,
} = mod

function pos(over) {
  return {
    kind: 'outline',
    position_key: over.key ?? 'outline:anomaly:tool_failed:0:c1:0',
    turn_index: over.turn ?? 0,
    line_start: over.line ?? 10,
    line_end: over.lineEnd,
    label: over.label ?? '',
    severity: over.severity ?? '',
    payload: {
      category: over.category ?? 'anomaly',
      code: over.code ?? 'tool_failed',
      precision: over.precision ?? 'exact',
      ...(over.payloadExtra ?? {}),
    },
  }
}

// --- payload validation ------------------------------------------------
assert.equal(toOutlineItem({ ...pos({}), kind: 'tool' }), null, 'non-outline kind rejected')
assert.equal(toOutlineItem({ ...pos({}), payload: undefined }), null, 'missing payload rejected')
assert.equal(
  toOutlineItem(pos({ payloadExtra: { category: 'bogus' } })),
  null,
  'unknown category rejected',
)

{
  const item = toOutlineItem(pos({ key: 'k1', category: 'key_result', code: 'tests_passed', line: 42, payloadExtra: { summary: 'go test ./...', tool_name: 'Bash', ts_ms: 123, logical_start: 40 } }))
  assert.equal(item.key, 'k1')
  assert.equal(item.category, 'key_result')
  assert.equal(item.code, 'tests_passed')
  assert.equal(item.line, 42)
  assert.equal(item.summary, 'go test ./...')
  assert.equal(item.toolName, 'Bash')
  assert.equal(item.tsMs, 123)
}

// Unknown codes degrade to '_other' but stay countable/jumpable.
{
  const item = toOutlineItem(pos({ code: 'brand_new_code_from_future' }))
  assert.equal(item.code, '_other', 'unknown code degrades to _other')
  assert.notEqual(item, null)
}

// precision: anything not 'estimated' is exact.
assert.equal(toOutlineItem(pos({ precision: 'estimated' })).precision, 'estimated')
assert.equal(toOutlineItem(pos({ precision: 'bogus' })).precision, 'exact')

// --- counts -------------------------------------------------------------
const items = outlineItemsFromPositions([
  pos({ key: 'a1', category: 'anomaly', line: 10 }),
  pos({ key: 'a2', category: 'anomaly', line: 20 }),
  pos({ key: 'f1', category: 'file_change', code: 'file_modified', line: 30, payloadExtra: { file_path: 'x.go' } }),
  pos({ key: 'k1', category: 'key_result', code: 'tests_passed', line: 40 }),
])
assert.equal(items.length, 4)

{
  const visible = items.filter(i => i.key !== 'a2')
  const counts = countByCategory(items, visible)
  assert.deepEqual(counts.anomaly, { visible: 1, total: 2 }, 'category totals ignore the toggle')
  assert.deepEqual(counts.context, { visible: 0, total: 0 })
  assert.deepEqual(counts.key_result, { visible: 1, total: 1 })
}

// --- text search ---------------------------------------------------------
const titleFor = () => 'Tool failed'
assert.ok(matchesOutlineQuery(items[0], 'Tool failed', 'tool_fa'), 'matches stable code')
assert.ok(matchesOutlineQuery(items[0], 'Tool failed', 'failed'), 'matches localized title')
assert.ok(matchesOutlineQuery(items[2], 'File modified', 'x.go'), 'matches path')
assert.ok(!matchesOutlineQuery(items[0], 'Tool failed', 'zzz'), 'no match')
assert.ok(matchesOutlineQuery(items[0], 'Tool failed', '  '), 'blank query matches all')

{
  const visible = filterOutlineItems(items, ['anomaly'], '', titleFor)
  assert.deepEqual(visible.map(i => i.key), ['a1', 'a2'])
  const searched = filterOutlineItems(items, ['anomaly', 'file_change'], 'x.go', titleFor)
  assert.deepEqual(searched.map(i => i.key), ['f1'])
}

// --- nearest / current position ------------------------------------------
{
  const spaced = [
    { key: 'e1', line: 100, lineEnd: null },
    { key: 'e2', line: 200, lineEnd: 210 },
    { key: 'e3', line: 300, lineEnd: null },
  ]
  assert.equal(nearestOutlineKey(spaced, 50), 'e1', 'before all → first')
  assert.equal(nearestOutlineKey(spaced, 150), 'e1', 'equal distance prefers previous')
  assert.equal(nearestOutlineKey(spaced, 151), 'e2', 'closer to next')
  assert.equal(nearestOutlineKey(spaced, 205), 'e2', 'anchor inside range')
  assert.equal(nearestOutlineKey(spaced, 999), 'e3', 'after all → last')
  assert.equal(nearestOutlineKey([], 10), null, 'no items → null')

  // Containment prefers the narrowest covering range.
  const nested = [
    { key: 'wide', line: 100, lineEnd: 400 },
    { key: 'narrow', line: 200, lineEnd: 210 },
  ]
  assert.equal(nearestOutlineKey(nested, 205), 'narrow')
}

// --- hidden-current reasoning ----------------------------------------------
{
  const all = items
  const cur = 'f1'
  assert.equal(currentHiddenReason(cur, all, ['anomaly'], '', titleFor), 'hidden_by_category')
  assert.equal(
    currentHiddenReason(cur, all, ['anomaly', 'file_change'], 'nomatch', titleFor),
    'hidden_by_search',
  )
  assert.equal(currentHiddenReason(cur, all, ['file_change'], 'x.go', titleFor), null, 'visible → no reason')
  assert.equal(currentHiddenReason(null, all, [], 'x', titleFor), null, 'no current → no reason')
}

// --- localStorage resilience ------------------------------------------------
function memStorage(initial) {
  let store = { ...initial }
  return {
    getItem: k => (k in store ? store[k] : null),
    setItem: (k, v) => { store[k] = String(v) },
  }
}
{
  assert.deepEqual(loadOutlineCategories(memStorage({})), ['anomaly', 'context', 'file_change', 'key_result'], 'missing key → all on')
  assert.deepEqual(loadOutlineCategories(memStorage({ 'si-outline-categories': 'not json' })), ['anomaly', 'context', 'file_change', 'key_result'], 'corrupt → all on')
  assert.deepEqual(loadOutlineCategories(memStorage({ 'si-outline-categories': '[]' })), [], 'explicit all-off selection is restored')
  assert.deepEqual(loadOutlineCategories(memStorage({ 'si-outline-categories': '["anomaly","bogus"]' })), ['anomaly'], 'unknown entries dropped')
  assert.deepEqual(loadOutlineCategories(memStorage({ 'si-outline-categories': '["bogus"]' })), ['anomaly', 'context', 'file_change', 'key_result'], 'fully invalid → all on')
  const s = memStorage({})
  persistOutlineCategories(s, ['context'])
  assert.deepEqual(loadOutlineCategories(s), ['context'], 'round-trip persists choice')
}

console.log('semantic-outline: all assertions passed')
