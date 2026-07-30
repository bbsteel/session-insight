/**
 * Production collaboration timeline component (standalone core).
 *
 * Renders the frozen collaboration contract as a swimlane projection:
 * one SVG viewport for intervals/connectors/markers, a DOM label column with
 * the tree keyboard model, and a DOM tooltip. All geometry comes from the
 * pure layout engine (src/collaboration/layoutTimeline.ts); this component
 * owns interaction state only.
 *
 * Integration boundary (frozen for the later dock task):
 * - props in, callbacks out; a lane click selects and never navigates;
 * - the parent owns terminal navigation side effects (jump to launch/result,
 *   open child content) via the explicit callbacks;
 * - the parent owns dock sizing/persistence; this component fills the height
 *   it is given (heightPx) and measures its own width.
 *
 * Not mounted anywhere yet; validated by frontend/harness/collab-timeline.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { formatDate, useI18n } from '../i18n.js'
import { reasonCodeLabelKey } from '../capabilityPresentation.js'
import {
  normalizeTimelineModel,
  selectedPathIds,
  type TimelineInvocation,
} from '../collaboration/normalizeTimelineModel.js'
import {
  layoutTimeline,
  type EdgePrim,
  type MarkerPrim,
  type RenderPrimitives,
} from '../collaboration/layoutTimeline.js'
import { axisStepMs, axisTicks } from '../collaboration/timeAxis.js'
import type { CollaborationGraphDTO, InvocationStatus, SourceAnchorDTO } from '../collaboration/types.js'

const DEFAULT_ROW_HEIGHT = 28
const DEFAULT_OVERSCAN = 3
const DEFAULT_MIN_SEGMENT_PX = 3
const DEFAULT_LABEL_WIDTH = 220
const DEFAULT_HEIGHT = 240
const MIN_LIVE_INTERVAL_MS = 250
const MAX_LIVE_INTERVAL_MS = 1000
const MIN_ZOOM_SPAN_MS = 60_000
/** Drag distance below which a pointerup is a click (select), not a pan. */
const CLICK_SLOP_PX = 3

/** Availability of the "View child Agent record" action for one lane. */
export interface ChildContentActionState {
  available: boolean
  /** i18n key explaining unavailability (already cataloged). */
  reasonKey?: string
}

export interface CollaborationTimelineProps {
  /** Contract-shaped collaboration payload (internal/collaboration JSON tags). */
  graph: CollaborationGraphDTO
  /**
   * Current time in epoch ms used to extend live intervals. When omitted the
   * component tracks wall-clock time itself at the live cadence.
   */
  nowMs?: number
  /** Component height in px; the future dock owns sizing and persistence. */
  heightPx?: number
  labelWidthPx?: number
  rowHeightPx?: number
  overscanRows?: number
  /** LOD: merge adjacent activity segments below this pixel width. */
  minSegmentPx?: number
  /**
   * Geometry refresh cadence for active intervals in ms, clamped to the
   * accepted 250–1000 ms band. 0 disables live refresh.
   */
  liveIntervalMs?: number
  /** Controlled selection. When omitted the component manages selection. */
  selectedId?: string | null
  defaultSelectedId?: string | null
  /** Fired whenever selection changes (lane click or Enter). Never navigates. */
  onSelect?: (invocationId: string | null) => void
  /** Explicit "View child Agent record" action. */
  onOpenChildContent?: (invocationId: string) => void
  /** Explicit jump to the launch anchor in the parent replay. */
  onJumpToLaunch?: (invocationId: string, anchor: SourceAnchorDTO | null) => void
  /** Explicit jump to the returned-result anchor in the parent replay. */
  onJumpToResult?: (invocationId: string, anchor: SourceAnchorDTO) => void
  /** Overrides child-content availability; defaults to content_precision. */
  isChildContentAvailable?: (invocation: TimelineInvocation) => ChildContentActionState
  /** Accessible label for the whole timeline region. */
  ariaLabel?: string
}

