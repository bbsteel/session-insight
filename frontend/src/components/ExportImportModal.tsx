import { useEffect, useState } from 'react'
import {
  APIError,
  deleteImportBundle,
  exportBundle,
  importBundle,
  listImportBundles,
  type ImportBundleSummary,
} from '../api'
import type { SessionSummary } from '../types'
import {
  buildExportRequest,
  bundleFilename,
  formatBundleSummary,
  initialExportSelection,
  sessionSelectionKey,
} from '../importPresentation'
import { getAgentLabel } from '../sidebarRows'
import { formatDate, useI18n } from '../i18n'

/** Warn (and confirm on download) once the selection is this large. */
const LARGE_SELECTION_WARN = 50

interface Props {
  /** The sidebar's already-filtered session list. */
  sessions: SessionSummary[]
  /** Currently focused sidebar session, if any — pre-checked when present in `sessions`. */
  preferred?: Pick<SessionSummary, 'id' | 'agent_type'> | null
  onClose: () => void
}

// Export: pick sessions + options → streamed .sibundle download via an
// object-URL anchor (fetch POST can't ride window.location). Import: pick a
// .sibundle file → multipart POST; the sidebar refetches off the SSE ping.
//
// Selection defaults to empty (or only the focused session) so opening the
// dialog never implies "export every filtered session" (e.g. 680 rows).
export default function ExportImportModal({ sessions, preferred = null, onClose }: Props) {
  const { locale, t } = useI18n()

  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(initialExportSelection(sessions, preferred)),
  )
  const [includeRaw, setIncludeRaw] = useState(false)
  const [redact, setRedact] = useState(false)
  const [caseLabel, setCaseLabel] = useState('')
  const [exporting, setExporting] = useState(false)
  const [exportError, setExportError] = useState<string | null>(null)

  const [importing, setImporting] = useState(false)
  const [importMessage, setImportMessage] = useState<{ kind: 'ok' | 'error'; text: string } | null>(null)
  const [bundles, setBundles] = useState<ImportBundleSummary[] | null>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const reloadBundles = () => {
    listImportBundles()
      .then(setBundles)
      .catch(() => setBundles([]))
  }
  useEffect(reloadBundles, [])

  const toggleOne = (key: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const selectAll = () => {
    if (sessions.length >= LARGE_SELECTION_WARN) {
      const ok = window.confirm(t('exportBundle.selectAllConfirm', { count: sessions.length }))
      if (!ok) return
    }
    setSelected(new Set(sessions.map(sessionSelectionKey)))
  }

  const clearSelection = () => {
    setSelected(new Set())
  }

  const download = async () => {
    const chosen = sessions.filter(s => selected.has(sessionSelectionKey(s)))
    if (chosen.length === 0) return
    if (chosen.length >= LARGE_SELECTION_WARN) {
      const ok = window.confirm(t('exportBundle.largeExportConfirm', { count: chosen.length }))
      if (!ok) return
    }
    setExporting(true)
    setExportError(null)
    try {
      const blob = await exportBundle(buildExportRequest(chosen, { includeRaw, redact, caseLabel }))
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = bundleFilename()
      document.body.appendChild(anchor)
      anchor.click()
      anchor.remove()
      URL.revokeObjectURL(url)
    } catch (err) {
      const detail = err instanceof Error ? err.message : ''
      setExportError(detail ? `${t('exportBundle.failed')} ${detail}` : t('exportBundle.failed'))
    } finally {
      setExporting(false)
    }
  }

  const pickFile = async (file: File | undefined) => {
    if (!file) return
    setImporting(true)
    setImportMessage(null)
    try {
      const result = await importBundle(file)
      setImportMessage({
        kind: 'ok',
        text: t('importBundle.success', { count: result.imported, host: result.origin_host || t('importBundle.unknownHost') }),
      })
      reloadBundles()
    } catch (err) {
      // The backend answers 400 for an unsupported bundle format version.
      const unsupported = err instanceof APIError && err.status === 400
      const detail = !unsupported && err instanceof Error ? err.message : ''
      const base = t(unsupported ? 'importBundle.unsupportedVersion' : 'importBundle.failed')
      setImportMessage({ kind: 'error', text: detail ? `${base} ${detail}` : base })
    } finally {
      setImporting(false)
    }
  }

  const removeBundle = async (bundle: ImportBundleSummary) => {
    if (!window.confirm(t('importBundle.deleteConfirm'))) return
    try {
      await deleteImportBundle(bundle.bundle_id)
      reloadBundles()
    } catch (err) {
      setImportMessage({ kind: 'error', text: err instanceof Error ? err.message : t('importBundle.failed') })
    }
  }

  const inputCls = 'h-8 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2.5 text-helper text-[var(--text-primary)] shadow-sm placeholder:text-[var(--text-muted)] hover:border-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]'
  const btnCls = 'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'
  const primaryCls = 'h-7 rounded-md bg-[var(--accent-blue)] px-3 text-helper font-medium text-[var(--text-inverse)] transition-opacity duration-fast hover:opacity-90 disabled:opacity-50'
  const checkboxCls = 'h-3.5 w-3.5 accent-[var(--accent-blue)]'
  const allSelected = sessions.length > 0 && selected.size === sessions.length

  return (
    <div className="fixed inset-0 z-[400] flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={t('sidebar.exportImport')}
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-xl w-[min(560px,94vw)] h-[min(680px,88vh)] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex flex-shrink-0 items-center justify-between px-4 py-2.5 border-b border-[var(--border-default)]">
          <div className="text-sm font-medium text-[var(--text-primary)]">{t('sidebar.exportImport')}</div>
          <button onClick={onClose} aria-label={t('common.close')} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] text-lg leading-none px-1">✕</button>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
          {/* Export */}
          <section className="flex flex-col gap-2">
            <h3 className="text-nav font-semibold text-[var(--text-primary)]">{t('exportBundle.title')}</h3>
            {sessions.length === 0 ? (
              <div className="rounded-md border border-dashed border-[var(--border-default)] p-3 text-center text-helper text-[var(--text-secondary)]">
                {t('exportBundle.empty')}
              </div>
            ) : (
              <>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-meta text-[var(--text-muted)]">{t('exportBundle.selected', { count: selected.size })}</span>
                  <div className="flex items-center gap-1.5">
                    <button type="button" className={btnCls} onClick={selectAll} disabled={allSelected}>
                      {t('exportBundle.selectAll')}
                    </button>
                    <button type="button" className={btnCls} onClick={clearSelection} disabled={selected.size === 0}>
                      {t('exportBundle.clearSelection')}
                    </button>
                  </div>
                </div>
                {selected.size >= LARGE_SELECTION_WARN && (
                  <p className="text-meta text-[var(--warning)]">
                    {t('exportBundle.largeSelectionHint', { count: selected.size })}
                  </p>
                )}
                <ul className="max-h-40 overflow-y-auto rounded-md border border-[var(--border-default)] py-0.5">
                  {sessions.map(s => {
                    const key = sessionSelectionKey(s)
                    return (
                      <li key={key}>
                        <label className="flex cursor-pointer items-center gap-2 px-2.5 py-1 text-helper text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]">
                          <input
                            type="checkbox"
                            className={checkboxCls}
                            checked={selected.has(key)}
                            onChange={() => toggleOne(key)}
                          />
                          <span className="min-w-0 flex-1 truncate">{s.name || s.repository || s.id.slice(0, 8)}</span>
                          <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">{getAgentLabel(s.agent_type)}</span>
                        </label>
                      </li>
                    )
                  })}
                </ul>
                <label className="flex items-center gap-1.5 text-helper text-[var(--text-primary)]">
                  <input type="checkbox" className={checkboxCls} checked={includeRaw} onChange={e => setIncludeRaw(e.target.checked)} />
                  {t('exportBundle.includeRaw')}
                </label>
                <div>
                  <label className="flex items-center gap-1.5 text-helper text-[var(--text-primary)]">
                    <input type="checkbox" className={checkboxCls} checked={redact} onChange={e => setRedact(e.target.checked)} />
                    {t('exportBundle.redact')}
                  </label>
                  <p className="ml-5 text-meta text-[var(--text-muted)]">{t('exportBundle.redactHint')}</p>
                </div>
                <label className="block text-helper text-[var(--text-primary)]">
                  {t('exportBundle.caseLabel')}
                  <input
                    type="text"
                    className={`${inputCls} mt-1`}
                    placeholder={t('exportBundle.caseLabelPlaceholder')}
                    value={caseLabel}
                    onChange={e => setCaseLabel(e.target.value)}
                  />
                </label>
                {exportError && <p className="text-helper text-[var(--error)] break-all">{exportError}</p>}
                <button className={`${primaryCls} self-start`} disabled={exporting || selected.size === 0} onClick={() => void download()}>
                  {t('exportBundle.download')}
                </button>
              </>
            )}
          </section>

          {/* Import */}
          <section className="flex flex-col gap-2 border-t border-[var(--border-default)] pt-3">
            <h3 className="text-nav font-semibold text-[var(--text-primary)]">{t('importBundle.title')}</h3>
            <label className={`${btnCls} inline-flex w-fit cursor-pointer items-center`}>
              <input
                type="file"
                accept=".sibundle"
                className="hidden"
                disabled={importing}
                onChange={e => {
                  void pickFile(e.target.files?.[0])
                  e.target.value = ''
                }}
              />
              {importing ? t('importBundle.importing') : t('importBundle.chooseFile')}
            </label>
            {importMessage && (
              <p className={`text-helper break-all ${importMessage.kind === 'ok' ? 'text-[var(--success)]' : 'text-[var(--error)]'}`}>
                {importMessage.text}
              </p>
            )}

            <h4 className="mt-1 text-helper font-medium text-[var(--text-secondary)]">{t('importBundle.bundles')}</h4>
            {bundles === null ? (
              <div className="text-helper text-[var(--text-secondary)]">{t('common.loading')}</div>
            ) : bundles.length === 0 ? (
              <div className="rounded-md border border-dashed border-[var(--border-default)] p-3 text-center text-helper text-[var(--text-secondary)]">
                {t('importBundle.empty')}
              </div>
            ) : (
              <ul className="space-y-1.5">
                {bundles.map(b => (
                  <li key={b.bundle_id} className="flex items-center gap-2 rounded-md border border-[var(--border-muted)] px-2.5 py-1.5">
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-helper text-[var(--text-primary)]">{formatBundleSummary(t, b)}</div>
                      <div className="text-meta text-[var(--text-muted)]">{formatDate(locale, b.imported_at, { dateStyle: 'medium', timeStyle: 'short' })}</div>
                    </div>
                    <button className={`${btnCls} flex-shrink-0 text-[var(--error)]`} onClick={() => void removeBundle(b)}>
                      {t('importBundle.delete')}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      </div>
    </div>
  )
}
