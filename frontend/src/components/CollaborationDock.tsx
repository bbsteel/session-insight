/**
 * Collaboration dock: the collapsible horizontal strip below the replay
 * header and above the terminal (design §10.4).
 *
 * ReplayView owns the detail fetch, the open/collapsed mode, and the height;
 * this component renders the strip and owns lane selection. The terminal is
 * a sibling below the strip — it stays mounted across every mode change, and
 * because the strip only changes container height (never width), xterm cols
 * are untouched and no replacement /render?cols= request is triggered.
 *
 * Backing-session affordances are gated strictly on the contract
 * backing_session reference — never on agent_type branching.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { CollaborationDetailResponse } from '../api'
import { fetchSession } from '../api'
import { formatDate, useI18n, type Locale } from '../i18n'
import { reasonCodeLabelKey } from '../capabilityPresentation'
import { openOnModifiedClick, openSessionInNewTab } from '../sessionLink'
import { copySessionIdToClipboard } from '../copySessionId'
import {
  normalizeTimelineModel,
  UNLINKED_GROUP_ID,
  type TimelineInvocation,
} from '../collaboration/normalizeTimelineModel'
import {
  backingSessionOf,
  classifyCollaborationError,
  hasInvocation,
  isGraphEmpty,
  summarizeTimeline,
} from '../collaboration/dockState'
import type { FactEvidenceDTO, SourceAnchorDTO, BackingSessionRefDTO } from '../collaboration/types'
import CollaborationTimeline, { type ChildContentActionState } from './CollaborationTimeline'

export type CollaborationDockStatus =
  | { kind: 'loading' }
  | { kind: 'error'; code: string }
  | { kind: 'ready'; detail: CollaborationDetailResponse }

export type CollaborationDockMode = 'collapsed' | 'expanded'

const HEADER_PX = 32
const HANDLE_PX = 6
const BANNER_PX = 26
const DETAIL_MIN_HEIGHT_PX = 164
export const MIN_DOCK_HEIGHT_PX = 120
// Header (32) + handle (6) + timeline chrome (52) + four fixed 28px lanes
// leaves a small viewport margin at the default expansion. This keeps the
// root and three children visible before the user has to scroll.
export const DEFAULT_DOCK_HEIGHT_PX = 216
export const COLLAPSED_DOCK_HEIGHT_PX = 34

interface Props {
  status: CollaborationDockStatus
  mode: CollaborationDockMode
  /** Expanded strip height (header + body + drag handle). */
  heightPx: number
  maxHeightPx: number
  onExpand: () => void
  onCollapse: () => void
  onClose: () => void
  /** Live drag updates; the parent persists on onResizeEnd. */
  onResize: (px: number) => void
  onResizeEnd: () => void
  /** Minimum height required by the visible timeline lanes. */
  onContentHeightChange: (heightPx: number) => void
  onOpenSession: (id: string, agentType: string) => void
  onJumpToLaunch: (invocationId: string, anchor: SourceAnchorDTO | null) => void
  onJumpToResult: (invocationId: string, anchor: SourceAnchorDTO) => void
  /**
   * True while the breadcrumb "back to parent session" shortcut is active.
   * Escape then propagates to ReplayView's window handler so it can return
   * to the parent session instead of being swallowed by the dock.
   */
  returnToParentActive: boolean
}

