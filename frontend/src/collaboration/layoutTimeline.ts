/**
 * Pure TypeScript layout engine for the production collaboration timeline.
 *
 * No React, DOM, SVG, Canvas, ECharts, theme, or i18n imports. Receives the
 * normalized timeline model plus viewport inputs and returns immutable render
 * primitives in timeline coordinates (y in absolute row coordinates; the
 * renderer's shared scroll container positions the viewport).
 *
 * Ported from the accepted renderer spike (frontend/spike/collab-timeline)
 * onto the frozen contract view model: hierarchy comes from canonical
 * delegations, launch edges from trigger anchors, result edges only for the
 * selected/hovered path, LOD merge below the pixel threshold, and row-level
 * hit regions of one row height (>= 12 px effective target).
 */

import type { InvocationStatus } from './types.js'
import type { TimelineInvocation, TimelineModel } from './normalizeTimelineModel.js'

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
  status: InvocationStatus
  hasChildren: boolean
  collapsed: boolean
  isGroup: boolean
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
  inv: TimelineInvocation
  rowIndex: number
}

/**
 * Pre-order flattening that skips descendants of collapsed lanes. The model
 * order is already a deterministic pre-order walk (normalizeTimelineModel),
 * so a collapsed row hides the following rows with greater depth.
 */
export function flattenVisibleLanes(model: TimelineModel, collapsedIds: ReadonlySet<string>): VisibleLane[] {
  const out: VisibleLane[] = []
  let skipBelowDepth = -1
  for (const inv of model.invocations) {
    if (skipBelowDepth >= 0) {
      if (inv.depth > skipBelowDepth) continue
      skipBelowDepth = -1
    }
    out.push({ inv, rowIndex: out.length })
    if (collapsedIds.has(inv.id)) skipBelowDepth = inv.depth
  }
  return out
}

/** Effective activity spans for one lane at the given current time. */
function effectiveSpans(inv: TimelineInvocation, nowMs: number): { startMs: number; endMs: number }[] {
  const spans = [...inv.spans]
  const live = inv.status === 'running' || inv.status === 'waiting' || inv.status === 'pending'
  if (inv.startedAtMs !== null && inv.endedAtMs === null && live) {
    spans.push({ startMs: inv.startedAtMs, endMs: Math.max(nowMs, inv.startedAtMs) })
  } else if (inv.startedAtMs !== null && inv.endedAtMs !== null && inv.endedAtMs <= inv.startedAtMs) {
    // Zero-duration lifecycle: minimum visible width, real duration in the tooltip.
    spans.push({ startMs: inv.startedAtMs, endMs: inv.startedAtMs })
  } else if (inv.startedAtMs === null && inv.endedAtMs !== null) {
    spans.push({ startMs: inv.endedAtMs, endMs: inv.endedAtMs })
  }
  return spans
}

export function layoutTimeline(
  model: TimelineModel,
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
    selectedPathIds: selectedPath,
    hoverId,
  } = params

  const visible = flattenVisibleLanes(model, collapsedIds)
  const totalRows = visible.length
  const totalHeightPx = totalRows * rowHeightPx

  const firstRow = Math.max(0, Math.floor(scrollTopPx / rowHeightPx) - overscanRows)
  const lastRow = Math.min(totalRows - 1, Math.ceil((scrollTopPx + viewportHeightPx) / rowHeightPx) + overscanRows)

  const scale = widthPx / Math.max(1, domainEndMs - domainStartMs)
  const x = (tMs: number): number => (tMs - domainStartMs) * scale

  const childrenCount = new Map<string, number>()
  for (const inv of model.invocations) {
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
      isGroup: inv.isGroup,
      y,
    })

    // One row-level hit region per visible lane; visual lines stay thin.
    hitRegions.push({ rowIndex, invocationId: inv.id, x: 0, w: widthPx })

    if (inv.isGroup) continue // group lanes carry no interval or status markers

    // LOD merge: accumulate sub-threshold segments into aggregate blocks.
    const spans = effectiveSpans(inv, nowMs)
    inputSegments += spans.length
    const estimated = inv.timePrecision.state === 'estimated'
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
    for (const seg of spans) {
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
      // Missing start: marker at the first known anchor (design §11.3).
      const anchor = inv.fallbackAnchorMs ?? domainStartMs
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
  }

  // Edges. Launch edges render when the child row is visible; result edges
  // only for the selected/hovered causal path (LOD rule from the design).
  const edges: EdgePrim[] = []
  let culledEdges = 0
  const viewportTopRow = firstRow
  const viewportBottomRow = lastRow
  for (const rel of model.relations) {
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
    const onPath = selectedPath.has(rel.childId) || rel.childId === hoverId
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
    const atMs = rel.atMs ?? childInv.startedAtMs ?? domainStartMs
    const ex = x(atMs)
    const cy = childRow * rowHeightPx + rowHeightPx / 2
    const py = parentRow * rowHeightPx + rowHeightPx / 2
    const clippedTop = parentRow < viewportTopRow
    const clippedBottom = parentRow > viewportBottomRow
    const y1 = clippedTop ? viewportTopRow * rowHeightPx : clippedBottom ? (viewportBottomRow + 1) * rowHeightPx : py
    const estimated: boolean = rel.precision === 'estimated' || rel.precision === 'missing'
    if (rel.kind === 'launch') {
      // Elbow: down from parent anchor, across to the child start marker.
      const x2 = x(childInv.startedAtMs ?? childInv.fallbackAnchorMs ?? atMs)
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
