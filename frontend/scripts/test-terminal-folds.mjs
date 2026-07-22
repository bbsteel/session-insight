import { foldsFromPositions, composeFoldView, foldKeysInTurn } from '/tmp/session-insight-terminal-folds/terminalFolds.js'

let failures = 0
function assertEq(actual, expected, label) {
  const a = JSON.stringify(actual)
  const e = JSON.stringify(expected)
  if (a !== e) {
    failures++
    console.error(`FAIL ${label}: got ${a}, want ${e}`)
  } else {
    console.log(`ok   ${label}`)
  }
}

// Fixture: 10 logical lines, no soft wraps (display == logical).
//  0: user text
//  1: ▼ Tools (1/1)   ← fold A header, body rows 2..4
//  2..4: tool box
//  5: text
//  6: ▼ Tools (2/2)   ← fold B header, body rows 7..8
//  7..8: tool box
//  9: tail text
const lines = [
  'user', '▼ Tools (1/1)', 'boxA1', 'boxA2', 'boxA3',
  'text', '▼ Tools (2/2)', 'boxB1', 'boxB2', 'tail',
]
const ansi = lines.join('\n')

const positions = [
  { kind: 'fold', position_key: 'fold:0:1', turn_index: 0, line_start: 1, label: 'Tools (1/1)',
    payload: { display_start: 2, display_end: 5, logical_start: 2, logical_end: 5, header_logical: 1 } },
  { kind: 'fold', position_key: 'fold:0:6', turn_index: 0, line_start: 6, label: 'Tools (2/2)',
    payload: { display_start: 7, display_end: 9, logical_start: 7, logical_end: 9, header_logical: 6 } },
  { kind: 'turn', position_key: 'turn:0:0', turn_index: 0, line_start: 0, label: 'Turn 0' },
]

const folds = foldsFromPositions(positions)
assertEq(folds.length, 2, 'foldsFromPositions extracts fold kinds only')

const rollbackLines = ['active', '▼ ↩ 已回滚 2 个 turn', 'old1', 'old2', 'tail']
const rollbackFolds = foldsFromPositions([{
  kind: 'fold', position_key: 'rollback:0:1', turn_index: 0, line_start: 1, label: '已回滚 2 turns',
  payload: { level: 'rollback', display_start: 2, display_end: 4, logical_start: 2, logical_end: 4, header_logical: 1 },
}])
assertEq(rollbackFolds[0]?.level, 'rollback', 'rollback fold level preserved')
const rollbackCollapsed = composeFoldView(rollbackLines.join('\n'), rollbackFolds, new Set(['rollback:0:1']), '行')
assertEq(rollbackCollapsed.text.split('\n'), [
  'active', '▶ ↩ 已回滚 2 个 turn\x1b[2m (2 行)\x1b[0m', 'tail',
], 'rollback fold hides abandoned turns')

// Nothing collapsed → identity.
const open = composeFoldView(ansi, folds, new Set())
assertEq(open.text === ansi, true, 'no collapse → text unchanged')
assertEq(open.hiddenTotal, 0, 'no collapse → 0 hidden')
assertEq(open.toDisplay(9), 9, 'identity toDisplay')
assertEq(open.toOriginal(9), 9, 'identity toOriginal')

// Collapse fold A (body rows 2..4 hidden, 3 rows).
const a = composeFoldView(ansi, folds, new Set(['fold:0:1']), '行')
assertEq(a.hiddenTotal, 3, 'collapse A hides 3 rows')
assertEq(a.text.split('\n'), ['user', '▶ Tools (1/1)\x1b[2m (3 行)\x1b[0m', 'text', '▼ Tools (2/2)', 'boxB1', 'boxB2', 'tail'], 'composed text A')
assertEq(a.toDisplay(0), 0, 'row before fold unchanged')
assertEq(a.toDisplay(1), 1, 'header row unchanged')
assertEq(a.toDisplay(3), 1, 'hidden body row maps to header')
assertEq(a.toDisplay(5), 2, 'row after fold shifts up by 3')
assertEq(a.toDisplay(6), 3, 'fold B header shifts up by 3')
assertEq(a.toOriginal(2), 5, 'toOriginal inverts shift')
assertEq(a.toOriginal(1), 1, 'toOriginal header identity')
assertEq(a.toOriginal(6), 9, 'toOriginal tail')
assertEq(composeFoldView(ansi, folds, new Set(['fold:0:1']), 'lines').text.includes('(3 lines)'), true, 'fold badge uses supplied locale unit')

// Collapse both folds.
const ab = composeFoldView(ansi, folds, new Set(['fold:0:1', 'fold:0:6']), '行')
assertEq(ab.hiddenTotal, 5, 'collapse A+B hides 5 rows')
assertEq(ab.text.split('\n'), ['user', '▶ Tools (1/1)\x1b[2m (3 行)\x1b[0m', 'text', '▶ Tools (2/2)\x1b[2m (2 行)\x1b[0m', 'tail'], 'composed text A+B')
assertEq(ab.toDisplay(9), 4, 'tail row after both folds')
assertEq(ab.toDisplay(8), 3, 'hidden B body maps to B header display row')
assertEq(ab.toOriginal(4), 9, 'toOriginal tail both')
assertEq(ab.toOriginal(3), 6, 'toOriginal B header')

// foldKeysInTurn: turn banners at original display rows 0 and 6 → turn 0
// spans [0,6) holding fold A, turn 1 spans [6,∞) holding fold B.
const turnStarts = [0, 6]
assertEq(foldKeysInTurn(folds, turnStarts, 3), ['fold:0:1'], 'row in turn 0 → fold A')
assertEq(foldKeysInTurn(folds, turnStarts, 0), ['fold:0:1'], 'turn banner row itself → fold A')
assertEq(foldKeysInTurn(folds, turnStarts, 5), ['fold:0:1'], 'last row of turn 0 → fold A')
assertEq(foldKeysInTurn(folds, turnStarts, 6), ['fold:0:6'], 'turn 1 banner row → fold B')
assertEq(foldKeysInTurn(folds, turnStarts, 9), ['fold:0:6'], 'tail row in last turn → fold B')
assertEq(foldKeysInTurn(folds, [2, 6], 1), [], 'row before first banner → no turn, no folds')
assertEq(foldKeysInTurn(folds, [], 3), [], 'no turn banners → no folds')
assertEq(foldKeysInTurn([], turnStarts, 3), [], 'no folds → empty')
assertEq(foldKeysInTurn(folds, [0], 9), ['fold:0:1', 'fold:0:6'], 'single turn spans everything → both folds')

if (failures > 0) {
  console.error(`${failures} failure(s)`)
  process.exit(1)
}
console.log('all terminal-folds tests passed')
