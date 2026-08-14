import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const compiledDir = path.join('/tmp', 'session-insight-terminal-scrollback')
const {
  DEFAULT_TERMINAL_SCROLLBACK,
  estimateRenderedLineCount,
  ensureTerminalScrollback,
  nextTerminalScrollback,
  requiredTerminalScrollback,
  terminalBufferCapacity,
} = await import(pathToFileURL(path.join(compiledDir, 'terminalScrollback.js')).href)

assert.equal(DEFAULT_TERMINAL_SCROLLBACK, 20_000)
assert.equal(terminalBufferCapacity(20_000, 50), 20_050)
assert.equal(requiredTerminalScrollback(1_000, 50), DEFAULT_TERMINAL_SCROLLBACK)
assert.equal(requiredTerminalScrollback(43_296, 50), 43_246)

assert.equal(nextTerminalScrollback(20_000, 1_000, 50), 20_000)
assert.equal(nextTerminalScrollback(20_000, 43_296, 50), 43_246)
assert.equal(nextTerminalScrollback(20_000, 25_000, 50), 40_000)
assert.equal(nextTerminalScrollback(40_000, 43_296, 50), 80_000)
assert.equal(nextTerminalScrollback(50_000, 43_296, 50), 50_000)

assert.equal(estimateRenderedLineCount('', 80), 0)
assert.equal(estimateRenderedLineCount('hello', 80), 1)
assert.equal(estimateRenderedLineCount('hello\nworld', 80), 2)
assert.equal(estimateRenderedLineCount('hello\n', 80), 2)
assert.equal(estimateRenderedLineCount('hello\rworld', 80), 1)
assert.equal(estimateRenderedLineCount('hello\r\nworld', 80), 2)
assert.equal(estimateRenderedLineCount('abcd', 2), 2)
assert.equal(estimateRenderedLineCount('abcdef', 2), 3)

const term = { rows: 50, options: { scrollback: 20_000 } }
assert.equal(ensureTerminalScrollback(term, 1_000), 20_000)
assert.equal(term.options.scrollback, 20_000)
assert.equal(ensureTerminalScrollback(term, 25_000), 40_000)
assert.equal(term.options.scrollback, 40_000)
assert.equal(ensureTerminalScrollback(term, 43_296), 80_000)
assert.equal(term.options.scrollback, 80_000)
assert.ok(terminalBufferCapacity(term.options.scrollback, term.rows) >= 43_296)

console.log('terminal scrollback tests passed')
