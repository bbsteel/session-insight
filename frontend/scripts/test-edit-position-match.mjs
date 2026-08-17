import { editPositionsSorted, editPositionForIndex, editIndexForRow } from '/tmp/session-insight-edit-position-match/editPositionMatch.js'

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

const edit = (id, file = 'a.ts') => ({ turn_index: 0, file_path: file, old_string: 'o', new_string: 'n', tool_call_id: id })
const pos = (id, lineStart) => ({ kind: 'edit', position_key: `edit:0:${lineStart}`, turn_index: 0, line_start: lineStart, label: 'x', payload: { tool_call_id: id } })

// Render order (positions, by line_start) differs from raw event order
// (edits): a nested Chrys edit is re-paired before an earlier sibling.
const edits = [edit('call-A'), edit('call-B'), edit('call-C')]
const positions = editPositionsSorted([
  pos('call-C', 100),
  pos('call-A', 500),
  pos('call-B', 900),
])

assertEq(editPositionForIndex(edits, positions, 0)?.line_start, 500, 'edit 0 (call-A) resolves by id despite reorder')
assertEq(editPositionForIndex(edits, positions, 1)?.line_start, 900, 'edit 1 (call-B) resolves by id despite reorder')
assertEq(editPositionForIndex(edits, positions, 2)?.line_start, 100, 'edit 2 (call-C) resolves by id despite reorder')

assertEq(editIndexForRow(edits, positions, 100), 2, 'row 100 → edit 2 (call-C)')
assertEq(editIndexForRow(edits, positions, 500), 0, 'row 500 → edit 0 (call-A)')
assertEq(editIndexForRow(edits, positions, 4242), -1, 'row without an edit position → -1')

// apply_patch: several EditCalls share one invocation id; per-id ordinal
// disambiguates which file of the patch each side refers to.
const patchEdits = [edit('patch-1', 'a.ts'), edit('patch-1', 'b.ts'), edit('call-Z', 'z.ts')]
const patchPositions = editPositionsSorted([
  pos('patch-1', 200), // b.ts landed earlier in render order
  pos('patch-1', 300), // a.ts
  pos('call-Z', 400),
])
assertEq(editPositionForIndex(patchEdits, patchPositions, 0)?.line_start, 200, 'multi-file patch: first edit → first same-id position')
assertEq(editPositionForIndex(patchEdits, patchPositions, 1)?.line_start, 300, 'multi-file patch: second edit → second same-id position')
assertEq(editPositionForIndex(patchEdits, patchPositions, 2)?.line_start, 400, 'multi-file patch: unrelated edit still by id')
assertEq(editIndexForRow(patchEdits, patchPositions, 300), 1, 'row 300 → second same-id edit')
assertEq(editIndexForRow(patchEdits, patchPositions, 200), 0, 'row 200 → first same-id edit')

// No ids at all (older backend payload): both directions fall back to plain
// indexing by ascending row.
const legacyEdits = [
  { turn_index: 0, file_path: 'a', old_string: 'o', new_string: 'n' },
  { turn_index: 0, file_path: 'b', old_string: 'o', new_string: 'n' },
]
const legacyPositions = editPositionsSorted([
  { kind: 'edit', position_key: 'e:0:10', turn_index: 0, line_start: 10, label: 'x' },
  { kind: 'edit', position_key: 'e:0:20', turn_index: 0, line_start: 20, label: 'x' },
])
assertEq(editPositionForIndex(legacyEdits, legacyPositions, 1)?.line_start, 20, 'no ids: index fallback forward')
assertEq(editIndexForRow(legacyEdits, legacyPositions, 10), 0, 'no ids: index fallback reverse')

assertEq(editPositionForIndex(edits, positions, 99), undefined, 'out-of-range index → undefined')

if (failures > 0) {
  console.error(`${failures} failure(s)`)
  process.exit(1)
}
console.log('all edit-position-match tests passed')
