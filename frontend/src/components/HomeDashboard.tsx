import { useCallback, useEffect, useMemo, useState } from 'react'
import { fetchSessions, watchSessionsChanged } from '../api'
import { applyBookmarkChange, type BookmarkChange } from '../bookmarkState'
import { formatDate, formatNumber, useI18n } from '../i18n'
import { formatRelativeTime, getAgentLabel } from '../sidebarRows'
import { openOnModifiedClick, openSessionInNewTab } from '../sessionLink'
import type { SessionSummary } from '../types'
import AgentIcon from './AgentIcon'
import GlobalSearch from './GlobalSearch'

interface HomeDashboardProps {
  onSelect?: (sessionId: string, agentType?: string) => void
  onOpenCodingQuotas?: () => void
  bookmarkChange: BookmarkChange | null
}

interface ProjectActivity {
  name: string
  sessions: number
  latestSession: SessionSummary
}

function localDayKey(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function sessionUpdatedAt(session: SessionSummary): number {
  const timestamp = Date.parse(session.updated_at)
  return Number.isFinite(timestamp) ? timestamp : 0
}

export default function HomeDashboard({ onSelect, onOpenCodingQuotas, bookmarkChange }: HomeDashboardProps) {
  const { t, locale } = useI18n()
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [windowDays, setWindowDays] = useState<7 | 30>(7)
  const [currentTime, setCurrentTime] = useState(() => Date.now())

  const loadSessions = useCallback(async () => {
    try {
      const loadedSessions = await fetchSessions()
      setSessions(loadedSessions)
      setLoadError(false)
    } catch {
      setLoadError(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadSessions()
    let refreshTimer: ReturnType<typeof setTimeout> | undefined
    const scheduleRefresh = () => {
      if (refreshTimer) return
      refreshTimer = setTimeout(() => {
        refreshTimer = undefined
        void loadSessions()
      }, 500)
    }
    const stopWatching = watchSessionsChanged(scheduleRefresh, connected => {
      if (connected) scheduleRefresh()
    })
    const clockTimer = window.setInterval(() => setCurrentTime(Date.now()), 60_000)
    return () => {
      stopWatching()
      window.clearInterval(clockTimer)
      if (refreshTimer) clearTimeout(refreshTimer)
    }
  }, [loadSessions])

  useEffect(() => {
    if (bookmarkChange) setSessions(currentSessions => applyBookmarkChange(currentSessions, bookmarkChange))
  }, [bookmarkChange])

  const activity = useMemo(() => {
    const today = new Date(currentTime)
    const firstDay = new Date(today.getFullYear(), today.getMonth(), today.getDate() - windowDays + 1)
    const firstDayKey = localDayKey(firstDay)
    const todayKey = localDayKey(today)
    const dailyCounts = new Map<string, number>()
    const recentSessions = sessions
      .filter(session => {
        const updatedAt = sessionUpdatedAt(session)
        if (!updatedAt) return false
        const dayKey = localDayKey(new Date(updatedAt))
        return dayKey >= firstDayKey && dayKey <= todayKey
      })
      .sort((left, right) => sessionUpdatedAt(right) - sessionUpdatedAt(left))

    for (const session of recentSessions) {
      const dayKey = localDayKey(new Date(sessionUpdatedAt(session)))
      dailyCounts.set(dayKey, (dailyCounts.get(dayKey) ?? 0) + 1)
    }

    const days = Array.from({ length: windowDays }, (_, dayIndex) => {
      const dayDate = new Date(firstDay.getFullYear(), firstDay.getMonth(), firstDay.getDate() + dayIndex)
      const dayKey = localDayKey(dayDate)
      return { key: dayKey, date: dayDate, count: dailyCounts.get(dayKey) ?? 0 }
    })

    const projects = new Map<string, ProjectActivity>()
    for (const session of recentSessions) {
      if (!session.project) continue
      const project = projects.get(session.project)
      if (project) project.sessions += 1
      else projects.set(session.project, { name: session.project, sessions: 1, latestSession: session })
    }

    return {
      days,
      recentSessions,
      projects: [...projects.values()].sort((left, right) => right.sessions - left.sessions || left.name.localeCompare(right.name)),
      activeAgents: new Set(recentSessions.map(session => session.agent_type)).size,
      bookmarkedSessions: recentSessions.filter(session => session.bookmarked).length,
      liveSessions: sessions.filter(session => session.is_live).length,
    }
  }, [sessions, windowDays, currentTime])

  const maxDailyCount = Math.max(1, ...activity.days.map(day => day.count))

  const openSession = (event: React.MouseEvent, session: SessionSummary) => {
    if (!openOnModifiedClick(event, session.agent_type, session.id)) onSelect?.(session.id, session.agent_type)
  }

  return (
    <main className="flex min-w-[360px] flex-1 flex-col bg-[var(--bg-surface)]" data-testid="home-dashboard">
      <GlobalSearch onSelect={onSelect} onOpenCodingQuotas={onOpenCodingQuotas} />
      <div className="min-h-0 flex-1 overflow-y-auto bg-[var(--bg-primary)]">
        <div className="mx-auto max-w-[1180px] px-6 py-7">
          <div className="mb-6 flex items-end justify-between gap-4">
            <div>
              <p className="mb-1 text-meta font-semibold uppercase tracking-[0.12em] text-[var(--accent-blue)]">Session Insight</p>
              <h1 className="text-2xl font-semibold tracking-tight text-[var(--text-primary)]">{t('home.title')}</h1>
              <p className="mt-1 text-helper text-[var(--text-secondary)]">{t('home.subtitle')}</p>
            </div>
            <div className="flex shrink-0 gap-1 rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] p-1" aria-label={t('home.dateRange')}>
              {([7, 30] as const).map(days => (
                <button
                  key={days}
                  type="button"
                  onClick={() => setWindowDays(days)}
                  aria-pressed={windowDays === days}
                  className={`rounded-md px-3 py-1.5 text-helper font-medium transition-colors ${windowDays === days ? 'bg-[var(--accent-blue)]/12 text-[var(--accent-blue)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]'}`}
                >
                  {t(days === 7 ? 'home.last7Days' : 'home.last30Days')}
                </button>
              ))}
            </div>
          </div>

          {loadError && (
            <div role="alert" className="mb-5 flex items-center justify-between gap-3 rounded-lg border border-[var(--error)]/30 bg-[var(--error)]/5 px-4 py-3 text-helper text-[var(--text-primary)]">
              <span>{t('home.loadFailed')}</span>
              <button type="button" onClick={() => void loadSessions()} className="font-medium text-[var(--accent-blue)] hover:underline">{t('common.retry')}</button>
            </div>
          )}

          {loading ? (
            <div role="status" className="py-16 text-center text-helper text-[var(--text-muted)]">{t('common.loading')}</div>
          ) : sessions.length === 0 && !loadError ? (
            <div className="rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] px-8 py-16 text-center">
              <h2 className="text-nav font-semibold text-[var(--text-primary)]">{t('home.emptyTitle')}</h2>
              <p className="mt-2 text-helper text-[var(--text-secondary)]">{t('home.emptyHelp')}</p>
            </div>
          ) : (
            <>
              <div className="grid grid-cols-4 gap-3" data-testid="home-summary">
                {[
                  { label: t('home.updatedSessions'), value: activity.recentSessions.length, note: t('home.inSelectedRange') },
                  { label: t('home.projects'), value: activity.projects.length, note: t('home.inSelectedRange') },
                  { label: t('home.agents'), value: activity.activeAgents, note: t('home.inSelectedRange') },
                  { label: t('home.liveSessions'), value: activity.liveSessions, note: t('home.liveNow') },
                ].map(metric => (
                  <div key={metric.label} className="rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] p-4">
                    <p className="text-helper text-[var(--text-secondary)]">{metric.label}</p>
                    <p className="mt-2 text-2xl font-semibold tabular-nums text-[var(--text-primary)]">{formatNumber(locale, metric.value)}</p>
                    <p className="mt-1 text-meta text-[var(--text-muted)]">{metric.note}</p>
                  </div>
                ))}
              </div>

              <div className="mt-4 grid grid-cols-[minmax(0,1.65fr)_minmax(260px,1fr)] gap-4">
                <section className="rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] p-5" aria-labelledby="home-activity-heading">
                  <div className="mb-1 flex items-baseline justify-between gap-3">
                    <h2 id="home-activity-heading" className="text-nav font-semibold text-[var(--text-primary)]">{t('home.activity')}</h2>
                    <span className="text-meta text-[var(--text-muted)]">{t('home.activityBasis')}</span>
                  </div>
                  <p className="text-helper text-[var(--text-secondary)]">{t('home.activityDescription')}</p>
                  <div className="mt-5 flex h-36 items-end gap-1" data-testid="home-activity-chart">
                    {activity.days.map(day => (
                      <div key={day.key} className="flex h-full min-w-0 flex-1 items-end" title={`${formatDate(locale, day.date, { dateStyle: 'medium' })}: ${formatNumber(locale, day.count)}`}>
                        <div className={`w-full rounded-t-[3px] ${day.count > 0 ? 'bg-[var(--accent-blue)]' : 'bg-[var(--bg-inset)]'}`} style={{ height: day.count > 0 ? `${Math.max(6, day.count / maxDailyCount * 100)}%` : '4px' }} />
                      </div>
                    ))}
                  </div>
                  <div className="mt-2 flex justify-between text-meta text-[var(--text-muted)]">
                    <span>{formatDate(locale, activity.days[0].date, { month: 'short', day: 'numeric' })}</span>
                    <span>{formatDate(locale, activity.days[activity.days.length - 1].date, { month: 'short', day: 'numeric' })}</span>
                  </div>
                </section>

                <section className="rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] p-5" aria-labelledby="home-projects-heading">
                  <div className="mb-3 flex items-baseline justify-between gap-3">
                    <h2 id="home-projects-heading" className="text-nav font-semibold text-[var(--text-primary)]">{t('home.topProjects')}</h2>
                    <span className="text-meta text-[var(--text-muted)]">{t('home.updatedSessions')}</span>
                  </div>
                  {activity.projects.length === 0 ? (
                    <p className="py-8 text-center text-helper text-[var(--text-muted)]">{t('home.noProjects')}</p>
                  ) : (
                    <div className="space-y-1" data-testid="home-projects">
                      {activity.projects.slice(0, 5).map(project => (
                        <button key={project.name} type="button" onClick={event => openSession(event, project.latestSession)} onAuxClick={event => { if (event.button === 1) openSessionInNewTab(project.latestSession.agent_type, project.latestSession.id) }} className="flex w-full items-center gap-3 rounded-md px-2 py-2 text-left hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]" title={t('home.openProjectRecent')}>
                          <span className="min-w-0 flex-1 truncate text-helper text-[var(--text-primary)]">{project.name}</span>
                          <span className="text-helper tabular-nums text-[var(--text-muted)]">{formatNumber(locale, project.sessions)}</span>
                        </button>
                      ))}
                    </div>
                  )}
                </section>
              </div>

              <section className="mt-4 rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] p-5" aria-labelledby="home-recent-heading">
                <div className="mb-3 flex items-baseline justify-between gap-3">
                  <h2 id="home-recent-heading" className="text-nav font-semibold text-[var(--text-primary)]">{t('home.recentSessions')}</h2>
                  <span className="text-meta text-[var(--text-muted)]">{t('home.bookmarkedCount', { count: activity.bookmarkedSessions })}</span>
                </div>
                {activity.recentSessions.length === 0 ? (
                  <p className="py-8 text-center text-helper text-[var(--text-muted)]">{t('home.noRecentSessions')}</p>
                ) : (
                  <div className="divide-y divide-[var(--border-muted)]" data-testid="home-recent-sessions">
                    {activity.recentSessions.slice(0, 6).map(session => (
                      <button key={`${session.agent_type}:${session.id}`} type="button" onClick={event => openSession(event, session)} onAuxClick={event => { if (event.button === 1) openSessionInNewTab(session.agent_type, session.id) }} className="flex w-full items-center gap-3 py-2.5 text-left hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]" data-testid="home-session-row">
                        <AgentIcon agentType={session.agent_type} size={20} className="shrink-0" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-helper font-medium text-[var(--text-primary)]">{session.name || session.repository || session.id}</span>
                          <span className="block truncate text-meta text-[var(--text-muted)]">{getAgentLabel(session.agent_type)} · {session.project || t('home.unknownProject')}</span>
                        </span>
                        {session.is_live && <span className="rounded-full bg-[var(--success)]/10 px-2 py-0.5 text-meta text-[var(--success)]">{t('sidebar.live')}</span>}
                        <time dateTime={session.updated_at} className="shrink-0 text-meta text-[var(--text-muted)]">{formatRelativeTime(session.updated_at, currentTime, locale)}</time>
                      </button>
                    ))}
                  </div>
                )}
              </section>
            </>
          )}
        </div>
      </div>
    </main>
  )
}
