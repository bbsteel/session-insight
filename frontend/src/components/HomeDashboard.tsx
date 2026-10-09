import { useCallback, useEffect, useState, type MouseEvent } from 'react'
import {
  fetchCodingQuotas,
  fetchHome,
  watchSessionsChanged,
  type CodingQuotaResponse,
  type HomeQuery,
  type HomeReport,
  type HomeSessionCard,
} from '../api'
import { formatDate, formatNumber, useI18n } from '../i18n'
import { openOnModifiedClick } from '../sessionLink'
import { formatRelativeTime, getAgentLabel } from '../sidebarRows'
import AgentIcon from './AgentIcon'
import GlobalSearch from './GlobalSearch'

interface HomeDashboardProps {
  query: HomeQuery
  onQueryChange: (query: HomeQuery) => void
  onSelect?: (sessionId: string, agentType?: string) => void
  onOpenCodingQuotas?: () => void
}

const HEALTH = [
  ['tool_failure', 'tool_failures'],
  ['duration_spike', 'duration_spikes'],
  ['continuation_nudge', 'continuation_nudges'],
  ['missing_shutdown', 'missing_shutdowns'],
] as const

export default function HomeDashboard({ query, onQueryChange, onSelect, onOpenCodingQuotas }: HomeDashboardProps) {
  const { t, locale } = useI18n()
  const [report, setReport] = useState<HomeReport | null>(null)
  const [quotas, setQuotas] = useState<CodingQuotaResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [toolsExpanded, setToolsExpanded] = useState(false)
  const [skillsExpanded, setSkillsExpanded] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setError(false)
    fetchHome(query)
      .then(next => {
        setReport(next)
        setLoading(false)
      })
      .catch(() => {
        setError(true)
        setLoading(false)
      })
  }, [query])

  useEffect(() => { load() }, [load])
  useEffect(() => {
    fetchCodingQuotas().then(setQuotas).catch(() => setQuotas(null))
  }, [])
  useEffect(() => watchSessionsChanged(load), [load])

  const openSession = (event: MouseEvent, session: HomeSessionCard) => {
    if (openOnModifiedClick(event, session.agent_type, session.id)) return
    onSelect?.(session.id, session.agent_type)
  }

  const toggleFocus = (kind: HomeQuery['focusKind'], value: string) => {
    if (query.focusKind === kind && query.focusValue === value) {
      onQueryChange({ ...query, focusKind: '', focusValue: '' })
      return
    }
    onQueryChange({ ...query, focusKind: kind, focusValue: value })
  }

  const filtersActive = Boolean(query.day || query.project || query.agent || query.focusKind)

  return (
    <main className="flex min-w-[360px] flex-1 flex-col bg-[var(--bg-surface)]" data-testid="session-home">
      <GlobalSearch onSelect={onSelect} onOpenCodingQuotas={onOpenCodingQuotas} />
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <h2 className="mr-2 text-nav font-semibold text-[var(--text-primary)]">{t('home.title')}</h2>
          <WindowButton active={query.windowDays === 7 && !query.day} label={t('home.days7')} onClick={() => onQueryChange({ ...query, windowDays: 7, day: '' })} />
          <WindowButton active={query.windowDays === 30 && !query.day} label={t('home.days30')} onClick={() => onQueryChange({ ...query, windowDays: 30, day: '' })} />
          {query.day && <FilterChip label={t('home.filterDay', { day: query.day })} onClick={() => onQueryChange({ ...query, day: '' })} />}
          {query.project && <FilterChip label={t('home.filterProject', { name: query.project })} onClick={() => onQueryChange({ ...query, project: '' })} />}
          {query.agent && <FilterChip label={t('home.filterAgent', { name: getAgentLabel(query.agent) })} onClick={() => onQueryChange({ ...query, agent: '' })} />}
          {query.focusKind && query.focusValue && (
            <FilterChip label={t('home.filterFocus', { name: focusLabel(t, query.focusKind, query.focusValue) })} onClick={() => onQueryChange({ ...query, focusKind: '', focusValue: '' })} />
          )}
          {filtersActive && (
            <button type="button" className="text-helper text-[var(--accent-blue)]" onClick={() => onQueryChange({ ...query, day: '', project: '', agent: '', focusKind: '', focusValue: '' })}>
              {t('home.clearFilters')}
            </button>
          )}
        </div>

        <QuotaStrip quotas={quotas} />

        {loading && !report && <p className="text-helper text-[var(--text-muted)]">…</p>}
        {error && (
          <div className="rounded-md border border-[var(--border-default)] p-4">
            <p className="text-body text-[var(--text-primary)]">{t('home.loadFailed')}</p>
            <button type="button" className="mt-2 text-helper text-[var(--accent-blue)]" onClick={load}>{t('home.retry')}</button>
          </div>
        )}
        {report && !filtersActive && report.summary.sessions === 0 && report.live.length === 0 && report.unfinished.length === 0 && !loading && (
          <p className="mb-4 text-helper text-[var(--text-muted)]">{t('home.empty')}</p>
        )}
        {report && (
          <div className="space-y-6">
            <section className="grid grid-cols-2 gap-2 md:grid-cols-4" data-testid="home-summary">
              <Stat label={t('home.summarySessions')} value={formatNumber(locale, report.summary.sessions)} />
              <Stat label={t('home.summaryProjects')} value={formatNumber(locale, report.summary.projects)} />
              <Stat label={t('home.summaryAgents')} value={formatNumber(locale, report.summary.agents)} />
              <Stat label={t('home.summaryMessages')} value={formatNumber(locale, report.summary.messages)} mark={report.coverage.untimed_utterances > 0 ? t('home.coverage.untimedUtterances', { count: report.coverage.untimed_utterances }) : ''} />
              <Stat label={t('home.summaryTurns')} value={formatNumber(locale, report.summary.turns)} />
              <Stat label={t('home.summaryTokens')} value={formatNumber(locale, report.summary.tokens)} mark={tokenMark(t, report)} />
              <Stat label={t('home.summaryCost')} value={costText(report)} mark={costMark(t, report)} />
              <Stat label={t('home.summaryCode')} value={`${formatNumber(locale, report.summary.additions)} / ${formatNumber(locale, report.summary.deletions)}`} mark={report.coverage.untimed_code_changes > 0 ? t('home.coverage.untimedCode', { count: report.coverage.untimed_code_changes }) : ''} />
            </section>
            <p className="text-helper text-[var(--text-muted)]">
              {t('home.codeNote', {
                files: report.summary.code_files,
                additions: report.summary.additions,
                deletions: report.summary.deletions,
                sessions: report.coverage.code_sessions,
              })}
            </p>
            <CoverageNotes report={report} />

            <section data-testid="home-activity">
              <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{t('home.activity')}</h3>
              <div className="flex items-end gap-1 overflow-x-auto pb-2">
                {report.days.map(day => {
                  const max = Math.max(1, ...report.days.map(item => item.turns))
                  const height = 8 + Math.round((day.turns / max) * 56)
                  const selected = query.day === day.date
                  return (
                    <button
                      key={day.date}
                      type="button"
                      title={`${day.date}: ${day.turns}`}
                      data-testid={`home-day-${day.date}`}
                      onClick={() => onQueryChange({ ...query, day: selected ? '' : day.date })}
                      className={`flex w-4 flex-col items-center justify-end ${selected ? 'opacity-100' : 'opacity-80'}`}
                    >
                      <span className={`block w-3 rounded-sm ${selected ? 'bg-[var(--accent-blue)]' : 'bg-[var(--accent-blue)]/50'}`} style={{ height }} />
                    </button>
                  )
                })}
              </div>
              <div className="mt-3 space-y-1">
                {report.days.map(day => (
                  <div key={`trend-${day.date}`} className="grid grid-cols-[5.5rem_1fr_1fr_1fr] gap-2 text-helper text-[var(--text-muted)]">
                    <span>{day.date.slice(5)}</span>
                    <span>{t('home.messages')} {formatNumber(locale, day.messages)}</span>
                    <span>{t('home.turns')} {formatNumber(locale, day.turns)}</span>
                    <span>{t('home.tokens')} {formatNumber(locale, day.tokens)}</span>
                  </div>
                ))}
              </div>
            </section>

            <div className="grid gap-4 md:grid-cols-2">
              <SessionSection title={t('home.live')} sessions={report.live} empty={t('home.noLive')} onOpen={openSession} />
              <section>
                <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{t('home.unfinished')}</h3>
                {report.unfinished.length === 0 && <p className="text-helper text-[var(--text-muted)]">{t('home.noUnfinished')}</p>}
                <ul className="space-y-1">
                  {report.unfinished.map(session => (
                    <li key={`${session.agent_type}:${session.id}`}>
                      <button type="button" className="w-full rounded-md px-2 py-1.5 text-left hover:bg-[var(--bg-surface-hover)]" onClick={event => openSession(event, session)}>
                        <span className="block truncate text-body text-[var(--text-primary)]">{session.name || session.id}</span>
                        <span className="text-helper text-[var(--text-muted)]">
                          {session.reasons.map(reason => t(`home.reason.${reason}`)).join(' · ')}
                          {' · '}
                          {formatRelativeTime(session.updated_at, Date.now(), locale)}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <SessionSection title={t('home.recent')} sessions={report.recent} empty={t('home.noRecent')} onOpen={openSession} />
              <SessionSection title={t('home.starred')} sessions={report.starred} empty={t('home.noStarred')} onOpen={openSession} />
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <CountSection title={t('home.projects')} rows={report.projects} selected={query.project} onPick={name => onQueryChange({ ...query, project: query.project === name ? '' : name })} />
              <CountSection title={t('home.agents')} rows={report.agents.map(row => ({ ...row, name: row.name }))} selected={query.agent} labelFor={name => getAgentLabel(name)} onPick={name => onQueryChange({ ...query, agent: query.agent === name ? '' : name })} />
            </div>

            <section data-testid="home-health">
              <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{t('home.health')}</h3>
              <div className="flex flex-wrap gap-2">
                {HEALTH.map(([value, field]) => (
                  <button
                    key={value}
                    type="button"
                    onClick={() => toggleFocus('health', value)}
                    className={`rounded-md border px-2 py-1 text-helper ${query.focusKind === 'health' && query.focusValue === value ? 'border-[var(--accent-blue)] text-[var(--accent-blue)]' : 'border-[var(--border-default)] text-[var(--text-secondary)]'}`}
                  >
                    {t(`home.health.${value}`)} {formatNumber(locale, report.health[field])}
                    {value === 'missing_shutdown' && <CoverageMark text={t('home.coverage.shutdown')} />}
                  </button>
                ))}
              </div>
            </section>

            <RankedSection
              title={t('home.tools')}
              rows={report.tools}
              expanded={toolsExpanded}
              onToggle={() => setToolsExpanded(value => !value)}
              selected={query.focusKind === 'tool' ? query.focusValue : ''}
              onPick={name => toggleFocus('tool', name)}
            />
            <RankedSection
              title={t('home.skills')}
              rows={report.skills}
              expanded={skillsExpanded}
              onToggle={() => setSkillsExpanded(value => !value)}
              selected={query.focusKind === 'skill' ? query.focusValue : ''}
              onPick={name => toggleFocus('skill', name)}
              mark={report.coverage.skill_uncovered_agents?.length ? t('home.coverage.skills', { agents: report.coverage.skill_uncovered_agents.join(', ') }) : ''}
            />
          </div>
        )}
      </div>
    </main>
  )
}

function WindowButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={`h-7 rounded-md border px-2 text-helper ${active ? 'border-[var(--accent-blue)] text-[var(--accent-blue)]' : 'border-[var(--border-default)] text-[var(--text-secondary)]'}`}>
      {label}
    </button>
  )
}

