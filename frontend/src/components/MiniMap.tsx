import { useEffect, useMemo, useRef, useState } from 'react'
import type { TurnVM } from '../types'
import {
  getScrollTopFromTrackPosition,
  getViewportFrame,
  type ScrollMetrics,
} from '../minimapGeometry'
import type { VisibleTurnRange } from '../scrollSync'

type ReplayScrollBehavior = 'auto' | 'smooth'

interface Props {
  turns: TurnVM[]
  controlRef?: React.MutableRefObject<MiniMapControl | null>
  scrollToIndexRef?: React.MutableRefObject<((index: number, behavior?: ReplayScrollBehavior) => void) | null>
  scrollToTopRef?: React.MutableRefObject<((top: number, behavior?: ScrollBehavior) => void) | null>
}

export interface MiniMapControl {
  updateViewport: (metrics: ScrollMetrics, range?: VisibleTurnRange) => void
}

function getTotalTokens(t: TurnVM): number {
  return t.token_usage.prompt_tokens + t.token_usage.completion_tokens
}

function clamp(n: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, n))
}

function getPressureColor(ratio: number): string {
  if (ratio > 0.95) return 'var(--error)'
  if (ratio >= 0.8) return 'var(--warning)'
  return 'var(--success)'
}

function hasCompaction(turn: TurnVM): boolean {
  return turn.anomalies?.some(a => a.includes('compaction') || a.includes('compression')) ?? false
}

