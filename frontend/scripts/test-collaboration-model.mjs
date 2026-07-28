/**
 * Durable logic tests: collaboration graph -> timeline model normalization.
 *
 * Loads the frozen Go contract golden fixtures (read-only; the goldens are
 * owned by internal/collaboration and must never be modified to suit the
 * frontend) and asserts the frontend normalization mirrors the contract's
 * canonical projection: delegation-derived hierarchy, one deterministic
 * root, the Unlinked group, quarantine behavior, preserved status/evidence,
 * and deterministic ordering.
 */

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { pathToFileURL, fileURLToPath } from 'node:url'

const core = '/tmp/session-insight-collaboration/src/collaboration'
const {
  normalizeTimelineModel,
  selectedPathIds,
  rootInvocationID,
  UNLINKED_GROUP_ID,
} = await import(pathToFileURL(`${core}/normalizeTimelineModel.js`).href)

const goldenDir = fileURLToPath(new URL('../../internal/collaboration/testdata/golden/', import.meta.url))
const load = (name) => JSON.parse(readFileSync(`${goldenDir}/${name}`, 'utf8'))

const ms = (iso) => Date.parse(iso)

// --- Identity helpers --------------------------------------------------------

assert.equal(rootInvocationID('copilot', 'collab-copilot-1'), 'copilot:collab-copilot-1:root')
// Percent-escaping matches the Go helpers (% first, then ':' and '>').
assert.equal(rootInvocationID('a%b:c>d', 's:x'), 'a%25b%3Ac%3Ed:s%3Ax:root')

// --- embedded-child (Chrys): exact two-sided anchors --------------------------

{
  const model = normalizeTimelineModel(load('embedded-child.json'))
  const rootId = 'chrys:28491d6d491e:root'
  const childId = 'chrys:28491d6d491e:child:call_sub_1'
  assert.equal(model.invocations.length, 2)
  assert.equal(model.invocations[0].id, rootId, 'root lane first')
  assert.equal(model.invocations[0].parentId, null)
  assert.equal(model.invocations[0].depth, 0)
  const child = model.invocations[1]
  assert.equal(child.id, childId)
  assert.equal(child.parentId, rootId, 'hierarchy derived from delegation')
  assert.equal(child.depth, 1)
  assert.deepEqual(child.spans, [{ startMs: ms('2026-01-02T00:00:05Z'), endMs: ms('2026-01-02T00:00:09Z') }])
  const kinds = model.relations.map((r) => r.kind).sort()
  assert.deepEqual(kinds, ['launch', 'result'])
  const launch = model.relations.find((r) => r.kind === 'launch')
  assert.equal(launch.precision, 'exact')
  assert.equal(launch.atMs, ms('2026-01-02T00:00:05Z'))
  const result = model.relations.find((r) => r.kind === 'result')
  assert.equal(result.precision, 'exact')
  assert.equal(result.atMs, ms('2026-01-02T00:00:09Z'))
  assert.equal(model.unlinkedGroupId, null)
  assert.equal(model.live, false)
  assert.equal(model.quarantinedCount, 0)
  assert.equal(child.taskSummary, null, 'task content stays out when the source records none')
}

// --- estimated-facts (Claude FIFO join): launch exact, result estimated ------

{
  const model = normalizeTimelineModel(load('estimated-facts.json'))
  const child = model.invocations[1]
  assert.equal(child.status, 'unknown', 'unknown stays first-class')
  assert.equal(child.spans.length, 0, 'no end evidence -> no closed span')
  assert.equal(child.timePrecision.state, 'estimated')
  assert.equal(child.timePrecision.reason_code, 'completion_not_recorded')
  const launch = model.relations.find((r) => r.kind === 'launch')
  const result = model.relations.find((r) => r.kind === 'result')
  assert.equal(launch.precision, 'exact')
  assert.equal(result.precision, 'estimated', 'FIFO-joined result stays estimated')
  assert.equal(child.taskSummary, 'Review the filter changes')
}

// --- interrupted: stale graph retained, unknown child status -----------------

{
  const model = normalizeTimelineModel(load('interrupted.json'))
  const child = model.invocations[1]
  assert.equal(model.invocations.length, 2, 'last valid invocations kept')
  assert.equal(child.status, 'unknown')
  assert.equal(child.hasBackingSession, true)
  assert.equal(model.relations.length, 1)
  assert.equal(model.relations[0].kind, 'launch')
  assert.equal(model.relations[0].precision, 'missing', 'missing anchors stay missing')
  assert.equal(model.relations[0].atMs, ms('2026-01-02T00:00:01Z'), 'falls back to child start')
  assert.equal(model.quarantinedCount, 0)
}

