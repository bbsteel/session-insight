/**
 * Pure TypeScript layout engine for the collaboration timeline spike.
 *
 * No React, DOM, SVG, Canvas, ECharts, or theme dependencies. Receives the
 * normalized (spike-only) model plus viewport inputs and returns immutable
 * render primitives in timeline coordinates (y in absolute row coordinates;
 * renderers translate by scrollTop as needed).
 */

import type { SpikeDataset, SpikeInvocation } from './types'

export interface LayoutParams {
  /** Graphics viewport width in px. */
  widthPx: number
  viewportHeightPx: number
  rowHeightPx: number
  scrollTopPx: number
  overscanRows: number
  domainStartMs: number
  domainEndMs: number
  nowMs: number
  /** LOD: merge adjacent activity segments below this pixel width. */
  minSegmentPx: number
  /** Lanes on the selected causal path render secondary (result) edges. */
  selectedPathIds: ReadonlySet<string>
  hoverId: string | null
}

export interface LaneRowPrim {
  rowIndex: number
  invocationId: string
  label: string
  depth: number
  status: SpikeInvocation['status']
  hasChildren: boolean
  collapsed: boolean
  y: number
}

export interface IntervalPrim {
  rowIndex: number
  invocationId: string
  x: number
  w: number
  kind: 'activity' | 'aggregate'
  estimated: boolean
}

export interface EdgePrim {
  kind: 'launch' | 'result'
  relationId: string
  x1: number
  y1: number
  x2: number
  y2: number
  estimated: boolean
  clippedTop: boolean
  clippedBottom: boolean
  onSelectedPath: boolean
}

export type MarkerType =
  | 'start'
  | 'end'
  | 'running'
  | 'waiting'
  | 'failed'
  | 'orphaned'
  | 'unknown-end'
  | 'missing-start'
  | 'open-end'

export interface MarkerPrim {
  rowIndex: number
  invocationId: string
  x: number
  type: MarkerType
}

export interface HitRegionPrim {
  rowIndex: number
  invocationId: string
  x: number
  w: number
}

export interface LayoutStats {
  totalRows: number
  visibleRows: number
  firstVisibleRow: number
  lastVisibleRow: number
  inputSegments: number
  mountedIntervals: number
  mountedEdges: number
  mountedMarkers: number
  mountedHitRegions: number
  mountedTotal: number
  culledEdges: number
}

export interface RenderPrimitives {
  lanes: LaneRowPrim[]
  intervals: IntervalPrim[]
  edges: EdgePrim[]
  markers: MarkerPrim[]
  hitRegions: HitRegionPrim[]
  totalHeightPx: number
  stats: LayoutStats
}

export interface VisibleLane {
  inv: SpikeInvocation
  rowIndex: number
}

/** Pre-order DFS flattening that skips descendants of collapsed lanes. */
export function flattenVisibleLanes(dataset: SpikeDataset, collapsedIds: ReadonlySet<string>): VisibleLane[] {
  const children = new Map<string | null, SpikeInvocation[]>()
  for (const inv of dataset.invocations) {
    const list = children.get(inv.parentId) ?? []
    list.push(inv)
    children.set(inv.parentId, list)
  }
  const byId = new Map(dataset.invocations.map((inv) => [inv.id, inv]))
  const startOf = (inv: SpikeInvocation): number =>
    inv.startedAtMs ?? (inv.parentId ? (byId.get(inv.parentId)?.startedAtMs ?? dataset.domainStartMs) : dataset.domainStartMs)
  // Siblings ordered by launch time, then id for determinism.
  for (const list of children.values()) {
    list.sort((a, b) => startOf(a) - startOf(b) || a.id.localeCompare(b.id))
  }
  const out: VisibleLane[] = []
  const walk = (inv: SpikeInvocation) => {
    out.push({ inv, rowIndex: out.length })
    if (collapsedIds.has(inv.id)) return
    for (const child of children.get(inv.id) ?? []) walk(child)
  }
  for (const root of children.get(null) ?? []) walk(root)
  return out
}

