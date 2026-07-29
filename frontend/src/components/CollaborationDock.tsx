/**
 * Collaboration dock: the right-side panel that surfaces the frozen
 * collaboration contract for the selected root session.
 *
 * Owns the dock-level state machine (loading / ready / empty / unsupported /
 * not-indexed / session-missing / error) and selection; the timeline itself is
 * the production CollaborationTimeline component, fed the detail payload
 * directly (normalizeTimelineModel tolerates the state/time_range/validation
 * extras). Backing-session affordances are gated strictly on the contract
 * backing_session reference — never on agent_type branching.
 *
 * The dock is a sibling of the replay view in the App shell; closing it never
 * unmounts the replay or terminal. App remounts the dock per session
 * (key={agent:session}), so a session switch never flashes the previous
 * session's graph.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  APIError,
  fetchCollaborationDetail,
  watchSessionsChanged,
  type CollaborationDetailResponse,
} from '../api'
import { formatDate, useI18n, type Locale } from '../i18n'
import { reasonCodeLabelKey } from '../capabilityPresentation'
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
} from '../collaboration/dockState'
import type { FactEvidenceDTO } from '../collaboration/types'
import CollaborationTimeline, { type ChildContentActionState } from './CollaborationTimeline'

const TIMELINE_HEIGHT_PX = 280

type DockData =
  | { kind: 'loading' }
  | { kind: 'error'; code: string }
  | { kind: 'ready'; detail: CollaborationDetailResponse }

interface Props {
  sessionId: string
  agentType: string
  onClose: () => void
  /** Opens a backing session in the main view (session switch). */
  onOpenSession: (id: string, agentType: string) => void
}

