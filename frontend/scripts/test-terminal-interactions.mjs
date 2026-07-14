import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join('/tmp', 'session-insight-terminalInteractionGeometry.mjs')

const {
  getBufferLineFromPointer,
  getBufferLineFromXtermCoords,
  getMarkerOffsetForBufferLine,
  parseEditHeaderLine,
} = await import(pathToFileURL(compiledModule).href)

assert.equal(
  getBufferLineFromPointer({
    clientY: 146,
    screenTop: 40,
    cellHeight: 16,
    viewportY: 120,
    rowCount: 24,
  }),
  126,
)

assert.equal(
  getBufferLineFromPointer({
    clientY: 36,
    screenTop: 40,
    cellHeight: 16,
    viewportY: 120,
    rowCount: 24,
  }),
  null,
)

assert.equal(
  getBufferLineFromPointer({
    clientY: 425,
    screenTop: 40,
    cellHeight: 16,
    viewportY: 120,
    rowCount: 24,
  }),
  null,
)

assert.equal(getBufferLineFromXtermCoords([1, 1], 120), 120)
assert.equal(getBufferLineFromXtermCoords([1, 7], 120), 126)
assert.equal(getBufferLineFromXtermCoords(undefined, 120), null)

assert.equal(
  getMarkerOffsetForBufferLine({
    bufferLine: 126,
    baseY: 140,
    cursorY: 3,
  }),
  -17,
)

assert.deepEqual(
  parseEditHeaderLine('╔══ ✏️ Edit: /home/user/.claude/skills/example/SKILL.md'),
  {
    toolName: 'Edit',
    filePath: '/home/user/.claude/skills/example/SKILL.md',
  },
)

assert.deepEqual(
  parseEditHeaderLine('╔══ ✏️ Edit: /tmp/example.md ═════════════════════════════════════'),
  {
    toolName: 'Edit',
    filePath: '/tmp/example.md',
  },
)
