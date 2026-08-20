import { useEffect, useMemo, useState } from 'react'
import { deleteSnippet, fetchSnippets } from '../api'
import { formatDate, useI18n } from '../i18n'
import { getAgentLabel } from '../sidebarRows'
import type { Snippet } from '../types'
import AgentIcon from './AgentIcon'

interface Props {
  onBack: () => void
  onOpenSource: (snippet: Snippet) => void
}

const SNIPPET_PREVIEW_LINE_LIMIT = 8
const SNIPPET_PREVIEW_CHARACTER_LIMIT = 900
const NO_PROJECT_FILTER_KEY = '__no-project__'

interface SnippetFacet {
  key: string
  label: string
  count: number
}

function NotebookIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="5" y="3" width="14" height="18" rx="2" />
      <path d="M8 3v18M9 7h7M9 11h7M9 15h5" />
    </svg>
  )
}

function FolderIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 6.5A2.5 2.5 0 0 1 5.5 4H10l2 2h6.5A2.5 2.5 0 0 1 21 8.5v8A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5z" />
    </svg>
  )
}

function projectFilterKey(project: string): string {
  return project.trim() || NO_PROJECT_FILTER_KEY
}

function getAgentFacetLabel(agentType: string, unknownAgentLabel: string): string {
  return agentType ? getAgentLabel(agentType) : unknownAgentLabel
}

function getSnippetPreview(content: string): { text: string; truncated: boolean } {
  const normalizedContent = content.replace(/\r\n/g, '\n')
  const previewLines = normalizedContent.split('\n').slice(0, SNIPPET_PREVIEW_LINE_LIMIT)
  let previewText = previewLines.join('\n')
  if (previewText.length > SNIPPET_PREVIEW_CHARACTER_LIMIT) {
    previewText = previewText.slice(0, SNIPPET_PREVIEW_CHARACTER_LIMIT).trimEnd()
  }
  const truncated = previewText.length < normalizedContent.length
  return {
    text: truncated ? `${previewText}…` : normalizedContent,
    truncated,
  }
}

