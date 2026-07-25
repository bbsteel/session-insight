import { useEffect, useRef } from 'react'
import type { AgentInfo, SessionDetail } from '../types'
import {
  actionAvailabilityLabelKey,
  actionRows,
  capabilityDescriptionKey,
  capabilityLabelKey,
  capabilityStateLabelKey,
  livenessPresentation,
  orderedSessionStatuses,
  reasonCodeLabelKey,
  summarizeSessionCaps,
} from '../capabilityPresentation'
import CapabilityStateIndicator from './CapabilityStateIndicator'
import AgentCapabilitySummary from './AgentCapabilitySummary'
import AgentIcon from './AgentIcon'
import { useI18n } from '../i18n'
import { CloseIcon } from './icons'

interface Props {
  open: boolean
  session: SessionDetail
  /** Static Agent declaration from GET /api/agents when available. */
  agentInfo?: AgentInfo | null
  onClose: () => void
  onOpenCompare?: () => void
  onOpenSettingsAgents?: () => void
}

export default function SessionCapabilityPanel({
  open,
  session,
  agentInfo,
  onClose,
  onOpenCompare,
  onOpenSettingsAgents,
}: Props) {
  const { t } = useI18n()
  const closeRef = useRef<HTMLButtonElement>(null)
  const caps = session.agent_capabilities
  const summary = summarizeSessionCaps(caps)
  const live = livenessPresentation(caps?.liveness)

  useEffect(() => {
    if (!open) return
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-[410] flex justify-end bg-black/40"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="session-cap-panel-title"
      data-testid="session-capability-panel"
    >
      <aside
        className="flex h-full w-[min(420px,100vw)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-2 border-b border-[var(--border-default)] px-4 py-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <AgentIcon agentType={session.agent_type} className="h-5 w-5 shrink-0" />
              <h2 id="session-cap-panel-title" className="truncate text-body font-semibold text-[var(--text-primary)]">
                {agentInfo?.display_name || session.agent_type}
              </h2>
            </div>
            <p className="mt-1 text-meta text-[var(--text-muted)]">{t('capability.session.subtitle')}</p>
            <div className="mt-1">
              <AgentCapabilitySummary summary={summary} />
            </div>
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            aria-label={t('common.close')}
          >
            <CloseIcon className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-3">
          {!caps ? (
            <div className="rounded-lg border border-[var(--border-muted)] bg-[var(--bg-inset)] p-3 text-helper text-[var(--text-secondary)]" role="status">
              {t('capability.session.unavailable')}
            </div>
          ) : (
            <>
              <section>
                <h3 className="mb-2 text-helper font-medium text-[var(--text-primary)]">
                  {t('capability.session.currentStatus')}
                </h3>
                <ul className="space-y-2">
                  {orderedSessionStatuses(caps.status).map(({ id, status }) => {
                    const staticDecl = agentInfo?.capabilities?.[id]
                    return (
                      <li
                        key={id}
                        className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] px-2.5 py-2"
                        data-testid={`session-cap-row-${id}`}
                      >
                        <div className="flex items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="text-helper font-medium text-[var(--text-primary)]">
                              {t(capabilityLabelKey(id))}
                            </div>
                            <div className="text-meta text-[var(--text-muted)]">
                              {t(capabilityDescriptionKey(id))}
                            </div>
                          </div>
                          {status ? (
                            <CapabilityStateIndicator
                              state={status.state}
                              reasonCode={status.reason_code}
                              showLabel
                            />
                          ) : (
                            <span className="text-meta text-[var(--text-muted)]">{t('capability.unavailable')}</span>
                          )}
                        </div>
                        {status && staticDecl && status.state !== staticDecl.state && (
                          <div className="mt-1.5 space-y-0.5 border-t border-[var(--border-muted)] pt-1.5 text-meta text-[var(--text-secondary)]">
                            <div>
                              <span className="text-[var(--text-muted)]">{t('capability.session.currentLabel')}: </span>
                              {t(capabilityStateLabelKey(status.state))}
                              {status.reason_code && reasonCodeLabelKey(status.reason_code) && (
                                <> — {t(reasonCodeLabelKey(status.reason_code)!, status.reason_code ? { code: status.reason_code } : undefined)}</>
                              )}
                            </div>
                            <div>
                              <span className="text-[var(--text-muted)]">{t('capability.session.agentSupportLabel')}: </span>
                              {t(capabilityStateLabelKey(staticDecl.state))}
                            </div>
                          </div>
                        )}
                      </li>
                    )
                  })}
                </ul>
              </section>

              <section>
                <h3 className="mb-2 text-helper font-medium text-[var(--text-primary)]">
                  {t('capability.liveness.title')}
                </h3>
                <div className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] px-2.5 py-2 text-helper">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-[var(--text-primary)]">
                      {live.isLive ? t('capability.liveness.live') : t('capability.liveness.notLive')}
                    </span>
                    <CapabilityStateIndicator
                      state={caps.liveness?.state}
                      reasonCode={caps.liveness?.reason_code}
                      showLabel
                    />
                  </div>
                  <p className="mt-1 text-meta text-[var(--text-muted)]">{t(live.qualityKey)}</p>
                  {live.reasonKey && (
                    <p className="mt-0.5 text-meta text-[var(--text-secondary)]">
                      {t(live.reasonKey, caps.liveness?.reason_code ? { code: caps.liveness.reason_code } : undefined)}
                    </p>
                  )}
                </div>
              </section>

              <section>
                <h3 className="mb-2 text-helper font-medium text-[var(--text-primary)]">
                  {t('capability.actions.title')}
                </h3>
                <ul className="space-y-1.5">
                  {actionRows(caps.actions).map(({ id, action }) => (
                    <li
                      key={id}
                      className="flex items-center justify-between gap-2 rounded-md border border-[var(--border-muted)] px-2.5 py-2 text-helper"
                      data-testid={`session-action-${id}`}
                    >
                      <span className="text-[var(--text-primary)]">{t(capabilityLabelKey(id))}</span>
                      <span className="text-right text-meta text-[var(--text-secondary)]">
                        {t(actionAvailabilityLabelKey(action.availability))}
                        {action.reason_code && reasonCodeLabelKey(action.reason_code) && (
                          <span className="mt-0.5 block text-[var(--text-muted)]">
                            {t(reasonCodeLabelKey(action.reason_code)!, { code: action.reason_code })}
                          </span>
                        )}
                      </span>
                    </li>
                  ))}
                </ul>
                <p className="mt-2 text-meta text-[var(--text-muted)]">{t('capability.actions.advisoryNote')}</p>
              </section>
            </>
          )}
        </div>

        <div className="flex flex-wrap gap-2 border-t border-[var(--border-default)] px-4 py-3">
          {onOpenCompare && (
            <button
              type="button"
              onClick={onOpenCompare}
              className="h-8 rounded-md border border-[var(--border-default)] px-3 text-helper text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              {t('capability.compare.open')}
            </button>
          )}
          {onOpenSettingsAgents && (
            <button
              type="button"
              onClick={onOpenSettingsAgents}
              className="h-8 rounded-md border border-[var(--accent-blue)] px-3 text-helper font-medium text-[var(--accent-blue)] hover:bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              {t('capability.agents.openSettings')}
            </button>
          )}
        </div>
      </aside>
    </div>
  )
}