export default function MiniMap({ turns, controlRef, scrollToIndexRef, scrollToTopRef }: Props) {
  const barCount = turns.length
  const containerRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<HTMLDivElement>(null)
  const rangeLabelRef = useRef<HTMLSpanElement>(null)
  const draggingRef = useRef(false)
  const dragOffsetRef = useRef(0)
  const scrollFrameRef = useRef(0)
  const pendingScrollTopRef = useRef<number | null>(null)
  const scrollMetricsRef = useRef<ScrollMetrics>()
  const visibleRangeRef = useRef<VisibleTurnRange>()
  const trackLengthRef = useRef(0)
  const [isDragging, setIsDragging] = useState(false)
  const maxTokens = useMemo(() => Math.max(...turns.map(getTotalTokens), 1), [turns])

  function updateViewport(metrics: ScrollMetrics, range?: VisibleTurnRange) {
    scrollMetricsRef.current = metrics
    visibleRangeRef.current = range
    const viewport = viewportRef.current
    const length = trackLengthRef.current
    if (viewport && length > 0) {
      const frame = getViewportFrame(metrics, length)
      viewport.style.display = 'block'
      viewport.style.transform = `translateY(${frame.top}px)`
      viewport.style.height = `${frame.height}px`
    }
    if (rangeLabelRef.current) {
      rangeLabelRef.current.textContent = range
        ? `${range.start + 1}-${range.end + 1} / ${barCount}`
        : `1 / ${barCount}`
    }
  }

  useEffect(() => {
    if (!controlRef) return
    controlRef.current = { updateViewport }
    return () => {
      if (controlRef.current?.updateViewport === updateViewport) controlRef.current = null
    }
  }, [controlRef, barCount])

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const updateTrackLength = () => {
      const next = container.getBoundingClientRect().height
      trackLengthRef.current = next
      if (scrollMetricsRef.current) {
        updateViewport(scrollMetricsRef.current, visibleRangeRef.current)
      }
    }
    updateTrackLength()
    const resizeObserver = new ResizeObserver(updateTrackLength)
    resizeObserver.observe(container)
    return () => resizeObserver.disconnect()
  }, [barCount])

  const anomalyMarkers = useMemo(() => new Set(turns
    .map((turn, index) => ({ turn, index }))
    .filter(({ turn }) => (turn.anomalies?.length ?? 0) > 0 || turn.error_count > 0)
    .map(({ index }) => index)), [turns])

  function scrollTo(index: number, behavior: ReplayScrollBehavior = 'smooth') {
    if (barCount === 0) return
    scrollToIndexRef?.current?.(clamp(index, 0, barCount - 1), behavior)
  }

  function scrollFromPointer(clientY: number) {
    if (!containerRef.current || barCount === 0) return
    const rect = containerRef.current.getBoundingClientRect()
    const scrollMetrics = scrollMetricsRef.current
    if (scrollMetrics && scrollToTopRef?.current) {
      const viewportFrame = getViewportFrame(scrollMetrics, rect.height)
      const nextScrollTop = getScrollTopFromTrackPosition({
        pointerPosition: clientY,
        trackStart: rect.top,
        trackLength: rect.height,
        viewportLength: viewportFrame.height,
        scrollHeight: scrollMetrics.scrollHeight,
        clientHeight: scrollMetrics.clientHeight,
        dragOffset: dragOffsetRef.current,
      })

      if (viewportRef.current) {
        const displayHeight = viewportFrame.height
        const maxScroll = scrollMetrics.scrollHeight - scrollMetrics.clientHeight
        const maxTop = Math.max(0, rect.height - displayHeight)
        const visualTop = maxScroll > 0
          ? clamp((nextScrollTop / maxScroll) * maxTop, 0, maxTop)
          : 0
        viewportRef.current.style.transform = `translateY(${visualTop}px)`
        viewportRef.current.style.height = `${displayHeight}px`
      }

      pendingScrollTopRef.current = nextScrollTop
      if (!scrollFrameRef.current) {
        scrollFrameRef.current = window.requestAnimationFrame(() => {
          scrollFrameRef.current = 0
          const pending = pendingScrollTopRef.current
          if (pending === null) return
          pendingScrollTopRef.current = null
          scrollToTopRef.current?.(pending, 'auto')
        })
      }
      return
    }

    const ratio = clamp((clientY - rect.top) / rect.height, 0, 1)
    const visibleRange = visibleRangeRef.current
    const visibleCount = visibleRange ? visibleRange.end - visibleRange.start + 1 : 1
    const maxStart = Math.max(0, barCount - visibleCount)
    const targetIndex = Math.round(ratio * maxStart)
    scrollTo(targetIndex, 'auto')
  }

  function handlePointerDown(e: React.PointerEvent<HTMLDivElement>) {
    draggingRef.current = true
    setIsDragging(true)
    e.currentTarget.setPointerCapture(e.pointerId)
    const scrollMetrics = scrollMetricsRef.current
    const viewportFrame = containerRef.current && scrollMetrics
      ? getViewportFrame(scrollMetrics, containerRef.current.getBoundingClientRect().height)
      : undefined
    if (containerRef.current && viewportFrame) {
      const rect = containerRef.current.getBoundingClientRect()
      const trackY = e.clientY - rect.top
      const insideViewport = trackY >= viewportFrame.top && trackY <= viewportFrame.top + viewportFrame.height
      dragOffsetRef.current = insideViewport ? trackY - viewportFrame.top : viewportFrame.height / 2
    } else {
      dragOffsetRef.current = 0
    }
    scrollFromPointer(e.clientY)
  }

  function handlePointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!draggingRef.current) return
    scrollFromPointer(e.clientY)
  }

  function handlePointerUp(e: React.PointerEvent<HTMLDivElement>) {
    draggingRef.current = false
    setIsDragging(false)
    if (scrollFrameRef.current) {
      window.cancelAnimationFrame(scrollFrameRef.current)
      scrollFrameRef.current = 0
    }
    if (pendingScrollTopRef.current !== null) {
      scrollToTopRef?.current?.(pendingScrollTopRef.current, 'auto')
      pendingScrollTopRef.current = null
    }
    e.currentTarget.releasePointerCapture(e.pointerId)
  }

  if (barCount === 0) {
    return (
      <nav className="minimap-shell flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex items-center justify-center" aria-label="MiniMap">
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>MiniMap</span>
      </nav>
    )
  }

  return (
    <nav
      className="minimap-shell flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col select-none"
      aria-label="MiniMap"
    >
      <div
        ref={containerRef}
        className="flex-1 relative overflow-hidden bg-[var(--bg-inset)]"
        style={{ cursor: isDragging ? 'grabbing' : 'grab', touchAction: 'none' }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
      >
        <div className="absolute inset-y-2 left-[8px] w-px bg-[var(--border-muted)]" />
        <div className="absolute inset-y-2 left-[20px] w-[56px] rounded-sm bg-[var(--bg-primary)] border border-[var(--border-muted)]" />
        <div className="absolute inset-y-2 right-[8px] w-px bg-[var(--border-muted)]" />

        {turns.map((turn, index) => {
          const tokens = getTotalTokens(turn)
          const rowTop = `${((index + 0.5) / barCount) * 100}%`
          const tokenWidth = Math.max(5, (tokens / maxTokens) * 50)
          const pressureRatio = tokens / maxTokens
          const isAnomaly = anomalyMarkers.has(index)
          const isUserTurn = !!turn.user_message
          const isCompaction = hasCompaction(turn)
          const color = isAnomaly ? 'var(--error)' : getPressureColor(pressureRatio)

          return (
            <div
              key={index}
              className="pointer-events-none absolute left-0 right-0 h-3 -translate-y-1/2"
              style={{ top: rowTop }}
              title={`Turn ${turn.turn_index}: ${tokens.toLocaleString()} tokens${isAnomaly ? ' · 异常' : ''}${isUserTurn ? ' · 用户输入' : ''}`}
            >
              <span
                className="absolute left-[6px] top-1/2 h-1.5 w-1.5 -translate-y-1/2 rounded-full"
                style={{ background: isAnomaly ? 'var(--error)' : isUserTurn ? 'var(--success)' : 'var(--text-muted)' }}
              />
              <span
                className="absolute left-[23px] top-1/2 h-2 -translate-y-1/2 rounded-sm"
                style={{
                  width: tokenWidth,
                  background: color,
                }}
              />
              {isCompaction && (
                <span className="absolute right-[15px] top-1/2 h-2.5 w-1 -translate-y-1/2 rounded-sm bg-[var(--accent-blue)]" />
              )}
              {isUserTurn && (
                <span className="absolute right-[6px] top-1/2 h-1.5 w-1.5 -translate-y-1/2 rounded-full bg-[var(--success)]" />
              )}
            </div>
          )
        })}

        <div
          ref={viewportRef}
          className="absolute left-0 right-0 top-0 pointer-events-none"
          style={{
            display: 'none',
            background: 'rgba(37, 99, 235, 0.14)',
            borderTop: '1px solid var(--accent-blue)',
            borderBottom: '1px solid var(--accent-blue)',
            boxShadow: isDragging ? 'inset 0 0 0 1px var(--accent-blue)' : undefined,
            willChange: 'transform',
          }}
        >
          <span className="absolute left-1/2 top-0 h-1 w-3 -translate-x-1/2 rounded-b-sm bg-[var(--accent-blue)]" />
          <span className="absolute bottom-0 left-1/2 h-1 w-3 -translate-x-1/2 rounded-t-sm bg-[var(--accent-blue)]" />
        </div>
      </div>

      <div className="flex h-[22px] flex-shrink-0 items-center justify-center border-t border-[var(--border-muted)] bg-[var(--bg-surface)]">
        <span ref={rangeLabelRef} className="text-meta text-[var(--text-muted)]">
          1 / {barCount}
        </span>
      </div>
    </nav>
  )
}