export default function SnippetPage({ onBack, onOpenSource }: Props) {
  const { locale, t } = useI18n()
  const [snippets, setSnippets] = useState<Snippet[]>([])
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deletingID, setDeletingID] = useState<number | null>(null)
  const [expandedSnippetIDs, setExpandedSnippetIDs] = useState<Set<number>>(() => new Set())
  const [agentFilter, setAgentFilter] = useState('')
  const [projectFilter, setProjectFilter] = useState('')

  useEffect(() => {
    let cancelled = false
    void fetchSnippets()
      .then(items => {
        if (!cancelled) setSnippets(items)
      })
      .catch(() => {
        if (!cancelled) setError('common.error')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [])

  const agentFacets = useMemo<SnippetFacet[]>(() => {
    const counts = new Map<string, number>()
    for (const snippet of snippets) {
      if (projectFilter && projectFilterKey(snippet.project) !== projectFilter) continue
      counts.set(snippet.agent_type, (counts.get(snippet.agent_type) ?? 0) + 1)
    }
    return [...counts.entries()]
      .map(([key, count]) => ({ key, label: getAgentFacetLabel(key, t('snippets.unknownAgent')), count }))
      .sort((first, second) => second.count - first.count || first.label.localeCompare(second.label))
  }, [projectFilter, snippets, t])

  const projectFacets = useMemo<SnippetFacet[]>(() => {
    const counts = new Map<string, number>()
    for (const snippet of snippets) {
      if (agentFilter && snippet.agent_type !== agentFilter) continue
      const key = projectFilterKey(snippet.project)
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
    return [...counts.entries()]
      .map(([key, count]) => ({
        key,
        label: key === NO_PROJECT_FILTER_KEY ? t('snippets.unknownProject') : key,
        count,
      }))
      .sort((first, second) => second.count - first.count || first.label.localeCompare(second.label))
  }, [agentFilter, snippets, t])

  const visibleSnippets = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase()
    return snippets.filter(snippet =>
      (!agentFilter || snippet.agent_type === agentFilter) &&
      (!projectFilter || projectFilterKey(snippet.project) === projectFilter) &&
      (!normalizedQuery || `${snippet.content}\n${snippet.session_name}\n${snippet.session_id}\n${snippet.agent_type}\n${snippet.project}`
        .toLocaleLowerCase()
        .includes(normalizedQuery)),
    )
  }, [agentFilter, projectFilter, query, snippets])

  const hasActiveFilters = Boolean(query.trim() || agentFilter || projectFilter)
  const selectedProjectLabel = projectFilter === NO_PROJECT_FILTER_KEY ? t('snippets.unknownProject') : projectFilter
  const currentNotebookLabel = agentFilter && projectFilter
    ? `${getAgentFacetLabel(agentFilter, t('snippets.unknownAgent'))} · ${selectedProjectLabel}`
    : agentFilter
      ? getAgentFacetLabel(agentFilter, t('snippets.unknownAgent'))
      : projectFilter
        ? selectedProjectLabel
        : t('snippets.allNotes')

  useEffect(() => {
    if (agentFilter && !agentFacets.some(facet => facet.key === agentFilter)) setAgentFilter('')
  }, [agentFacets, agentFilter])

  useEffect(() => {
    if (projectFilter && !projectFacets.some(facet => facet.key === projectFilter)) setProjectFilter('')
  }, [projectFacets, projectFilter])

  const remove = async (id: number) => {
    if (deletingID !== null) return
    setDeletingID(id)
    setError(null)
    try {
      await deleteSnippet(id)
      setSnippets(items => items.filter(snippet => snippet.id !== id))
      setExpandedSnippetIDs(ids => {
        if (!ids.has(id)) return ids
        const nextIDs = new Set(ids)
        nextIDs.delete(id)
        return nextIDs
      })
    } catch {
      setError('snippets.deleteFailed')
    } finally {
      setDeletingID(null)
    }
  }

  return (
    <main className="h-full min-h-0 overflow-y-auto bg-[var(--bg-primary)] text-[var(--text-primary)]" data-testid="snippets-page">
      <div className="mx-auto w-full max-w-6xl px-6 py-6 lg:px-8 lg:py-8">
        <header className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="h-2 w-2 flex-shrink-0 rounded-full bg-[var(--accent-blue)]" aria-hidden="true" />
              <h1 className="text-xl font-semibold">{t('snippets.title')}</h1>
            </div>
            <p className="mt-1 text-helper text-[var(--text-muted)]">{t('snippets.count', { count: snippets.length })}</p>
          </div>
          <button
            type="button"
            onClick={onBack}
            className="h-8 rounded-md border border-[var(--border-default)] px-3 text-helper text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            {t('snippets.back')}
          </button>
        </header>

        <div className="relative mt-5">
          <input
            value={query}
            onChange={event => setQuery(event.target.value)}
            placeholder={t('snippets.search')}
            aria-label={t('snippets.searchLabel')}
            className="h-10 w-full rounded-lg border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] shadow-sm focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20"
          />
        </div>
        {error && <p className="mt-3 text-helper text-[var(--error)]" role="status">{t(error)}</p>}

        <div className="mt-5 grid grid-cols-1 items-start gap-5 lg:grid-cols-[14rem_minmax(0,1fr)]">
          <aside
            className="relative overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] p-3 shadow-sm"
            data-testid="snippet-notebook-nav"
            aria-label={t('snippets.notebook')}
          >
            <div className="absolute inset-y-0 left-0 w-1 bg-[var(--accent-purple)]" aria-hidden="true" />
            <div className="flex items-center gap-2 px-2">
              <span className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-[var(--accent-purple)]/10 text-[var(--accent-purple)]">
                <NotebookIcon />
              </span>
              <div className="min-w-0">
                <p className="text-meta font-semibold uppercase tracking-[0.12em] text-[var(--accent-purple)]">{t('snippets.notebook')}</p>
                <p className="truncate text-helper font-medium text-[var(--text-primary)]">{t('snippets.title')}</p>
              </div>
            </div>

            <nav className="mt-4 space-y-1" aria-label={t('snippets.notebook')}>
              <button
                type="button"
                data-testid="snippet-filter-all"
                aria-pressed={!agentFilter && !projectFilter}
                onClick={() => {
                  setAgentFilter('')
                  setProjectFilter('')
                }}
                className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${!agentFilter && !projectFilter ? 'bg-[var(--accent-purple)]/10 font-medium text-[var(--accent-purple)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
              >
                <NotebookIcon size={16} />
                <span className="min-w-0 flex-1 truncate">{t('snippets.allNotes')}</span>
                <span className="text-meta text-[var(--text-muted)]">{snippets.length}</span>
              </button>
            </nav>

            <div className="mt-5">
              <p className="px-2.5 text-meta font-semibold uppercase tracking-[0.12em] text-[var(--text-muted)]">{t('snippets.agents')}</p>
              <div className="mt-2 space-y-0.5">
                {agentFacets.map(facet => (
                  <button
                    key={facet.key}
                    type="button"
                    data-testid={`snippet-filter-agent-${facet.key}`}
                    aria-pressed={agentFilter === facet.key}
                    onClick={() => setAgentFilter(current => current === facet.key ? '' : facet.key)}
                    className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${agentFilter === facet.key ? 'bg-[var(--accent-purple)]/10 font-medium text-[var(--accent-purple)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
                  >
                    <AgentIcon agentType={facet.key} size={16} />
                    <span className="min-w-0 flex-1 truncate">{facet.label}</span>
                    <span className="text-meta text-[var(--text-muted)]">{facet.count}</span>
                  </button>
                ))}
              </div>
            </div>

            <div className="mt-5">
              <p className="px-2.5 text-meta font-semibold uppercase tracking-[0.12em] text-[var(--text-muted)]">{t('snippets.projects')}</p>
              <div className="mt-2 max-h-56 space-y-0.5 overflow-y-auto pr-1">
                {projectFacets.map(facet => (
                  <button
                    key={facet.key}
                    type="button"
                    data-testid={`snippet-filter-project-${facet.key}`}
                    aria-pressed={projectFilter === facet.key}
                    onClick={() => setProjectFilter(current => current === facet.key ? '' : facet.key)}
                    className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${projectFilter === facet.key ? 'bg-[var(--accent-purple)]/10 font-medium text-[var(--accent-purple)]' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
                  >
                    <FolderIcon />
                    <span className="min-w-0 flex-1 truncate">{facet.label}</span>
                    <span className="text-meta text-[var(--text-muted)]">{facet.count}</span>
                  </button>
                ))}
              </div>
            </div>

            {hasActiveFilters && (
              <button
                type="button"
                data-testid="snippet-clear-filters"
                onClick={() => {
                  setQuery('')
                  setAgentFilter('')
                  setProjectFilter('')
                }}
                className="mt-5 w-full rounded-lg border border-[var(--border-default)] px-2.5 py-1.5 text-helper text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
              >
                {t('snippets.clearFilters')}
              </button>
            )}
          </aside>

          <div className="min-w-0">
            <div className="flex items-center justify-between gap-3 px-1">
              <div className="flex min-w-0 items-center gap-2">
                <span className="h-2 w-2 flex-shrink-0 rounded-full bg-[var(--accent-purple)]" aria-hidden="true" />
                <h2 className="truncate text-base font-semibold">{currentNotebookLabel}</h2>
              </div>
              <span className="flex-shrink-0 text-helper text-[var(--text-muted)]" data-testid="snippet-visible-count">{t('snippets.count', { count: visibleSnippets.length })}</span>
            </div>

            {loading ? (
              <p className="mt-6 text-helper text-[var(--text-muted)]">{t('common.loading')}</p>
            ) : visibleSnippets.length === 0 ? (
              <p className="mt-6 max-w-xl rounded-xl border border-dashed border-[var(--border-default)] px-5 py-8 text-helper leading-6 text-[var(--text-muted)]">{hasActiveFilters ? t('snippets.noMatches') : t('snippets.empty')}</p>
            ) : (
              <section className="mt-4 grid grid-cols-1 items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label={t('snippets.title')}>
                {visibleSnippets.map(snippet => {
                  const preview = getSnippetPreview(snippet.content)
                  const expanded = expandedSnippetIDs.has(snippet.id)
                  const isAssistantSnippet = snippet.source_kind === 'assistant'
                  return (
                    <article
                      key={snippet.id}
                      className="group relative flex min-h-[236px] flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] p-4 shadow-sm transition-shadow duration-fast hover:shadow-md"
                    >
                      <div className={`absolute inset-x-0 top-0 h-0.5 ${isAssistantSnippet ? 'bg-[var(--accent-purple)]' : 'bg-[var(--accent-blue)]'}`} aria-hidden="true" />
                      <div className="flex items-center justify-between gap-3">
                        <span className="inline-flex min-w-0 items-center gap-1.5 rounded-full border border-[var(--border-muted)] bg-[var(--bg-inset)] px-2 py-1 text-meta text-[var(--text-secondary)]">
                          <span className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${isAssistantSnippet ? 'bg-[var(--accent-purple)]' : 'bg-[var(--accent-blue)]'}`} aria-hidden="true" />
                          <span className="truncate">{isAssistantSnippet ? t('snippets.sourceAssistant') : t('snippets.sourceSelection')}</span>
                        </span>
                        <button
                          type="button"
                          onClick={() => void remove(snippet.id)}
                          disabled={deletingID === snippet.id}
                          aria-label={t('snippets.deleteLabel')}
                          title={t('snippets.delete')}
                          className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-base leading-none text-[var(--text-muted)] opacity-70 hover:bg-[var(--bg-surface-hover)] hover:text-[var(--error)] group-hover:opacity-100 disabled:cursor-wait disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                        >
                          ×
                        </button>
                      </div>

                      <div className={`relative mt-3 flex-none rounded-lg border border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-3 ${expanded ? 'max-h-[22rem] overflow-y-auto' : 'h-[11rem] overflow-hidden'}`}>
                        <pre className="m-0 whitespace-pre-wrap break-words font-mono text-helper leading-6 text-[var(--text-primary)]">{expanded ? snippet.content : preview.text}</pre>
                        {!expanded && preview.truncated && (
                          <div
                            className="pointer-events-none absolute inset-x-0 bottom-0 h-10"
                            style={{ background: 'linear-gradient(to bottom, transparent, var(--bg-inset))' }}
                            aria-hidden="true"
                          />
                        )}
                      </div>

                      <div className="mt-3 flex flex-wrap items-end justify-between gap-3">
                        <div className="min-w-0 flex-1 text-meta text-[var(--text-muted)]">
                          <div className="truncate" title={snippet.session_name || snippet.session_id}>{snippet.session_name || snippet.session_id}</div>
                          <div className="mt-0.5 truncate" title={snippet.project || t('snippets.unknownProject')}>{snippet.project || t('snippets.unknownProject')}</div>
                          <div className="mt-0.5 truncate">{formatDate(locale, snippet.created_at)}</div>
                        </div>
                        <div className="flex flex-shrink-0 flex-wrap items-center justify-end gap-1.5">
                          {preview.truncated && (
                            <button
                              type="button"
                              onClick={() => setExpandedSnippetIDs(ids => {
                                const nextIDs = new Set(ids)
                                if (nextIDs.has(snippet.id)) nextIDs.delete(snippet.id)
                                else nextIDs.add(snippet.id)
                                return nextIDs
                              })}
                              aria-expanded={expanded}
                              className="h-7 rounded-md px-2 text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                            >
                              {expanded ? t('snippets.collapse') : t('snippets.expand')}
                            </button>
                          )}
                          <button
                            type="button"
                            onClick={() => onOpenSource(snippet)}
                            className="h-7 rounded-md border border-[var(--border-default)] px-2 text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                          >
                            {t('snippets.openSource')}
                          </button>
                        </div>
                      </div>
                    </article>
                  )
                })}
              </section>
            )}
          </div>
        </div>
      </div>
    </main>
  )
}