// --- lifecycle-only (Copilot): estimated content, exact timing ----------------

{
  const model = normalizeTimelineModel(load('lifecycle-only.json'))
  const child = model.invocations[1]
  assert.equal(child.contentPrecision.state, 'estimated')
  assert.equal(child.contentPrecision.reason_code, 'aggregate_window')
  assert.equal(child.executionMode, 'blocking')
  assert.deepEqual(child.spans, [{ startMs: ms('2026-01-01T00:01:00Z'), endMs: ms('2026-01-01T00:01:10Z') }])
}

// --- malformed-self-link: quarantined, invocation survives into Unlinked ------

{
  const model = normalizeTimelineModel(load('malformed-self-link.json'))
  assert.equal(model.quarantinedCount, 1, 'self-link excluded from the canonical projection')
  assert.equal(model.unlinkedCount, 1, 'invocation is never discarded')
  assert.equal(model.unlinkedGroupId, UNLINKED_GROUP_ID)
  const group = model.invocations[1]
  assert.equal(group.isGroup, true)
  assert.equal(group.depth, 0)
  const child = model.invocations[2]
  assert.equal(child.parentId, UNLINKED_GROUP_ID)
  assert.equal(child.depth, 1)
  assert.equal(model.relations.length, 0, 'no edge drawn for a quarantined delegation')
}

// --- missing-parent: child attaches to Unlinked, not quarantined --------------

{
  const model = normalizeTimelineModel(load('missing-parent.json'))
  assert.equal(model.quarantinedCount, 0, 'missing parent is not a quarantine case')
  assert.equal(model.unlinkedCount, 1)
  const child = model.invocations[2]
  assert.equal(child.id, 'chrys:28491d6d491e:child:call_orphan_1')
  assert.equal(child.parentId, UNLINKED_GROUP_ID)
  assert.equal(model.relations.length, 0)
  // Transcript preserved: timing facts still normalize.
  assert.equal(child.spans.length, 1)
}

// --- malformed-cycle: cycle + duplicate quarantined, one canonical parent ----

{
  const model = normalizeTimelineModel(load('malformed-cycle.json'))
  const a = model.invocations.find((i) => i.id.endsWith('call-nest-a'))
  const b = model.invocations.find((i) => i.id.endsWith('call-nest-b'))
  assert.equal(model.quarantinedCount, 2, 'cycle-closing and duplicate edges quarantined')
  assert.equal(a.parentId, model.rootId, 'root->a is canonical (sorted delegation ID order)')
  assert.equal(b.parentId, a.id, 'a->b is canonical; root->b preserved but not canonical')
  assert.equal(b.delegationId, `${a.id}->${b.id}`)
  assert.equal(model.unlinkedCount, 0)
  assert.equal(model.invocations[0].id, model.rootId)
  // One canonical parent per child.
  const childIds = model.relations.filter((r) => r.kind === 'launch').map((r) => r.childId)
  assert.equal(new Set(childIds).size, childIds.length)
}

// --- nested: depth-2 canonical chain ------------------------------------------

{
  const model = normalizeTimelineModel(load('nested.json'))
  assert.deepEqual(model.invocations.map((i) => i.depth), [0, 1, 2])
  const [root, a, b] = model.invocations
  assert.equal(a.parentId, root.id)
  assert.equal(b.parentId, a.id, "grandchild's parent is the child")
  assert.equal(model.relations.filter((r) => r.kind === 'launch').length, 2)
  assert.equal(model.relations.filter((r) => r.kind === 'result').length, 2)
  assert.ok(model.relations.every((r) => r.precision === 'exact'))
  // Unordered input tolerated: reversed arrays produce the identical model.
  const g = load('nested.json')
  const shuffled = { ...g, invocations: [...g.invocations].reverse(), delegations: [...g.delegations].reverse() }
  assert.deepEqual(normalizeTimelineModel(shuffled), model, 'deterministic under input reordering')
}

// --- orphaned: status preserved, no guessed end --------------------------------

