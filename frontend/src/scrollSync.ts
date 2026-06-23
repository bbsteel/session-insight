import type { ScrollMetrics } from './minimapGeometry'

export interface VisibleTurnRange {
  start: number
  end: number
}

export function getVisibleTurnRange(
  metrics: ScrollMetrics,
  turnCount: number,
): VisibleTurnRange | undefined {
  if (turnCount <= 0) return undefined

  const maxScroll = Math.max(0, metrics.scrollHeight - metrics.clientHeight)
  const ratio = maxScroll > 0 ? metrics.scrollTop / maxScroll : 0
  const centerTurn = Math.round(ratio * (turnCount - 1))
  const visibleTurns = Math.max(
    1,
    Math.round((metrics.clientHeight / Math.max(metrics.scrollHeight, 1)) * turnCount),
  )
  const start = Math.max(0, centerTurn - Math.floor(visibleTurns / 2))
  const end = Math.min(turnCount - 1, start + visibleTurns - 1)

  return { start, end }
}

export function isSameVisibleRange(
  left: VisibleTurnRange | undefined,
  right: VisibleTurnRange | undefined,
): boolean {
  return left?.start === right?.start && left?.end === right?.end
}

export interface FrameBatcher<T> {
  push: (value: T) => void
  cancel: () => void
}

export function createFrameBatcher<T>(
  consume: (value: T) => void,
  requestFrame: (callback: FrameRequestCallback) => number = requestAnimationFrame,
  cancelFrame: (frame: number) => void = cancelAnimationFrame,
): FrameBatcher<T> {
  let frame = 0
  let pending: T | undefined

  return {
    push(value) {
      pending = value
      if (frame) return

      frame = requestFrame(() => {
        frame = 0
        if (pending === undefined) return
        const next = pending
        pending = undefined
        consume(next)
      })
    },
    cancel() {
      if (frame) cancelFrame(frame)
      frame = 0
      pending = undefined
    },
  }
}
