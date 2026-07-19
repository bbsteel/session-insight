export interface ScrollMetrics {
  scrollTop: number
  scrollHeight: number
  clientHeight: number
}

export interface ViewportFrame {
  top: number
  height: number
}

export interface PositionViewportFrame extends ViewportFrame {
  offset: number
}

export type ScrollBoundary = 'top' | 'bottom'

function clamp(n: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, n))
}

// minLength keeps the frame grabbable on long documents; must stay in sync
// with the position-mode clamp in MiniMap.posViewport or drag mapping drifts.
export function getViewportFrame(metrics: ScrollMetrics, trackLength: number, minLength = 48): ViewportFrame {
  if (trackLength <= 0 || metrics.scrollHeight <= 0 || metrics.clientHeight <= 0) {
    return { top: 0, height: Math.max(trackLength, 0) }
  }

  if (metrics.scrollHeight <= metrics.clientHeight) {
    return { top: 0, height: trackLength }
  }

  const maxScroll = metrics.scrollHeight - metrics.clientHeight
  const height = clamp((metrics.clientHeight / metrics.scrollHeight) * trackLength, minLength, trackLength)
  const maxTop = Math.max(0, trackLength - height)
  const top = clamp((metrics.scrollTop / maxScroll) * maxTop, 0, maxTop)

  return { top, height }
}

export function getPositionViewportFrame({
  scrollTop,
  clientHeight,
  totalLines,
  trackLength,
  contentHeight,
  lineHeight,
  minLength = 48,
}: {
  scrollTop: number
  clientHeight: number
  totalLines: number
  trackLength: number
  contentHeight: number
  lineHeight: number
  minLength?: number
}): PositionViewportFrame {
  if (totalLines <= 0 || trackLength <= 0 || contentHeight <= 0 || clientHeight <= 0 || lineHeight <= 0) {
    return { top: 0, height: Math.max(trackLength, 0), offset: 0 }
  }

  const viewportLine = scrollTop / lineHeight
  const rows = Math.round(clientHeight / lineHeight)
  const scrollRatio = clamp(viewportLine / Math.max(totalLines - rows, 1), 0, 1)
  const offset = scrollRatio * Math.max(contentHeight - trackLength, 0)
  const height = clamp((rows / totalLines) * contentHeight, minLength, trackLength)
  const top = scrollRatio * Math.max(0, trackLength - height)

  return { top, height, offset }
}

export function getScrollTopFromTrackPosition({
  pointerPosition,
  trackStart,
  trackLength,
  viewportLength,
  scrollHeight,
  clientHeight,
  dragOffset,
}: {
  pointerPosition: number
  trackStart: number
  trackLength: number
  viewportLength: number
  scrollHeight: number
  clientHeight: number
  dragOffset: number
}): number {
  const maxScroll = Math.max(0, scrollHeight - clientHeight)
  const maxViewportTop = Math.max(0, trackLength - viewportLength)
  if (maxScroll === 0 || maxViewportTop === 0) return 0

  const viewportTop = clamp(pointerPosition - trackStart - dragOffset, 0, maxViewportTop)
  return (viewportTop / maxViewportTop) * maxScroll
}

export function getScrollBoundaryTop(metrics: ScrollMetrics, boundary: ScrollBoundary): number {
  if (boundary === 'top') return 0
  return Math.max(0, metrics.scrollHeight - metrics.clientHeight)
}
