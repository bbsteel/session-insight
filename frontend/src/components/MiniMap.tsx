import { useEffect, useMemo, useRef, useState } from 'react'
import type { MiniMapPosition, PositionsResponse, SessionBillingSummary, TurnVM } from '../types'
import {
  getPositionViewportFrame,
  getScrollBoundaryTop,
  getScrollTopFromTrackPosition,
  getViewportFrame,
  type ScrollBoundary,
  type ScrollMetrics,
} from '../minimapGeometry'
import { getMiniMapEventKind, getMiniMapTurnPositionPercent, getTokenPressureTone, type MiniMapEventKind, type TokenPressureTone } from '../minimapSemantics'
import { TERMINAL_LINE_HEIGHT } from '../terminalControl'
import type { VisibleTurnRange } from '../scrollSync'

type ReplayScrollBehavior = 'auto' | 'smooth'

interface Props {
  turns: TurnVM[]
  positions?: PositionsResponse | null
  billing?: SessionBillingSummary | null
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

const pressureColors: Record<TokenPressureTone, string> = {
  empty: 'var(--border-muted)',
  low: 'var(--accent-teal)',
  medium: 'var(--accent-lime)',
  high: 'var(--warning)',
  critical: 'var(--error)',
}

const eventLabels: Record<MiniMapEventKind, string> = {
  anomaly: '异常',
  compaction: '压缩',
  rollback: '回滚',
  user: '用户输入',
}

const eventShortLabels: Record<MiniMapEventKind, string> = {
  anomaly: '!',
  compaction: 'C',
  rollback: '↩',
  user: 'U',
}

const eventClassNames: Record<MiniMapEventKind, string> = {
  anomaly: 'border-[var(--error)] bg-[var(--error)] text-white',
  compaction: 'border-[var(--accent-blue)] bg-[var(--accent-blue)] text-white',
  rollback: 'border-[var(--warning)] bg-[var(--warning)] text-white',
  user: 'border-[var(--success)] bg-[var(--success)] text-white',
}

function positionEventKind(position: MiniMapPosition): MiniMapEventKind | null {
  if (position.kind === 'fold' && position.payload?.level === 'rollback') return 'rollback'
  return positionKindToEventKind[position.kind]
}

const positionKindToEventKind: Record<MiniMapPosition['kind'], MiniMapEventKind | null> = {
  turn: null,
  user: 'user',
  assistant: null,  // assistant entries feed the interaction panel, not minimap markers
  error: 'anomaly',
  compaction: 'compaction',
  edit: null,
  fold: null,  // fold/trunc entries drive terminal interactions, not minimap markers
  trunc: null,
  tool: null,  // tool entries feed the tool-call panel, not minimap markers
}

// Compute minimapContentHeight: at least visibleTrackHeight, at most 4×, scaled
// so each terminal line gets ≥0.6px. Returns visibleTrackHeight if no positions.
function computeContentHeight(totalLines: number, visibleTrackHeight: number): number {
  if (totalLines <= 0 || visibleTrackHeight <= 0) return visibleTrackHeight
  return clamp(totalLines * 0.6, visibleTrackHeight, visibleTrackHeight * 4)
}

// ── Cost profile weights ──────────────────────────────────────────────────────
// Per-turn attribution weight, mirroring the backend estimation rule:
// requests dominate cost; tokens are the fallback.

interface TurnWeight {
  weight: number
  share: number // 0..1 of session total
  estCost: number | null
}

function computeTurnWeights(turns: TurnVM[], billing?: SessionBillingSummary | null): Map<number, TurnWeight> {
  const byRequests = turns.some(t => (t.request_count ?? 0) > 0)
  const weightOf = (t: TurnVM) => byRequests ? (t.request_count ?? 0) : getTotalTokens(t)
  const total = turns.reduce((sum, t) => sum + weightOf(t), 0)
  const billed = billing && billing.billing_unit && (billing.billing_amount ?? 0) > 0 ? billing.billing_amount! : null
  const map = new Map<number, TurnWeight>()
  for (const t of turns) {
    const w = weightOf(t)
    const share = total > 0 ? w / total : 0
    map.set(t.turn_index, { weight: w, share, estCost: billed !== null ? billed * share : null })
  }
  return map
}

function fmtCost(v: number, unit?: string): string {
  if (unit === 'aiu') return `${v.toFixed(1)} AIU`
  if (unit === 'usd') return `$${v.toFixed(4)}`
  return v.toFixed(2)
}

function turnTitle(turn: TurnVM | undefined, tw: TurnWeight | undefined, unit?: string): string {
  if (!turn) return ''
  const parts = [`Turn ${turn.turn_index}`]
  if (tw?.estCost != null) parts.push(`~${fmtCost(tw.estCost, unit)}（估算）`)
  parts.push(`${getTotalTokens(turn).toLocaleString()} tokens`)
  if ((turn.request_count ?? 0) > 0) parts.push(`${turn.request_count} req`)
  return parts.join(' · ')
}

// ── Position-based rendering ──────────────────────────────────────────────────

interface PositionModeProps {
  positions: PositionsResponse
  turns: TurnVM[]
  weights: Map<number, TurnWeight>
  billingUnit?: string
  visibleTrackHeight: number
  contentOffset: number
  activeKey: string | null
  onMarkerClick: (pos: MiniMapPosition) => void
}

function PositionModeContent({
  positions,
  turns,
  weights,
  billingUnit,
  visibleTrackHeight,
  contentOffset,
  activeKey,
  onMarkerClick,
}: PositionModeProps) {
  const { total_lines: totalLines, positions: items } = positions
  if (totalLines <= 0) return null

  const contentHeight = computeContentHeight(totalLines, visibleTrackHeight)

  // Filter to non-turn events with visible marker kinds.
  const markerItems = items.filter(p => positionEventKind(p) !== null)
  const turnItems = items.filter(p => p.kind === 'turn' && p.turn_index >= 0)
  const maxShare = Math.max(...[...weights.values()].map(w => w.share), 0.0001)

  return (
    <div
      className="absolute inset-0 overflow-hidden"
      style={{ pointerEvents: 'none' }}
    >
      {/* Background strip */}
      <div className="absolute inset-y-2 left-[36px] right-[10px] rounded-sm bg-[var(--bg-primary)] border border-[var(--border-muted)]" />

      {/* Content layer — translated by contentOffset */}
      <div
        className="absolute left-0 right-0 top-0"
        style={{
          height: contentHeight,
          transform: `translateY(${-contentOffset}px)`,
          pointerEvents: 'none',
        }}
      >
        {/* Cost profile segments: each turn spans its real line range in the
            document (tall block = long turn), width = its cost share. Fixed
            thin bars became invisible slivers on long sessions. */}
        {turnItems.map((pos, i) => {
          const tw = weights.get(pos.turn_index)
          if (!tw || tw.weight <= 0) return null
          const segStart = (pos.line_start / totalLines) * contentHeight
          const nextStart = i + 1 < turnItems.length
            ? (turnItems[i + 1].line_start / totalLines) * contentHeight
            : contentHeight
          const segHeight = Math.max(nextStart - segStart - 2, 4)
          const rel = tw.share / maxShare
          const tone = getTokenPressureTone(rel)
          return (
            <div
              key={pos.position_key}
              // pointer-events-auto only for the hover tooltip; pointerdown is
              // NOT stopped here so dragging through a segment still scrolls.
              className="pointer-events-auto absolute left-[40px] right-[14px]"
              style={{ top: segStart + 1, height: segHeight }}
              title={turnTitle(turns[pos.turn_index], tw, billingUnit)}
            >
              <span
                className="absolute left-0 top-0 h-full rounded-[2px]"
                style={{ width: `${clamp(rel * 100, 6, 100)}%`, background: pressureColors[tone], opacity: 0.65 }}
              />
            </div>
          )
        })}

        {markerItems.map(pos => {
          const eventKind = positionEventKind(pos)!
          const markerY = (pos.line_start / totalLines) * contentHeight
          const isActive = pos.position_key === activeKey

          return (
            <div
              key={pos.position_key}
              className="absolute left-0 right-0 h-3"
              style={{ top: markerY, transform: 'translateY(-50%)' }}
            >
              <button
                type="button"
                className={`pointer-events-auto absolute left-[7px] top-1/2 flex h-[16px] w-[22px] -translate-y-1/2 items-center justify-center rounded-sm border text-[10px] font-semibold leading-none shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                  eventClassNames[eventKind]
                } ${isActive ? 'ring-2 ring-[var(--accent-blue)] ring-offset-1 ring-offset-[var(--bg-inset)]' : ''}`}
                style={{ minWidth: '16px', minHeight: '16px' }}
                title={`${eventLabels[eventKind]} · line ${pos.line_start}`}
                aria-label={`${eventLabels[eventKind]} · 跳转`}
                onPointerDown={e => e.stopPropagation()}
                onClick={e => { e.stopPropagation(); onMarkerClick(pos) }}
              >
                {eventShortLabels[eventKind]}
              </button>
            </div>
          )
        })}
      </div>

    </div>
  )
}

// ── Main MiniMap component ────────────────────────────────────────────────────

export default function MiniMap({ turns, positions, billing, controlRef, scrollToIndexRef, scrollToTopRef }: Props) {
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
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const [activePositionKey, setActivePositionKey] = useState<string | null>(null)
  // contentOffset drives the markers layer transform in position mode; stored
  // as ref so PositionModeContent re-renders on scroll without extra state churn.
  const contentOffsetRef = useRef(0)
  const [, setContentOffsetVersion] = useState(0)
  const maxTokens = useMemo(() => Math.max(...turns.map(getTotalTokens), 1), [turns])
  const weights = useMemo(() => computeTurnWeights(turns, billing), [turns, billing])

  const usePositions = positions != null && positions.positions.length > 0

  // Compute position-mode viewport geometry from a raw scrollTop.
  function posViewport(scrollTop: number, clientHeight: number): { top: number; height: number; offset: number } {
    if (!positions || !usePositions) return { top: 0, height: 0, offset: 0 }
    const totalLines = positions.total_lines
    const trackLength = trackLengthRef.current
    const contentHeight = computeContentHeight(totalLines, trackLength)
    return getPositionViewportFrame({
      scrollTop,
      clientHeight,
      totalLines,
      trackLength,
      contentHeight,
      lineHeight: TERMINAL_LINE_HEIGHT,
    })
  }

  function applyPositionViewport(scrollTop: number, clientHeight: number) {
    const viewport = viewportRef.current
    if (!viewport) return
    const { top, height, offset } = posViewport(scrollTop, clientHeight)
    viewport.style.display = 'block'
    viewport.style.transform = `translateY(${top}px)`
    viewport.style.height = `${height}px`
    if (offset !== contentOffsetRef.current) {
      contentOffsetRef.current = offset
      setContentOffsetVersion(v => v + 1)
    }
  }

  function updateViewport(metrics: ScrollMetrics, range?: VisibleTurnRange) {
    scrollMetricsRef.current = metrics
    visibleRangeRef.current = range

    const viewport = viewportRef.current
    const length = trackLengthRef.current
    if (usePositions) {
      applyPositionViewport(metrics.scrollTop, metrics.clientHeight)
    } else if (viewport && length > 0) {
      const frame = getViewportFrame(metrics, length)
      viewport.style.display = 'block'
      viewport.style.transform = `translateY(${frame.top}px)`
      viewport.style.height = `${frame.height}px`
    }
    if (rangeLabelRef.current) {
      const displayCount = usePositions ? (positions?.total_lines ?? 0) : barCount
      rangeLabelRef.current.textContent = range
        ? `${range.start + 1}-${range.end + 1} / ${displayCount}`
        : `1 / ${displayCount}`
    }
  }

  useEffect(() => {
    if (!controlRef) return
    controlRef.current = { updateViewport }
    return () => {
      if (controlRef.current?.updateViewport === updateViewport) controlRef.current = null
    }
  }, [controlRef, barCount, usePositions, positions?.total_lines])

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
  }, [barCount, usePositions, positions?.total_lines])

  function scrollTo(index: number, behavior: ReplayScrollBehavior = 'smooth') {
    if (barCount === 0) return
    scrollToIndexRef?.current?.(clamp(index, 0, barCount - 1), behavior)
  }

  function scrollToLine(line: number) {
    scrollToTopRef?.current?.(line * TERMINAL_LINE_HEIGHT, 'auto')
  }

  function scrollToBoundary(boundary: ScrollBoundary) {
    const metrics = scrollMetricsRef.current
    const targetTop = metrics ? getScrollBoundaryTop(metrics, boundary) : 0
    scrollToTopRef?.current?.(targetTop, 'auto')
  }

  function scrollFromPointer(clientY: number) {
    if (!containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const scrollMetrics = scrollMetricsRef.current
    if (scrollMetrics && scrollToTopRef?.current) {
      const viewportFrame = usePositions
        ? posViewport(scrollMetrics.scrollTop, scrollMetrics.clientHeight)
        : getViewportFrame(scrollMetrics, rect.height)
      const nextScrollTop = getScrollTopFromTrackPosition({
        pointerPosition: clientY,
        trackStart: rect.top,
        trackLength: rect.height,
        viewportLength: viewportFrame.height,
        scrollHeight: scrollMetrics.scrollHeight,
        clientHeight: scrollMetrics.clientHeight,
        dragOffset: dragOffsetRef.current,
      })

      if (usePositions) {
        applyPositionViewport(nextScrollTop, scrollMetrics.clientHeight)
      } else if (viewportRef.current) {
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

    if (barCount === 0) return
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
      ? usePositions
        ? posViewport(scrollMetrics.scrollTop, scrollMetrics.clientHeight)
        : getViewportFrame(scrollMetrics, containerRef.current.getBoundingClientRect().height)
      : undefined
    if (containerRef.current && viewportFrame) {
      const rect = containerRef.current.getBoundingClientRect()
      const trackY = e.clientY - rect.top
      const insideViewport = trackY >= viewportFrame.top && trackY <= viewportFrame.top + viewportFrame.height
      if (insideViewport) {
        dragOffsetRef.current = trackY - viewportFrame.top
        return
      }
      dragOffsetRef.current = viewportFrame.height / 2
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

  if (barCount === 0 && !usePositions) {
    return (
      <nav className="minimap-shell flex-shrink-0 border-l border-[var(--border-default)] bg-[var(--bg-inset)] flex items-center justify-center" aria-label="MiniMap">
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>MiniMap</span>
      </nav>
    )
  }

  return (
    <nav
      className="minimap-shell flex-shrink-0 border-l border-[var(--border-default)] bg-[var(--bg-inset)] flex flex-col select-none"
      aria-label="MiniMap"
    >
      <button
        type="button"
        className="flex h-[22px] flex-shrink-0 items-center justify-center border-b border-[var(--border-muted)] bg-[var(--bg-surface)] text-[12px] font-semibold leading-none text-[var(--text-muted)] hover:bg-[var(--bg-primary)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--accent-blue)]"
        title="滚动到顶部"
        aria-label="滚动到顶部"
        onPointerDown={e => e.stopPropagation()}
        onClick={e => {
          e.stopPropagation()
          scrollToBoundary('top')
        }}
      >
        ↑
      </button>
      <div
        ref={containerRef}
        className="flex-1 relative overflow-hidden bg-[var(--bg-inset)]"
        style={{ cursor: isDragging ? 'grabbing' : 'grab', touchAction: 'none' }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
      >
        {usePositions ? (
          <PositionModeContent
            positions={positions!}
            turns={turns}
            weights={weights}
            billingUnit={billing?.billing_unit}
            visibleTrackHeight={trackLengthRef.current}
            contentOffset={contentOffsetRef.current}
            activeKey={activePositionKey}
            onMarkerClick={pos => {
              setActivePositionKey(pos.position_key)
              scrollToLine(pos.line_start)
            }}
          />
        ) : (
          <>
            <div className="absolute inset-y-2 left-[36px] right-[10px] rounded-sm bg-[var(--bg-primary)] border border-[var(--border-muted)]" />

            {turns.map((turn, index) => {
              const tokens = getTotalTokens(turn)
              const rowTop = `${getMiniMapTurnPositionPercent(index, barCount)}%`
              const rowTransform = index === 0 ? 'translateY(0)' : index === barCount - 1 ? 'translateY(-100%)' : 'translateY(-50%)'
              const pressureRatio = tokens / maxTokens
              const pressureTone = getTokenPressureTone(pressureRatio)
              const eventKind = getMiniMapEventKind(turn)
              const eventLabel = eventKind ? eventLabels[eventKind] : ''
              const title = `${turnTitle(turn, weights.get(turn.turn_index), billing?.billing_unit)}${eventLabel ? ` · ${eventLabel}` : ''}`

              return (
                <div
                  key={index}
                  className="pointer-events-none absolute left-0 right-0 h-3"
                  style={{ top: rowTop, transform: rowTransform }}
                  title={title}
                >
                  {eventKind && (
                    <button
                      type="button"
                      className={`pointer-events-auto absolute left-[7px] top-1/2 flex h-[16px] w-[22px] -translate-y-1/2 items-center justify-center rounded-sm border text-[10px] font-semibold leading-none shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                        eventClassNames[eventKind]
                      } ${activeIndex === index ? 'ring-2 ring-[var(--accent-blue)] ring-offset-1 ring-offset-[var(--bg-inset)]' : ''}`}
                      title={`${eventLabel} · 跳转到 Turn ${turn.turn_index}`}
                      aria-label={`${eventLabel} · 跳转到 Turn ${turn.turn_index}`}
                      onPointerDown={e => e.stopPropagation()}
                      onClick={e => {
                        e.stopPropagation()
                        setActiveIndex(index)
                        scrollTo(index)
                      }}
                    >
                      {eventShortLabels[eventKind]}
                    </button>
                  )}
                  <span
                    className="absolute left-[40px] right-[14px] top-1/2 h-[4px] -translate-y-1/2 rounded-[2px]"
                    style={{
                      background: pressureColors[pressureTone],
                    }}
                  />
                </div>
              )
            })}
          </>
        )}

        {/* Shared viewport frame — controlled imperatively via viewportRef */}
        <div
          ref={viewportRef}
          className="absolute left-[4px] right-[8px] top-0 pointer-events-none rounded-sm"
          style={{
            display: 'none',
            background: 'rgba(37, 99, 235, 0.12)',
            border: '1px solid var(--accent-blue)',
            boxShadow: isDragging ? 'inset 0 0 0 1px var(--accent-blue)' : '0 0 0 1px rgba(37, 99, 235, 0.08)',
            willChange: 'transform',
          }}
        >
          <span className="absolute left-1/2 top-0 h-1 w-2.5 -translate-x-1/2 rounded-b-sm bg-[var(--accent-blue)]" />
          <span className="absolute bottom-0 left-1/2 h-1 w-2.5 -translate-x-1/2 rounded-t-sm bg-[var(--accent-blue)]" />
        </div>
      </div>

      <button
        type="button"
        className="flex h-[22px] flex-shrink-0 items-center justify-center border-t border-[var(--border-muted)] bg-[var(--bg-surface)] text-[12px] font-semibold leading-none text-[var(--text-muted)] hover:bg-[var(--bg-primary)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--accent-blue)]"
        title="滚动到底部"
        aria-label="滚动到底部"
        onPointerDown={e => e.stopPropagation()}
        onClick={e => {
          e.stopPropagation()
          scrollToBoundary('bottom')
        }}
      >
        ↓
      </button>
      <div className="flex h-[22px] flex-shrink-0 items-center justify-center border-t border-[var(--border-muted)] bg-[var(--bg-surface)]">
        <span ref={rangeLabelRef} className="text-meta text-[var(--text-muted)]">
          {usePositions ? `0 / ${positions!.total_lines}` : `1 / ${barCount}`}
        </span>
      </div>
    </nav>
  )
}
