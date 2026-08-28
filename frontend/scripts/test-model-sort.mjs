import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join(
  path.dirname(new URL(import.meta.url).pathname),
  '..',
  '.runtime',
  'model-sort-test',
  'modelSort.js',
)
const {
  compareModels,
  defaultOrderForModelKey,
  DEFAULT_MODEL_SORT,
  isModelSortKey,
  sortModels,
} = await import(pathToFileURL(compiledModule).href)

const entry = (id, label, session_count, last_active = '') => ({ id, label, session_count, last_active })

// Defaults: most-used models first — the filter serves high-frequency picks.
assert.deepEqual(DEFAULT_MODEL_SORT, { key: 'sessions', order: 'desc' })
assert.equal(defaultOrderForModelKey('name'), 'asc')
assert.equal(defaultOrderForModelKey('sessions'), 'desc')
assert.equal(defaultOrderForModelKey('recent'), 'desc')
assert.equal(isModelSortKey('recent'), true)
assert.equal(isModelSortKey('bogus'), false)

const models = [
  entry('zeta', 'zeta', 2, '2026-08-01T10:00:00Z'),
  entry('alpha', 'alpha', 5, '2026-07-01T10:00:00Z'),
  entry('beta', 'Beta', 5, '2026-08-10T10:00:00Z'),
  entry('gamma', 'gamma', 1, '2026-06-01T10:00:00Z'),
]

// Name ordering uses the display label, case-insensitively
assert.deepEqual(
  sortModels(models, 'name', 'asc').map(m => m.id),
  ['alpha', 'beta', 'gamma', 'zeta'],
)
assert.deepEqual(
  sortModels(models, 'name', 'desc').map(m => m.id),
  ['zeta', 'gamma', 'beta', 'alpha'],
)

// Sessions: ties break by label ascending
assert.deepEqual(
  sortModels(models, 'sessions', 'desc').map(m => m.id),
  ['alpha', 'beta', 'zeta', 'gamma'],
)

// Recent activity
assert.deepEqual(
  sortModels(models, 'recent', 'desc').map(m => m.id),
  ['beta', 'zeta', 'alpha', 'gamma'],
)
assert.deepEqual(
  sortModels(models, 'recent', 'asc').map(m => m.id),
  ['gamma', 'alpha', 'zeta', 'beta'],
)

// The catch-all 'Other' bucket sinks to the end regardless of key or direction
const withOther = [
  entry('Other', 'Other', 999, '2026-08-20T10:00:00Z'),
  entry('alpha', 'alpha', 1, '2026-01-01T10:00:00Z'),
  entry('beta', 'beta', 2, '2026-02-01T10:00:00Z'),
]
for (const key of ['name', 'sessions', 'recent']) {
  for (const order of ['asc', 'desc']) {
    const ids = sortModels(withOther, key, order).map(m => m.id)
    assert.equal(ids[ids.length - 1], 'Other', `${key}/${order} should keep Other last`)
  }
}

// Empty / invalid timestamps sort as oldest (0)
const withMissing = [
  entry('old', 'old', 1, '2026-01-01T00:00:00Z'),
  entry('unknown', 'unknown', 1, ''),
  entry('bad', 'bad', 1, 'not-a-date'),
]
assert.deepEqual(
  sortModels(withMissing, 'recent', 'desc').map(m => m.id),
  ['old', 'bad', 'unknown'],
)

// compareModels label tie-break is stable when primary equal
assert.ok(compareModels(
  entry('a', 'a', 1),
  entry('b', 'b', 1),
  'sessions',
  'desc',
) < 0)

// sortModels does not mutate input
const frozen = Object.freeze([...models])
const out = sortModels(frozen, 'name', 'asc')
assert.notEqual(out, frozen)
assert.equal(frozen[0].id, 'zeta')

console.log('model-sort tests passed')
