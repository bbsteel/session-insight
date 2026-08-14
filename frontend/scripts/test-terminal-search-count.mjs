import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = '/tmp/session-insight-terminal-search-count'
fs.rmSync(outDir, { recursive: true, force: true })
const tsc = spawnSync(
  'npx',
  ['tsc', 'src/terminalSearchCount.ts', '--outDir', outDir, '--module', 'ES2020', '--target', 'ES2020', '--moduleResolution', 'node', '--skipLibCheck'],
  { cwd: root, encoding: 'utf8' },
)
if (tsc.status !== 0) {
  console.error(tsc.stdout, tsc.stderr)
  process.exit(tsc.status ?? 1)
}

const require = createRequire(import.meta.url)
// Dynamic import compiled module
const mod = await import(path.join(outDir, 'terminalSearchCount.js'))
const { matchOffsetsInLine, matchSpansInLine, selectionTextMatchesQuery, offsetToBufferPos } = mod

const plain = { caseSensitive: false, wholeWord: false, regex: false }

// Plain substring
assert.deepEqual(matchOffsetsInLine('foo bar foo', 'foo', plain), [0, 8])
// Case
assert.deepEqual(matchOffsetsInLine('Foo foo', 'foo', { caseSensitive: true, wholeWord: false, regex: false }), [4])
// Whole word
assert.deepEqual(matchOffsetsInLine('a aa a', 'a', { caseSensitive: false, wholeWord: true, regex: false }), [0, 5])
// Regex
assert.deepEqual(matchOffsetsInLine('a1 a2 b1', 'a\\d', { caseSensitive: false, wholeWord: false, regex: true }), [0, 3])
// Offset map
assert.deepEqual(offsetToBufferPos(10, 125, 80), { row: 11, col: 45 })
// Spans include match length
assert.deepEqual(matchSpansInLine('foo bar foo', 'foo', plain), [
  { offset: 0, length: 3 },
  { offset: 8, length: 3 },
])
// Backspace on a long URL must still match the current selection
const url = 'https://github.com/bbsteel/session-insight/pull/13'
assert.equal(selectionTextMatchesQuery(url, url, plain), true)
assert.equal(selectionTextMatchesQuery(url, url.slice(0, -1), plain), true)
assert.equal(selectionTextMatchesQuery(url, 'nope', plain), false)

console.log('terminal search count unit tests passed')
