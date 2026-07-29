/**
 * Durable logic tests: collaboration dock state helpers
 * (src/collaboration/dockState.ts).
 *
 * Asserts the dock's contract-driven distinctions: error-code classification
 * (unsupported vs not-indexed vs session-missing vs generic), the exact
 * zero-child "empty" graph, backing-session gating (the only source of the
 * "open standalone child session" affordance), and invocation presence used
 * for the invocation-missing state after a live re-index.
 */

import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const core = '/tmp/session-insight-collaboration-dock/src/collaboration'
const {
  classifyCollaborationError,
  childInvocations,
  isGraphEmpty,
  backingSessionOf,
  hasInvocation,
  summarizeTimeline,
} = await import(pathToFileURL(`${core}/dockState.js`).href)
const { rootInvocationID, normalizeTimelineModel } = await import(pathToFileURL(`${core}/normalizeTimelineModel.js`).href)

// --- Error classification -----------------------------------------------------

assert.equal(classifyCollaborationError('collaboration_unsupported'), 'unsupported')
assert.equal(classifyCollaborationError('collaboration_not_indexed'), 'not_indexed')
assert.equal(classifyCollaborationError('session_not_found'), 'session_missing')
assert.equal(classifyCollaborationError('missing_agent'), 'generic')
assert.equal(classifyCollaborationError('request_failed'), 'generic')
assert.equal(classifyCollaborationError('internal'), 'generic')
assert.equal(classifyCollaborationError(''), 'generic')

// --- Fixtures -----------------------------------------------------------------

const rootId = rootInvocationID('codex', 'root-session')
const childBacked = 'codex:root-session:child:1'
const childEmbedded = 'codex:root-session:child:2'

const baseInvocation = {
  id: '',
  display_name: '',
  agent_type: 'codex',
  status: 'completed',
  time_precision: { state: 'exact' },
  content_precision: { state: 'exact' },
  source_identity: { kind: 'test', native_id: 'x' },
}

function graph(invocations) {
  return {
    root_agent_type: 'codex',
    root_session_id: 'root-session',
    revision: 1,
    completeness: { state: 'exact' },
    invocations,
    delegations: [],
  }
}

const rootOnly = graph([{ ...baseInvocation, id: rootId }])
const withChildren = graph([
  { ...baseInvocation, id: rootId },
  { ...baseInvocation, id: childBacked, backing_session: { agent_type: 'codex', session_id: 'child-session' } },
  { ...baseInvocation, id: childEmbedded },
])

// --- Empty vs non-empty --------------------------------------------------------

assert.equal(isGraphEmpty(rootOnly), true, 'root-only graph is the exact empty state')
assert.equal(isGraphEmpty(withChildren), false)
assert.deepEqual(childInvocations(rootOnly).map((i) => i.id), [])
assert.deepEqual(childInvocations(withChildren).map((i) => i.id), [childBacked, childEmbedded])

// A graph with zero invocations at all is still "empty" (defensive; the API
// always includes the root).
assert.equal(isGraphEmpty(graph([])), true)

// --- Backing-session gating ----------------------------------------------------

assert.deepEqual(backingSessionOf(withChildren, childBacked), { agent_type: 'codex', session_id: 'child-session' })
assert.equal(backingSessionOf(withChildren, childEmbedded), null, 'embedded child has no backing session')
assert.equal(backingSessionOf(withChildren, rootId), null, 'root never exposes a backing session')
assert.equal(backingSessionOf(withChildren, 'codex:root-session:child:missing'), null)

// --- Invocation presence (invocation-missing state) ----------------------------

assert.equal(hasInvocation(withChildren, childEmbedded), true)
assert.equal(hasInvocation(withChildren, 'codex:root-session:child:gone'), false)

// --- Collapsed-bar summary (mirrors list-summary semantics) ---------------------

{
  const statuses = ['running', 'waiting', 'failed', 'orphaned', 'completed', 'unknown', 'pending']
  const g = graph([
    { ...baseInvocation, id: rootId, status: 'running' }, // root never counted
    ...statuses.map((status, i) => ({ ...baseInvocation, id: `codex:root-session:child:${i}`, status })),
  ])
  const summary = summarizeTimeline(normalizeTimelineModel(g))
  assert.deepEqual(summary, { childCount: 7, activeCount: 3, problemCount: 2 })
}
{
  // Unlinked children still count (the synthetic group lane itself does not).
  const g = graph([
    { ...baseInvocation, id: rootId },
    { ...baseInvocation, id: childBacked },
    { ...baseInvocation, id: childEmbedded, status: 'failed' },
  ])
  const summary = summarizeTimeline(normalizeTimelineModel(g))
  assert.deepEqual(summary, { childCount: 2, activeCount: 0, problemCount: 1 })
  assert.deepEqual(summarizeTimeline(normalizeTimelineModel(rootOnly)), { childCount: 0, activeCount: 0, problemCount: 0 })
}

console.log('collaboration dock state tests passed')
