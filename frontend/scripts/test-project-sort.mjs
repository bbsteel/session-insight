import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join(
  path.dirname(new URL(import.meta.url).pathname),
  '..',
  '.runtime',
  'project-sort-test',
  'projectSort.js',
)
const {
  compareProjects,
  defaultDirForKey,
  DEFAULT_PROJECT_SORT,
  isProjectSortDir,
  isProjectSortKey,
  sortProjects,
} = await import(pathToFileURL(compiledModule).href)

// Defaults preserve previous list behavior (most sessions first).
assert.deepEqual(DEFAULT_PROJECT_SORT, { key: 'sessions', dir: 'desc' })
assert.equal(defaultDirForKey('name'), 'asc')
assert.equal(defaultDirForKey('sessions'), 'desc')
assert.equal(defaultDirForKey('recent'), 'desc')
assert.equal(isProjectSortKey('name'), true)
assert.equal(isProjectSortKey('bogus'), false)
assert.equal(isProjectSortDir('asc'), true)
assert.equal(isProjectSortDir('up'), false)

const projects = [
  { name: 'zeta', session_count: 2, last_active: '2026-08-01T10:00:00Z' },
  { name: 'alpha', session_count: 5, last_active: '2026-07-01T10:00:00Z' },
  { name: 'beta', session_count: 5, last_active: '2026-08-10T10:00:00Z' },
  { name: 'gamma', session_count: 1, last_active: '2026-06-01T10:00:00Z' },
]

// Name ascending / descending
assert.deepEqual(
  sortProjects(projects, 'name', 'asc').map(p => p.name),
  ['alpha', 'beta', 'gamma', 'zeta'],
)
assert.deepEqual(
  sortProjects(projects, 'name', 'desc').map(p => p.name),
  ['zeta', 'gamma', 'beta', 'alpha'],
)

// Sessions: desc is previous default; ties break by name asc
assert.deepEqual(
  sortProjects(projects, 'sessions', 'desc').map(p => p.name),
  ['alpha', 'beta', 'zeta', 'gamma'],
)
assert.deepEqual(
  sortProjects(projects, 'sessions', 'asc').map(p => p.name),
  ['gamma', 'zeta', 'alpha', 'beta'],
)

// Recent activity
assert.deepEqual(
  sortProjects(projects, 'recent', 'desc').map(p => p.name),
  ['beta', 'zeta', 'alpha', 'gamma'],
)
assert.deepEqual(
  sortProjects(projects, 'recent', 'asc').map(p => p.name),
  ['gamma', 'alpha', 'zeta', 'beta'],
)

// Empty / invalid timestamps sort as oldest (0)
const withMissing = [
  { name: 'old', session_count: 1, last_active: '2026-01-01T00:00:00Z' },
  { name: 'unknown', session_count: 1, last_active: '' },
  { name: 'bad', session_count: 1, last_active: 'not-a-date' },
]
assert.deepEqual(
  sortProjects(withMissing, 'recent', 'desc').map(p => p.name),
  ['old', 'bad', 'unknown'],
)

// compareProjects secondary name tie-break is stable when primary equal
assert.ok(compareProjects(
  { name: 'a', session_count: 1, last_active: '' },
  { name: 'b', session_count: 1, last_active: '' },
  'sessions',
  'desc',
) < 0)

// sortProjects does not mutate input
const frozen = Object.freeze([...projects])
const out = sortProjects(frozen, 'name', 'asc')
assert.notEqual(out, frozen)
assert.equal(frozen[0].name, 'zeta')

console.log('project-sort tests passed')