{
  const model = normalizeTimelineModel(load('orphaned.json'))
  const child = model.invocations[1]
  assert.equal(child.status, 'orphaned')
  assert.equal(child.endedAtMs, null, 'no guessed end timestamp')
  assert.equal(child.spans.length, 0)
  assert.equal(child.timePrecision.reason_code, 'completion_not_recorded')
  assert.equal(model.relations.length, 1)
  assert.equal(model.relations[0].kind, 'launch')
  assert.equal(child.executionMode, 'background')
}

// --- standalone-child (Codex): backed, anchors missing, unknown status ---------

{
  const model = normalizeTimelineModel(load('standalone-child.json'))
  const child = model.invocations[1]
  assert.equal(child.hasBackingSession, true, 'BackingSessionRef respected')
  assert.equal(child.status, 'unknown')
  assert.equal(child.triggerAnchor, null)
  assert.equal(child.resultAnchor, null)
  assert.equal(model.relations.length, 1)
  assert.equal(model.relations[0].precision, 'missing')
  assert.equal(child.spans.length, 0)
}

// --- unknown-status: never collapsed into success/failure -----------------------

{
  const model = normalizeTimelineModel(load('unknown-status.json'))
  assert.equal(model.invocations[1].status, 'unknown')
  assert.equal(model.live, false, 'unknown is not treated as live')
}

// --- Cross-fixture invariants ---------------------------------------------------

const KNOWN_STATUSES = new Set(['pending', 'running', 'waiting', 'completed', 'failed', 'cancelled', 'orphaned', 'unknown'])
const FIXTURES = [
  'embedded-child.json',
  'estimated-facts.json',
  'interrupted.json',
  'lifecycle-only.json',
  'malformed-cycle.json',
  'malformed-self-link.json',
  'missing-parent.json',
  'nested.json',
  'orphaned.json',
  'standalone-child.json',
  'unknown-status.json',
]
for (const name of FIXTURES) {
  const graph = load(name)
  const model = normalizeTimelineModel(graph)
  // Exactly one deterministic root, rendered first.
  assert.equal(model.invocations[0].id, model.rootId, `${name}: root first`)
  assert.equal(model.invocations.filter((i) => i.parentId === null && !i.isGroup).length, 1, `${name}: one root`)
  // Deterministic: normalize twice, and under reversed input order.
  assert.deepEqual(normalizeTimelineModel(graph), model, `${name}: stable output`)
  const reversed = { ...graph, invocations: [...graph.invocations].reverse(), delegations: [...graph.delegations].reverse() }
  assert.deepEqual(
    normalizeTimelineModel(reversed).invocations.map((i) => i.id),
    model.invocations.map((i) => i.id),
    `${name}: order independent of input order`,
  )
  // Every lane except the root and the group has exactly one parent row.
  const ids = new Set(model.invocations.map((i) => i.id))
  for (const inv of model.invocations) {
    assert.ok(KNOWN_STATUSES.has(inv.status), `${name}: known status ${inv.status}`)
    if (inv.id === model.rootId || inv.isGroup) continue
    assert.notEqual(inv.parentId, null, `${name}: ${inv.id} has a parent`)
    assert.ok(ids.has(inv.parentId), `${name}: parent of ${inv.id} exists`)
  }
  // Unlinked group holds exactly the unlinked children, right after the group lane.
  if (model.unlinkedGroupId) {
    const groupIndex = model.invocations.findIndex((i) => i.isGroup)
    assert.ok(groupIndex > 0, `${name}: group after the root tree`)
    const members = model.invocations.filter((i) => i.parentId === UNLINKED_GROUP_ID)
    assert.equal(members.length, model.unlinkedCount, `${name}: group size`)
    assert.deepEqual(model.invocations.slice(groupIndex + 1, groupIndex + 1 + members.length).map((i) => i.id), members.map((i) => i.id))
  } else {
    assert.equal(model.unlinkedCount, 0, `${name}: no group without unlinked children`)
  }
  assert.ok(model.domainStartMs < model.domainEndMs, `${name}: non-empty time domain`)
}

// --- selectedPathIds: ancestors + self -------------------------------------------

{
  const model = normalizeTimelineModel(load('nested.json'))
  const [root, a, b] = model.invocations
  assert.deepEqual([...selectedPathIds(model, b.id)].sort(), [root.id, a.id, b.id].sort())
  assert.deepEqual([...selectedPathIds(model, root.id)], [root.id])
  assert.equal(selectedPathIds(model, null).size, 0)
}

console.log('collaboration model normalization tests passed')
