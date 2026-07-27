/**
 * Canvas candidate: one viewport-sized <canvas> (position: sticky) redrawn on
 * every scroll/pan/zoom frame from the same RenderPrimitives as the SVG
 * candidate. Hit testing uses a per-row spatial index built from the same
 * primitives; keyboard access stays on the shared DOM label column, so Canvas
 * adoption does not reduce keyboard access.
 */

import type { TimelineCandidate, CandidateUiState } from './candidate'
import type { MarkerPrim, RenderPrimitives } from './layout'

interface ThemeColors {
  interval: string
  aggregate: string
  estimated: string
  launch: string
  result: string
  selected: string
  hoverFill: string
  selectedFill: string
  marker: string
  failed: string
  orphaned: string
  unknown: string
  running: string
  waiting: string
  muted: string
}

/**
 * The Canvas palette reads the same CSS custom properties that style the SVG
 * candidate (see harness.html), so the two renderers cannot drift apart.
 */
function colors(): ThemeColors {
  const cs = getComputedStyle(document.documentElement)
  const v = (name: string): string => cs.getPropertyValue(name).trim()
  return {
    interval: v('--interval-activity'),
    aggregate: v('--interval-aggregate'),
    estimated: v('--interval-estimated'),
    launch: v('--edge-launch'),
    result: v('--edge-result'),
    selected: v('--path'),
    hoverFill: v('--hover-fill'),
    selectedFill: v('--selected-fill'),
    marker: v('--marker'),
    failed: v('--mk-failed'),
    orphaned: v('--mk-orphaned'),
    unknown: v('--mk-unknown'),
    running: v('--success'),
    waiting: v('--mk-waiting'),
    muted: v('--mk-unknown'),
  }
}