export default function CollaborationDock({
  status,
  mode,
  heightPx,
  maxHeightPx,
  onExpand,
  onCollapse,
  onClose,
  onResize,
  onResizeEnd,
  onContentHeightChange,
  onOpenSession,
  onJumpToLaunch,
  onJumpToResult,
  returnToParentActive,
}: Props) {
  const { t } = useI18n()
  const detail = status.kind === 'ready' ? status.detail : null
  const model = useMemo(() => (detail ? normalizeTimelineModel(detail) : null), [detail])
  const summary = useMemo(() => (model ? summarizeTimeline(model) : null), [model])

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [invocationMissing, setInvocationMissing] = useState(false)

  // A live re-index can drop the selected invocation: clear the selection and
  // say so instead of showing details for a node that no longer exists.
  useEffect(() => {
    if (!detail || !selectedId || selectedId === UNLINKED_GROUP_ID) return
    if (!hasInvocation(detail, selectedId)) {
      setSelectedId(null)
      setInvocationMissing(true)
    }
  }, [detail, selectedId])

  const handleSelect = useCallback((id: string | null) => {
    setSelectedId(id)
    setInvocationMissing(false)
  }, [])

  const selectedInv = useMemo(() => {
    if (!model || !selectedId) return null
    return model.invocations.find((inv) => inv.id === selectedId) ?? null
  }, [model, selectedId])

  const openBacking = useCallback(
    (invocationId: string, newTab = false) => {
      if (!detail) return
      const backing = backingSessionOf(detail, invocationId)
      if (!backing) return
      if (newTab) openSessionInNewTab(backing.agent_type, backing.session_id)
      else onOpenSession(backing.session_id, backing.agent_type)
    },
    [detail, onOpenSession],
  )

  // The contract gates "View child Agent record" on a backing session: without
  // one there is no standalone record to open, regardless of content precision.
  const childContentState = useCallback(
    (inv: TimelineInvocation): ChildContentActionState =>
      inv.hasBackingSession
        ? { available: true }
        : { available: false, reasonKey: 'collaboration.dock.noBackingSession' },
    [],
  )

  const summaryText = summary
    ? [
        t('collaboration.dock.childCount', { count: summary.childCount }),
        summary.activeCount > 0 ? t('collaboration.dock.activeCount', { count: summary.activeCount }) : null,
        summary.problemCount > 0 ? t('collaboration.dock.problemCount', { count: summary.problemCount }) : null,
      ]
        .filter(Boolean)
        .join(' · ')
    : ''

  // ---- Collapsed bar ---------------------------------------------------------

  if (mode === 'collapsed') {
    return (
      <div
        className="collab-dock flex flex-shrink-0 items-center gap-2 border-b border-[var(--border-default)] bg-[var(--bg-surface)] px-2"
        style={{ height: COLLAPSED_DOCK_HEIGHT_PX }}
        data-testid="collaboration-dock"
        data-state="collapsed"
      >
        <button
          type="button"
          onClick={onExpand}
          className="flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          aria-label={t('collaboration.dock.expand')}
          title={t('collaboration.dock.expand')}
          data-testid="collaboration-dock-expand"
        >
          <span aria-hidden="true">▸</span>
          <span className="font-medium text-[var(--text-primary)]">{t('collaboration.dock.title')}</span>
          <span className="truncate">{summaryText}</span>
        </button>
        <button
          type="button"
          onClick={onClose}
          className="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          aria-label={t('collaboration.dock.close')}
          data-testid="collaboration-dock-close"
        >
          ✕
        </button>
      </div>
    )
  }

  // ---- Expanded strip --------------------------------------------------------

  const showBanner = detail?.state === 'stale' || invocationMissing
  const timelinePx = Math.max(60, heightPx - HEADER_PX - HANDLE_PX - (showBanner ? BANNER_PX : 0))
  const stateAttr =
    status.kind === 'loading'
      ? 'loading'
      : status.kind === 'error'
        ? classifyCollaborationError(status.code)
        : detail && isGraphEmpty(detail)
          ? 'empty'
          : 'ready'

  return (
    <section
      className="collab-dock relative z-20 flex flex-shrink-0 flex-col border-b border-[var(--border-default)] bg-[var(--bg-primary)]"
      style={{ height: heightPx }}
      role="region"
      aria-label={t('collaboration.dock.title')}
      id="collaboration-dock"
      data-testid="collaboration-dock"
      data-state={stateAttr}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          // While the parent-return shortcut is active, let Escape reach
          // ReplayView's window handler (it returns to the parent session);
          // otherwise keep the event dock-local and just close.
          if (!returnToParentActive) e.stopPropagation()
          onClose()
        }
      }}
    >
      <header
        className="flex flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] bg-[var(--bg-surface)] px-2"
        style={{ height: HEADER_PX }}
      >
        <h2 className="text-nav font-medium text-[var(--text-primary)]">{t('collaboration.dock.title')}</h2>
        {summaryText && <span className="truncate text-meta text-[var(--text-muted)]">{summaryText}</span>}
        {selectedInv && (
          <span
            className="truncate rounded-sm bg-[var(--bg-surface-hover)] px-1.5 py-0.5 text-meta text-[var(--text-secondary)]"
            data-testid="collaboration-selected-summary"
          >
            {selectedInv.isGroup ? t('collaboration.unlinkedGroup') : selectedInv.label}
            {' · '}
            {t(`collaboration.status.${selectedInv.status}`)}
          </span>
        )}
        <span className="flex-1" />
        <button
          type="button"
          onClick={onCollapse}
          className="flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          aria-label={t('collaboration.dock.collapsePanel')}
          title={t('collaboration.dock.collapsePanel')}
          data-testid="collaboration-dock-collapse"
        >
          ▾
        </button>
        <button
          type="button"
          onClick={onClose}
          className="flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          aria-label={t('collaboration.dock.close')}
          data-testid="collaboration-dock-close"
        >
          ✕
        </button>
      </header>

      {detail?.state === 'stale' && (
        <p
          className="flex-shrink-0 border-b border-[var(--border-muted)] bg-[color-mix(in_srgb,var(--warning)_10%,transparent)] px-3 py-1 text-meta text-[var(--warning)]"
          role="status"
          data-testid="collaboration-stale-banner"
        >
          {t('capability.reason.stale_graph_retained')}
        </p>
      )}
      {invocationMissing && (
        <p
          className="flex-shrink-0 border-b border-[var(--border-muted)] px-3 py-1 text-meta text-[var(--text-secondary)]"
          role="status"
          data-testid="collaboration-invocation-missing"
        >
          {t('collaboration.dock.invocationMissing')}
        </p>
      )}

      {status.kind === 'loading' && (
        <p className="px-3 py-4 text-nav text-[var(--text-secondary)]" role="status">
          {t('collaboration.dock.loading')}
        </p>
      )}

      {status.kind === 'error' && <ErrorState code={status.code} />}

      {detail && isGraphEmpty(detail) && (
        <p className="px-3 py-4 text-nav text-[var(--text-secondary)]" role="status" data-testid="collaboration-empty">
          {t('collaboration.dock.empty')}
        </p>
      )}

      {detail && !isGraphEmpty(detail) && (
        <div className="min-h-0 flex flex-1">
          <div className="min-w-0 flex-1">
            <CollaborationTimeline
              graph={detail}
              heightPx={timelinePx}
              selectedId={selectedId}
              onSelect={handleSelect}
              onOpenChildContent={openBacking}
              onJumpToLaunch={onJumpToLaunch}
              onJumpToResult={onJumpToResult}
              isChildContentAvailable={childContentState}
              onContentHeightChange={(timelineHeightPx) => onContentHeightChange(Math.max(
                HEADER_PX + HANDLE_PX + (showBanner ? BANNER_PX : 0) + timelineHeightPx,
                selectedInv ? HEADER_PX + HANDLE_PX + (showBanner ? BANNER_PX : 0) + DETAIL_MIN_HEIGHT_PX : 0,
              ))}
            />
          </div>
          {selectedInv && (
            <InvocationDetail
              inv={selectedInv}
              detail={detail}
              onClose={() => handleSelect(null)}
              onOpenBacking={() => openBacking(selectedInv.id)}
            />
          )}
        </div>
      )}

      <ResizeHandle
        heightPx={heightPx}
        maxHeightPx={maxHeightPx}
        onResize={onResize}
        onResizeEnd={onResizeEnd}
      />
    </section>
  )
}

