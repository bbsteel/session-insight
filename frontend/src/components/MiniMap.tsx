import { useEffect, useMemo, useRef, useState } from 'react'
import type { MiniMapPosition, PositionsResponse, SessionBillingSummary, TurnVM } from '../types'
import type { ScrollMetrics } from '../minimapGeometry'
import { getMiniMapEventKind, getMiniMapTurnPositionPercent, getTokenPressureTone, type MiniMapEventKind, type TokenPressureTone } from '../minimapSemantics'
import type { VisibleTurnRange } from '../scrollSync'

type ReplayScrollBehavior = 'auto' | 'smooth'

// MiniMap is a cost profile of the session, not a scrollbar: each turn shows
// a bar sized by its share of the session's spend (requests, falling back to
// tokens), plus anomaly/compaction/user markers. Interaction is click-to-jump
// only — drag-to-scroll competed with native scrolling and never felt right.
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

const TERMINAL_LINE_HEIGHT = 16

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
  user: '用户输入',
}

const eventShortLabels: Record<MiniMapEventKind, string> = {
  anomaly: '!',
  compaction: 'C',
  user: 'U',
}

const eventClassNames: Record<MiniMapEventKind, string> = {
  anomaly: 'border-[var(--error)] bg-[var(--error)] text-white',
  compaction: 'border-[var(--accent-blue)] bg-[var(--accent-blue)] text-white',
  user: 'border-[var(--success)] bg-[var(--success)] text-white',
}

const positionKindToEventKind: Record<MiniMapPosition['kind'], MiniMapEventKind | null> = {
  turn: null,
  user: 'user',
  error: 'anomaly',
  compaction: 'compaction',
  edit: null,
}

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
    map.set(t.turn_index, {
      weight: w,
      share,
      estCost: billed !== null ? billed * share : null,
    })
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
  parts.push(`${getTotalTokens(turn).toLocaleString()} tok`)
  if ((turn.request_count ?? 0) > 0) parts.push(`${turn.request_count} req`)
  return parts.join(' · ')
}

