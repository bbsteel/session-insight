import { useEffect, useState } from 'react'
import { fetchAgents } from '../api'
import type { AgentInfo } from '../types'
import {
  orderedStaticCapabilities,
  summarizeStaticAgent,
  capabilityLabelKey,
  capabilityDescriptionKey,
} from '../capabilityPresentation'
import CapabilityStateIndicator from './CapabilityStateIndicator'
import AgentCapabilitySummary from './AgentCapabilitySummary'
import AgentCapabilityCompareDialog from './AgentCapabilityCompareDialog'
import AgentIcon from './AgentIcon'
import { useI18n } from '../i18n'

interface Props {
  sectionBox: string
  sectionTitle: string
  sectionDesc: string
  btnCls: string
  primaryBtnCls: string
}

export default function SettingsAgentsTab({
  sectionBox,
  sectionTitle,
  sectionDesc,
  btnCls,
  primaryBtnCls,
}: Props) {
  const { t } = useI18n()
  const [agents, setAgents] = useState<AgentInfo[] | null>(null)
  const [error, setError] = useState(false)
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState<string | null>(null)
  const [compareOpen, setCompareOpen] = useState(false)

  const load = () => {
    setLoading(true)
    setError(false)
    fetchAgents()
      .then(list => {
        setAgents(list)
        if (list.length && !selected) setSelected(list[0].type)
      })
      .catch(() => {
        setAgents(null)
        setError(true)
      })
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- load once on mount
  }, [])

  const selectedAgent = agents?.find(a => a.type === selected) ?? null

  return (
    <div className="space-y-4" data-testid="settings-agents-tab">
      <div className={sectionBox}>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div>
            <div className={sectionTitle}>{t('capability.agents.title')}</div>
            <p className={sectionDesc}>{t('capability.agents.help')}</p>
          </div>
          <button
            type="button"
            className={primaryBtnCls}
            onClick={() => setCompareOpen(true)}
            disabled={!agents?.length}
            data-testid="settings-agents-compare"
          >
            {t('capability.compare.open')}
          </button>
        </div>

        {loading && (
          <p className="mt-3 text-helper text-[var(--text-muted)]">{t('common.loading')}</p>
        )}
        {error && (
          <div className="mt-3 flex items-center gap-2 text-helper text-[var(--error)]" role="alert">
            <span>{t('capability.agents.loadError')}</span>
            <button type="button" className={btnCls} onClick={load}>
              {t('common.retry')}
            </button>
          </div>
        )}

        {agents && (
          <ul className="mt-3 space-y-1.5" data-testid="settings-agents-list">
            {agents.map(agent => {
              const summary = summarizeStaticAgent(agent.capabilities)
              const isSel = agent.type === selected
              return (
                <li key={agent.type}>
                  <button
                    type="button"
                    onClick={() => setSelected(agent.type)}
                    className={`flex w-full items-center gap-3 rounded-lg border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${
                      isSel
                        ? 'border-[var(--accent-blue)] bg-[color-mix(in_srgb,var(--accent-blue)_8%,transparent)]'
                        : 'border-[var(--border-muted)] hover:bg-[var(--bg-surface-hover)]'
                    }`}
                    aria-current={isSel ? 'true' : undefined}
                    data-testid={`settings-agent-${agent.type}`}
                  >
                    <AgentIcon agentType={agent.type} className="h-6 w-6 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-helper font-medium text-[var(--text-primary)]">
                          {agent.display_name || agent.type}
                        </span>
                        {agent.discovered ? (
                          <span className="text-meta text-[var(--accent-green)]">
                            {t('capability.agents.detected')}
                          </span>
                        ) : (
                          <span className="text-meta text-[var(--text-muted)]">
                            {t('capability.agents.notDetected')}
                          </span>
                        )}
                      </div>
                      <div className="mt-0.5 flex flex-wrap items-center gap-2 text-meta text-[var(--text-muted)]">
                        {agent.discovered && (
                          <span>{t('capability.agents.sessionCount', { n: agent.session_count })}</span>
                        )}
                        {agent.adapter_revision != null && (
                          <span>{t('capability.agents.adapterRevision', { n: agent.adapter_revision })}</span>
                        )}
                        <AgentCapabilitySummary summary={summary} />
                      </div>
                    </div>
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      {selectedAgent && (
        <div className={sectionBox} data-testid="settings-agent-detail">
          <div className={sectionTitle}>
            {t('capability.agents.detailTitle', { name: selectedAgent.display_name || selectedAgent.type })}
          </div>
          <p className={sectionDesc}>{t('capability.agents.detailHelp')}</p>
          {!selectedAgent.capabilities ? (
            <p className="mt-2 text-helper text-[var(--text-muted)]">{t('capability.unavailable')}</p>
          ) : (
            <ul className="mt-3 space-y-1.5">
              {orderedStaticCapabilities(selectedAgent.capabilities).map(({ id, decl }) => (
                <li
                  key={id}
                  className="flex items-start justify-between gap-2 rounded-md border border-[var(--border-muted)] px-2.5 py-2"
                  data-testid={`settings-cap-${id}`}
                >
                  <div className="min-w-0">
                    <div className="text-helper font-medium text-[var(--text-primary)]">
                      {t(capabilityLabelKey(id))}
                    </div>
                    <div className="text-meta text-[var(--text-muted)]">
                      {t(capabilityDescriptionKey(id))}
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
      )}

      <AgentCapabilityCompareDialog
        open={compareOpen}
        agents={agents ?? []}
        onClose={() => setCompareOpen(false)}
      />
    </div>
  )
}
