import assert from 'node:assert/strict'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const compiledModule = path.join('/tmp', 'session-insight-terminalInteractionGeometry.mjs')

const {
  getBufferLineFromPointer,
  getBufferLineFromXtermCoords,
  getMarkerOffsetForBufferLine,
  parseEditHeaderLine,
  terminalCellColumnToTextOffset,
  terminalTextOffsetToCellColumn,
  terminalTextRangeToCellRange,
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

const cjkLineCells = [
  { chars: '前', width: 2 },
  { chars: '', width: 0 },
  { chars: ' ', width: 1 },
  { chars: 'P', width: 1 },
  { chars: 'R', width: 1 },
  { chars: ' ', width: 1 },
]
const cjkLine = {
  getCell(column) {
    const cell = cjkLineCells[column]
    return cell ? { getChars: () => cell.chars, getWidth: () => cell.width } : undefined
  },
}

assert.equal(terminalTextOffsetToCellColumn(cjkLine, 1, 6), 2)
assert.equal(terminalCellColumnToTextOffset(cjkLine, 3, 6), 2)
assert.equal(terminalCellColumnToTextOffset(cjkLine, 5, 6), 4)
assert.deepEqual(
  terminalTextRangeToCellRange(cjkLine, { start: 2, end: 4 }, '前 PR '.length, 6),
  { start: 3, end: 5 },
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
