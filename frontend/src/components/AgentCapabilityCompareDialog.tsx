import { useEffect, useRef } from 'react'
import type { AgentInfo } from '../types'
import {
  BASELINE_CAPABILITY_IDS,
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
  agents: AgentInfo[]
  onClose: () => void
}

export default function AgentCapabilityCompareDialog({ open, agents, onClose }: Props) {
  const { t } = useI18n()
  const closeRef = useRef<HTMLButtonElement>(null)

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

  const sorted = [...agents].sort((a, b) => a.type.localeCompare(b.type))

  return (
    <div
      className="fixed inset-0 z-[420] flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="agent-cap-compare-title"
      data-testid="agent-capability-compare"
    >
      <div
        className="flex max-h-[min(720px,90vh)] w-[min(1100px,96vw)] flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border-default)] px-4 py-3">
          <div>
            <h2 id="agent-cap-compare-title" className="text-body font-semibold text-[var(--text-primary)]">
              {t('capability.compare.title')}
            </h2>
            <p className="mt-0.5 text-meta text-[var(--text-muted)]">{t('capability.compare.subtitle')}</p>
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            aria-label={t('common.close')}
          >
            <CloseIcon className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          <table className="w-max min-w-full border-collapse text-helper">
            <thead>
              <tr className="bg-[var(--bg-surface)]">
                <th
                  scope="col"
                  className="sticky left-0 top-0 z-20 border-b border-r border-[var(--border-default)] bg-[var(--bg-surface)] px-3 py-2 text-left font-medium text-[var(--text-secondary)]"
                >
                  {t('capability.compare.capabilityColumn')}
                </th>
                {sorted.map(agent => (
                  <th
                    key={agent.type}
                    scope="col"
                    className="sticky top-0 z-10 border-b border-[var(--border-default)] bg-[var(--bg-surface)] px-3 py-2 text-center font-medium text-[var(--text-primary)]"
                  >
                    <div className="flex flex-col items-center gap-1">
                      <AgentIcon agentType={agent.type} className="h-5 w-5" />
                      <span className="max-w-[7rem] truncate">{agent.display_name || agent.type}</span>
                      {!agent.discovered && (
                        <span className="text-meta font-normal text-[var(--text-muted)]">
                          {t('capability.agents.notDetected')}
                        </span>
                      )}
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {BASELINE_CAPABILITY_IDS.map(id => (
                <tr key={id} className="border-b border-[var(--border-muted)]">
                  <th
                    scope="row"
                    className="sticky left-0 z-10 border-r border-[var(--border-default)] bg-[var(--bg-surface)] px-3 py-2 text-left font-normal text-[var(--text-primary)]"
                    title={t(capabilityDescriptionKey(id), capabilityIdI18nVars(id))}
                  >
                    <div className="font-medium">{t(capabilityLabelKey(id), capabilityIdI18nVars(id))}</div>
                    <div className="max-w-[12rem] text-meta text-[var(--text-muted)] line-clamp-2">
                      {t(capabilityDescriptionKey(id), capabilityIdI18nVars(id))}
                    </div>
                  </th>
                  {sorted.map(agent => {
                    const decl = agent.capabilities?.[id]
                    return (
                      <td key={agent.type} className="px-3 py-2 text-center">
                        {decl ? (
                          <CapabilityStateIndicator
                            state={decl.state}
                            reasonCode={decl.reason_code}
                            showLabel
                          />
                        ) : (
                          <span className="text-meta text-[var(--text-muted)]">{t('capability.unavailable')}</span>
                        )}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="border-t border-[var(--border-default)] px-4 py-2 text-meta text-[var(--text-muted)]">
          {t('capability.compare.legend')}
        </div>
      </div>
    </div>
  )
}
