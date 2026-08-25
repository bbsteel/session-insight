import { useCallback, useEffect, useRef, useState } from 'react'
import {
  APIError,
  deleteSessionChangeRequest,
  fetchSessionChangeRequests,
  fetchSessionGitEvidence,
  fetchSessionGitPatch,
} from '../api'
import type {
  GitFileChange,
  SessionChangeRequestLink,
  SessionGitEvidenceEnvelope,
  SessionGitRepositoryEvidence,
  SessionRecordedChangeReference,
} from '../gitEvidence'
import { formatDate, useI18n } from '../i18n'
import { useModalFocus } from '../modalFocus'
import ChangeRequestLookupDialog from './ChangeRequestLookupDialog'

interface Props {
  session: { id: string; agent_type: string }
  onClose: () => void
  onSelectSession?: (id: string, agentType?: string, focusSidebar?: boolean) => void
}

function changeIdentity(link: SessionChangeRequestLink): string {
  const repository = link.change.target_repository?.slug
  const object = link.change.provider_object_id || link.change.generic_opaque_id
  return [repository, object].filter(Boolean).join(' · ') || link.change.provider
}

function recordedReferenceName(entry: SessionRecordedChangeReference): string {
  const repository = entry.reference.target_repository_slug
  const number = entry.reference.display_number
  if (repository && number) return `${repository}#${number}`
  return entry.reference.normalized_url
}

function AssessmentBadge({ state }: { state: string }) {
  const { t } = useI18n()
  const tone = state === 'exact'
    ? 'border-[var(--accent-green)]/30 bg-[color-mix(in_srgb,var(--accent-green)_12%,transparent)] text-[var(--accent-green)]'
    : state === 'estimated'
      ? 'border-[var(--accent-blue)]/30 bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
      : 'border-[var(--warning)]/30 bg-[var(--warning)]/10 text-[var(--warning)]'
  return <span className={`rounded border px-1.5 py-0.5 text-meta ${tone}`}>{t(`git.state.${state}`)}</span>
}

