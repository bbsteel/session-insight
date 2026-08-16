import { captureViewportAnchor, resolveViewportAnchor } from '/tmp/session-insight-viewport-anchor/viewportAnchor.js'
import { foldsFromPositions, composeFoldView } from '/tmp/session-insight-viewport-anchor/terminalFolds.js'

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

const identity = (n) => n

// --- capture: binary search over composed logical buffer rows ---
// Soft-wrapped layout: line 0 → row 0, line 1 → rows 1-2, line 2 → rows 3-4,
// line 3 → row 5.
const wrappedRows = [0, 1, 3, 5]
assertEq(captureViewportAnchor(wrappedRows, 0, identity), { originalLogical: 0, offsetRows: 0 }, 'capture: very top row')
assertEq(captureViewportAnchor(wrappedRows, 2, identity), { originalLogical: 1, offsetRows: 1 }, 'capture: mid wrap group')
assertEq(captureViewportAnchor(wrappedRows, 4, identity), { originalLogical: 2, offsetRows: 1 }, 'capture: last row of a wrap group')
assertEq(captureViewportAnchor(wrappedRows, 5, identity), { originalLogical: 3, offsetRows: 0 }, 'capture: exact line start')

// --- resolve: offset clamping when a re-wrap shortened the line ---
// Same anchor, but line 1 now wraps into 2 rows ([1,2]) instead of 3:
// offset 2 would overshoot into line 2's rows, so it clamps to row 2.
assertEq(
  resolveViewportAnchor({ originalLogical: 1, offsetRows: 2 }, [0, 1, 3, 5], 6, identity),
  2,
  'resolve: wrap offset clamps to the line extent after a cols change',
)
assertEq(
  resolveViewportAnchor({ originalLogical: 1, offsetRows: 1 }, [0, 1, 3, 5], 6, identity),
  2,
  'resolve: in-extent offset survives unchanged',
)
// Anchor on the last logical line: the extent runs to the buffer end.
assertEq(
  resolveViewportAnchor({ originalLogical: 3, offsetRows: 9 }, [0, 1, 3, 5], 6, identity),
  5,
  'resolve: last line clamps to buffer length',
)
assertEq(
  resolveViewportAnchor({ originalLogical: 99, offsetRows: 0 }, [0, 1, 3, 5], 6, identity),
  5,
  'resolve: out-of-range logical line clamps to the final composed line',
)
assertEq(resolveViewportAnchor({ originalLogical: 3, offsetRows: 0 }, [], 0, identity), 0, 'resolve: empty buffer → row 0')

// --- round trip across a fold-state change (the live-growth path) ---
// Same fixture as test-terminal-folds: fold A body = original lines 2..4.
const lines = [
  'user', '▼ Tools (1/1)', 'boxA1', 'boxA2', 'boxA3',
  'text', '▼ Tools (2/2)', 'boxB1', 'boxB2', 'tail',
]
const folds = foldsFromPositions([
  { kind: 'fold', position_key: 'fold:0:1', turn_index: 0, line_start: 1, label: 'Tools (1/1)',
    payload: { display_start: 2, display_end: 5, logical_start: 2, logical_end: 5, header_logical: 1 } },
  { kind: 'fold', position_key: 'fold:0:6', turn_index: 0, line_start: 6, label: 'Tools (2/2)',
    payload: { display_start: 7, display_end: 9, logical_start: 7, logical_end: 9, header_logical: 6 } },
])
const ansi = lines.join('\n')
// No soft wraps in this fixture: composed buffer rows are identity.
const openRows = lines.map((_, i) => i)
const openView = composeFoldView(ansi, folds, new Set())

// User reads boxB1 (original line 7) with everything expanded.
const captured = captureViewportAnchor(openRows, 7, (c) => openView.toOriginalLogical(c))
assertEq(captured, { originalLogical: 7, offsetRows: 0 }, 'round trip: capture in open view')

// Live growth collapses fold A; the same content now sits at composed row 4.
const collapsedView = composeFoldView(ansi, folds, new Set(['fold:0:1']), '行')
const collapsedRows = collapsedView.text.split('\n').map((_, i) => i)
assertEq(
  resolveViewportAnchor(captured, collapsedRows, collapsedRows.length, (o) => collapsedView.toComposedLogical(o)),
  4,
  'round trip: reading position survives a fold collapsing above it',
)

// An anchor inside a body that gets collapsed lands on the fold's header.
const bodyAnchor = captureViewportAnchor(openRows, 3, (c) => openView.toOriginalLogical(c))
assertEq(
  resolveViewportAnchor(bodyAnchor, collapsedRows, collapsedRows.length, (o) => collapsedView.toComposedLogical(o)),
  1,
  'round trip: anchor inside a freshly collapsed body lands on its header',
)

if (failures > 0) {
  console.error(`${failures} failure(s)`)
  process.exit(1)
}
console.log('all viewport-anchor tests passed')