function ResizeHandle({
  heightPx,
  maxHeightPx,
  onResize,
  onResizeEnd,
}: {
  heightPx: number
  maxHeightPx: number
  onResize: (px: number) => void
  onResizeEnd: () => void
}) {
  const { t } = useI18n()
  const dragRef = useRef<{ pointerId: number; startY: number; startHeight: number } | null>(null)

  const clamp = (px: number) => Math.max(MIN_DOCK_HEIGHT_PX, Math.min(maxHeightPx, px))

  return (
    <div
      className="collab-dock-handle flex flex-shrink-0 cursor-row-resize items-center justify-center hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
      style={{ height: HANDLE_PX }}
      role="separator"
      aria-orientation="horizontal"
      aria-label={t('collaboration.dock.dragHandle')}
      title={t('collaboration.dock.dragHandle')}
      tabIndex={0}
      data-testid="collaboration-dock-handle"
      onPointerDown={(e) => {
        if (e.button !== 0) return
        dragRef.current = { pointerId: e.pointerId, startY: e.clientY, startHeight: heightPx }
        e.currentTarget.setPointerCapture(e.pointerId)
      }}
      onPointerMove={(e) => {
        const drag = dragRef.current
        if (!drag || drag.pointerId !== e.pointerId) return
        onResize(clamp(drag.startHeight + (e.clientY - drag.startY)))
      }}
      onPointerUp={(e) => {
        if (dragRef.current?.pointerId !== e.pointerId) return
        dragRef.current = null
        e.currentTarget.releasePointerCapture(e.pointerId)
        onResizeEnd()
      }}
      onPointerCancel={() => {
        dragRef.current = null
      }}
      onKeyDown={(e) => {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault()
          onResize(clamp(heightPx + (e.key === 'ArrowDown' ? 16 : -16)))
          onResizeEnd()
        }
      }}
    >
      <span className="h-0.5 w-10 rounded-full bg-[var(--border-default)]" aria-hidden="true" />
    </div>
  )
}

