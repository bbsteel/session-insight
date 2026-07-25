import { useEffect, useState } from 'react'
import { fetchAgents } from '../api'
import type { AgentInfo } from '../types'
import { summarizeStaticAgent } from '../capabilityPresentation'
import AgentCapabilitySummary from './AgentCapabilitySummary'
import AgentCapabilityCompareDialog from './AgentCapabilityCompareDialog'
import AgentCapabilityDetailDialog from './AgentCapabilityDetailDialog'
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
  const [detailType, setDetailType] = useState<string | null>(null)
  const [compareOpen, setCompareOpen] = useState(false)

  const load = () => {
    setLoading(true)
    setError(false)
    fetchAgents()
      .then(list => {
        setAgents(list)
      })
      .catch(() => {
        setAgents(null)
        setError(true)
      })
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
  }, [])

  const detailAgent = agents?.find(a => a.type === detailType) ?? null

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
              return (
                <li key={agent.type}>
                  <button
                    type="button"
                    onClick={() => setDetailType(agent.type)}
                    className="flex w-full items-center gap-3 rounded-lg border border-[var(--border-muted)] px-3 py-2 text-left transition-colors hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
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

      <AgentCapabilityDetailDialog
        open={detailType != null}
        agent={detailAgent}
        onClose={() => setDetailType(null)}
      />
      <AgentCapabilityCompareDialog
        open={compareOpen}
        agents={agents ?? []}
        onClose={() => setCompareOpen(false)}
      />
    </div>
  )
}