const STATUS_GLYPH: Record<InvocationStatus, string> = {
  pending: '○',
  running: '●',
  waiting: '◐',
  completed: '✓',
  failed: '✕',
  cancelled: '⊘',
  orphaned: '◆',
  unknown: '?',
}

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
      return { d: `M ${x} ${y - r} L ${x} ${y + r}`, cls: 'ct-mk-start' }
    case 'end':
      return { d: `M ${x} ${y - r} L ${x} ${y + r} M ${x - 2.5} ${y - r} L ${x - 2.5} ${y + r}`, cls: 'ct-mk-end' }
    case 'failed':
      return { d: `M ${x - r} ${y - r} L ${x + r} ${y + r} M ${x + r} ${y - r} L ${x - r} ${y + r}`, cls: 'ct-mk-failed' }
    case 'orphaned':
      return { d: `M ${x - r} ${y} L ${x} ${y - r} L ${x + r} ${y} L ${x} ${y + r} Z`, cls: 'ct-mk-orphaned' }
    case 'unknown-end':
      return { d: `M ${x - r} ${y - r} L ${x + r} ${y - r} L ${x + r} ${y + r} L ${x - r} ${y + r}`, cls: 'ct-mk-unknown' }
    case 'open-end':
      return { d: `M ${x} ${y - r} L ${x + r} ${y - r} L ${x + r} ${y + r} L ${x} ${y + r}`, cls: 'ct-mk-open' }
    case 'missing-start':
      return { d: `M ${x - r} ${y} A ${r} ${r} 0 1 0 ${x + r} ${y} A ${r} ${r} 0 1 0 ${x - r} ${y}`, cls: 'ct-mk-missing' }
    case 'waiting':
      return { d: `M ${x - r} ${y} A ${r} ${r} 0 1 0 ${x + r} ${y} A ${r} ${r} 0 1 0 ${x - r} ${y}`, cls: 'ct-mk-waiting' }
    case 'running':
      return { d: `M ${x - r} ${y} A ${r} ${r} 0 1 0 ${x + r} ${y} A ${r} ${r} 0 1 0 ${x - r} ${y}`, cls: 'ct-mk-running' }
  }
}

function defaultChildContentState(inv: TimelineInvocation): ChildContentActionState {
  if (inv.contentPrecision.state === 'exact') return { available: true }
  return {
    available: false,
    reasonKey: reasonCodeLabelKey(inv.contentPrecision.reason_code) ?? `capability.state.${inv.contentPrecision.state}`,
  }
}

