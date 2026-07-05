export interface ScrollMetrics {
  scrollTop: number
  scrollHeight: number
  clientHeight: number
}

export interface ViewportFrame {
  top: number
  height: number
}

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
