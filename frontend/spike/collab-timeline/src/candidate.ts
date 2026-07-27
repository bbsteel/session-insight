/**
 * Shared renderer candidate interface. Both candidates consume the same
 * RenderPrimitives from the same pure layout engine, so the benchmark compares
 * renderers under equivalent visual content.
 */

import type { RenderPrimitives } from './layout'

export interface CandidateUiState {
  selectedId: string | null
  selectedPathIds: ReadonlySet<string>
  hoverId: string | null
  scrollTopPx: number
  viewportHeightPx: number
  rowHeightPx: number
  theme: 'light' | 'dark'
}

export interface TimelineCandidate {
  readonly name: 'svg' | 'canvas'
  /** Element placed in the graphics grid column. */
  readonly element: HTMLElement
  /** Full redraw from new primitives. */
  update(prims: RenderPrimitives, ui: CandidateUiState): void
  /** Mounted DOM node count (svg) or drawn primitive count (canvas). */
  mountedPrimitiveCount(): number
  /** Viewport-coordinate hit test; returns an invocation id or null. */
  hitTest(xPx: number, yPx: number): string | null
  dispose(): void
}