export default function CollaborationTimeline({
  graph,
  nowMs: nowMsProp,
  heightPx = DEFAULT_HEIGHT,
  labelWidthPx = DEFAULT_LABEL_WIDTH,
  rowHeightPx = DEFAULT_ROW_HEIGHT,
  overscanRows = DEFAULT_OVERSCAN,
  minSegmentPx = DEFAULT_MIN_SEGMENT_PX,
  liveIntervalMs,
  selectedId: selectedIdProp,
  defaultSelectedId = null,
  onSelect,
  onOpenChildContent,
  onJumpToLaunch,
  onJumpToResult,
  isChildContentAvailable,
  ariaLabel,
}: CollaborationTimelineProps) {
  const { t, locale } = useI18n()
  const model = useMemo(() => normalizeTimelineModel(graph), [graph])
  const byId = useMemo(() => new Map(model.invocations.map((inv) => [inv.id, inv])), [model])

  const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(() => new Set())
  const isControlled = selectedIdProp !== undefined
  const [innerSelected, setInnerSelected] = useState<string | null>(defaultSelectedId)
  const selectedId = isControlled ? (selectedIdProp ?? null) : innerSelected
  const [hoverId, setHoverId] = useState<string | null>(null)
  const [activeRowIndex, setActiveRowIndex] = useState(0)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewportSize, setViewportSize] = useState({ width: 1, height: Math.max(0, heightPx - 33) })
  const [domain, setDomain] = useState({ startMs: model.domainStartMs, endMs: model.domainEndMs })
  const [tooltip, setTooltip] = useState<{ id: string; x: number; y: number } | null>(null)

  const scrollRef = useRef<HTMLDivElement | null>(null)
  const graphRef = useRef<HTMLDivElement | null>(null)
  const labelsRef = useRef<HTMLDivElement | null>(null)
  const scrollRafRef = useRef(0)
  const panRef = useRef<{ pointerId: number; lastX: number; moved: boolean; id: string | null } | null>(null)

  // Reset view state when the payload changes.
  useEffect(() => {
    setDomain({ startMs: model.domainStartMs, endMs: model.domainEndMs })
    setCollapsedIds(new Set())
    setHoverId(null)
    setTooltip(null)
    setActiveRowIndex(0)
    setScrollTop(0)
    if (scrollRef.current) scrollRef.current.scrollTop = 0
  }, [model])

  // Live cadence: geometry updates for active intervals, 250–1000 ms.
  const liveEnabled = model.live && liveIntervalMs !== 0
  const liveCadence = Math.min(MAX_LIVE_INTERVAL_MS, Math.max(MIN_LIVE_INTERVAL_MS, liveIntervalMs ?? 500))
  const [, setTick] = useState(0)
  useEffect(() => {
    if (!liveEnabled) return
    const id = window.setInterval(() => setTick((n) => n + 1), liveCadence)
    return () => window.clearInterval(id)
  }, [liveEnabled, liveCadence])

  const nowMs = nowMsProp ?? Date.now()
  // Extend the domain while work is live so active intervals reach "now".
  useEffect(() => {
    if (model.live && nowMs > domain.endMs) {
      setDomain((d) => ({ ...d, endMs: nowMs }))
    }
  }, [model.live, nowMs, domain.endMs])

  // Measure the graphics viewport (ResizeObserver; width follows the dock).
  useEffect(() => {
    const graph = graphRef.current
    const scroll = scrollRef.current
    if (!graph || !scroll) return
    const measure = () => {
      setViewportSize({ width: Math.max(1, graph.clientWidth), height: Math.max(1, scroll.clientHeight) })
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(graph)
    ro.observe(scroll)
    return () => ro.disconnect()
  }, [])

  const selectedPath = useMemo(() => selectedPathIds(model, selectedId), [model, selectedId])
  // Stable "now" for layout: frozen for closed data, cadence-quantized when live.
  const layoutNowMs = model.live ? Math.floor(nowMs / liveCadence) * liveCadence : model.domainEndMs

  const prims: RenderPrimitives = useMemo(
    () =>
      layoutTimeline(model, collapsedIds, {
        widthPx: viewportSize.width,
        viewportHeightPx: viewportSize.height,
        rowHeightPx,
        scrollTopPx: scrollTop,
        overscanRows,
        domainStartMs: domain.startMs,
        domainEndMs: domain.endMs,
        nowMs: layoutNowMs,
        minSegmentPx,
        selectedPathIds: selectedPath,
        hoverId,
      }),
    [model, collapsedIds, viewportSize, rowHeightPx, scrollTop, overscanRows, domain, layoutNowMs, minSegmentPx, selectedPath, hoverId],
  )

  const select = useCallback(
    (id: string | null) => {
      if (!isControlled) setInnerSelected(id)
      onSelect?.(id)
    },
    [isControlled, onSelect],
  )

  const toggleBranch = useCallback((id: string) => {
    setCollapsedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const statusText = useCallback(
    (status: InvocationStatus) => t(`collaboration.status.${status}`),
    [t],
  )

  const laneLabel = useCallback(
    (inv: TimelineInvocation | undefined, fallback: string, isGroup: boolean): string => {
      if (isGroup) return t('collaboration.unlinkedGroup')
      return inv?.label ?? fallback
    },
    [t],
  )

  // ---- Tooltip -----------------------------------------------------------

  const hideTooltip = useCallback(() => setTooltip(null), [])

  const showTooltip = useCallback((id: string, x: number, y: number) => {
    setTooltip({ id, x, y })
  }, [])

  const tooltipInv = tooltip ? byId.get(tooltip.id) : undefined

  // ---- Keyboard (roving tabindex on the DOM label column) -----------------

  const focusRow = useCallback(
    (rowIndex: number) => {
      const total = prims.stats.totalRows
      if (total === 0) return
      const next = Math.max(0, Math.min(total - 1, rowIndex))
      setActiveRowIndex(next)
      const scroll = scrollRef.current
      if (scroll) {
        const first = prims.stats.firstVisibleRow
        const last = prims.stats.lastVisibleRow
        if (next <= first) scroll.scrollTop = Math.max(0, (next - 1) * rowHeightPx)
        else if (next >= last) scroll.scrollTop = (next + 2) * rowHeightPx - scroll.clientHeight
        setScrollTop(scroll.scrollTop)
      }
      const el = labelsRef.current?.querySelector(`[data-row="${next}"]`) as HTMLElement | null
      el?.focus()
    },
    [prims.stats, rowHeightPx],
  )

  const onLabelKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      const row = (e.target as HTMLElement).closest('[data-row]') as HTMLElement | null
      if (!row) return
      const rowIndex = Number(row.dataset.row)
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        focusRow(rowIndex + (e.key === 'ArrowDown' ? 1 : -1))
      } else if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
        const lane = prims.lanes.find((l) => l.rowIndex === rowIndex)
        if (!lane) return
        e.preventDefault()
        if (e.key === 'ArrowRight') {
          if (lane.hasChildren && lane.collapsed) toggleBranch(lane.invocationId)
          else if (lane.hasChildren) focusRow(rowIndex + 1)
        } else {
          if (lane.hasChildren && !lane.collapsed) {
            toggleBranch(lane.invocationId)
          } else {
            const parentId = byId.get(lane.invocationId)?.parentId
            const parentLane = prims.lanes.find((l) => l.invocationId === parentId)
            if (parentLane) focusRow(parentLane.rowIndex)
          }
        }
      } else if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        select(row.dataset.invocation ?? null)
      }
    },
    [prims.lanes, focusRow, toggleBranch, byId, select],
  )

  // ---- Pan / zoom ----------------------------------------------------------

  const shiftDomain = useCallback((dxMs: number) => {
    setDomain((d) => {
      const span = d.endMs - d.startMs
      const minStart = model.domainStartMs - span * 0.1
      const maxStart = model.domainEndMs + span * 0.1 - span
      const start = Math.max(minStart, Math.min(maxStart, d.startMs + dxMs))
      return { startMs: start, endMs: start + span }
    })
  }, [model.domainStartMs, model.domainEndMs])

  const zoomDomain = useCallback(
    (factor: number, centerMs?: number) => {
      setDomain((d) => {
        const center = centerMs ?? (d.startMs + d.endMs) / 2
        const fullSpan = model.domainEndMs - model.domainStartMs
        const span = Math.max(MIN_ZOOM_SPAN_MS, Math.min(fullSpan, (d.endMs - d.startMs) * factor))
        const ratio = (center - d.startMs) / Math.max(1, d.endMs - d.startMs)
        const start = center - ratio * span
        const clamped = Math.max(model.domainStartMs - span * 0.1, Math.min(model.domainEndMs + span * 0.1 - span, start))
        return { startMs: clamped, endMs: clamped + span }
      })
    },
    [model.domainStartMs, model.domainEndMs],
  )

  // Ctrl/cmd + wheel zoom must be non-passive, so it is attached natively.
  useEffect(() => {
    const el = graphRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      const rect = el.getBoundingClientRect()
      const frac = (e.clientX - rect.left) / Math.max(1, rect.width)
      setDomain((d) => {
        const center = d.startMs + frac * (d.endMs - d.startMs)
        const fullSpan = model.domainEndMs - model.domainStartMs
        const span = Math.max(MIN_ZOOM_SPAN_MS, Math.min(fullSpan, (d.endMs - d.startMs) * (e.deltaY > 0 ? 1.25 : 0.8)))
        const ratio = (center - d.startMs) / Math.max(1, d.endMs - d.startMs)
        const start = center - ratio * span
        const clamped = Math.max(model.domainStartMs - span * 0.1, Math.min(model.domainEndMs + span * 0.1 - span, start))
        return { startMs: clamped, endMs: clamped + span }
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [model.domainStartMs, model.domainEndMs])

  const onGraphPointerDown = useCallback((e: React.PointerEvent<SVGSVGElement>) => {
    if (e.button !== 0) return
    const target = (e.target as Element).closest('[data-invocation]') as Element | null
    panRef.current = { pointerId: e.pointerId, lastX: e.clientX, moved: false, id: target?.getAttribute('data-invocation') ?? null }
    try {
      e.currentTarget.setPointerCapture(e.pointerId)
    } catch {
      // Synthetic or already-released pointers cannot be captured; pan still works.
    }
  }, [])

  const onGraphPointerMove = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      const pan = panRef.current
      if (!pan || pan.pointerId !== e.pointerId) return
      const dx = e.clientX - pan.lastX
      pan.lastX = e.clientX
      if (!pan.moved && Math.abs(dx) < CLICK_SLOP_PX) return
      pan.moved = true
      const span = domain.endMs - domain.startMs
      const dxMs = (-dx / Math.max(1, viewportSize.width)) * span
      shiftDomain(dxMs)
    },
    [domain, viewportSize.width, shiftDomain],
  )

  const onGraphPointerUp = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      const pan = panRef.current
      panRef.current = null
      if (!pan || pan.pointerId !== e.pointerId) return
      try {
        if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId)
      } catch {
        // Best-effort release; see setPointerCapture guard above.
      }
      if (!pan.moved) {
        // A lane click selects; it never switches terminal content.
        select(pan.id === selectedId ? null : pan.id)
      }
    },
    [select, selectedId],
  )

  const onScroll = useCallback(() => {
    if (scrollRafRef.current) return
    scrollRafRef.current = requestAnimationFrame(() => {
      scrollRafRef.current = 0
      setScrollTop(scrollRef.current?.scrollTop ?? 0)
    })
  }, [])
  useEffect(() => () => cancelAnimationFrame(scrollRafRef.current), [])

  // ---- Selection actions ---------------------------------------------------

  const selectedInv = selectedId ? byId.get(selectedId) : undefined

  // ---- Time axis -------------------------------------------------------------

  // Ticks follow the visible domain, so pan/zoom/reset update them implicitly.
  const ticks = useMemo(() => axisTicks(domain.startMs, domain.endMs, 5), [domain])
  const tickStep = useMemo(() => axisStepMs(domain.endMs - domain.startMs, 5), [domain])
  // Same linear mapping the layout engine uses (layoutTimeline.ts x()).
  const axisX = useCallback(
    (tMs: number) => ((tMs - domain.startMs) / Math.max(1, domain.endMs - domain.startMs)) * viewportSize.width,
    [domain, viewportSize.width],
  )
  const formatTick = useCallback(
    (ms: number) =>
      tickStep < 60_000
        ? formatDate(locale, ms, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
        : tickStep < 86_400_000
          ? formatDate(locale, ms, { hour: '2-digit', minute: '2-digit' })
          : formatDate(locale, ms, { month: '2-digit', day: '2-digit' }),
    [locale, tickStep],
  )
  // ---- Targeted zoom ---------------------------------------------------------

  // Keep button enablement and zoom action on the same source of time truth.
  // Spans normally derive from complete invocation boundaries, but using them
  // directly also keeps this interaction sound for future span-only models.
  const selectedExtent = useMemo(() => {
    if (!selectedInv || selectedInv.isGroup) return null
    if (selectedInv.spans.length > 0) {
      const s = selectedInv.spans[0]
      return { startMs: s.startMs, endMs: s.endMs }
    }
    const startMs = selectedInv.startedAtMs ?? selectedInv.fallbackAnchorMs
    return startMs === null ? null : { startMs, endMs: selectedInv.endedAtMs ?? startMs }
  }, [selectedInv])
  const selectedCenterMs = selectedExtent ? (selectedExtent.startMs + selectedExtent.endMs) / 2 : null

  const zoomToSelection = useCallback(() => {
    if (!selectedExtent) return
    const { startMs: start, endMs: end } = selectedExtent
    const pad = Math.max((end - start) * 0.25, 5_000)
    let ns = start - pad
    let ne = end + pad
    if (ne - ns < MIN_ZOOM_SPAN_MS) {
      const center = (ns + ne) / 2
      ns = center - MIN_ZOOM_SPAN_MS / 2
      ne = center + MIN_ZOOM_SPAN_MS / 2
    }
    const fullSpan = model.domainEndMs - model.domainStartMs
    const span = ne - ns
    ns = Math.max(model.domainStartMs - fullSpan * 0.1, Math.min(model.domainEndMs + fullSpan * 0.1 - span, ns))
    setDomain({ startMs: ns, endMs: ns + span })
  }, [selectedExtent, model.domainStartMs, model.domainEndMs])

  // A selected invocation has a persistent detail surface in the dock. Do
  // not leave a second hover/focus tooltip competing with it.
  useEffect(() => {
    if (selectedId === null) return
    setTooltip(null)
    setHoverId(null)
  }, [selectedId])

  const childContentState: ChildContentActionState = selectedInv
    ? (isChildContentAvailable ?? defaultChildContentState)(selectedInv)
    : { available: false }
  const openChildReason = !selectedInv
    ? ''
    : selectedInv.isGroup
      ? ''
      : selectedInv.id === model.rootId
        ? t('collaboration.action.rootIsCurrent')
        : !childContentState.available
          ? t('collaboration.action.noChildContent', {
              reason: childContentState.reasonKey ? t(childContentState.reasonKey) : t(`capability.state.${selectedInv.contentPrecision.state}`),
            })
          : !onOpenChildContent
            ? t('collaboration.action.notWired')
            : ''
  const openChildEnabled =
    Boolean(selectedInv && !selectedInv.isGroup && selectedInv.id !== model.rootId && childContentState.available && onOpenChildContent)

  const launchAnchorAvailable = Boolean(selectedInv && (selectedInv.triggerAnchor || selectedInv.startedAtMs !== null))
  const jumpLaunchEnabled = Boolean(selectedInv && !selectedInv.isGroup && launchAnchorAvailable && onJumpToLaunch)
  const jumpLaunchReason = !launchAnchorAvailable
    ? t('collaboration.action.noLaunchAnchor')
    : !onJumpToLaunch
      ? t('collaboration.action.notWired')
      : ''

  const jumpResultEnabled = Boolean(selectedInv && !selectedInv.isGroup && selectedInv.resultAnchor && onJumpToResult)
  const jumpResultReason = !selectedInv?.resultAnchor
    ? t('collaboration.action.noResultAnchor')
    : !onJumpToResult
      ? t('collaboration.action.notWired')
      : ''

  // ---- Render ---------------------------------------------------------------

  const regionLabel = ariaLabel ?? t('collaboration.timeline')

  return (
    <div className="collab-timeline" style={{ height: heightPx }} role="region" aria-label={regionLabel} data-testid="collab-timeline">
      <div className="ct-toolbar">
        <div className="ct-toolbar-group">
          <button type="button" className="ct-btn" title={t('collaboration.zoomOut')} aria-label={t('collaboration.zoomOut')} onClick={() => zoomDomain(1.25, selectedCenterMs ?? undefined)}>
            −
          </button>
          <button type="button" className="ct-btn" title={t('collaboration.zoomIn')} aria-label={t('collaboration.zoomIn')} onClick={() => zoomDomain(0.8, selectedCenterMs ?? undefined)}>
            +
          </button>
          <button type="button" className="ct-btn" title={t('collaboration.zoomReset')} aria-label={t('collaboration.zoomReset')} onClick={() => setDomain({ startMs: model.domainStartMs, endMs: model.domainEndMs })}>
            ⤾
          </button>
          <button
            type="button"
            className="ct-btn"
            title={t('collaboration.zoomToSelection')}
            aria-label={t('collaboration.zoomToSelection')}
            disabled={selectedCenterMs === null}
            data-testid="ct-zoom-selection"
            onClick={zoomToSelection}
          >
            ⌖
          </button>
        </div>
        <div className="ct-toolbar-group ct-actions" data-testid="ct-actions">
          <button
            type="button"
            className="ct-btn ct-action"
            disabled={!openChildEnabled}
            title={openChildEnabled ? t('collaboration.action.openChild') : openChildReason}
            aria-label={t('collaboration.action.openChild')}
            onClick={() => selectedInv && onOpenChildContent?.(selectedInv.id)}
          >
            {t('collaboration.action.openChild')}
          </button>
          <button
            type="button"
            className="ct-btn ct-action"
            disabled={!jumpLaunchEnabled}
            title={jumpLaunchEnabled ? t('collaboration.action.jumpLaunch') : jumpLaunchReason}
            aria-label={t('collaboration.action.jumpLaunch')}
            onClick={() => selectedInv && onJumpToLaunch?.(selectedInv.id, selectedInv.triggerAnchor)}
          >
            {t('collaboration.action.jumpLaunch')}
          </button>
          <button
            type="button"
            className="ct-btn ct-action"
            disabled={!jumpResultEnabled}
            title={jumpResultEnabled ? t('collaboration.action.jumpResult') : jumpResultReason}
            aria-label={t('collaboration.action.jumpResult')}
            onClick={() => selectedInv?.resultAnchor && onJumpToResult?.(selectedInv.id, selectedInv.resultAnchor)}
          >
            {t('collaboration.action.jumpResult')}
          </button>
        </div>
      </div>
      <div className="ct-axis" data-testid="ct-axis">
        <div className="ct-axis-spacer" style={{ width: labelWidthPx, flexBasis: labelWidthPx }} />
        <div className="ct-axis-track">
          {ticks.map((ms) => (
            <span key={ms} className="ct-axis-tick" style={{ left: axisX(ms) }} data-testid="ct-axis-tick">
              {formatTick(ms)}
            </span>
          ))}
        </div>
      </div>
      <div className="ct-scroll" ref={scrollRef} onScroll={onScroll}>
        <div className="ct-grid" style={{ height: prims.totalHeightPx }}>
          <div
            className="ct-labels"
            ref={labelsRef}
            role="tree"
            aria-label={t('collaboration.lanes')}
            style={{ width: labelWidthPx, flexBasis: labelWidthPx }}
            onKeyDown={onLabelKeyDown}
          >
            {prims.lanes.map((lane) => {
              const inv = byId.get(lane.invocationId)
              const label = laneLabel(inv, lane.label, lane.isGroup)
              const rowAria = lane.isGroup
                ? t('collaboration.groupLaneLabel', { label, count: model.unlinkedCount })
                : t('collaboration.laneLabel', { label, status: statusText(lane.status) })
              return (
                <div
                  key={lane.invocationId}
                  className={`ct-label${lane.isGroup ? ' ct-label-group' : ''}`}
                  style={{ top: lane.y, height: rowHeightPx, paddingLeft: 8 + lane.depth * 14 }}
                  role="treeitem"
                  aria-level={lane.depth + 1}
                  aria-expanded={lane.hasChildren ? !lane.collapsed : undefined}
                  aria-selected={selectedId === lane.invocationId}
                  aria-label={rowAria}
                  tabIndex={lane.rowIndex === activeRowIndex ? 0 : -1}
                  data-invocation={lane.invocationId}
                  data-status={lane.status}
                  data-row={lane.rowIndex}
                  data-group={lane.isGroup || undefined}
                  onClick={(e) => {
                    const toggle = (e.target as HTMLElement).closest('[data-toggle]') as HTMLElement | null
                    if (toggle) {
                      toggleBranch(toggle.getAttribute('data-toggle') ?? '')
                      return
                    }
                    select(lane.invocationId)
                  }}
                  onFocus={(e) => {
                    setActiveRowIndex(lane.rowIndex)
                    if (hoverId !== lane.invocationId) setHoverId(lane.invocationId)
                    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
                    showTooltip(lane.invocationId, rect.right, rect.top)
                  }}
                  onBlur={(e) => {
                    const next = e.relatedTarget as HTMLElement | null
                    if (next && labelsRef.current?.contains(next)) return
                    requestAnimationFrame(() => {
                      if (labelsRef.current?.contains(document.activeElement)) return
                      setHoverId((h) => (h === lane.invocationId ? null : h))
                      hideTooltip()
                    })
                  }}
                >
                  {!lane.isGroup && (
                    <span className={`ct-status ct-st-${lane.status}`} aria-hidden="true">
                      {STATUS_GLYPH[lane.status] ?? '?'}
                    </span>
                  )}
                  <span className="ct-label-text">{label}</span>
                  {lane.hasChildren && (
                    <span
                      className="ct-collapse"
                      aria-hidden="true"
                      data-toggle={lane.invocationId}
                      title={lane.collapsed ? t('collaboration.branch.expand') : t('collaboration.branch.collapse')}
                    >
                      {lane.collapsed ? '▸' : '▾'}
                    </span>
                  )}
                </div>
              )
            })}
          </div>
          <div className="ct-graph" ref={graphRef}>
            <svg
              className="ct-svg"
              width={viewportSize.width}
              height={prims.totalHeightPx}
              role="presentation"
              aria-hidden="true"
              onPointerDown={onGraphPointerDown}
              onPointerMove={onGraphPointerMove}
              onPointerUp={onGraphPointerUp}
              onPointerCancel={() => {
                panRef.current = null
              }}
            >
              <g aria-hidden="true">
                {ticks.map((ms) => {
                  const gx = axisX(ms)
                  return <line key={ms} x1={gx} x2={gx} y1={0} y2={prims.totalHeightPx} className="ct-grid-line" />
                })}
              </g>
              <g>
                {prims.edges.map((e) => (
                  <path
                    key={e.relationId}
                    d={edgePath(e)}
                    className={`ct-edge ct-edge-${e.kind}${e.estimated ? ' ct-edge-estimated' : ''}${e.onSelectedPath ? ' ct-edge-selected' : ''}`}
                    data-clipped={e.clippedTop || e.clippedBottom || undefined}
                  />
                ))}
              </g>
              <g>
                {prims.intervals.map((it, i) => (
                  <rect
                    key={`${it.invocationId}:${i}`}
                    x={it.x}
                    y={it.rowIndex * rowHeightPx + rowHeightPx / 2 - 4}
                    width={Math.max(1, it.w)}
                    height={8}
                    rx={it.kind === 'aggregate' ? 4 : 2}
                    className={`ct-interval ct-interval-${it.kind}${it.estimated ? ' ct-interval-estimated' : ''}`}
                    data-on-path={selectedPath.has(it.invocationId) || undefined}
                  />
                ))}
              </g>
              <g>
                {prims.markers.map((m, i) => {
                  const { d, cls } = markerShape(m, rowHeightPx)
                  return <path key={`${m.invocationId}:${m.type}:${i}`} d={d} className={`ct-marker ${cls}`} data-shape={m.type} data-invocation={m.invocationId} />
                })}
              </g>
              <g>
                {prims.hitRegions.map((h) => (
                  <rect
                    key={h.invocationId}
                    x={h.x}
                    y={h.rowIndex * rowHeightPx}
                    width={h.w}
                    height={rowHeightPx}
                    className="ct-hit-region"
                    data-invocation={h.invocationId}
                    data-selected={selectedId === h.invocationId || undefined}
                    data-hover={hoverId === h.invocationId || undefined}
                    pointerEvents="all"
                    onPointerMove={(e) => {
                      if (panRef.current?.moved) return
                      if (hoverId !== h.invocationId) setHoverId(h.invocationId)
                      showTooltip(h.invocationId, e.clientX, e.clientY)
                    }}
                    onPointerLeave={() => {
                      setHoverId((cur) => (cur === h.invocationId ? null : cur))
                      hideTooltip()
                    }}
                  />
                ))}
              </g>
            </svg>
          </div>
        </div>
      </div>
      {tooltip && tooltipInv && selectedId === null && (
        <div
          className="ct-tooltip"
          role="tooltip"
          style={{
            left: Math.min(tooltip.x + 12, window.innerWidth - 328),
            top: Math.min(tooltip.y + 12, window.innerHeight - 120),
          }}
        >
          <div className="ct-tooltip-title">{tooltipInv.isGroup ? t('collaboration.unlinkedGroup') : tooltipInv.label}</div>
          {!tooltipInv.isGroup && (
            <>
              <div>{`${t('collaboration.tooltip.status')}: ${statusText(tooltipInv.status)}`}</div>
              <div>{`${t('collaboration.tooltip.duration')}: ${formatDuration(tooltipInv, nowMs, t)}`}</div>
              <div>
                {`${t('collaboration.tooltip.precision')}: ${t(`capability.state.${tooltipInv.timePrecision.state}`)}`}
                {tooltipInv.timePrecision.reason_code && reasonCodeLabelKey(tooltipInv.timePrecision.reason_code)
                  ? ` — ${t(reasonCodeLabelKey(tooltipInv.timePrecision.reason_code) as string)}`
                  : ''}
              </div>
              <div>{`${t('collaboration.tooltip.started')}: ${tooltipInv.startedAtMs !== null ? formatDate(locale, tooltipInv.startedAtMs, { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : t('collaboration.duration.unknown')}`}</div>
              {tooltipInv.taskSummary && <div className="ct-tooltip-task">{`${t('collaboration.tooltip.task')}: ${tooltipInv.taskSummary}`}</div>}
            </>
          )}
        </div>
      )}
    </div>
  )
}

/** One-second-granularity duration label (design §10.6). */
function formatDuration(inv: TimelineInvocation, nowMs: number, t: (key: string, vars?: Record<string, string | number>) => string): string {
  const live = inv.status === 'running' || inv.status === 'waiting' || inv.status === 'pending'
  const start = inv.startedAtMs
  const end = inv.endedAtMs ?? (live ? nowMs : null)
  if (start === null || end === null) return t('collaboration.duration.unknown')
  const totalSeconds = Math.max(0, Math.round((end - start) / 1000))
  if (totalSeconds >= 3600) {
    return t('collaboration.duration.hours', { count: Math.floor(totalSeconds / 3600), minutes: Math.round((totalSeconds % 3600) / 60) })
  }
  if (totalSeconds >= 120) return t('collaboration.duration.minutes', { count: Math.round(totalSeconds / 60) })
  return t('collaboration.duration.seconds', { count: totalSeconds })
}