export default function CollaborationDock({ sessionId, agentType, onClose, onOpenSession }: Props) {
  const { t, locale } = useI18n()
  const [data, setData] = useState<DockData>({ kind: 'loading' })
  const [reloadToken, setReloadToken] = useState(0)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [invocationMissing, setInvocationMissing] = useState(false)
  const etagRef = useRef<string | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)

  // Initial load and manual retry. A remount per session (App key) means this
  // always starts from the loading state — no cross-session flash.
  useEffect(() => {
    const ctrl = new AbortController()
    setData({ kind: 'loading' })
    fetchCollaborationDetail(sessionId, agentType, { signal: ctrl.signal })
      .then((res) => {
        if (res === 'not-modified') return
        etagRef.current = res.etag
        setData({ kind: 'ready', detail: res.detail })
      })
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        setData({ kind: 'error', code: err instanceof APIError ? err.code : 'request_failed' })
      })
    return () => ctrl.abort()
  }, [sessionId, agentType, reloadToken])

  // Conditional live refetch on the backend session-change ping. A 304 keeps
  // the mounted graph untouched (no timeline reset); a failure keeps the
  // current state — the next ping retries.
  useEffect(() => {
    return watchSessionsChanged(() => {
      fetchCollaborationDetail(sessionId, agentType, { etag: etagRef.current })
        .then((res) => {
          if (res === 'not-modified') return
          etagRef.current = res.etag
          setData({ kind: 'ready', detail: res.detail })
        })
        .catch(() => {})
    })
  }, [sessionId, agentType])

  // A live re-index can drop the selected invocation: clear the selection and
  // say so instead of showing details for a node that no longer exists.
  useEffect(() => {
    if (data.kind !== 'ready' || !selectedId || selectedId === UNLINKED_GROUP_ID) return
    if (!hasInvocation(data.detail, selectedId)) {
      setSelectedId(null)
      setInvocationMissing(true)
    }
  }, [data, selectedId])

  // Move focus into the dock when it opens so Escape/keyboard use is immediate.
  useEffect(() => {
    closeButtonRef.current?.focus()
  }, [])

  const handleSelect = useCallback((id: string | null) => {
    setSelectedId(id)
    setInvocationMissing(false)
  }, [])

  const detail = data.kind === 'ready' ? data.detail : null
  const model = useMemo(() => (detail ? normalizeTimelineModel(detail) : null), [detail])
  const selectedInv = useMemo(() => {
    if (!model || !selectedId) return null
    return model.invocations.find((inv) => inv.id === selectedId) ?? null
  }, [model, selectedId])

  const openBacking = useCallback(
    (invocationId: string) => {
      if (!detail) return
      const backing = backingSessionOf(detail, invocationId)
      if (backing) onOpenSession(backing.session_id, backing.agent_type)
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

  const stateAttr =
    data.kind === 'loading'
      ? 'loading'
      : data.kind === 'error'
        ? classifyCollaborationError(data.code)
        : detail && isGraphEmpty(detail)
          ? 'empty'
          : 'ready'

  return (
    <aside
      className="collab-dock flex h-full w-[400px] flex-shrink-0 flex-col border-l border-[var(--border-default)] bg-[var(--bg-primary)]"
      role="complementary"
      aria-label={t('collaboration.dock.title')}
      id="collaboration-dock"
      data-testid="collaboration-dock"
      data-state={stateAttr}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.stopPropagation()
          onClose()
        }
      }}
    >
      <header className="flex h-10 flex-shrink-0 items-center justify-between border-b border-[var(--border-default)] bg-[var(--bg-surface)] px-3">
        <h2 className="text-nav font-medium text-[var(--text-primary)]">{t('collaboration.dock.title')}</h2>
        <button
          type="button"
          ref={closeButtonRef}
          onClick={onClose}
          className="flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          aria-label={t('collaboration.dock.close')}
          data-testid="collaboration-dock-close"
        >
          ✕
        </button>
      </header>

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {detail?.state === 'stale' && (
          <p
            className="flex-shrink-0 border-b border-[var(--border-muted)] bg-[color-mix(in_srgb,var(--warning)_10%,transparent)] px-3 py-1.5 text-meta text-[var(--warning)]"
            role="status"
            data-testid="collaboration-stale-banner"
          >
            {t('capability.reason.stale_graph_retained')}
          </p>
        )}
        {invocationMissing && (
          <p
            className="flex-shrink-0 border-b border-[var(--border-muted)] px-3 py-1.5 text-meta text-[var(--text-secondary)]"
            role="status"
            data-testid="collaboration-invocation-missing"
          >
            {t('collaboration.dock.invocationMissing')}
          </p>
        )}

        {data.kind === 'loading' && (
          <p className="px-3 py-4 text-nav text-[var(--text-secondary)]" role="status">
            {t('collaboration.dock.loading')}
          </p>
        )}

        {data.kind === 'error' && <ErrorState code={data.code} onRetry={() => setReloadToken((n) => n + 1)} />}

        {detail && isGraphEmpty(detail) && (
          <p className="px-3 py-4 text-nav text-[var(--text-secondary)]" data-testid="collaboration-empty">
            {t('collaboration.dock.empty')}
          </p>
        )}

        {detail && !isGraphEmpty(detail) && (
          <>
            <div className="flex-shrink-0 border-b border-[var(--border-default)]">
              <CollaborationTimeline
                graph={detail}
                heightPx={TIMELINE_HEIGHT_PX}
                labelWidthPx={160}
                selectedId={selectedId}
                onSelect={handleSelect}
                onOpenChildContent={openBacking}
                isChildContentAvailable={childContentState}
              />
            </div>
            {selectedInv ? (
              <InvocationDetail
                inv={selectedInv}
                detail={detail}
                locale={locale}
                onOpenBacking={() => openBacking(selectedInv.id)}
              />
            ) : (
              <p className="px-3 py-3 text-meta text-[var(--text-muted)]">
                {t('collaboration.dock.selectHint')}
              </p>
            )}
          </>
        )}
      </div>
    </aside>
  )
}

