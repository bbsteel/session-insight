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

function getSnippetPageLabel(snippet: Snippet): string {
  const firstContentLine = snippet.content.split('\n').find(line => line.trim())?.trim() ?? ''
  return snippet.session_name.trim() || firstContentLine || snippet.session_id
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
  const [selectedSnippetID, setSelectedSnippetID] = useState<number | null>(null)

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

  const selectedSnippet = visibleSnippets.find(snippet => snippet.id === selectedSnippetID) ?? visibleSnippets[0] ?? null

  useEffect(() => {
    if (visibleSnippets.length === 0) {
      if (selectedSnippetID !== null) setSelectedSnippetID(null)
      return
    }
    if (!visibleSnippets.some(snippet => snippet.id === selectedSnippetID)) {
      setSelectedSnippetID(visibleSnippets[0].id)
    }
  }, [selectedSnippetID, visibleSnippets])

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

        <div className="mt-5 overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-sm" data-testid="snippet-notebook-workspace">
          <div className="flex min-w-0 border-b border-[var(--border-default)] bg-[var(--bg-inset)]" data-testid="snippet-agent-nav">
            <nav className="flex min-w-0 flex-1 items-end gap-1 overflow-x-auto px-3 pt-2" aria-label={t('snippets.agents')}>
              <div className="mr-2 flex flex-shrink-0 items-center gap-2 border-r border-[var(--border-default)] pb-2 pr-4">
                <span className="flex h-7 w-7 items-center justify-center rounded-md bg-[var(--accent-purple)]/10 text-[var(--accent-purple)]">
                  <NotebookIcon size={16} />
                </span>
                <span className="text-helper font-medium text-[var(--text-primary)]">{t('snippets.notebook')}</span>
              </div>
              <button
                type="button"
                data-testid="snippet-filter-all"
                aria-pressed={!agentFilter && !projectFilter}
                role="tab"
                aria-selected={!agentFilter && !projectFilter}
                onClick={() => {
                  setAgentFilter('')
                  setProjectFilter('')
                }}
                className={`flex h-8 flex-shrink-0 items-center gap-1.5 rounded-t-md border-x border-t px-3 text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${!agentFilter && !projectFilter ? 'border-[var(--border-default)] bg-[var(--bg-surface)] font-medium text-[var(--accent-purple)]' : 'border-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
              >
                <NotebookIcon size={15} />
                <span>{t('snippets.allNotes')}</span>
              </button>
              {agentFacets.map(facet => (
                <button
                  key={facet.key}
                  type="button"
                  data-testid={`snippet-filter-agent-${facet.key}`}
                  aria-pressed={agentFilter === facet.key}
                  role="tab"
                  aria-selected={agentFilter === facet.key}
                  onClick={() => setAgentFilter(current => current === facet.key ? '' : facet.key)}
                  className={`flex h-8 flex-shrink-0 items-center gap-1.5 rounded-t-md border-x border-t px-3 text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${agentFilter === facet.key ? 'border-[var(--border-default)] bg-[var(--bg-surface)] font-medium text-[var(--accent-purple)]' : 'border-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
                >
                  <AgentIcon agentType={facet.key} size={15} />
                  <span>{facet.label}</span>
                  <span className="text-meta text-[var(--text-muted)]">{facet.count}</span>
                </button>
              ))}
            </nav>
            <span className="flex flex-shrink-0 items-center border-l border-[var(--border-default)] px-3 text-meta text-[var(--text-muted)]" data-testid="snippet-total-count">{t('snippets.count', { count: snippets.length })}</span>
          </div>

          <div className="grid min-h-[32rem] grid-cols-1 lg:grid-cols-[12rem_minmax(0,1fr)_14rem]">
            <aside className="flex min-h-[12rem] flex-col border-b border-[var(--border-default)] bg-[var(--bg-inset)]/45 lg:min-h-0 lg:border-b-0 lg:border-r" data-testid="snippet-project-nav" aria-label={t('snippets.projects')}>
              <div className="flex items-center gap-2 border-b border-[var(--border-default)] px-3 py-3">
                <FolderIcon />
                <span className="text-meta font-semibold uppercase tracking-[0.12em] text-[var(--text-muted)]">{t('snippets.projects')}</span>
              </div>
              <div className="flex-1 overflow-y-auto p-2">
                <button
                  type="button"
                  data-testid="snippet-filter-all-projects"
                  aria-pressed={!projectFilter}
                  onClick={() => setProjectFilter('')}
                  className={`flex w-full items-center gap-2 border-l-2 px-2 py-2 text-left text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${!projectFilter ? 'border-[var(--accent-purple)] bg-[var(--accent-purple)]/10 font-medium text-[var(--accent-purple)]' : 'border-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
                >
                  <NotebookIcon size={15} />
                  <span className="min-w-0 flex-1 truncate">{t('snippets.allProjects')}</span>
                </button>
                <div className="mt-2 space-y-0.5 border-l border-[var(--border-muted)] pl-2">
                  {projectFacets.map(facet => (
                    <button
                      key={facet.key}
                      type="button"
                      data-testid={`snippet-filter-project-${facet.key}`}
                      aria-pressed={projectFilter === facet.key}
                      onClick={() => setProjectFilter(current => current === facet.key ? '' : facet.key)}
                      className={`flex w-full items-center gap-2 border-l-2 px-2 py-1.5 text-left text-helper transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${projectFilter === facet.key ? 'border-[var(--accent-purple)] bg-[var(--accent-purple)]/10 font-medium text-[var(--accent-purple)]' : 'border-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'}`}
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
                  className="m-2 border-t border-[var(--border-default)] px-2 pt-3 text-left text-helper text-[var(--text-secondary)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                >
                  {t('snippets.clearFilters')}
                </button>
              )}
            </aside>

            <section className="min-w-0 bg-[var(--bg-primary)]" data-testid="snippet-reading-pane" aria-label={t('snippets.title')}>
              <div className="flex items-center justify-between gap-3 border-b border-[var(--border-default)] px-5 py-3">
                <div className="min-w-0">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="h-2 w-2 flex-shrink-0 rounded-full bg-[var(--accent-purple)]" aria-hidden="true" />
                    <h2 className="truncate text-base font-semibold">{currentNotebookLabel}</h2>
                  </div>
                  {selectedSnippet && <p className="mt-1 truncate text-meta text-[var(--text-muted)]">{getSnippetPageLabel(selectedSnippet)}</p>}
                </div>
                <span className="flex-shrink-0 text-helper text-[var(--text-muted)]" data-testid="snippet-visible-count">{t('snippets.count', { count: visibleSnippets.length })}</span>
              </div>

              {loading ? (
                <p className="px-5 py-8 text-helper text-[var(--text-muted)]">{t('common.loading')}</p>
              ) : !selectedSnippet ? (
                <p className="m-5 rounded-lg border border-dashed border-[var(--border-default)] px-5 py-8 text-helper leading-6 text-[var(--text-muted)]">{hasActiveFilters ? t('snippets.noMatches') : t('snippets.empty')}</p>
              ) : (() => {
                const preview = getSnippetPreview(selectedSnippet.content)
                const expanded = expandedSnippetIDs.has(selectedSnippet.id)
                const isAssistantSnippet = selectedSnippet.source_kind === 'assistant'
                return (
                  <article
                    className="group relative m-5 overflow-hidden rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-sm"
                    data-testid="snippet-reading-card"
                  >
                    <div className={`absolute inset-x-0 top-0 h-0.5 ${isAssistantSnippet ? 'bg-[var(--accent-purple)]' : 'bg-[var(--accent-blue)]'}`} aria-hidden="true" />
                    <div className="flex items-center justify-between gap-3 border-b border-[var(--border-muted)] px-4 py-3">
                      <span className="inline-flex min-w-0 items-center gap-1.5 text-meta text-[var(--text-secondary)]">
                        <span className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${isAssistantSnippet ? 'bg-[var(--accent-purple)]' : 'bg-[var(--accent-blue)]'}`} aria-hidden="true" />
                        <span className="truncate">{isAssistantSnippet ? t('snippets.sourceAssistant') : t('snippets.sourceSelection')}</span>
                      </span>
                      <button
                        type="button"
                        onClick={() => void remove(selectedSnippet.id)}
                        disabled={deletingID === selectedSnippet.id}
                        aria-label={t('snippets.deleteLabel')}
                        title={t('snippets.delete')}
                        className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-base leading-none text-[var(--text-muted)] opacity-70 hover:bg-[var(--bg-surface-hover)] hover:text-[var(--error)] group-hover:opacity-100 disabled:cursor-wait disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                      >
                        ×
                      </button>
                    </div>

                    <div className={`relative m-4 rounded-lg border border-[var(--border-muted)] bg-[var(--bg-inset)] px-4 py-4 ${expanded ? 'max-h-[32rem] overflow-y-auto' : 'max-h-[22rem] overflow-hidden'}`}>
                      <pre className="m-0 whitespace-pre-wrap break-words font-mono text-helper leading-6 text-[var(--text-primary)]">{expanded ? selectedSnippet.content : preview.text}</pre>
                      {!expanded && preview.truncated && (
                        <div
                          className="pointer-events-none absolute inset-x-0 bottom-0 h-12"
                          style={{ background: 'linear-gradient(to bottom, transparent, var(--bg-inset))' }}
                          aria-hidden="true"
                        />
                      )}
                    </div>

                    <div className="flex flex-wrap items-end justify-between gap-3 border-t border-[var(--border-muted)] px-4 py-3">
                      <div className="min-w-0 flex-1 text-meta text-[var(--text-muted)]">
                        <div className="truncate" title={selectedSnippet.session_name || selectedSnippet.session_id}>{selectedSnippet.session_name || selectedSnippet.session_id}</div>
                        <div className="mt-0.5 truncate" title={selectedSnippet.project || t('snippets.unknownProject')}>{selectedSnippet.project || t('snippets.unknownProject')}</div>
                        <div className="mt-0.5 truncate">{formatDate(locale, selectedSnippet.created_at)}</div>
                      </div>
                      <div className="flex flex-shrink-0 flex-wrap items-center justify-end gap-1.5">
                        {preview.truncated && (
                          <button
                            type="button"
                            onClick={() => setExpandedSnippetIDs(ids => {
                              const nextIDs = new Set(ids)
                              if (nextIDs.has(selectedSnippet.id)) nextIDs.delete(selectedSnippet.id)
                              else nextIDs.add(selectedSnippet.id)
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
                          onClick={() => onOpenSource(selectedSnippet)}
                          className="h-7 rounded-md border border-[var(--border-default)] px-2 text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                        >
                          {t('snippets.openSource')}
                        </button>
                      </div>
                    </div>
                  </article>
                )
              })()}
            </section>

            <aside className="flex min-h-[12rem] flex-col border-t border-[var(--border-default)] bg-[var(--bg-inset)]/45 lg:min-h-0 lg:border-l lg:border-t-0" data-testid="snippet-page-nav" aria-label={t('snippets.pages')}>
              <div className="flex items-center justify-between gap-2 border-b border-[var(--border-default)] px-3 py-3">
                <div className="flex min-w-0 items-center gap-2">
                  <NotebookIcon size={15} />
                  <span className="truncate text-meta font-semibold uppercase tracking-[0.12em] text-[var(--text-muted)]">{t('snippets.pages')}</span>
                </div>
                <span className="text-meta text-[var(--text-muted)]">{visibleSnippets.length}</span>
              </div>
              <div className="flex-1 space-y-0.5 overflow-y-auto p-2">
                {visibleSnippets.map(snippet => {
                  const selected = selectedSnippet?.id === snippet.id
                  const isAssistantSnippet = snippet.source_kind === 'assistant'
                  return (
                    <button
                      key={snippet.id}
                      type="button"
                      data-testid={`snippet-page-${snippet.id}`}
                      aria-current={selected ? 'page' : undefined}
                      onClick={() => setSelectedSnippetID(snippet.id)}
                      className={`flex w-full items-start gap-2 border-l-2 px-2 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] ${selected ? 'border-[var(--accent-purple)] bg-[var(--accent-purple)]/10' : 'border-transparent hover:bg-[var(--bg-surface-hover)]'}`}
                    >
                      <span className={`mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-full ${isAssistantSnippet ? 'bg-[var(--accent-purple)]' : 'bg-[var(--accent-blue)]'}`} aria-hidden="true" />
                      <span className="min-w-0">
                        <span className="block truncate text-helper text-[var(--text-primary)]">{getSnippetPageLabel(snippet)}</span>
                        <span className="mt-0.5 block truncate text-meta text-[var(--text-muted)]">{snippet.project || t('snippets.unknownProject')}</span>
                      </span>
                    </button>
                  )
                })}
                {!loading && visibleSnippets.length === 0 && (
                  <p className="px-2 py-3 text-meta leading-5 text-[var(--text-muted)]">{hasActiveFilters ? t('snippets.noMatches') : t('snippets.empty')}</p>
                )}
              </div>
            </aside>
          </div>
        </div>
      </div>
    </main>
  )
}