function FilterChip({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className="h-7 rounded-md bg-[var(--accent-blue)]/10 px-2 text-helper text-[var(--accent-blue)]">
      {label} ×
    </button>
  )
}

function Stat({ label, value, mark }: { label: string; value: string; mark?: string }) {
  return (
    <div className="rounded-md border border-[var(--border-muted)] px-3 py-2">
      <div className="text-helper text-[var(--text-muted)]">{label}{mark && <CoverageMark text={mark} />}</div>
      <div className="text-body font-semibold text-[var(--text-primary)]">{value}</div>
    </div>
  )
}

function CoverageMark({ text }: { text: string }) {
  return <span className="ml-1 cursor-help text-[var(--warning)]" title={text} aria-label={text}>!</span>
}

function SessionSection({ title, sessions, empty, onOpen }: { title: string; sessions: HomeSessionCard[]; empty: string; onOpen: (event: MouseEvent, session: HomeSessionCard) => void }) {
  const { locale, t } = useI18n()
  return (
    <section>
      <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{title}</h3>
      {sessions.length === 0 && <p className="text-helper text-[var(--text-muted)]">{empty}</p>}
      <ul className="space-y-1">
        {sessions.map(session => (
          <li key={`${session.agent_type}:${session.id}`}>
            <button type="button" className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-[var(--bg-surface-hover)]" onClick={event => onOpen(event, session)}>
              <AgentIcon agentType={session.agent_type} size={16} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-body text-[var(--text-primary)]">{session.name || session.id}</span>
                <span className="text-helper text-[var(--text-muted)]">{session.project || getAgentLabel(session.agent_type)} · {formatRelativeTime(session.updated_at, Date.now(), locale)}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      <span className="sr-only">{t('home.recent')}</span>
    </section>
  )
}

function CountSection({ title, rows, selected, onPick, labelFor }: { title: string; rows: { name: string; count: number }[]; selected: string; onPick: (name: string) => void; labelFor?: (name: string) => string }) {
  const { locale } = useI18n()
  return (
    <section>
      <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{title}</h3>
      <ul className="space-y-1">
        {rows.map(row => (
          <li key={row.name || '(empty)'}>
            <button type="button" onClick={() => onPick(row.name)} className={`flex w-full justify-between rounded-md px-2 py-1 text-helper ${selected === row.name ? 'text-[var(--accent-blue)]' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)]`}>
              <span className="truncate">{labelFor ? labelFor(row.name) : (row.name || '—')}</span>
              <span>{formatNumber(locale, row.count)}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}

function RankedSection({ title, rows, expanded, onToggle, selected, onPick, mark }: { title: string; rows: { name: string; count: number }[]; expanded: boolean; onToggle: () => void; selected: string; onPick: (name: string) => void; mark?: string }) {
  const { t, locale } = useI18n()
  const visible = expanded ? rows : rows.slice(0, 8)
  return (
    <section>
      <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{title}{mark && <CoverageMark text={mark} />}</h3>
      <ul className="space-y-1">
        {visible.map(row => (
          <li key={row.name}>
            <button type="button" onClick={() => onPick(row.name)} className={`flex w-full justify-between rounded-md px-2 py-1 text-helper ${selected === row.name ? 'text-[var(--accent-blue)]' : 'text-[var(--text-secondary)]'} hover:bg-[var(--bg-surface-hover)]`}>
              <span className="truncate">{row.name}</span>
              <span>{formatNumber(locale, row.count)}</span>
            </button>
          </li>
        ))}
      </ul>
      {rows.length > 8 && (
        <button type="button" className="mt-1 text-helper text-[var(--accent-blue)]" onClick={onToggle}>
          {expanded ? t('home.showLess') : t('home.showAll', { count: rows.length })}
        </button>
      )}
    </section>
  )
}

function QuotaStrip({ quotas }: { quotas: CodingQuotaResponse | null }) {
  const { t, locale } = useI18n()
  return (
    <section className="mb-4" data-testid="home-quota">
      <h3 className="mb-2 text-nav font-semibold text-[var(--text-primary)]">{t('home.quota')}</h3>
      {!quotas && <p className="text-helper text-[var(--text-muted)]">{t('home.quotaUnavailable')}</p>}
      <div className="flex flex-wrap gap-2">
        {quotas?.providers.map(provider => (
          <div key={provider.id} className="min-w-[10rem] rounded-md border border-[var(--border-muted)] px-2 py-1.5">
            <div className="text-helper font-medium text-[var(--text-primary)]">{t(provider.display_name_key)}</div>
            {provider.snapshot.status !== 'available' && provider.snapshot.status !== 'stale' && (
              <div className="text-helper text-[var(--text-muted)]">{t('home.quotaUnavailable')}</div>
            )}
            {provider.snapshot.windows?.map(window => (
              <div key={window.id} className="text-helper text-[var(--text-secondary)]">
                {window.remaining_percent != null ? `${window.remaining_percent}%` : (window.remaining_amount != null ? String(window.remaining_amount) : '—')}
                {window.unit ? ` ${window.unit}` : ''}
                {window.reset_at ? ` · ${t('home.quotaReset', { time: formatDate(locale, window.reset_at, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) })}` : ''}
              </div>
            ))}
            {provider.snapshot.observed_at && (
              <div className="text-helper text-[var(--text-muted)]">{t('home.quotaObserved', { time: formatDate(locale, provider.snapshot.observed_at, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) })}</div>
            )}
          </div>
        ))}
      </div>
    </section>
  )
}

function CoverageNotes({ report }: { report: HomeReport }) {
  const { t } = useI18n()
  const notes = [
    report.coverage.detail_missing_sessions > 0 ? t('home.coverage.detail', { count: report.coverage.detail_missing_sessions }) : '',
    report.coverage.unattributed_child_sessions > 0 ? t('home.coverage.child', { count: report.coverage.unattributed_child_sessions }) : '',
    report.coverage.untimed_skills > 0 ? t('home.coverage.untimedTokens', { count: report.coverage.untimed_skills }) : '',
  ].filter(Boolean)
  if (notes.length === 0) return null
  return (
    <ul className="text-helper text-[var(--text-muted)]">
      {notes.map(note => <li key={note}>{note}</li>)}
    </ul>
  )
}

function tokenMark(t: (key: string, vars?: Record<string, string | number>) => string, report: HomeReport): string {
  const parts = []
  if (report.coverage.missing_cache_read_sessions > 0 || report.coverage.missing_cache_write_sessions > 0) {
    parts.push(t('home.coverage.cache', { read: report.coverage.missing_cache_read_sessions, write: report.coverage.missing_cache_write_sessions }))
  }
  if (report.coverage.untimed_token_sessions > 0) {
    parts.push(t('home.coverage.untimedTokens', { count: report.coverage.untimed_token_sessions }))
  }
  return parts.join(' ')
}

function costText(report: HomeReport): string {
  const costs = report.summary.costs ?? []
  if (costs.length === 0) return '—'
  return costs.map(cost => `${cost.amount} ${cost.unit}`).join(' · ')
}

function costMark(t: (key: string, vars?: Record<string, string | number>) => string, report: HomeReport): string {
  const parts = []
  if (report.coverage.cost_omitted_sessions > 0) parts.push(t('home.coverage.costOmitted', { count: report.coverage.cost_omitted_sessions }))
  if (report.coverage.spanning_cost_sessions > 0) parts.push(t('home.coverage.spanningCost', { count: report.coverage.spanning_cost_sessions }))
  if ((report.summary.costs ?? []).some(cost => cost.precision === 'estimated')) parts.push(t('home.estimated'))
  return parts.join(' ')
}

function focusLabel(t: (key: string, vars?: Record<string, string | number>) => string, kind: string, value: string): string {
  if (kind === 'health') return t(`home.health.${value}`)
  return value
}
