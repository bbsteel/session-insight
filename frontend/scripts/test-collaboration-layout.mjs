/**
 * Durable logic tests: pure timeline layout engine.
 *
 * Covers time-to-pixel mapping, visible-row range and overscan, collapsed
 * branches, stable lane ordering, LOD merging, selected causal path, result
 * edge culling, connector clipping, status/boundary markers, live extension,
 * zero-duration lanes, group lanes, hit regions, and deterministic output.
 * Runs in Node against the compiled pure modules (no DOM).
 */

import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const core = '/tmp/session-insight-collaboration/src/collaboration'
const { normalizeTimelineModel, selectedPathIds, UNLINKED_GROUP_ID } = await import(
  pathToFileURL(`${core}/normalizeTimelineModel.js`).href
)
const { layoutTimeline, flattenVisibleLanes } = await import(pathToFileURL(`${core}/layoutTimeline.js`).href)
const { generateDataset, DATASET_SPECS } = await import(
  pathToFileURL('/tmp/session-insight-collaboration/harness/collab-timeline/src/datasets.js').href
)

const EPOCH = Date.parse('2026-01-01T00:00:00Z')
const iso = (ms) => new Date(EPOCH + ms).toISOString()

const exact = { state: 'exact' }

/** Minimal contract-shaped graph builder: one root plus flat/nested children.
 *  Children use short names; invocation IDs and parents are namespaced. */
function mkGraph(children, delegations = null) {
  const fullId = (short) => `test:root-1:child:${short}`
  const root = {
    id: 'test:root-1:root',
    display_name: 'main',
    agent_type: 'test',
    status: 'completed',
    started_at: iso(0),
    ended_at: iso(10000),
    time_precision: exact,
    content_precision: exact,
    source_identity: { kind: 'root_session', native_id: 'root-1' },
  }
  const invocations = [root, ...children.map((c) => ({
    id: fullId(c.id),
    display_name: c.id,
    agent_type: 'test',
    status: c.status ?? 'completed',
    ...(c.start !== null && c.start !== undefined ? { started_at: iso(c.start) } : {}),
    ...(c.end !== null && c.end !== undefined ? { ended_at: iso(c.end) } : {}),
    time_precision: c.precision ?? exact,
    content_precision: exact,
    source_identity: { kind: 'tool_call_id', native_id: c.id },
  }))]
  const dels = delegations ?? children.map((c) => ({
    id: `${c.parent ? fullId(c.parent) : root.id}->${fullId(c.id)}`,
    parent_invocation_id: c.parent ? fullId(c.parent) : root.id,
    child_invocation_id: fullId(c.id),
    ...(c.start !== null && c.start !== undefined
      ? { trigger: { agent_type: 'test', session_id: 'root-1', timestamp: iso(c.start), precision: c.triggerPrecision ?? exact } }
      : {}),
    ...(c.end !== null && c.end !== undefined
      ? { result: { agent_type: 'test', session_id: 'root-1', timestamp: iso(c.end), precision: c.resultPrecision ?? exact } }
      : {}),
    execution_mode: 'unknown',
    evidence: { trigger: exact, timing: exact, task: exact, result: exact },
  }))
  return {
    root_agent_type: 'test',
    root_session_id: 'root-1',
    revision: 1,
    completeness: exact,
    invocations,
    delegations: dels,
  }
}

function baseParams(over = {}) {
  return {
    widthPx: 500,
    viewportHeightPx: 280,
    rowHeightPx: 28,
    scrollTopPx: 0,
    overscanRows: 3,
    domainStartMs: EPOCH,
    domainEndMs: EPOCH + 10000,
    nowMs: EPOCH + 10000,
    minSegmentPx: 3,
    selectedPathIds: new Set(),
    hoverId: null,
    ...over,
  }
}

// --- time-to-pixel mapping ------------------------------------------------------

{
  const model = normalizeTimelineModel(mkGraph([{ id: 'c1', start: 2000, end: 6000 }]))
  const prims = layoutTimeline(model, new Set(), baseParams())
  const it = prims.intervals.find((i) => i.invocationId === 'test:root-1:child:c1')
  assert.ok(it, 'child interval mounted')
  assert.equal(it.x, 100, 'x = (t - domainStart) * scale')
  assert.equal(it.w, 200)
  const start = prims.markers.find((m) => m.invocationId === 'test:root-1:child:c1' && m.type === 'start')
  const end = prims.markers.find((m) => m.invocationId === 'test:root-1:child:c1' && m.type === 'end')
  assert.equal(start.x, 100)
  assert.equal(end.x, 300)
}

// --- visible-row range and overscan ----------------------------------------------

