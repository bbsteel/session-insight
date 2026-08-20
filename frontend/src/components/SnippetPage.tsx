import { useEffect, useMemo, useState } from 'react'
import { deleteSnippet, fetchSnippets } from '../api'
import { formatDate, useI18n } from '../i18n'
import type { Snippet } from '../types'

interface Props {
  onBack: () => void
  onOpenSource: (snippet: Snippet) => void
}

const SNIPPET_PREVIEW_LINE_LIMIT = 8
const SNIPPET_PREVIEW_CHARACTER_LIMIT = 900

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

  const visibleSnippets = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase()
    if (!normalizedQuery) return snippets
    return snippets.filter(snippet =>
      `${snippet.content}\n${snippet.session_name}\n${snippet.session_id}\n${snippet.agent_type}`
        .toLocaleLowerCase()
        .includes(normalizedQuery),
    )
  }, [query, snippets])

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
            <p className="mt-1 text-helper text-[var(--text-muted)]">{t('snippets.count', { count: visibleSnippets.length })}</p>
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

        {loading ? (
          <p className="mt-6 text-helper text-[var(--text-muted)]">{t('common.loading')}</p>
        ) : visibleSnippets.length === 0 ? (
          <p className="mt-6 max-w-xl text-helper leading-6 text-[var(--text-muted)]">{t('snippets.empty')}</p>
        ) : (
          <section className="mt-5 grid grid-cols-1 items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label={t('snippets.title')}>
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
    </main>
  )
}
