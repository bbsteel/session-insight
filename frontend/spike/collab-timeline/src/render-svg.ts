/**
 * SVG candidate: one <svg> viewport spanning the full virtual height, with
 * only visible rows' primitives mounted (viewport culling by the layout
 * engine). Transparent hit rects provide >= 12 px effective targets while
 * visual lines stay thin.
 */

import type { TimelineCandidate, CandidateUiState } from './candidate'
import type { EdgePrim, MarkerPrim, RenderPrimitives } from './layout'

const SVG_NS = 'http://www.w3.org/2000/svg'

function edgePath(e: EdgePrim): string {
  // Elbow connector: vertical from the source anchor, then horizontal.
  const midX = e.kind === 'launch' ? e.x1 : e.x2
  return `M ${e.x1} ${e.y1} L ${midX} ${e.y2} L ${e.x2} ${e.y2}`
}

function markerShape(m: MarkerPrim, rowH: number): { d: string; cls: string } {
  const y = m.rowIndex * rowH + rowH / 2
  const x = m.x
  const r = 4
  switch (m.type) {
    case 'start':
      return { d: `M ${x} ${y - r} L ${x} ${y + r}`, cls: 'mk-start' }
    case 'end':
      return { d: `M ${x} ${y - r} L ${x} ${y + r} M ${x - 2.5} ${y - r} L ${x - 2.5} ${y + r}`, cls: 'mk-end' }
    case 'failed':
      return { d: `M ${x - r} ${y - r} L ${x + r} ${y + r} M ${x + r} ${y - r} L ${x - r} ${y + r}`, cls: 'mk-failed' }
    case 'orphaned':
      return { d: `M ${x - r} ${y} L ${x} ${y - r} L ${x + r} ${y} L ${x} ${y + r} Z`, cls: 'mk-orphaned' }
    case 'unknown-end':
      return { d: `M ${x - r} ${y - r} L ${x + r} ${y - r} L ${x + r} ${y + r} L ${x - r} ${y + r}`, cls: 'mk-unknown' }
    case 'open-end':
      return { d: `M ${x} ${y - r} L ${x + r} ${y - r} L ${x + r} ${y + r} L ${x} ${y + r}`, cls: 'mk-open' }
    case 'missing-start':
      return { d: `M ${x - r} ${y} A ${r} ${r} 0 1 0 ${x + r} ${y} A ${r} ${r} 0 1 0 ${x - r} ${y}`, cls: 'mk-missing' }
    case 'waiting':
      return { d: `M ${x - r} ${y} A ${r} ${r} 0 1 0 ${x + r} ${y} A ${r} ${r} 0 1 0 ${x - r} ${y}`, cls: 'mk-waiting' }
    case 'running':
      return { d: `M ${x - r} ${y} A ${r} ${r} 0 1 0 ${x + r} ${y} A ${r} ${r} 0 1 0 ${x - r} ${y}`, cls: 'mk-running' }
  }
}

export function createSvgCandidate(rowHeightPx: number): TimelineCandidate {
  const svg = document.createElementNS(SVG_NS, 'svg') as unknown as HTMLElement
  svg.setAttribute('class', 'tl-svg')
  svg.setAttribute('role', 'presentation')
  svg.setAttribute('aria-hidden', 'true')
  let lastPrims: RenderPrimitives | null = null

  const candidate: TimelineCandidate = {
    name: 'svg',
    element: svg,

    update(prims: RenderPrimitives, ui: CandidateUiState): void {
      lastPrims = prims
      svg.setAttribute('width', '100%')
      svg.setAttribute('height', String(prims.totalHeightPx))
      svg.textContent = ''

      const gEdges = document.createElementNS(SVG_NS, 'g')
      const gIntervals = document.createElementNS(SVG_NS, 'g')
      const gMarkers = document.createElementNS(SVG_NS, 'g')
      const gHits = document.createElementNS(SVG_NS, 'g')

      for (const e of prims.edges) {
        const p = document.createElementNS(SVG_NS, 'path')
        p.setAttribute('d', edgePath(e))
        p.setAttribute('class', `edge edge-${e.kind}${e.estimated ? ' edge-estimated' : ''}${e.onSelectedPath ? ' edge-selected' : ''}`)
        if (e.clippedTop || e.clippedBottom) p.setAttribute('data-clipped', 'true')
        gEdges.appendChild(p)
      }

      for (const it of prims.intervals) {
        const rect = document.createElementNS(SVG_NS, 'rect')
        rect.setAttribute('x', String(it.x))
        rect.setAttribute('y', String(it.rowIndex * rowHeightPx + rowHeightPx / 2 - 4))
        rect.setAttribute('width', String(Math.max(1, it.w)))
        rect.setAttribute('height', '8')
        rect.setAttribute('rx', it.kind === 'aggregate' ? '4' : '2')
        rect.setAttribute('class', `interval interval-${it.kind}${it.estimated ? ' interval-estimated' : ''}`)
        if (ui.selectedPathIds.has(it.invocationId)) rect.setAttribute('data-on-path', 'true')
        gIntervals.appendChild(rect)
      }

      for (const m of prims.markers) {
        const { d, cls } = markerShape(m, rowHeightPx)
        const p = document.createElementNS(SVG_NS, 'path')
        p.setAttribute('d', d)
        p.setAttribute('class', `marker ${cls}`)
        p.setAttribute('data-shape', m.type)
        p.setAttribute('data-invocation', m.invocationId)
        gMarkers.appendChild(p)
      }

      for (const h of prims.hitRegions) {
        const rect = document.createElementNS(SVG_NS, 'rect')
        rect.setAttribute('x', String(h.x))
        rect.setAttribute('y', String(h.rowIndex * rowHeightPx))
        rect.setAttribute('width', String(h.w))
        rect.setAttribute('height', String(rowHeightPx))
        rect.setAttribute('class', 'hit-region')
        rect.setAttribute('data-invocation', h.invocationId)
        if (ui.selectedId === h.invocationId) rect.setAttribute('data-selected', 'true')
        if (ui.hoverId === h.invocationId) rect.setAttribute('data-hover', 'true')
        gHits.appendChild(rect)
      }

      svg.appendChild(gEdges)
      svg.appendChild(gIntervals)
      svg.appendChild(gMarkers)
      svg.appendChild(gHits)
    },

    mountedPrimitiveCount(): number {
      return svg.childElementCount === 0 ? 0 : Array.from(svg.children).reduce((n, g) => n + g.childElementCount, 0)
    },

    hitTest(xPx: number, yPx: number): string | null {
      if (!lastPrims) return null
      const row = Math.floor((yPx + 0) / rowHeightPx)
      const hit = lastPrims.hitRegions.find((h) => h.rowIndex === row && xPx >= h.x && xPx <= h.x + h.w)
      return hit?.invocationId ?? null
    },

    dispose(): void {
      svg.remove()
    },
  }
  return candidate
}