/** Ancestor chain + self for the selected causal path. */
export function selectedPath(dataset: SpikeDataset, selectedId: string | null): Set<string> {
  const path = new Set<string>()
  if (!selectedId) return path
  const byId = new Map(dataset.invocations.map((inv) => [inv.id, inv]))
  let cur = byId.get(selectedId)
  while (cur) {
    path.add(cur.id)
    cur = cur.parentId ? byId.get(cur.parentId) : undefined
  }
  return path
}

export function layoutTimeline(
  dataset: SpikeDataset,
  collapsedIds: ReadonlySet<string>,
  params: LayoutParams,
): RenderPrimitives {
  const {
    widthPx,
    viewportHeightPx,
    rowHeightPx,
    scrollTopPx,
    overscanRows,
    domainStartMs,
    domainEndMs,
    nowMs,
    minSegmentPx,
    selectedPathIds,
    hoverId,
  } = params

  const visible = flattenVisibleLanes(dataset, collapsedIds)
  const totalRows = visible.length
  const totalHeightPx = totalRows * rowHeightPx

  const firstRow = Math.max(0, Math.floor(scrollTopPx / rowHeightPx) - overscanRows)
  const lastRow = Math.min(totalRows - 1, Math.ceil((scrollTopPx + viewportHeightPx) / rowHeightPx) + overscanRows)

  const scale = widthPx / Math.max(1, domainEndMs - domainStartMs)
  const x = (tMs: number): number => (tMs - domainStartMs) * scale

  const childrenCount = new Map<string, number>()
  for (const inv of dataset.invocations) {
    if (inv.parentId) childrenCount.set(inv.parentId, (childrenCount.get(inv.parentId) ?? 0) + 1)
  }

  const rowOf = new Map<string, number>()
  visible.forEach(({ inv, rowIndex }) => rowOf.set(inv.id, rowIndex))

  const lanes: LaneRowPrim[] = []
  const intervals: IntervalPrim[] = []
  const markers: MarkerPrim[] = []
  const hitRegions: HitRegionPrim[] = []
  let inputSegments = 0

  for (let row = firstRow; row <= lastRow; row++) {
    const { inv, rowIndex } = visible[row]
    const y = rowIndex * rowHeightPx
    lanes.push({
      rowIndex,
      invocationId: inv.id,
      label: inv.label,
      depth: inv.depth,
      status: inv.status,
      hasChildren: (childrenCount.get(inv.id) ?? 0) > 0,
      collapsed: collapsedIds.has(inv.id),
      y,
    })

    // LOD merge: accumulate sub-threshold segments into aggregate blocks.
    inputSegments += inv.segments.length
    const estimated = inv.timePrecision === 'estimated'
    let aggStart = -1
    let aggEnd = -1
    const flushAgg = () => {
      if (aggStart >= 0) {
        intervals.push({
          rowIndex,
          invocationId: inv.id,
          x: x(aggStart),
          w: Math.max(1, x(aggEnd) - x(aggStart)),
          kind: 'aggregate',
          estimated,
        })
        aggStart = -1
        aggEnd = -1
      }
    }
    for (const seg of inv.segments) {
      const wPx = (seg.endMs - seg.startMs) * scale
      if (wPx < minSegmentPx) {
        if (aggStart < 0) {
          aggStart = seg.startMs
          aggEnd = seg.endMs
        } else {
          aggEnd = seg.endMs
        }
        if ((aggEnd - aggStart) * scale >= minSegmentPx) flushAgg()
      } else {
        flushAgg()
        intervals.push({ rowIndex, invocationId: inv.id, x: x(seg.startMs), w: wPx, kind: 'activity', estimated })
      }
    }
    flushAgg()

    // Status and boundary markers (shape + color, never color alone).
    const live = inv.status === 'running' || inv.status === 'waiting' || inv.status === 'pending'
    if (inv.startedAtMs === null) {
      // Missing start: marker at first known anchor (first segment or parent start).
      const anchor = inv.segments[0]?.startMs ?? domainStartMs
      markers.push({ rowIndex, invocationId: inv.id, x: x(anchor), type: 'missing-start' })
    } else {
      markers.push({ rowIndex, invocationId: inv.id, x: x(inv.startedAtMs), type: 'start' })
    }
    if (inv.endedAtMs !== null) {
      const type: MarkerType =
        inv.status === 'failed' ? 'failed' : inv.status === 'orphaned' ? 'orphaned' : 'end'
      markers.push({ rowIndex, invocationId: inv.id, x: x(inv.endedAtMs), type })
    } else if (live) {
      markers.push({
        rowIndex,
        invocationId: inv.id,
        x: x(nowMs),
        type: inv.status === 'waiting' ? 'waiting' : 'running',
      })
    } else if (inv.startedAtMs !== null) {
      // Closed Session with missing completion evidence: open end cap.
      markers.push({
        rowIndex,
        invocationId: inv.id,
        x: x(inv.startedAtMs),
        type: inv.status === 'unknown' ? 'unknown-end' : 'open-end',
      })
    }

    // One row-level hit region per visible lane; visual lines stay thin.
    hitRegions.push({ rowIndex, invocationId: inv.id, x: 0, w: widthPx })
  }

  // Edges. Launch edges render when the child row is visible; result edges
  // only for the selected/hovered causal path (LOD rule from the design).
  const edges: EdgePrim[] = []
  let culledEdges = 0
  const viewportTopRow = firstRow
  const viewportBottomRow = lastRow
  for (const rel of dataset.relations) {
    const childRow = rowOf.get(rel.childId)
    const parentRow = rowOf.get(rel.parentId)
    if (childRow === undefined || parentRow === undefined) {
      culledEdges++
      continue
    }
    const childVisible = childRow >= viewportTopRow && childRow <= viewportBottomRow
    if (!childVisible) {
      culledEdges++
      continue
    }
    const onPath = selectedPathIds.has(rel.childId) || rel.childId === hoverId
    if (rel.kind === 'result' && !onPath) {
      culledEdges++
      continue
    }
    const parentVisible = parentRow >= viewportTopRow && parentRow <= viewportBottomRow
    if (!parentVisible && rel.kind === 'result') {
      culledEdges++
      continue
    }
    const childInv = visible[childRow].inv
    const atMs = rel.atMs ?? childInv.startedAtMs ?? dataset.domainStartMs
    const ex = x(atMs)
    const cy = childRow * rowHeightPx + rowHeightPx / 2
    const py = parentRow * rowHeightPx + rowHeightPx / 2
    const clippedTop = parentRow < viewportTopRow
    const clippedBottom = parentRow > viewportBottomRow
    const y1 = clippedTop ? viewportTopRow * rowHeightPx : clippedBottom ? (viewportBottomRow + 1) * rowHeightPx : py
    const estimated = rel.precision === 'estimated' || rel.precision === 'missing'
    if (rel.kind === 'launch') {
      // Elbow: down from parent anchor, across to the child start marker.
      const x2 = x(childInv.startedAtMs ?? atMs)
      edges.push({ kind: 'launch', relationId: rel.id, x1: ex, y1, x2, y2: cy, estimated, clippedTop, clippedBottom, onSelectedPath: onPath })
    } else {
      const x2 = x(childInv.endedAtMs ?? atMs)
      edges.push({ kind: 'result', relationId: rel.id, x1: x2, y1: cy, x2: ex, y2: y1, estimated, clippedTop, clippedBottom, onSelectedPath: true })
    }
  }

  const mountedTotal = intervals.length + edges.length + markers.length + hitRegions.length
  return {
    lanes,
    intervals,
    edges,
    markers,
    hitRegions,
    totalHeightPx,
    stats: {
      totalRows,
      visibleRows: lanes.length,
      firstVisibleRow: firstRow,
      lastVisibleRow: lastRow,
      inputSegments,
      mountedIntervals: intervals.length,
      mountedEdges: edges.length,
      mountedMarkers: markers.length,
      mountedHitRegions: hitRegions.length,
      mountedTotal,
      culledEdges,
    },
  }
}
