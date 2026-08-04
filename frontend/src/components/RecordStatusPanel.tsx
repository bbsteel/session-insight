import { useEffect, useRef, useState } from 'react'
import type { SessionDetail } from '../types'
import {
  formatRecordTime,
  formatSourceSize,
  impactLabelKey,
  isEditorFriendlySourcePath,
  partitionSourceFiles,
  presentFromSession,
  sourceFileBaseName,
  sourceFileHelpKey,
  sourceFileLabelKey,
  sourceRoleHelpKey,
  sourceRoleLabelKey,
  sourceStateLabelKey,
  toneClass,
  warningCodeLabelKey,
} from '../recordStatusPresentation'
import type { SessionSourceFile } from '../types'
import { useI18n } from '../i18n'
import { CloseIcon } from './icons'
import { openFile, removeSessionFromIndex, resolveFile } from '../api'
import InstantTooltip from './InstantTooltip'

interface Props {
  open: boolean
  session: SessionDetail
  onClose: () => void
  onRemovedFromIndex?: () => void
  onRescan?: () => void
}

export default function RecordStatusPanel({
  open,
  session,
  onClose,
  onRemovedFromIndex,
  onRescan,
}: Props) {
  const { t, locale } = useI18n()
  const closeRef = useRef<HTMLButtonElement>(null)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [openingPath, setOpeningPath] = useState<string | null>(null)
  /** Path currently showing copy feedback: 'copied' | 'failed' */
  const [copyFeedback, setCopyFeedback] = useState<{ path: string; state: 'copied' | 'failed' } | null>(null)
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pres = presentFromSession(session)
  const prov = session.provenance
  const { main: mainSources, groups: sourceGroups } = partitionSourceFiles(prov?.sources)

  useEffect(() => {
    if (!open) return
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        e.stopImmediatePropagation()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [open, onClose])

  useEffect(() => () => {
    if (copyTimer.current) clearTimeout(copyTimer.current)
  }, [])

  if (!open) return null

  const label =
    pres.state === 'unknown' || !pres.labelKey.startsWith('record.status.')
      ? t('record.status.unknown', { code: String(pres.state) })
      : pres.state === 'degraded' && pres.warningCount > 0
        ? t('record.header.degradedCount', { n: pres.warningCount })
        : t(pres.labelKey)

  async function copyPath(path: string) {
    if (copyTimer.current) clearTimeout(copyTimer.current)
    try {
      await navigator.clipboard.writeText(path)
      setCopyFeedback({ path, state: 'copied' })
    } catch {
      setCopyFeedback({ path, state: 'failed' })
    }
    copyTimer.current = setTimeout(() => setCopyFeedback(null), 2000)
  }

  async function openPathInEditor(path: string) {
    setOpeningPath(path)
    setError(null)
    const cwd = session.cwd || ''
    try {
      // Prefer absolute regular file when resolvable; directories open via
      // open-file folder path (file managers / VS Code workspace).
      const resolved = await resolveFile(path, cwd)
      await openFile({ path: resolved || path, cwd: cwd || undefined })
    } catch {
      // Host open chain failed: SI built-in viewer for regular files only.
      try {
        const resolved = await resolveFile(path, cwd)
        if (!resolved) {
          setError(t('record.panel.openInEditorFailed'))
          return
        }
        const params = new URLSearchParams({ path: resolved, cwd })
        const w = window.open(`#/file?${params.toString()}`, '_blank', 'noopener')
        if (!w) setError(t('record.panel.openInEditorFailed'))
      } catch {
        setError(t('record.panel.openInEditorFailed'))
      }
    } finally {
      setOpeningPath(null)
    }
  }

  async function doRemove() {
    setRemoving(true)
    setError(null)
    try {
      await removeSessionFromIndex(session.id)
      setConfirmRemove(false)
      onRemovedFromIndex?.()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setRemoving(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-[410] flex justify-end bg-black/40"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="record-status-panel-title"
      data-testid="record-status-panel"
    >
      <aside
        className="flex h-full w-[min(420px,100vw)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-2 border-b border-[var(--border-default)] px-4 py-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 id="record-status-panel-title" className="text-body font-semibold text-[var(--text-primary)]">
                {t('record.panel.title')}
              </h2>
              {/* Status chip immediately after the title, e.g. 记录状态 [完整] */}
              <span
                className={`inline-flex rounded-md border px-2 py-0.5 text-meta font-medium ${toneClass(pres.tone)}`}
                data-testid="record-status-chip"
                role="status"
              >
                {label}
              </span>
            </div>
            <p className="mt-1 text-meta text-[var(--text-muted)]">{t('record.panel.subtitle')}</p>
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]"
            aria-label={t('common.close')}
          >
            <CloseIcon className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-3">
          {!prov ? (
            <p className="text-meta text-[var(--text-muted)]">{t('record.status.unavailable')}</p>
          ) : (
            <>
              {pres.emptyStateKey && (
                <p className="text-body text-[var(--text-secondary)]" data-testid="record-impact-copy">
                  {t(pres.emptyStateKey)}
                </p>
              )}

              <section>
                <h3 className="text-meta font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                  {t('record.panel.sources')}
                </h3>
                <ul className="mt-2 space-y-2" data-testid="record-source-list">
                  {mainSources.map((s, i) => (
                    <SourceFileRow
                      key={`${s.role}-${s.path}-${i}`}
                      source={s}
                      locale={locale}
                      t={t}
                      copyFeedback={copyFeedback}
                      openingPath={openingPath}
                      onCopyPath={copyPath}
                      onOpenPath={openPathInEditor}
                    />
                  ))}
                  {sourceGroups.map(g => (
                    <li key={g.role} className="rounded-md border border-[var(--border-default)] p-2">
                      <details
                        data-testid={`record-source-group-${g.role}`}
                        className="group"
                      >
                        <summary className="cursor-pointer list-none text-meta [&::-webkit-details-marker]:hidden">
                          <div className="flex items-center justify-between gap-2">
                            <InstantTooltip text={t(sourceRoleHelpKey(g.role), { role: g.role })} maxWidth={320}>
                              <span
                                className="cursor-help border-b border-dotted border-[var(--text-muted)] font-medium text-[var(--text-primary)]"
                                data-testid={`record-source-group-label-${g.role}`}
                              >
                                {t('record.panel.sourceGroup', {
                                  role: t(sourceRoleLabelKey(g.role), { role: g.role }),
                                  n: g.sources.length,
                                })}
                              </span>
                            </InstantTooltip>
                            <span className="text-[var(--text-muted)] group-open:hidden">
                              {t('record.panel.sourceGroupExpand')}
                            </span>
                            <span className="hidden text-[var(--text-muted)] group-open:inline">
                              {t('record.panel.sourceGroupCollapse')}
                            </span>
                          </div>
                          <p className="mt-0.5 text-[var(--text-muted)]">
                            {t(sourceRoleHelpKey(g.role), { role: g.role })}
                          </p>
                        </summary>
                        <ul className="mt-2 space-y-2 border-t border-[var(--border-default)] pt-2">
                          {g.sources.map((s, i) => (
                            <SourceFileRow
                              key={`${g.role}-${s.path}-${i}`}
                              source={s}
                              locale={locale}
                              t={t}
                              copyFeedback={copyFeedback}
                              openingPath={openingPath}
                              onCopyPath={copyPath}
                              onOpenPath={openPathInEditor}
                              compact
                            />
                          ))}
                        </ul>
                      </details>
                    </li>
                  ))}
                </ul>
              </section>

              <section className="space-y-1 text-meta text-[var(--text-secondary)]">
                <div>
                  <span className="text-[var(--text-muted)]">{t('record.panel.siCapture')}: </span>
                  <span data-testid="record-si-capture">{formatRecordTime(locale, prov.captured_at)}</span>
                </div>
                {prov.source_updated_at && (
                  <div>
                    <span className="text-[var(--text-muted)]">{t('record.panel.sourceUpdated')}: </span>
                    <span data-testid="record-source-updated">{formatRecordTime(locale, prov.source_updated_at)}</span>
                  </div>
                )}
                {prov.last_successful_at && (
                  <div>
                    <span className="text-[var(--text-muted)]">{t('record.panel.lastSuccessful')}: </span>
                    {formatRecordTime(locale, prov.last_successful_at)}
                  </div>
                )}
                {prov.missing_since && (
                  <div>
                    <span className="text-[var(--text-muted)]">{t('record.panel.missingSince')}: </span>
                    {formatRecordTime(locale, prov.missing_since)}
                  </div>
                )}
                <div className="flex items-center gap-1">
                  <InstantTooltip text={t('record.panel.adapterRevisionHelp')} maxWidth={320}>
                    <span
                      className="cursor-help border-b border-dotted border-[var(--text-muted)] text-[var(--text-muted)]"
                      data-testid="record-adapter-revision-label"
                    >
                      {t('record.panel.adapterRevision')}
                    </span>
                  </InstantTooltip>
                  <span>: </span>
                  <span data-testid="record-adapter-revision">{prov.adapter_revision}</span>
                </div>
              </section>

              <section>
                <h3 className="text-meta font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                  {t('record.panel.warnings')}
                </h3>
                {prov.warning_summary?.impact_counts && Object.keys(prov.warning_summary.impact_counts).length > 0 && (
                  <div className="mt-1 flex flex-wrap gap-1">
                    {Object.entries(prov.warning_summary.impact_counts).map(([imp, n]) => (
                      <span key={imp} className="rounded bg-[var(--bg-surface-hover)] px-1.5 py-0.5 text-meta">
                        {t(impactLabelKey(imp), { impact: imp })}: {n}
                      </span>
                    ))}
                  </div>
                )}
                {(prov.warnings || []).length === 0 ? (
                  <p className="mt-2 text-meta text-[var(--text-muted)]">{t('record.panel.noWarnings')}</p>
                ) : (
                  <ul className="mt-2 space-y-2" data-testid="record-warning-list">
                    {prov.warnings.map((w, i) => (
                      <li key={`${w.code}-${i}`} className="rounded-md border border-[var(--border-default)] p-2 text-meta">
                        <div className="font-medium text-[var(--text-primary)]">
                          {t(warningCodeLabelKey(w.code), { code: w.code })}
                          {w.count > 1 ? ` ×${w.count}` : ''}
                        </div>
                        <div className="mt-0.5 text-[var(--text-muted)]">
                          {w.source_role && (
                            <span>{t(sourceRoleLabelKey(w.source_role), { role: w.source_role })} · </span>
                          )}
                          {(w.impacts || []).map(imp => t(impactLabelKey(imp), { impact: imp })).join(', ')}
                          {typeof w.first_record === 'number' && ` · #${w.first_record}`}
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              <section className="flex flex-col gap-2">
                {onRescan && (
                  <button
                    type="button"
                    className="rounded-md border border-[var(--border-default)] px-3 py-1.5 text-nav hover:bg-[var(--bg-surface-hover)]"
                    onClick={onRescan}
                  >
                    {t('record.panel.rescan')}
                  </button>
                )}
                {pres.state === 'source_missing' && (
                  <div className="rounded-md border border-[var(--error)]/30 bg-[var(--error)]/5 p-2">
                    <p className="text-meta text-[var(--text-secondary)]">{t('record.panel.removeFromIndexHelp')}</p>
                    {!confirmRemove ? (
                      <button
                        type="button"
                        className="mt-2 rounded-md border border-[var(--error)]/50 px-3 py-1.5 text-nav text-[var(--error)] hover:bg-[var(--error)]/10"
                        data-testid="record-remove-from-index"
                        onClick={() => setConfirmRemove(true)}
                      >
                        {t('record.panel.removeFromIndex')}
                      </button>
                    ) : (
                      <div className="mt-2 space-y-2" data-testid="record-remove-confirm">
                        <p className="text-meta font-medium">{t('record.panel.removeConfirmTitle')}</p>
                        <p className="text-meta">{t('record.panel.removeConfirm')}</p>
                        <div className="flex gap-2">
                          <button
                            type="button"
                            disabled={removing}
                            className="rounded-md bg-[var(--error)] px-3 py-1.5 text-nav text-white"
                            onClick={() => void doRemove()}
                          >
                            {t('record.panel.removeFromIndex')}
                          </button>
                          <button
                            type="button"
                            className="rounded-md border border-[var(--border-default)] px-3 py-1.5 text-nav"
                            onClick={() => setConfirmRemove(false)}
                          >
                            {t('common.cancel')}
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
                {error && <p className="text-meta text-[var(--error)]">{error}</p>}
              </section>
            </>
          )}
        </div>
      </aside>
    </div>
  )
}

type CopyFeedback = { path: string; state: 'copied' | 'failed' } | null

function SourceFileRow({
  source: s,
  locale,
  t,
  copyFeedback,
  openingPath,
  onCopyPath,
  onOpenPath,
  compact = false,
}: {
  source: SessionSourceFile
  locale: string
  t: (key: string, vars?: Record<string, string | number>) => string
  copyFeedback: CopyFeedback
  openingPath: string | null
  onCopyPath: (path: string) => void | Promise<void>
  onOpenPath: (path: string) => void | Promise<void>
  compact?: boolean
}) {
  return (
    <li
      className={
        compact
          ? 'rounded border border-[var(--border-default)]/70 p-1.5'
          : 'rounded-md border border-[var(--border-default)] p-2'
      }
    >
      <div className={`flex items-center justify-between gap-2 text-meta ${compact ? '' : ''}`}>
        <div className="min-w-0 flex flex-wrap items-center gap-x-2 gap-y-0.5">
          {!compact && (
            <InstantTooltip text={t(sourceRoleHelpKey(s.role), { role: s.role })} maxWidth={320}>
              <span
                className="cursor-help border-b border-dotted border-[var(--text-muted)] font-medium text-[var(--text-primary)]"
                data-testid="record-source-role"
              >
                {t(sourceRoleLabelKey(s.role), { role: s.role })}
              </span>
            </InstantTooltip>
          )}
          {(() => {
            const labelKey = sourceFileLabelKey(s.path)
            const helpKey = sourceFileHelpKey(s.path)
            const base = sourceFileBaseName(s.path)
            if (labelKey && helpKey) {
              return (
                <InstantTooltip text={t(helpKey, { name: base })} maxWidth={340}>
                  <span
                    className="cursor-help border-b border-dotted border-[var(--text-muted)] text-[var(--text-secondary)]"
                    data-testid="record-source-file-label"
                  >
                    {t(labelKey, { name: base })}
                  </span>
                </InstantTooltip>
              )
            }
            if (helpKey) {
              return (
                <InstantTooltip text={t(helpKey, { name: base })} maxWidth={340}>
                  <span
                    className="cursor-help border-b border-dotted border-[var(--text-muted)] font-mono text-[11px] text-[var(--text-secondary)]"
                    data-testid="record-source-file-label"
                  >
                    {base}
                  </span>
                </InstantTooltip>
              )
            }
            return null
          })()}
        </div>
        {!compact && (
          <span className="shrink-0 text-[var(--text-muted)]">
            {t(sourceStateLabelKey(s.state), { state: s.state })}
          </span>
        )}
      </div>
      <code
        className="mt-1 block break-all font-mono text-[11px] text-[var(--text-secondary)]"
        title={s.path}
      >
        {s.path}
      </code>
      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-meta text-[var(--text-muted)]">
        {s.updated_at && (
          <span>
            {t('record.panel.sourceUpdated')}: {formatRecordTime(locale, s.updated_at)}
          </span>
        )}
        {typeof s.size_bytes === 'number' && (
          <span data-testid="record-source-size">· {formatSourceSize(s.size_bytes)}</span>
        )}
        <button
          type="button"
          className="text-[var(--accent-blue)] hover:underline"
          data-testid="record-copy-path"
          data-state={copyFeedback?.path === s.path ? copyFeedback.state : 'idle'}
          onClick={() => void onCopyPath(s.path)}
        >
          {copyFeedback?.path === s.path && copyFeedback.state === 'copied'
            ? t('common.copied')
            : copyFeedback?.path === s.path && copyFeedback.state === 'failed'
              ? t('record.panel.copyFailed')
              : t('record.panel.copyPath')}
        </button>
        {s.path && s.state === 'present' && isEditorFriendlySourcePath(s.path) && (
          <button
            type="button"
            className="text-[var(--accent-blue)] hover:underline disabled:opacity-50"
            data-testid="record-open-in-editor"
            disabled={openingPath === s.path}
            onClick={() => void onOpenPath(s.path)}
          >
            {t('record.panel.openInEditor')}
          </button>
        )}
        {s.path && s.state === 'present' && !isEditorFriendlySourcePath(s.path) && (
          <span className="text-[var(--text-muted)]" data-testid="record-open-not-text">
            {t('record.panel.openBinaryHint')}
          </span>
        )}
      </div>
    </li>
  )
}