export function createCanvasCandidate(rowHeightPx: number): TimelineCandidate {
  const canvas = document.createElement('canvas')
  canvas.setAttribute('class', 'tl-canvas')
  canvas.setAttribute('role', 'presentation')
  canvas.setAttribute('aria-hidden', 'true')
  let lastPrims: RenderPrimitives | null = null
  let lastOffsetY = 0
  let drawnCount = 0
  // Spatial index: row -> hit regions (built from the same primitives).
  const rowHits = new Map<number, Array<{ x: number; w: number; id: string }>>()

  const drawMarker = (ctx: CanvasRenderingContext2D, m: MarkerPrim, c: ThemeColors) => {
    const y = m.rowIndex * rowHeightPx + rowHeightPx / 2 - lastOffsetY
    const x = m.x
    const r = 4
    ctx.lineWidth = 1.5 // matches the SVG marker stroke-width
    ctx.beginPath()
    switch (m.type) {
      case 'start':
        ctx.strokeStyle = c.marker
        ctx.moveTo(x, y - r)
        ctx.lineTo(x, y + r)
        break
      case 'end':
        ctx.strokeStyle = c.marker
        ctx.moveTo(x, y - r)
        ctx.lineTo(x, y + r)
        ctx.moveTo(x - 2.5, y - r)
        ctx.lineTo(x - 2.5, y + r)
        break
      case 'failed':
        ctx.strokeStyle = c.failed
        ctx.moveTo(x - r, y - r)
        ctx.lineTo(x + r, y + r)
        ctx.moveTo(x + r, y - r)
        ctx.lineTo(x - r, y + r)
        break
      case 'orphaned':
        ctx.strokeStyle = c.orphaned
        ctx.moveTo(x - r, y)
        ctx.lineTo(x, y - r)
        ctx.lineTo(x + r, y)
        ctx.lineTo(x, y + r)
        ctx.closePath()
        break
      case 'unknown-end':
        ctx.strokeStyle = c.unknown
        ctx.moveTo(x - r, y - r)
        ctx.lineTo(x + r, y - r)
        ctx.lineTo(x + r, y + r)
        ctx.lineTo(x - r, y + r)
        break
      case 'open-end':
        ctx.strokeStyle = c.unknown
        ctx.moveTo(x, y - r)
        ctx.lineTo(x + r, y - r)
        ctx.lineTo(x + r, y + r)
        ctx.lineTo(x, y + r)
        break
      case 'missing-start':
        ctx.strokeStyle = c.muted
        ctx.setLineDash([2, 2])
        ctx.arc(x, y, r, 0, Math.PI * 2)
        break
      case 'waiting':
        ctx.fillStyle = c.waiting
        ctx.arc(x, y, r, 0, Math.PI * 2)
        break
      case 'running':
        ctx.fillStyle = c.running
        ctx.arc(x, y, r, 0, Math.PI * 2)
        break
    }
    if (m.type === 'running' || m.type === 'waiting') ctx.fill()
    else ctx.stroke()
    ctx.setLineDash([])
    ctx.lineWidth = 1
    drawnCount++
  }

  const candidate: TimelineCandidate = {
    name: 'canvas',
    element: canvas,

    update(prims: RenderPrimitives, ui: CandidateUiState): void {
      lastPrims = prims
      lastOffsetY = ui.scrollTopPx
      drawnCount = 0
      rowHits.clear()
      for (const h of prims.hitRegions) {
        const list = rowHits.get(h.rowIndex) ?? []
        list.push({ x: h.x, w: h.w, id: h.invocationId })
        rowHits.set(h.rowIndex, list)
      }

      const parent = canvas.parentElement
      const widthPx = Math.max(1, parent ? parent.clientWidth : canvas.clientWidth || 1)
      const heightPx = Math.max(1, ui.viewportHeightPx)
      const dpr = window.devicePixelRatio || 1
      if (canvas.width !== Math.round(widthPx * dpr) || canvas.height !== Math.round(heightPx * dpr)) {
        canvas.width = Math.round(widthPx * dpr)
        canvas.height = Math.round(heightPx * dpr)
        canvas.style.width = `${widthPx}px`
        canvas.style.height = `${heightPx}px`
      }
      const ctx = canvas.getContext('2d')
      if (!ctx) return
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.clearRect(0, 0, widthPx, heightPx)
      ctx.lineWidth = 1

      const c = colors()

      // Row hover/selection feedback.
      for (const h of prims.hitRegions) {
        const y = h.rowIndex * rowHeightPx - lastOffsetY
        if (ui.selectedId === h.invocationId) {
          ctx.fillStyle = c.selectedFill
          ctx.fillRect(0, y, widthPx, rowHeightPx)
          drawnCount++
        } else if (ui.hoverId === h.invocationId) {
          ctx.fillStyle = c.hoverFill
          ctx.fillRect(0, y, widthPx, rowHeightPx)
          drawnCount++
        }
      }

      // Edges.
      for (const e of prims.edges) {
        const y1 = e.y1 - lastOffsetY
        const y2 = e.y2 - lastOffsetY
        ctx.beginPath()
        ctx.strokeStyle = e.onSelectedPath ? c.selected : e.kind === 'launch' ? c.launch : c.result
        ctx.lineWidth = e.onSelectedPath ? 1.5 : 1
        ctx.setLineDash(e.estimated ? [4, 3] : [])
        const midX = e.kind === 'launch' ? e.x1 : e.x2
        ctx.moveTo(e.x1, y1)
        ctx.lineTo(midX, y2)
        ctx.lineTo(e.x2, y2)
        ctx.stroke()
        drawnCount++
      }
      ctx.setLineDash([])
      ctx.lineWidth = 1

      // Intervals.
      for (const it of prims.intervals) {
        const y = it.rowIndex * rowHeightPx + rowHeightPx / 2 - 4 - lastOffsetY
        const onPath = ui.selectedPathIds.has(it.invocationId)
        ctx.fillStyle = it.kind === 'aggregate' ? c.aggregate : onPath ? c.selected : it.estimated ? c.estimated : c.interval
        const w = Math.max(1, it.w)
        const x = it.x
        const rr = it.kind === 'aggregate' ? 4 : 2
        if (typeof ctx.roundRect === 'function') {
          ctx.beginPath()
          ctx.roundRect(x, y, w, 8, rr)
          ctx.fill()
        } else {
          ctx.fillRect(x, y, w, 8)
        }
        drawnCount++
      }

      // Markers (shape + color, never color alone).
      for (const m of prims.markers) drawMarker(ctx, m, c)
    },

    mountedPrimitiveCount(): number {
      // Real draw calls issued this frame (row feedback + edges + intervals +
      // markers). Unlike SVG, hit regions cost nothing on Canvas — they feed
      // only the spatial index — so they are not counted as primitives.
      return drawnCount
    },

    hitTest(xPx: number, yPx: number): string | null {
      // yPx is element-relative (viewport coords); convert to absolute rows.
      const row = Math.floor((yPx + lastOffsetY) / rowHeightPx)
      const hits = rowHits.get(row)
      if (!hits) return null
      const hit = hits.find((h) => xPx >= h.x && xPx <= h.x + h.w)
      return hit?.id ?? null
    },

    dispose(): void {
      canvas.remove()
    },
  }
  return candidate
}
