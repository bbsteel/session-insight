import { useEffect, useRef } from 'react'
import type { AgentInfo } from '../types'
import {
  orderedStaticCapabilities,
  capabilityLabelKey,
  capabilityDescriptionKey,
  capabilityIdI18nVars,
} from '../capabilityPresentation'
import CapabilityStateIndicator from './CapabilityStateIndicator'
import AgentIcon from './AgentIcon'
import { useI18n } from '../i18n'
import { CloseIcon } from './icons'

interface Props {
  open: boolean
  agent: AgentInfo | null
  onClose: () => void
}

/** Dedicated dialog for one Agent's ten static capability declarations. */
export default function AgentCapabilityDetailDialog({ open, agent, onClose }: Props) {
  const { t } = useI18n()
  const closeRef = useRef<HTMLButtonElement>(null)
  const restoreFocusRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!open) return
    restoreFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        e.stopImmediatePropagation()
        onClose()
      }
    }
    // Capture phase so Settings (and other parents) do not also close on Escape.
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      const prev = restoreFocusRef.current
      restoreFocusRef.current = null
      // Restore focus to the agent row that opened this dialog.
      if (prev && document.contains(prev)) {
        prev.focus({ preventScroll: true })
      }
    }
  }, [open, onClose])

  if (!open || !agent) return null

  const name = agent.display_name || agent.type

  return (
    <div
      className="fixed inset-0 z-[420] flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="agent-cap-detail-title"
      data-testid="settings-agent-detail"
    >
      <div
        className="flex max-h-[min(720px,90vh)] w-[min(480px,96vw)] flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-2 border-b border-[var(--border-default)] px-4 py-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <AgentIcon agentType={agent.type} className="h-5 w-5 shrink-0" />
              <h2 id="agent-cap-detail-title" className="truncate text-body font-semibold text-[var(--text-primary)]">
                {t('capability.agents.detailTitle', { name })}
              </h2>
            </div>
            <p className="mt-1 text-meta text-[var(--text-muted)]">{t('capability.agents.detailHelp')}</p>
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

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {!agent.capabilities ? (
            <p className="text-helper text-[var(--text-muted)]">{t('capability.unavailable')}</p>
          ) : (
            <ul className="space-y-1.5">
              {orderedStaticCapabilities(agent.capabilities).map(({ id, decl }) => (
                <li
                  key={id}
                  className="flex items-start justify-between gap-2 rounded-md border border-[var(--border-muted)] px-2.5 py-2"
                  data-testid={`settings-cap-${id}`}
                >
                  <div className="min-w-0">
                    <div className="text-helper font-medium text-[var(--text-primary)]">
                      {t(capabilityLabelKey(id), capabilityIdI18nVars(id))}
                    </div>
                    <div className="text-meta text-[var(--text-muted)]">
                      {t(capabilityDescriptionKey(id), capabilityIdI18nVars(id))}
                    </div>
                  </div>
                  {decl ? (
                    <CapabilityStateIndicator state={decl.state} reasonCode={decl.reason_code} showLabel />
                  ) : (
                    <span className="text-meta text-[var(--text-muted)]">{t('capability.unavailable')}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