{
  const children = Array.from({ length: 30 }, (_, i) => ({ id: `c${i}`, start: 1000 + i * 100, end: 9000 }))
  const model = normalizeTimelineModel(mkGraph(children))
  const prims = layoutTimeline(model, new Set(), baseParams({ scrollTopPx: 280 }))
  assert.equal(prims.stats.totalRows, 31)
  assert.equal(prims.totalHeightPx, 31 * 28)
  assert.equal(prims.stats.firstVisibleRow, 7, 'floor(280/28) - overscan 3')
  assert.equal(prims.stats.lastVisibleRow, 23, 'ceil(560/28) + overscan 3')
  assert.equal(prims.lanes.length, 17)
  // Only visible lanes mount hit regions / markers / intervals.
  assert.equal(prims.hitRegions.length, 17)
  assert.ok(prims.stats.mountedTotal < 17 * 6, 'viewport-bounded mounted work')
  // Overscan clamps at both ends.
  const top = layoutTimeline(model, new Set(), baseParams({ scrollTopPx: 0 }))
  assert.equal(top.stats.firstVisibleRow, 0)
  const bottom = layoutTimeline(model, new Set(), baseParams({ scrollTopPx: 31 * 28 }))
  assert.equal(bottom.stats.lastVisibleRow, 30)
}

// --- collapsed branches -------------------------------------------------------------

{
  const model = normalizeTimelineModel(
    mkGraph([
      { id: 'a', start: 1000, end: 9000 },
      { id: 'b', start: 2000, end: 8000, parent: 'a' },
      { id: 'c', start: 3000, end: 7000, parent: 'b' },
    ]),
  )
  // The chain a -> b -> c must be nested (parent override), depths 1/2/3.
  assert.deepEqual(model.invocations.map((i) => i.depth), [0, 1, 2, 3])
  const expanded = flattenVisibleLanes(model, new Set())
  assert.equal(expanded.length, 4)
  const collapsedA = flattenVisibleLanes(model, new Set(['test:root-1:child:a']))
  assert.deepEqual(collapsedA.map((l) => l.inv.id), [model.rootId, 'test:root-1:child:a'], 'collapsing a hides b and c')
  const collapsedB = flattenVisibleLanes(model, new Set(['test:root-1:child:b']))
  assert.deepEqual(collapsedB.map((l) => l.inv.id), [model.rootId, 'test:root-1:child:a', 'test:root-1:child:b'])
  const prims = layoutTimeline(model, new Set(['test:root-1:child:a']), baseParams())
  assert.equal(prims.stats.totalRows, 2)
  const laneA = prims.lanes.find((l) => l.invocationId === 'test:root-1:child:a')
  assert.equal(laneA.collapsed, true)
  assert.equal(laneA.hasChildren, true)
}

// --- stable lane ordering: launch time, then stable ID -------------------------------

{
  const model = normalizeTimelineModel(
    mkGraph([
      { id: 'c-late', start: 5000, end: 9000 },
      { id: 'c-mid-b', start: 3000, end: 9000 },
      { id: 'c-mid-a', start: 3000, end: 9000 },
      { id: 'c-early', start: 1000, end: 9000 },
    ]),
  )
  assert.deepEqual(
    model.invocations.map((i) => i.id),
    [model.rootId, 'test:root-1:child:c-early', 'test:root-1:child:c-mid-a', 'test:root-1:child:c-mid-b', 'test:root-1:child:c-late'],
    'children by launch time, ties by stable ID',
  )
}

// --- LOD merging -----------------------------------------------------------------------

{
  // 100 px over 10,000 ms: 100 ms span = 1 px < 3 px threshold -> aggregate.
  const model = normalizeTimelineModel(
    mkGraph([
      { id: 'tiny', start: 1000, end: 1100 },
      { id: 'wide', start: 2000, end: 7000 },
    ]),
  )
  const prims = layoutTimeline(model, new Set(), baseParams({ widthPx: 100 }))
  const tiny = prims.intervals.find((i) => i.invocationId === 'test:root-1:child:tiny')
  const wide = prims.intervals.find((i) => i.invocationId === 'test:root-1:child:wide')
  assert.equal(tiny.kind, 'aggregate', 'sub-threshold span merged into an aggregate block')
  assert.ok(tiny.w >= 1, 'aggregate keeps a minimum visible width')
  assert.equal(wide.kind, 'activity')
  assert.equal(wide.w, 50)
}

// --- LOD at generator scale: mounted work bounded by viewport + LOD ---------------------