function ErrorState({ code }: { code: string }) {
  const { t } = useI18n()
  const kind = classifyCollaborationError(code)
  if (kind === 'generic') {
    return (
      <p className="px-3 py-4 text-nav text-[var(--error)]" role="alert" data-testid="collaboration-error">
        {t('collaboration.dock.error')}
      </p>
    )
  }
  const copyKey =
    kind === 'unsupported'
      ? 'collaboration.dock.unsupported'
      : kind === 'not_indexed'
        ? 'collaboration.dock.notIndexed'
        : 'collaboration.dock.sessionMissing'
  return (
    <p className="px-3 py-4 text-nav text-[var(--text-secondary)]" role="status" data-testid={`collaboration-${kind}`}>
      {t(copyKey)}
    </p>
  )
}

function InvocationDetail({
  inv,
  detail,
  onClose,
  onOpenBacking,
}: {
  inv: TimelineInvocation
  detail: CollaborationDetailResponse
  onClose: () => void
  onOpenBacking: () => void
}) {
  const { t, locale } = useI18n()
  if (inv.isGroup) {
    return (
      <aside
        className="w-[280px] flex-shrink-0 overflow-y-auto border-l border-[var(--border-default)] bg-[var(--bg-surface)] px-3 py-2"
        data-testid="collaboration-invocation-detail"
        aria-label={t('collaboration.detail.heading')}
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-nav font-medium text-[var(--text-primary)]">{t('collaboration.unlinkedGroup')}</h3>
          <DetailCloseButton onClose={onClose} />
        </div>
      </aside>
    )
  }

  const backing = backingSessionOf(detail, inv.id)

  return (
    <aside
      className="w-[280px] flex-shrink-0 overflow-y-auto border-l border-[var(--border-default)] bg-[var(--bg-surface)] px-3 py-2"
      data-testid="collaboration-invocation-detail"
      aria-label={t('collaboration.detail.heading')}
    >
      <div className="flex items-center justify-between gap-2">
        <h3 className="truncate text-nav font-medium text-[var(--text-primary)]" title={inv.label}>
          {inv.label}
        </h3>
        <DetailCloseButton onClose={onClose} />
      </div>
      <dl className="mt-1.5 space-y-0.5 text-meta">
        <Row label={t('collaboration.tooltip.status')} value={t(`collaboration.status.${inv.status}`)} />
        <Row label={t('collaboration.detail.agent')} value={inv.agentType} />
        {inv.roleLabel && <Row label={t('collaboration.detail.role')} value={inv.roleLabel} />}
        <Row label={t('collaboration.tooltip.started')} value={timeLabel(locale, inv.startedAtMs, t)} />
        <Row label={t('collaboration.detail.ended')} value={timeLabel(locale, inv.endedAtMs, t)} />
        <Row label={t('collaboration.detail.timePrecision')} value={precisionText(t, inv.timePrecision)} />
        <Row label={t('collaboration.detail.contentPrecision')} value={precisionText(t, inv.contentPrecision)} />
        {inv.executionMode && <Row label={t('collaboration.detail.executionMode')} value={t(`collaboration.mode.${inv.executionMode}`)} />}
        {inv.taskSummary && <Row label={t('collaboration.tooltip.task')} value={inv.taskSummary} title={inv.taskSummary} />}
      </dl>
      {backing && (
        <div className="mt-2 flex items-center gap-1.5 border-t border-[var(--border-muted)] pt-2">
          <button
            type="button"
            onClick={(e) => { if (!openOnModifiedClick(e, backing.agent_type, backing.session_id)) onOpenBacking() }}
            onAuxClick={(e) => { openOnModifiedClick(e, backing.agent_type, backing.session_id) }}
            className="h-7 rounded-md border border-[var(--border-default)] px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            data-testid="collaboration-open-backing"
          >
            {t('collaboration.backing.open')}
          </button>
          <BackingCopyButton backing={backing} />
          <button
            type="button"
            onClick={() => openSessionInNewTab(backing.agent_type, backing.session_id)}
            className="flex h-7 w-7 items-center justify-center rounded-md border border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            aria-label={t('session.openInNewTab')}
            title={t('session.openInNewTab')}
            data-testid="collaboration-open-backing-new-tab"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
              <polyline points="15 3 21 3 21 9" />
              <line x1="10" y1="14" x2="21" y2="3" />
            </svg>
          </button>
        </div>
      )}
    </aside>
  )
}