export default function GitEvidencePanel({ session, onClose, onSelectSession }: Props) {
  const { locale, t } = useI18n()
  const [evidence, setEvidence] = useState<SessionGitEvidenceEnvelope | null>(null)
  const [links, setLinks] = useState<SessionChangeRequestLink[]>([])
  const [derived, setDerived] = useState<SessionRecordedChangeReference[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lookupOpen, setLookupOpen] = useState(false)
  const [unlinking, setUnlinking] = useState<string | null>(null)
  const [patch, setPatch] = useState<{ repository: SessionGitRepositoryEvidence; file: GitFileChange; text?: string; error?: string } | null>(null)
  const panelRef = useRef<HTMLElement>(null)
  const panelCloseRef = useRef<HTMLButtonElement>(null)
  const patchRef = useRef<HTMLElement>(null)
  const patchCloseRef = useRef<HTMLButtonElement>(null)
  const evidenceEtagRef = useRef<string | null>(null)
  const closePatch = useCallback(() => setPatch(null), [])
  useModalFocus(panelRef, onClose, panelCloseRef)
  useModalFocus(patchRef, closePatch, patchCloseRef, patch !== null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [evidenceResult, storedLinks] = await Promise.all([
        fetchSessionGitEvidence(session.id, session.agent_type, { etag: evidenceEtagRef.current }),
        fetchSessionChangeRequests(session.id, session.agent_type),
      ])
      if (evidenceResult !== 'not-modified') {
        evidenceEtagRef.current = evidenceResult.etag
        setEvidence(evidenceResult.evidence)
      }
      setLinks(storedLinks.links)
      setDerived(storedLinks.derived)
    } catch (cause) {
      setError(cause instanceof APIError ? cause.code : 'git_evidence_load_failed')
    } finally {
      setLoading(false)
    }
  }, [session.agent_type, session.id])

  useEffect(() => { void load() }, [load])

  const openPatch = async (repository: SessionGitRepositoryEvidence, file: GitFileChange) => {
    setPatch({ repository, file })
    try {
      const text = await fetchSessionGitPatch(
        session.id,
        session.agent_type,
        repository.repository_entry_key,
        file.key,
      )
      setPatch(current => current?.file.key === file.key ? { ...current, text } : current)
    } catch (cause) {
      const code = cause instanceof APIError ? cause.code : 'git_patch_load_failed'
      setPatch(current => current?.file.key === file.key ? { ...current, error: code } : current)
    }
  }

  const unlink = async (linkID: string) => {
    setUnlinking(linkID)
    setError(null)
    try {
      await deleteSessionChangeRequest(session.id, session.agent_type, linkID)
      await load()
    } catch (cause) {
      setError(cause instanceof APIError ? cause.code : 'change_request_unlink_failed')
    } finally {
      setUnlinking(null)
    }
  }

  return (
    <div className="fixed inset-0 z-[350] flex justify-end bg-[rgba(0,0,0,var(--opacity-overlay))] backdrop-blur-[1px]" onClick={onClose}>
      <aside
        ref={panelRef}
        className="flex h-full w-[min(920px,92vw)] flex-col border-l border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        onClick={event => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="git-evidence-title"
        data-testid="git-evidence-panel"
      >
        <header className="flex min-h-14 flex-shrink-0 items-center gap-3 border-b border-[var(--border-default)] px-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 id="git-evidence-title" className="text-body font-semibold text-[var(--text-primary)]">{t('git.panel.title')}</h2>
              {evidence && <AssessmentBadge state={evidence.assessment.state} />}
            </div>
            <p className="truncate text-meta text-[var(--text-muted)]">{t('git.panel.subtitle')}</p>
          </div>
          <button
            type="button"
            onClick={() => setLookupOpen(true)}
            className="h-8 rounded-md bg-[var(--accent-blue)] px-3 text-nav font-medium text-white"
            data-testid="git-link-change-request"
          >
            {t('git.panel.linkChange')}
          </button>
          <button ref={panelCloseRef} type="button" onClick={onClose} className="h-8 w-8 rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)]" aria-label={t('common.close')}>×</button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {loading && <div className="py-12 text-center text-helper text-[var(--text-muted)]">{t('common.loading')}</div>}
          {error && (
            <div className="mb-4 flex items-center justify-between gap-3 rounded-md border border-[var(--error)]/30 bg-[var(--error)]/10 px-3 py-2 text-helper text-[var(--error)]" role="alert">
              <span>{t(`error.${error}`)}</span>
              <button type="button" className="underline" onClick={() => void load()}>{t('common.retry')}</button>
            </div>
          )}

          {!loading && evidence && (
            <>
              {evidence.assessment.state !== 'exact' && (
                <div className="mb-4 rounded-lg border border-[var(--warning)]/30 bg-[var(--warning)]/10 p-3">
                  <p className="text-helper font-medium text-[var(--warning)]">{t('git.panel.incomplete')}</p>
                  <p className="mt-1 text-meta text-[var(--text-secondary)]">
                    {t(`git.reason.${evidence.assessment.reason_code || 'unknown'}`)}
                  </p>
                </div>
              )}

              <section className="mb-5">
                <div className="mb-2 flex items-center justify-between">
                  <h3 className="text-nav font-semibold text-[var(--text-primary)]">{t('git.links.title')}</h3>
                  <span className="text-meta text-[var(--text-muted)]">{t('git.links.count', { count: links.length + derived.length })}</span>
                </div>
                {links.length === 0 && derived.length === 0 ? (
                  <div className="rounded-lg border border-dashed border-[var(--border-default)] px-4 py-5 text-center text-helper text-[var(--text-muted)]">
                    {t('git.links.empty')}
                  </div>
                ) : (
                  <div className="space-y-2">
                    {links.map(link => (
                      <div key={link.link_id} className="flex items-center gap-3 rounded-lg border border-[var(--border-default)] bg-[var(--bg-primary)] px-3 py-2" data-testid="session-change-request-link">
                        <span className="rounded border border-[var(--border-muted)] px-1.5 py-0.5 text-meta text-[var(--text-secondary)]">
                          {t(`git.provider.${link.change.provider}`)}
                        </span>
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-helper font-medium text-[var(--text-primary)]">{changeIdentity(link)}</div>
                          <div className="mt-0.5 text-meta text-[var(--text-muted)]">
                            {t(`git.relationship.${link.relationship}`)} · {t(`git.state.${link.assessment.state}`)}
                          </div>
                        </div>
                        <button
                          type="button"
                          onClick={() => void unlink(link.link_id)}
                          disabled={unlinking !== null}
                          className="rounded-md px-2 py-1 text-meta text-[var(--error)] hover:bg-[var(--error)]/10 disabled:opacity-50"
                        >
                          {unlinking === link.link_id ? t('git.links.unlinking') : t('git.links.unlink')}
                        </button>
                      </div>
                    ))}
                    {derived.map(entry => (
                      <a
                        key={`derived-${entry.reference.normalized_url}`}
                        href={entry.reference.normalized_url}
                        target="_blank"
                        rel="noreferrer"
                        className="flex items-center gap-3 rounded-lg border border-[var(--border-default)] bg-[var(--bg-primary)] px-3 py-2 hover:border-[var(--accent-blue)]"
                        data-testid="session-change-request-derived"
                      >
                        <span className="rounded border border-[var(--border-muted)] px-1.5 py-0.5 text-meta text-[var(--text-secondary)]">
                          {t(`git.provider.${entry.reference.provider}`)}
                        </span>
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-helper font-medium text-[var(--accent-blue)]">{recordedReferenceName(entry)}</div>
                          <div className="mt-0.5 text-meta text-[var(--text-muted)]">
                            {t(entry.kind === 'created' ? 'git.match.created' : 'git.match.mentioned')}
                            {' · '}
                            {t('git.links.automatic')}
                            {' · '}
                            {formatDate(locale, entry.recorded_at)}
                          </div>
                        </div>
                      </a>
                    ))}
                  </div>
                )}
              </section>

              <section>
                <div className="mb-2 flex items-center justify-between">
                  <h3 className="text-nav font-semibold text-[var(--text-primary)]">{t('git.repositories.title')}</h3>
                  <span className="text-meta text-[var(--text-muted)]">{t('git.repositories.count', { count: evidence.repositories.length })}</span>
                </div>
                {evidence.repositories.length === 0 ? (
                  <div className="rounded-lg border border-dashed border-[var(--border-default)] px-4 py-8 text-center">
                    <p className="text-helper text-[var(--text-secondary)]">{t('git.repositories.empty')}</p>
                    <p className="mt-1 text-meta text-[var(--text-muted)]">{t('git.repositories.emptyHelp')}</p>
                  </div>
                ) : (
                  <div className="space-y-4">
                    {evidence.repositories.map(repository => (
                      <RepositoryCard key={repository.repository_entry_key} repository={repository} onOpenPatch={openPatch} />
                    ))}
                  </div>
                )}
              </section>

              <p className="mt-4 text-right text-meta text-[var(--text-muted)]">
                {t('git.panel.generated', { time: formatDate(locale, evidence.generated_at) })}
              </p>
            </>
          )}
        </div>
      </aside>

      {lookupOpen && evidence && (
        <ChangeRequestLookupDialog
          session={{ id: session.id, agentType: session.agent_type, repositories: evidence.repositories }}
          onSelectSession={onSelectSession}
          onLinked={() => { void load() }}
          onClose={() => setLookupOpen(false)}
        />
      )}

      {patch && (
        <div className="fixed inset-0 z-[440] flex items-center justify-center bg-[rgba(0,0,0,var(--opacity-overlay))] p-6" onClick={event => { event.stopPropagation(); closePatch() }}>
          <section ref={patchRef} className="flex h-[84vh] w-full max-w-6xl flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl" onClick={event => event.stopPropagation()} role="dialog" aria-modal="true" aria-label={t('git.patch.title')}>
            <header className="flex h-11 flex-shrink-0 items-center gap-3 border-b border-[var(--border-default)] px-4">
              <span className="min-w-0 flex-1 truncate font-mono text-helper text-[var(--text-primary)]">{patch.file.display_path}</span>
              <button ref={patchCloseRef} type="button" onClick={closePatch} className="h-7 w-7 rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)]" aria-label={t('common.close')}>×</button>
            </header>
            <div className="min-h-0 flex-1 overflow-auto bg-[var(--bg-primary)] p-4">
              {patch.text === undefined && !patch.error && <div className="py-12 text-center text-helper text-[var(--text-muted)]">{t('common.loading')}</div>}
              {patch.error && <div className="text-helper text-[var(--error)]">{t(`error.${patch.error}`)}</div>}
              {patch.text !== undefined && <pre className="min-w-max whitespace-pre font-mono text-[12px] leading-5 text-[var(--text-primary)]">{patch.text}</pre>}
            </div>
          </section>
        </div>
      )}
    </div>
  )
}