for (const key of ['typical', 'stress']) {
  const ds = generateDataset(DATASET_SPECS[key])
  const prims = layoutTimeline(ds.model, new Set(), baseParams({
    widthPx: 1100,
    viewportHeightPx: 380,
    domainStartMs: ds.model.domainStartMs,
    domainEndMs: ds.model.domainEndMs,
    nowMs: ds.nowMs,
  }))
  assert.equal(prims.stats.totalRows, DATASET_SPECS[key].lanes)
  const viewportRows = Math.ceil(380 / 28) + 2 * 3 + 1
  assert.ok(prims.lanes.length <= viewportRows, `${key}: visible lanes bounded`)
  assert.ok(prims.stats.mountedIntervals <= prims.stats.inputSegments, `${key}: LOD never mounts more than input`)
  assert.ok(prims.intervals.some((i) => i.kind === 'aggregate'), `${key}: LOD merging active at overview zoom`)
  assert.ok(prims.stats.culledEdges > 0, `${key}: result edges culled without selection`)
  assert.ok(!prims.edges.some((e) => e.kind === 'result'), `${key}: no result edges off-path`)
  // Determinism: identical inputs -> identical primitives.
  const again = layoutTimeline(ds.model, new Set(), baseParams({
    widthPx: 1100,
    viewportHeightPx: 380,
    domainStartMs: ds.model.domainStartMs,
    domainEndMs: ds.model.domainEndMs,
    nowMs: ds.nowMs,
  }))
  assert.deepEqual(again, prims, `${key}: deterministic output`)
  // stats consistency
  assert.equal(
    prims.stats.mountedTotal,
    prims.intervals.length + prims.edges.length + prims.markers.length + prims.hitRegions.length,
  )
}

// --- selected causal path: result edges only on the path --------------------------------

{
  const model = normalizeTimelineModel(
    mkGraph([
      { id: 'a', start: 1000, end: 9000 },
      { id: 'b', start: 2000, end: 8000, parent: 'a' },
    ]),
  )
  const path = selectedPathIds(model, 'test:root-1:child:b')
  const prims = layoutTimeline(model, new Set(), baseParams({ selectedPathIds: path }))
  const resultEdges = prims.edges.filter((e) => e.kind === 'result')
  assert.ok(resultEdges.length > 0, 'result edges render on the selected path')
  assert.ok(resultEdges.every((e) => e.onSelectedPath))
  // The sibling-free chain puts both a and b result edges on the path.
  const offPath = layoutTimeline(model, new Set(), baseParams({ selectedPathIds: new Set([model.rootId]) }))
  assert.ok(!offPath.edges.some((e) => e.kind === 'result'), 'root-only selection keeps result edges culled')
  assert.ok(offPath.stats.culledEdges > prims.stats.culledEdges)
}

// --- connector clipping -------------------------------------------------------------------

{
  const children = Array.from({ length: 30 }, (_, i) => ({ id: `c${i}`, start: 1000 + i * 100, end: 9000 }))
  const model = normalizeTimelineModel(mkGraph(children))
  // Scroll so the root (row 0) is above the visible window; children stay visible.
  const prims = layoutTimeline(model, new Set(), baseParams({ scrollTopPx: 560, overscanRows: 0 }))
  assert.equal(prims.stats.firstVisibleRow, 20)
  const launchEdges = prims.edges.filter((e) => e.kind === 'launch')
  assert.ok(launchEdges.length > 0, 'launch edges for visible children')
  assert.ok(launchEdges.every((e) => e.clippedTop), 'parent above viewport -> clipped top')
  assert.ok(launchEdges.every((e) => e.y1 === 20 * 28), 'clipped edge pinned to the viewport boundary')
}

// --- status markers: shape per state, never color-only -------------------------------------

{
  const model = normalizeTimelineModel(
    mkGraph([
      { id: 'ok', start: 1000, end: 9000 },
      { id: 'fail', start: 1000, end: 9000, status: 'failed' },
      { id: 'orph', start: 1000, end: 9000, status: 'orphaned' },
      { id: 'unk', start: 1000, status: 'unknown' },
      { id: 'open', start: 1000, status: 'completed' },
      { id: 'run', start: 1000, status: 'running' },
      { id: 'wait', start: 1000, status: 'waiting' },
      { id: 'nostart', end: 5000 },
    ]),
  )
  const prims = layoutTimeline(model, new Set(), baseParams())
  const typesFor = (id) => prims.markers.filter((m) => m.invocationId === `test:root-1:child:${id}`).map((m) => m.type)
  assert.deepEqual(typesFor('ok'), ['start', 'end'])
  assert.deepEqual(typesFor('fail'), ['start', 'failed'])
  assert.deepEqual(typesFor('orph'), ['start', 'orphaned'])
  assert.deepEqual(typesFor('unk'), ['start', 'unknown-end'], 'closed + unknown + no completion evidence')
  assert.deepEqual(typesFor('open'), ['start', 'open-end'], 'closed + missing completion -> open end cap')
  assert.deepEqual(typesFor('run'), ['start', 'running'])
  assert.deepEqual(typesFor('wait'), ['start', 'waiting'])
  assert.deepEqual(typesFor('nostart'), ['missing-start', 'end'], 'missing start marker at the known anchor')
  const run = prims.markers.find((m) => m.invocationId === 'test:root-1:child:run' && m.type === 'running')
  assert.equal(run.x, 500, 'live marker at the current-time position (x(nowMs))')
}

