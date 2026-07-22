import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { fetchToolOutputs, type TruncatedOutput } from '../api'
import { useI18n } from '../i18n'

const SyntaxCodeBlock = lazy(() => import('./SyntaxCodeBlock'))

interface Props {
  sessionId: string
  outputIndex: number
  onClose: () => void
}

// OutputModal shows the full content of a truncated tool output. The render
// keeps only the first 10 lines and a "[+] N 行被截断（点击展开）" note;
// clicking that note lands here with the note's output_index.
export default function OutputModal({ sessionId, outputIndex, onClose }: Props) {
  const { t } = useI18n()
  const [outputs, setOutputs] = useState<TruncatedOutput[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchToolOutputs(sessionId)
      .then(data => { if (!cancelled) setOutputs(data) })
      .catch(err => { if (!cancelled) setError(err.message) })
    return () => { cancelled = true }
  }, [sessionId])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const output = outputs?.[outputIndex]

  // JSON payloads get pretty-printed syntax highlighting; anything that
  // doesn't parse stays a plain <pre> untouched.
  const prettyJson = useMemo(() => {
    const t = output?.content.trim() ?? ''
    if (!t.startsWith('{') && !t.startsWith('[')) return null
    try {
      return JSON.stringify(JSON.parse(t), null, 2)
    } catch {
      return null
    }
  }, [output])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-xl w-[min(920px,92vw)] max-h-[84vh] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-[var(--border-default)]">
          <div className="text-sm font-medium text-[var(--text-primary)]">
            {output ? t('output.toolTitle', { tool: output.tool_name || t('output.toolFallback') }) : t('output.title')}
            {output && (
              <span className="ml-2 text-meta text-[var(--text-secondary)]">
                {t('output.meta', { turn: output.turn_index, kind: output.kind, lines: output.content.split('\n').length })}
              </span>
            )}
          </div>
          <button onClick={onClose} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] text-lg leading-none px-1">✕</button>
        </div>
        <div className="flex-1 overflow-auto p-3">
          {error && <div className="text-sm text-[var(--error)]">{t('output.loadFailed', { error })}</div>}
          {!error && !outputs && <div className="text-sm text-[var(--text-secondary)]">{t('common.loading')}</div>}
          {!error && outputs && !output && <div className="text-sm text-[var(--text-secondary)]">{t('output.notFound')}</div>}
          {output && (prettyJson !== null ? (
            <Suspense fallback={
              <pre className="text-xs leading-5 font-mono whitespace-pre-wrap break-all text-[var(--text-primary)] bg-[var(--bg-inset)] rounded-md p-3">
                {prettyJson}
              </pre>
            }>
              <SyntaxCodeBlock code={prettyJson} language="json" />
            </Suspense>
          ) : (
            <pre className="text-xs leading-5 font-mono whitespace-pre-wrap break-all text-[var(--text-primary)] bg-[var(--bg-inset)] rounded-md p-3">
              {output.content}
            </pre>
          ))}
        </div>
      </div>
    </div>
  )
}