function RepositoryCard({
  repository,
  onOpenPatch,
}: {
  repository: SessionGitRepositoryEvidence
  onOpenPatch: (repository: SessionGitRepositoryEvidence, file: GitFileChange) => void
}) {
  const { t } = useI18n()
  return (
    <article className="overflow-hidden rounded-lg border border-[var(--border-default)] bg-[var(--bg-primary)]" data-testid="git-repository-card">
      <header className="border-b border-[var(--border-muted)] px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <h4 className="min-w-0 flex-1 truncate font-mono text-helper font-medium text-[var(--text-primary)]">{repository.repository.worktree_root}</h4>
          <AssessmentBadge state={repository.assessment.state} />
          <span className="rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5 py-0.5 text-meta text-[var(--text-secondary)]">
            {t(`git.authority.${repository.authority}`)}
          </span>
        </div>
        <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-meta text-[var(--text-muted)]">
          <span>{repository.repository.branch || t('git.repository.detached')}</span>
          {repository.repository.head_sha && <span className="font-mono">{repository.repository.head_sha.slice(0, 12)}</span>}
          {repository.stale && <span className="text-[var(--warning)]">{t('git.repository.stale')}</span>}
        </div>
      </header>

      <div className="grid gap-0 lg:grid-cols-[minmax(0,1.5fr)_minmax(260px,1fr)]">
        <div className="min-w-0 border-b border-[var(--border-muted)] lg:border-b-0 lg:border-r">
          <div className="flex items-center justify-between px-4 py-2">
            <h5 className="text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">{t('git.files.title')}</h5>
            <span className="text-meta text-[var(--text-muted)]">{repository.files.length}</span>
          </div>
          {repository.files.length === 0 ? (
            <p className="px-4 pb-4 text-helper text-[var(--text-muted)]">{t('git.files.empty')}</p>
          ) : (
            <div className="max-h-72 overflow-y-auto border-t border-[var(--border-muted)]">
              {repository.files.map(file => (
                <button
                  type="button"
                  key={file.key}
                  onClick={() => onOpenPatch(repository, file)}
                  disabled={file.patch_assessment.state !== 'exact'}
                  className="flex w-full items-center gap-2 border-b border-[var(--border-muted)] px-4 py-2 text-left last:border-b-0 hover:bg-[var(--bg-surface-hover)] disabled:cursor-default disabled:opacity-60"
                  title={file.patch_assessment.state === 'exact' ? t('git.files.openPatch') : t('git.files.patchUnavailable')}
                >
                  <span className="w-4 flex-shrink-0 font-mono text-meta font-semibold text-[var(--accent-blue)]">{file.status.slice(0, 1).toUpperCase()}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-meta text-[var(--text-primary)]">
                    {file.old_display_path ? `${file.old_display_path} → ${file.display_path}` : file.display_path}
                  </span>
                  {(file.additions !== undefined || file.deletions !== undefined) && (
                    <span className="flex-shrink-0 text-meta">
                      <span className="text-[var(--accent-green)]">+{file.additions ?? 0}</span>{' '}
                      <span className="text-[var(--error)]">-{file.deletions ?? 0}</span>
                    </span>
                  )}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="min-w-0">
          <div className="flex items-center justify-between px-4 py-2">
            <h5 className="text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">{t('git.commits.title')}</h5>
            <span className="text-meta text-[var(--text-muted)]">{repository.candidate_commits.length}</span>
          </div>
          {repository.candidate_commits.length === 0 ? (
            <p className="px-4 pb-4 text-helper text-[var(--text-muted)]">{t('git.commits.empty')}</p>
          ) : (
            <div className="max-h-72 overflow-y-auto border-t border-[var(--border-muted)]">
              {repository.candidate_commits.map(commit => (
                <div key={commit.sha} className="border-b border-[var(--border-muted)] px-4 py-2 last:border-b-0">
                  <div className="truncate text-helper text-[var(--text-primary)]">{commit.subject}</div>
                  <div className="mt-0.5 flex gap-2 text-meta text-[var(--text-muted)]">
                    <span className="font-mono">{commit.sha.slice(0, 10)}</span>
                    <span>{t(`git.commitRelation.${commit.relation}`)}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </article>
  )
}