function ErrorState({ code, onRetry }: { code: string; onRetry: () => void }) {
  const { t } = useI18n()
  const kind = classifyCollaborationError(code)
  if (kind === 'generic') {
    return (
      <div className="flex items-center gap-2 px-3 py-4" data-testid="collaboration-error">
        <p className="text-nav text-[var(--error)]" role="alert">
          {t('collaboration.dock.error')}
        </p>
        <button
          type="button"
          onClick={onRetry}
          className="h-7 rounded-md border border-[var(--border-default)] px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
        >
          {t('common.retry')}
        </button>
      </div>
    )
  }
  const copyKey =
    kind === 'unsupported'
      ? 'collaboration.dock.unsupported'
      : kind === 'not_indexed'
        ? 'collaboration.dock.notIndexed'
        : 'collaboration.dock.sessionMissing'
  return (
    <p className="px-3 py-4 text-nav text-[var(--text-secondary)]" data-testid={`collaboration-${kind}`}>
      {t(copyKey)}
    </p>
  )
}

function InvocationDetail({
  inv,
  detail,
  locale,
  onOpenBacking,
}: {
  inv: TimelineInvocation
  detail: CollaborationDetailResponse
  locale: Locale
  onOpenBacking: () => void
}) {
  const { t } = useI18n()
  if (inv.isGroup) {
    return (
      <section className="px-3 py-3" data-testid="collaboration-invocation-detail" aria-label={t('collaboration.detail.heading')}>
        <h3 className="text-nav font-medium text-[var(--text-primary)]">{t('collaboration.unlinkedGroup')}</h3>
      </section>
    )
  }

  const backing = backingSessionOf(detail, inv.id)
  const timeLabel = (ms: number | null) =>
    ms === null ? t('collaboration.duration.unknown') : formatDate(locale, ms, { hour: '2-digit', minute: '2-digit', second: '2-digit' })

  return (
    <section className="px-3 py-3" data-testid="collaboration-invocation-detail" aria-label={t('collaboration.detail.heading')}>
      <h3 className="truncate text-nav font-medium text-[var(--text-primary)]" title={inv.label}>
        {inv.label}
      </h3>
      <dl className="mt-2 space-y-1 text-meta">
        <Row label={t('collaboration.tooltip.status')} value={t(`collaboration.status.${inv.status}`)} />
        <Row label={t('collaboration.detail.agent')} value={inv.agentType} />
        {inv.roleLabel && <Row label={t('collaboration.detail.role')} value={inv.roleLabel} />}
        <Row label={t('collaboration.tooltip.started')} value={timeLabel(inv.startedAtMs)} />
        <Row label={t('collaboration.detail.ended')} value={timeLabel(inv.endedAtMs)} />
        <Row label={t('collaboration.detail.timePrecision')} value={precisionText(t, inv.timePrecision)} />
        <Row label={t('collaboration.detail.contentPrecision')} value={precisionText(t, inv.contentPrecision)} />
        {inv.executionMode && <Row label={t('collaboration.detail.executionMode')} value={t(`collaboration.mode.${inv.executionMode}`)} />}
        {inv.taskSummary && <Row label={t('collaboration.tooltip.task')} value={inv.taskSummary} title={inv.taskSummary} />}
      </dl>
      {backing && (
        <div className="mt-3 border-t border-[var(--border-muted)] pt-3">
          <h4 className="text-meta font-medium text-[var(--text-secondary)]">{t('collaboration.backing.heading')}</h4>
          <p className="mt-1 text-meta text-[var(--text-muted)]">{t('collaboration.backing.hint')}</p>
          <button
            type="button"
            onClick={onOpenBacking}
            className="mt-2 h-7 rounded-md border border-[var(--border-default)] px-2 text-nav text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            data-testid="collaboration-open-backing"
          >
            {t('collaboration.backing.open')}
          </button>
        </div>
      )}
    </section>
  )
}

function Row({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="w-28 flex-shrink-0 text-[var(--text-muted)]">{label}</dt>
      <dd className="min-w-0 flex-1 truncate text-[var(--text-primary)]" title={title ?? value}>
        {value}
      </dd>
    </div>
  )
}

/** "Exact"/"Estimated" plus the contract reason code when one was recorded. */
function precisionText(t: (key: string, vars?: Record<string, string | number>) => string, ev: FactEvidenceDTO): string {
  const base = t(`capability.state.${ev.state}`)
  const reasonKey = reasonCodeLabelKey(ev.reason_code)
  return reasonKey ? `${base} — ${t(reasonKey)}` : base
}