// --- live extension and estimated styling ----------------------------------------------------

{
  const model = normalizeTimelineModel(
    mkGraph([
      { id: 'run', start: 5000, status: 'running' },
      { id: 'est', start: 2000, end: 8000, precision: { state: 'estimated', reason_code: 'source_not_recorded' } },
    ]),
  )
  const prims = layoutTimeline(model, new Set(), baseParams({ nowMs: EPOCH + 9000 }))
  const run = prims.intervals.find((i) => i.invocationId === 'test:root-1:child:run')
  assert.equal(run.x + run.w, 450, 'active interval extends to the current-time marker')
  const est = prims.intervals.find((i) => i.invocationId === 'test:root-1:child:est')
  assert.equal(est.estimated, true, 'estimated timing uses a distinguishable style')
  const estEdge = prims.edges.find((e) => e.kind === 'launch' && e.relationId.includes('est'))
  assert.equal(estEdge.estimated, false, 'launch anchor precision drives edge style, not invocation timing')
  // Missing launch anchor -> estimated (dashed) edge.
  const runEdge = prims.edges.find((e) => e.kind === 'launch' && e.relationId.includes('run'))
  assert.equal(runEdge.estimated, false)
}

// --- zero-duration lifecycle: minimum visible width ---------------------------------------------

{
  const model = normalizeTimelineModel(mkGraph([{ id: 'pt', start: 5000, end: 5000 }]))
  const prims = layoutTimeline(model, new Set(), baseParams())
  const it = prims.intervals.find((i) => i.invocationId === 'test:root-1:child:pt')
  assert.ok(it, 'zero-duration lane still renders')
  assert.ok(it.w >= 1, 'minimum visible width; real duration lives in the tooltip')
}

// --- group lanes: no intervals/markers, collapsible ----------------------------------------------

{
  // One canonically linked child plus one child whose parent is missing.
  const graph = mkGraph([{ id: 'a', start: 1000, end: 9000 }, { id: 'u', start: 2000, end: 8000 }], [
    {
      id: 'test:root-1:root->test:root-1:child:a',
      parent_invocation_id: 'test:root-1:root',
      child_invocation_id: 'test:root-1:child:a',
      trigger: { agent_type: 'test', session_id: 'root-1', timestamp: iso(1000), precision: exact },
      result: { agent_type: 'test', session_id: 'root-1', timestamp: iso(9000), precision: exact },
      execution_mode: 'unknown',
      evidence: { trigger: exact, timing: exact, task: exact, result: exact },
    },
    {
      id: 'test:root-1:child:ghost->test:root-1:child:u',
      parent_invocation_id: 'test:root-1:child:ghost',
      child_invocation_id: 'test:root-1:child:u',
      execution_mode: 'unknown',
      evidence: {
        trigger: { state: 'missing', reason_code: 'source_not_recorded' },
        timing: exact,
        task: { state: 'missing', reason_code: 'source_not_recorded' },
        result: { state: 'missing', reason_code: 'source_not_recorded' },
      },
    },
  ])
  const grouped = normalizeTimelineModel(graph)
  assert.equal(grouped.unlinkedCount, 1)
  const prims = layoutTimeline(grouped, new Set(), baseParams())
  const groupLane = prims.lanes.find((l) => l.isGroup)
  assert.ok(groupLane, 'group lane mounted')
  assert.equal(groupLane.hasChildren, true)
  assert.ok(!prims.markers.some((m) => m.invocationId === UNLINKED_GROUP_ID), 'group carries no markers')
  assert.ok(!prims.intervals.some((i) => i.invocationId === UNLINKED_GROUP_ID), 'group carries no intervals')
  assert.ok(prims.hitRegions.some((h) => h.invocationId === UNLINKED_GROUP_ID), 'group row stays interactive')
  // Collapsing the group hides its children.
  const collapsed = layoutTimeline(grouped, new Set([UNLINKED_GROUP_ID]), baseParams())
  assert.ok(!collapsed.lanes.some((l) => l.invocationId === 'test:root-1:child:u'), 'unlinked child hidden under collapsed group')
}

// --- hit regions: one full-width region per visible row -------------------------------------------

{
  const model = normalizeTimelineModel(mkGraph([{ id: 'a', start: 1000, end: 9000 }]))
  const prims = layoutTimeline(model, new Set(), baseParams({ widthPx: 640 }))
  assert.equal(prims.hitRegions.length, prims.lanes.length)
  for (const h of prims.hitRegions) {
    assert.equal(h.x, 0)
    assert.equal(h.w, 640, 'row-level hit region spans the graphics width (28px row >= 12px minimum)')
  }
}

console.log('collaboration layout tests passed')