// Compute minimapContentHeight: at least visibleTrackHeight, at most 4×, scaled
// so each terminal line gets ≥0.6px. Returns visibleTrackHeight if no positions.
function computeContentHeight(totalLines: number, visibleTrackHeight: number): number {
  if (totalLines <= 0 || visibleTrackHeight <= 0) return visibleTrackHeight
  return clamp(totalLines * 0.6, visibleTrackHeight, visibleTrackHeight * 4)
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

  const markerItems = items.filter(p => positionKindToEventKind[p.kind] !== null)
  const turnItems = items.filter(p => p.kind === 'turn')
  const maxShare = Math.max(...[...weights.values()].map(w => w.share), 0.0001)

  return (
    <div className="absolute inset-0 overflow-hidden" style={{ pointerEvents: 'none' }}>
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
        {/* Cost profile bars anchored at each turn's real line position */}
        {turnItems.map(pos => {
          const tw = weights.get(pos.turn_index)
          if (!tw || tw.weight <= 0) return null
          const turn = turns[pos.turn_index]
          const barY = (pos.line_start / totalLines) * contentHeight
          const rel = tw.share / maxShare
          const widthPct = clamp(rel * 100, 6, 100)
          const tone = getTokenPressureTone(rel)
          return (
            <div
              key={pos.position_key}
              className="pointer-events-auto absolute left-[40px] right-[14px] h-[6px]"
              style={{ top: barY, transform: 'translateY(2px)' }}
              title={turnTitle(turn, tw, billingUnit)}
            >
              <span
                className="absolute left-0 top-0 h-full rounded-[2px]"
                style={{ width: `${widthPct}%`, background: pressureColors[tone], opacity: 0.9 }}
              />
            </div>
          )
        })}

        {markerItems.map(pos => {
          const eventKind = positionKindToEventKind[pos.kind]!
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
                title={`${eventLabels[eventKind]} · Turn ${pos.turn_index} · line ${pos.line_start}`}
                aria-label={`${eventLabels[eventKind]} · 跳转`}
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
  const scrollMetricsRef = useRef<ScrollMetrics>()
  const visibleRangeRef = useRef<VisibleTurnRange>()
  const trackLengthRef = useRef(0)
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
    const viewportLine = scrollTop / TERMINAL_LINE_HEIGHT
    const rows = Math.round(clientHeight / TERMINAL_LINE_HEIGHT)
    const scrollRatio = clamp(viewportLine / Math.max(totalLines - rows, 1), 0, 1)
    const offset = scrollRatio * Math.max(contentHeight - trackLength, 0)
    const vpTopInContent = (viewportLine / totalLines) * contentHeight
    const vpHeight = clamp((rows / totalLines) * contentHeight, 28, trackLength)
    const vpTop = clamp(vpTopInContent - offset, 0, Math.max(0, trackLength - vpHeight))
    return { top: vpTop, height: vpHeight, offset }
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
    } else if (viewport && length > 0 && metrics.scrollHeight > 0) {
      const ratio = metrics.scrollTop / Math.max(metrics.scrollHeight - metrics.clientHeight, 1)
      const vpHeight = clamp((metrics.clientHeight / metrics.scrollHeight) * length, 28, length)
      const top = clamp(ratio * (length - vpHeight), 0, Math.max(0, length - vpHeight))
      viewport.style.display = 'block'
      viewport.style.transform = `translateY(${top}px)`
      viewport.style.height = `${vpHeight}px`
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

  function scrollToLine(line: number) {
    scrollToTopRef?.current?.(line * TERMINAL_LINE_HEIGHT, 'auto')
  }

  // Click-to-jump: a single click moves the viewport to the clicked spot.
  function handleTrackClick(e: React.MouseEvent<HTMLDivElement>) {
    const container = containerRef.current
    if (!container) return
    const rect = container.getBoundingClientRect()
    const y = clamp(e.clientY - rect.top, 0, rect.height)

    if (usePositions && positions) {
      const contentHeight = computeContentHeight(positions.total_lines, rect.height)
      const line = ((y + contentOffsetRef.current) / contentHeight) * positions.total_lines
      const metrics = scrollMetricsRef.current
      const rows = metrics ? Math.round(metrics.clientHeight / TERMINAL_LINE_HEIGHT) : 0
      scrollToLine(Math.max(0, Math.round(line - rows / 2)))
      return
    }

    if (barCount === 0) return
    const targetIndex = Math.round((y / rect.height) * (barCount - 1))
    scrollToIndexRef?.current?.(clamp(targetIndex, 0, barCount - 1), 'auto')
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
      aria-label="消耗剖面图"
    >
      <div
        ref={containerRef}
        className="flex-1 relative overflow-hidden bg-[var(--bg-inset)]"
        style={{ cursor: 'pointer' }}
        onClick={handleTrackClick}
        title="点击跳转到对应位置"
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
              const tw = weights.get(turn.turn_index)
              const pressureRatio = tokens / maxTokens
              const pressureTone = getTokenPressureTone(pressureRatio)
              const eventKind = getMiniMapEventKind(turn)
              const eventLabel = eventKind ? eventLabels[eventKind] : ''
              const title = `${turnTitle(turn, tw, billing?.billing_unit)}${eventLabel ? ` · ${eventLabel}` : ''}`

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
                      onClick={e => {
                        e.stopPropagation()
                        setActiveIndex(index)
                        scrollToIndexRef?.current?.(clamp(index, 0, barCount - 1), 'smooth')
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

        {/* Viewport frame — passive "you are here" indicator */}
        <div
          ref={viewportRef}
          className="absolute left-[4px] right-[8px] top-0 pointer-events-none rounded-sm"
          style={{
            display: 'none',
            background: 'rgba(37, 99, 235, 0.12)',
            border: '1px solid var(--accent-blue)',
            boxShadow: '0 0 0 1px rgba(37, 99, 235, 0.08)',
            willChange: 'transform',
          }}
        >
          <span className="absolute left-1/2 top-0 h-1 w-2.5 -translate-x-1/2 rounded-b-sm bg-[var(--accent-blue)]" />
          <span className="absolute bottom-0 left-1/2 h-1 w-2.5 -translate-x-1/2 rounded-t-sm bg-[var(--accent-blue)]" />
        </div>
      </div>

      <div className="flex h-[22px] flex-shrink-0 items-center justify-center border-t border-[var(--border-muted)] bg-[var(--bg-surface)]">
        <span ref={rangeLabelRef} className="text-meta text-[var(--text-muted)]">
          {usePositions ? `0 / ${positions!.total_lines}` : `1 / ${barCount}`}
        </span>
      </div>
    </nav>
  )
}
