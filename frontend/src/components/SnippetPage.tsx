import { useEffect, useMemo, useState } from 'react'
import { deleteSnippet, fetchSnippets } from '../api'
import { formatDate, useI18n } from '../i18n'
import type { Snippet } from '../types'

interface Props {
  onBack: () => void
  onOpenSource: (snippet: Snippet) => void
}

export default function SnippetPage({ onBack, onOpenSource }: Props) {
  const { locale, t } = useI18n()
  const [snippets, setSnippets] = useState<Snippet[]>([])
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deletingID, setDeletingID] = useState<number | null>(null)

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
    } catch {
      setError('snippets.deleteFailed')
    } finally {
      setDeletingID(null)
    }
  }

  return (
    <main className="min-h-screen overflow-y-auto bg-[var(--bg-primary)] text-[var(--text-primary)]">
      <div className="mx-auto w-full max-w-5xl px-6 py-8">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h1 className="text-xl font-semibold">{t('snippets.title')}</h1>
          </div>
          <button
            type="button"
            onClick={onBack}
            className="h-8 rounded-md border border-[var(--border-default)] px-3 text-helper text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            {t('snippets.back')}
          </button>
        </div>

        <input
          value={query}
          onChange={event => setQuery(event.target.value)}
          placeholder={t('snippets.search')}
          aria-label={t('snippets.searchLabel')}
          className="mt-5 h-9 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 text-helper text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20"
        />
        {error && <p className="mt-3 text-helper text-[var(--error)]" role="status">{t(error)}</p>}

        {loading ? (
          <p className="mt-6 text-helper text-[var(--text-muted)]">{t('common.loading')}</p>
        ) : visibleSnippets.length === 0 ? (
          <p className="mt-6 max-w-xl text-helper leading-6 text-[var(--text-muted)]">{t('snippets.empty')}</p>
        ) : (
          <section className="mt-5 space-y-3" aria-label={t('snippets.title')}>
            {visibleSnippets.map(snippet => (
              <article key={snippet.id} className="rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] p-4 shadow-sm">
                <pre className="m-0 whitespace-pre-wrap break-words font-mono text-helper leading-6 text-[var(--text-primary)]">{snippet.content}</pre>
                <div className="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border-muted)] pt-3">
                  <div className="min-w-0 text-meta text-[var(--text-muted)]">
                    <span>{snippet.source_kind === 'assistant' ? t('snippets.sourceAssistant') : t('snippets.sourceSelection')}</span>
                    <span> · {snippet.session_name || snippet.session_id}</span>
                    <span> · {formatDate(locale, snippet.created_at)}</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => onOpenSource(snippet)}
                      className="h-7 rounded-md border border-[var(--border-default)] px-2 text-meta text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                    >
                      {t('snippets.openSource')}
                    </button>
                    <button
                      type="button"
                      onClick={() => void remove(snippet.id)}
                      disabled={deletingID === snippet.id}
                      aria-label={t('snippets.deleteLabel')}
                      title={t('snippets.delete')}
                      className="h-7 rounded-md px-2 text-meta text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--error)] disabled:cursor-wait disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
                    >
                      ×
                    </button>
                  </div>
                </div>
              </article>
            ))}
          </section>
        )}
      </div>
    </main>
  )
}