// BackingCopyButton copies the backed session's CLI-resumable agent ID using
// the same shared logic as the session list (resume_id || id). The
// collaboration payload carries only the backing session id, so the resume id
// is fetched lazily from the session detail and cached per session; on fetch
// failure it falls back to the session id.
function BackingCopyButton({ backing }: { backing: BackingSessionRefDTO }) {
  const { t } = useI18n()
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const resumeCache = useRef(new Map<string, string | null>())
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  const copy = async () => {
    const key = backing.session_id
    let resumeId = resumeCache.current.get(key)
    if (resumeId === undefined) {
      try {
        const detail = await fetchSession(key)
        resumeId = detail.resume_id ?? null
      } catch {
        resumeId = null
      }
      resumeCache.current.set(key, resumeId)
    }
    const ok = await copySessionIdToClipboard({ id: key, resume_id: resumeId })
    setState(ok ? 'copied' : 'failed')
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), 2000)
  }

  const label = state === 'copied'
    ? t('sidebar.copiedSessionId')
    : state === 'failed'
      ? t('sidebar.copyFailed')
      : t('collaboration.backing.copyAgentId')

  return (
    <button
      type="button"
      onClick={() => { void copy() }}
      className={`flex h-7 items-center gap-1 rounded-md border border-[var(--border-default)] px-2 text-nav focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
        state === 'copied'
          ? 'text-[var(--success)]'
          : state === 'failed'
            ? 'text-[var(--error)]'
            : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
      }`}
      aria-label={t('collaboration.backing.copyAgentId')}
      title={label}
      data-testid="collaboration-copy-agent-id"
    >
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
      </svg>
      <span>{label}</span>
    </button>
  )
}

function DetailCloseButton({ onClose }: { onClose: () => void }) {
  const { t } = useI18n()
  return (
    <button
      type="button"
      onClick={onClose}
      className="flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-md text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
      aria-label={t('collaboration.detail.close')}
      data-testid="collaboration-detail-close"
    >
      ✕
    </button>
  )
}

function Row({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="w-24 flex-shrink-0 text-[var(--text-muted)]">{label}</dt>
      <dd className="min-w-0 flex-1 truncate text-[var(--text-primary)]" title={title ?? value}>
        {value}
      </dd>
    </div>
  )
}

function timeLabel(locale: Locale, ms: number | null, t: (key: string) => string): string {
  return ms === null ? t('collaboration.duration.unknown') : formatDate(locale, ms, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

/** "Exact"/"Estimated" plus the contract reason code when one was recorded. */
function precisionText(t: (key: string, vars?: Record<string, string | number>) => string, ev: FactEvidenceDTO): string {
  const base = t(`capability.state.${ev.state}`)
  const reasonKey = reasonCodeLabelKey(ev.reason_code)
  return reasonKey ? `${base} — ${t(reasonKey)}` : base
}
