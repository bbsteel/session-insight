import { useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  APIError,
  approveChangeHost,
  bindSessionChangeRequest,
  previewChangeHost,
  resolveChangeRequest,
} from '../api'
import {
  canSelectHostedAuthority,
  changeRequestSessionGroups,
  changeRequestDisplayName,
  type ChangeHostPreview,
  type ChangeRequestCreationSessionMatch,
  type ChangeRequestLookup,
  type ChangeRequestRelationship,
  type ChangeRequestResolveResponse,
  type ChangeRequestSessionMatch,
  type SessionGitRepositoryEvidence,
} from '../gitEvidence'
import { useI18n } from '../i18n'
import { useModalFocus } from '../modalFocus'

interface Props {
  onClose: () => void
  onSelectSession?: (id: string, agentType?: string, focusSidebar?: boolean) => void
  session?: { id: string; agentType: string; repositories: SessionGitRepositoryEvidence[] }
  onLinked?: () => void
}

function providerKey(provider: string): string {
  return `git.provider.${provider}`
}

function SessionMatchGroup({
  kind,
  matches,
  onSelect,
}: {
  kind: 'linked' | 'candidate'
  matches: ChangeRequestSessionMatch[]
  onSelect: (match: ChangeRequestSessionMatch) => void
}) {
  const { t } = useI18n()
  if (matches.length === 0) return null
  return (
    <div className="mt-4 border-t border-[var(--border-muted)] pt-3" data-testid={`change-request-${kind}-sessions`}>
      <h4 className="text-meta font-medium uppercase tracking-wide text-[var(--text-muted)]">
        {t(kind === 'linked' ? 'git.lookup.linkedSessions' : 'git.lookup.candidateSessions', { count: matches.length })}
      </h4>
      {kind === 'candidate' && (
        <p className="mt-1 text-meta text-[var(--warning)]">{t('git.lookup.candidateWarning')}</p>
      )}
      <div className="mt-2 space-y-1.5">
        {matches.map(match => (
          <button
            type="button"
            key={`${match.root_agent_type}-${match.root_session_id}-${match.match}`}
            onClick={() => onSelect(match)}
            className={`flex w-full items-center gap-2 rounded-md border bg-[var(--bg-surface)] px-2.5 py-2 text-left hover:border-[var(--accent-blue)] ${
              kind === 'candidate' ? 'border-[var(--warning)]/30' : 'border-[var(--border-default)]'
            }`}
          >
            <span className="min-w-0 flex-1 truncate text-helper text-[var(--text-primary)]">
              <span className="font-medium">{match.root_agent_type}</span>
              <span className="ml-1 font-mono text-meta">{match.root_session_id.slice(0, 16)}</span>
            </span>
            {match.relationship && (
              <span className="text-meta text-[var(--text-muted)]">{t(`git.relationship.${match.relationship}`)}</span>
            )}
            <span className="text-meta text-[var(--text-muted)]">{t(`git.match.${match.match}`)}</span>
            <span className={`rounded border px-1.5 py-0.5 text-meta ${
              match.assessment.state === 'exact'
                ? 'border-[var(--accent-green)]/30 text-[var(--accent-green)]'
                : 'border-[var(--warning)]/30 text-[var(--warning)]'
            }`}>
              {t(`git.state.${match.assessment.state}`)}
            </span>
            {match.assessment.reason_code && (
              <span className="max-w-48 truncate text-meta text-[var(--text-muted)]" title={t(`git.reason.${match.assessment.reason_code}`)}>
                {t(`git.reason.${match.assessment.reason_code}`)}
              </span>
            )}
          </button>
        ))}
      </div>
    </div>
  )
}

function CreationSessionGroup({
  matches,
  onSelect,
}: {
  matches: ChangeRequestCreationSessionMatch[]
  onSelect: (match: ChangeRequestCreationSessionMatch) => void
}) {
  const { t } = useI18n()
  if (matches.length === 0) return null
  return (
    <div className="rounded-lg border border-[var(--accent-green)]/30 bg-[var(--accent-green)]/5 p-4" data-testid="change-request-creation-sessions">
      <h3 className="text-body font-medium text-[var(--text-primary)]">
        {t('git.lookup.creationSessions', { count: matches.length })}
      </h3>
      <p className="mt-1 text-helper text-[var(--text-secondary)]">{t('git.lookup.creationHelp')}</p>
      <div className="mt-3 space-y-1.5">
        {matches.map(match => (
          <button
            type="button"
            key={match.evidence.evidence_id}
            onClick={() => onSelect(match)}
            className="flex w-full items-center gap-2 rounded-md border border-[var(--accent-green)]/25 bg-[var(--bg-surface)] px-2.5 py-2 text-left hover:border-[var(--accent-blue)]"
          >
            <span className="min-w-0 flex-1 truncate text-helper text-[var(--text-primary)]">
              <span className="font-medium">{match.root_agent_type}</span>
              <span className="ml-1 font-mono text-meta">{match.root_session_id.slice(0, 16)}</span>
            </span>
            <span className="text-meta text-[var(--text-muted)]">{t('git.match.created')}</span>
            <span className="rounded border border-[var(--accent-green)]/30 px-1.5 py-0.5 text-meta text-[var(--accent-green)]">
              {t('git.state.exact')}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

export default function ChangeRequestLookupDialog({ onClose, onSelectSession, session, onLinked }: Props) {
  const { t } = useI18n()
  const [reference, setReference] = useState('')
  // Keep sensitive host approval bound to the exact reference that produced
  // the approval prompt, rather than to editable input text.
  const [resolvedReference, setResolvedReference] = useState<string | null>(null)
  const [result, setResult] = useState<ChangeRequestResolveResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [enabling, setEnabling] = useState(false)
  const [hostPreview, setHostPreview] = useState<ChangeHostPreview | null>(null)
  const [hostScopeConfirmed, setHostScopeConfirmed] = useState(false)
  const [allowHTTP, setAllowHTTP] = useState(false)
  const [allowPrivateNetwork, setAllowPrivateNetwork] = useState(false)
  const [relationship, setRelationship] = useState<ChangeRequestRelationship>('related')
  const [repositoryKey, setRepositoryKey] = useState(session?.repositories[0]?.repository_entry_key ?? '')
  const [completeConfirmed, setCompleteConfirmed] = useState(false)
  const [bindingKey, setBindingKey] = useState<string | null>(null)
  const dialogRef = useRef<HTMLElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const resolveGenerationRef = useRef(0)
  useModalFocus(dialogRef, onClose, inputRef)

  const selectedRepository = useMemo(
    () => session?.repositories.find(repository => repository.repository_entry_key === repositoryKey),
    [repositoryKey, session?.repositories],
  )

  const runResolve = async () => {
    const value = reference.trim()
    if (!value) return
    const generation = ++resolveGenerationRef.current
    setLoading(true)
    setError(null)
    setResult(null)
    setResolvedReference(null)
    setHostPreview(null)
    setHostScopeConfirmed(false)
    try {
      const next = await resolveChangeRequest(value)
      if (resolveGenerationRef.current === generation) {
        setResult(next)
        setResolvedReference(value)
      }
    } catch (cause) {
      if (resolveGenerationRef.current === generation) {
        setError(cause instanceof APIError ? cause.code : 'change_request_resolve_failed')
      }
    } finally {
      if (resolveGenerationRef.current === generation) setLoading(false)
    }
  }

  const inspectHost = async () => {
    if (!resolvedReference) return
    const generation = ++resolveGenerationRef.current
    setEnabling(true)
    setError(null)
    try {
      const hosted = await resolveChangeRequest(resolvedReference, true)
      if (resolveGenerationRef.current !== generation) return
      setResult(hosted)
      if (hosted.assessment.reason_code === 'change_host_not_approved') {
        const preview = await previewChangeHost(resolvedReference)
        if (resolveGenerationRef.current === generation) setHostPreview(preview)
      }
    } catch (cause) {
      if (resolveGenerationRef.current === generation) {
        setError(cause instanceof APIError ? cause.code : 'change_host_preview_failed')
      }
    } finally {
      if (resolveGenerationRef.current === generation) setEnabling(false)
    }
  }

  const enableHost = async () => {
    if (!hostPreview || !resolvedReference) return
    const generation = ++resolveGenerationRef.current
    setEnabling(true)
    setError(null)
    try {
      await approveChangeHost(hostPreview.host.key, { allowHTTP, allowPrivateNetwork })
      const next = await resolveChangeRequest(resolvedReference, true)
      if (resolveGenerationRef.current === generation) {
        setResult(next)
        setHostPreview(null)
      }
    } catch (cause) {
      if (resolveGenerationRef.current === generation) {
        setError(cause instanceof APIError ? cause.code : 'change_host_approve_failed')
      }
    } finally {
      if (resolveGenerationRef.current === generation) setEnabling(false)
    }
  }

  const bind = async (lookup: ChangeRequestLookup) => {
    if (!session) return
    const version = lookup.change.snapshot?.content.content_version_key ?? ''
    setBindingKey(lookup.change.change_key)
    setError(null)
    try {
      await bindSessionChangeRequest(session.id, session.agentType, {
        change_key: lookup.change.change_key,
        repository_entry_key: relationship === 'related' ? undefined : repositoryKey,
        content_version_key: relationship === 'related' ? undefined : version,
        relationship,
        confirmation: relationship === 'exclusive'
          ? { complete_delivery: true, content_version_key: version }
          : undefined,
      })
      onLinked?.()
      onClose()
    } catch (cause) {
      setError(cause instanceof APIError ? cause.code : 'change_request_bind_failed')
    } finally {
      setBindingKey(null)
    }
  }

  const hostNeedsApproval = result?.assessment.reason_code === 'change_host_not_approved'
  const canLoadHostedDetails = result && !hostNeedsApproval &&
    result.matches.length === 0 &&
    (result.assessment.state === 'exact' || result.assessment.reason_code === 'change_request_not_found') &&
    (result.reference.provider === 'github' || result.reference.provider === 'gitlab')

  return createPortal(
    <div
      className="fixed inset-0 z-[420] flex items-center justify-center bg-[rgba(0,0,0,var(--opacity-overlay))] p-6 backdrop-blur-[2px]"
      onClick={event => { event.stopPropagation(); onClose() }}
      data-testid="change-request-lookup-dialog"
    >
      <section
        ref={dialogRef}
        className="flex max-h-[86vh] w-full max-w-4xl flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl"
        onClick={event => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="change-request-lookup-title"
      >
        <header className="flex h-12 flex-shrink-0 items-center gap-3 border-b border-[var(--border-default)] px-4">
          <div className="min-w-0 flex-1">
            <h2 id="change-request-lookup-title" className="text-body font-semibold text-[var(--text-primary)]">
              {t('git.lookup.title')}
            </h2>
            <p className="truncate text-meta text-[var(--text-muted)]">{t('git.lookup.help')}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="h-7 w-7 rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
            aria-label={t('common.close')}
          >
            ×
          </button>
        </header>

        <div className="flex flex-shrink-0 gap-2 border-b border-[var(--border-muted)] p-4">
          <input
            ref={inputRef}
            type="text"
            value={reference}
            onChange={event => setReference(event.target.value)}
            onKeyDown={event => { if (event.key === 'Enter') void runResolve() }}
            placeholder={t('git.lookup.placeholder')}
            aria-label={t('git.lookup.reference')}
            className="h-9 min-w-0 flex-1 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-3 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-2 focus:ring-[var(--accent-blue)]/20"
          />
          <button
            type="button"
            onClick={() => void runResolve()}
            disabled={loading || !reference.trim()}
            className="h-9 rounded-md bg-[var(--accent-blue)] px-4 text-nav font-medium text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            {loading ? t('git.lookup.searching') : t('git.lookup.search')}
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {error && (
            <div className="mb-3 rounded-md border border-[var(--error)]/30 bg-[var(--error)]/10 px-3 py-2 text-helper text-[var(--error)]" role="alert">
              {t(`error.${error}`)}
            </div>
          )}

          {hostNeedsApproval && result && (
            <div className="rounded-lg border border-[var(--warning)]/30 bg-[var(--warning)]/10 p-4" data-testid="change-host-approval">
              <h3 className="text-body font-medium text-[var(--text-primary)]">
                {t('git.host.enableTitle', { provider: t(providerKey(result.reference.provider)) })}
              </h3>
              <p className="mt-1 text-helper text-[var(--text-secondary)]">{t('git.host.enableHelp')}</p>
              {!hostPreview ? (
                <>
                  <p className="mt-2 text-meta text-[var(--text-muted)]">{result.reference.display_origin}</p>
                  <button
                    type="button"
                    onClick={() => void inspectHost()}
                    disabled={enabling}
                    className="mt-3 rounded-md border border-[var(--accent-blue)] px-3 py-1.5 text-nav font-medium text-[var(--accent-blue)] disabled:opacity-50"
                  >
                    {enabling ? t('git.host.inspecting') : t('git.host.inspect')}
                  </button>
                </>
              ) : (
                <div className="mt-3 rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] p-3">
                  <div className="text-helper font-medium text-[var(--text-primary)]">{hostPreview.host.display_origin}</div>
                  <div className="mt-2 text-meta font-medium text-[var(--text-muted)]">{t('git.host.endpoints')}</div>
                  <ul className="mt-1 space-y-0.5 font-mono text-meta text-[var(--text-secondary)]">
                    {hostPreview.host.endpoint_origins.map(origin => <li key={origin}>{origin}</li>)}
                  </ul>
                  {hostPreview.requires_http_approval && (
                    <label className="mt-2 flex items-start gap-2 text-helper text-[var(--warning)]">
                      <input type="checkbox" checked={allowHTTP} onChange={event => setAllowHTTP(event.target.checked)} />
                      <span>{t('git.host.allowHTTP')}</span>
                    </label>
                  )}
                  {hostPreview.requires_private_network_approval && (
                    <label className="mt-2 flex items-start gap-2 text-helper text-[var(--warning)]">
                      <input type="checkbox" checked={allowPrivateNetwork} onChange={event => setAllowPrivateNetwork(event.target.checked)} />
                      <span>{t('git.host.allowPrivate')}</span>
                    </label>
                  )}
                  <label className="mt-3 flex items-start gap-2 text-helper text-[var(--text-secondary)]">
                    <input type="checkbox" checked={hostScopeConfirmed} onChange={event => setHostScopeConfirmed(event.target.checked)} />
                    <span>{t('git.host.confirmScope')}</span>
                  </label>
                  <button
                    type="button"
                    onClick={() => void enableHost()}
                    disabled={enabling || !hostScopeConfirmed ||
                      (hostPreview.requires_http_approval && !allowHTTP) ||
                      (hostPreview.requires_private_network_approval && !allowPrivateNetwork)}
                    className="mt-3 rounded-md bg-[var(--accent-blue)] px-3 py-1.5 text-nav font-medium text-white disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {enabling ? t('git.host.enabling') : t('git.host.enable')}
                  </button>
                </div>
              )}
            </div>
          )}

          {result && !hostNeedsApproval && result.matches.length === 0 && result.creation_sessions.length === 0 && (
            <div className="py-10 text-center text-helper text-[var(--text-muted)]">{t('git.lookup.empty')}</div>
          )}

          <div className="space-y-3">
            {result && (
              <CreationSessionGroup
                matches={result.creation_sessions}
                onSelect={match => {
                  onSelectSession?.(match.root_session_id, match.root_agent_type, true)
                  onClose()
                }}
              />
            )}
            {result?.matches.map(lookup => {
              const sessionGroups = changeRequestSessionGroups(lookup)
              const snapshot = lookup.change.snapshot
              const exclusiveAvailable = canSelectHostedAuthority(lookup.change, repositoryKey)
              const bindDisabled = bindingKey !== null ||
                (relationship !== 'related' && !snapshot) ||
                (relationship !== 'related' && !selectedRepository) ||
                (relationship === 'exclusive' && (!exclusiveAvailable || !completeConfirmed))
              return (
                <article key={lookup.change.change_key} className="rounded-lg border border-[var(--border-default)] bg-[var(--bg-primary)] p-4" data-testid="change-request-result">
                  <div className="flex items-start gap-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5 py-0.5 text-meta text-[var(--text-secondary)]">
                          {t(providerKey(lookup.change.identity.provider))}
                        </span>
                        {snapshot && (
                          <span className="text-meta text-[var(--text-muted)]">{t(`git.lifecycle.${snapshot.lifecycle_state}`)}</span>
                        )}
                      </div>
                      <h3 className="mt-1 truncate text-body font-medium text-[var(--text-primary)]">
                        {changeRequestDisplayName(lookup.change)}
                      </h3>
                      {snapshot?.web_url ? (
                        <a className="mt-1 block truncate text-helper text-[var(--accent-blue)] hover:underline" href={snapshot.web_url} target="_blank" rel="noreferrer">
                          {snapshot.web_url}
                        </a>
                      ) : lookup.change.aliases[0] ? (
                        <span className="mt-1 block truncate text-helper text-[var(--text-muted)]">{lookup.change.aliases[0]}</span>
                      ) : null}
                      {snapshot && (
                        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-meta text-[var(--text-muted)]">
                          <span>{t('git.lookup.files', { count: snapshot.files.length })}</span>
                          <span>{t('git.lookup.commits', { count: snapshot.commits.length })}</span>
                          <span>{snapshot.source_ref || '—'} → {snapshot.target_ref || '—'}</span>
                          <span>{snapshot.content.head_sha?.slice(0, 12)}</span>
                        </div>
                      )}
                    </div>
                  </div>

                  <SessionMatchGroup
                    kind="linked"
                    matches={sessionGroups.linked}
                    onSelect={match => {
                      onSelectSession?.(match.root_session_id, match.root_agent_type, true)
                      onClose()
                    }}
                  />
                  <SessionMatchGroup
                    kind="candidate"
                    matches={sessionGroups.candidates}
                    onSelect={match => {
                      onSelectSession?.(match.root_session_id, match.root_agent_type, true)
                      onClose()
                    }}
                  />

                  {session && (
                    <div className="mt-4 border-t border-[var(--border-muted)] pt-3">
                      <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
                        <label className="text-meta text-[var(--text-muted)]">
                          <span className="mb-1 block">{t('git.bind.relationship')}</span>
                          <select
                            value={relationship}
                            onChange={event => {
                              setRelationship(event.target.value as ChangeRequestRelationship)
                              setCompleteConfirmed(false)
                            }}
                            className="h-8 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2 text-helper text-[var(--text-primary)]"
                          >
                            <option value="related">{t('git.relationship.related')}</option>
                            <option value="contributing" disabled={!snapshot}>{t('git.relationship.contributing')}</option>
                            <option value="exclusive" disabled={!snapshot}>{t('git.relationship.exclusive')}</option>
                          </select>
                        </label>
                        <label className="text-meta text-[var(--text-muted)]">
                          <span className="mb-1 block">{t('git.bind.repository')}</span>
                          <select
                            value={repositoryKey}
                            onChange={event => { setRepositoryKey(event.target.value); setCompleteConfirmed(false) }}
                            disabled={relationship === 'related' || !session.repositories.length}
                            className="h-8 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2 text-helper text-[var(--text-primary)] disabled:opacity-50"
                          >
                            {!session.repositories.length && <option value="">{t('git.bind.noRepository')}</option>}
                            {session.repositories.map(repository => (
                              <option key={repository.repository_entry_key} value={repository.repository_entry_key}>
                                {repository.repository.worktree_root}
                              </option>
                            ))}
                          </select>
                        </label>
                        <button
                          type="button"
                          onClick={() => void bind(lookup)}
                          disabled={bindDisabled}
                          className="self-end rounded-md bg-[var(--accent-blue)] px-3 py-1.5 text-nav font-medium text-white disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          {bindingKey === lookup.change.change_key ? t('git.bind.linking') : t('git.bind.action')}
                        </button>
                      </div>
                      {relationship === 'exclusive' && (
                        <label className="mt-3 flex items-start gap-2 rounded-md border border-[var(--warning)]/30 bg-[var(--warning)]/10 p-3 text-helper text-[var(--text-secondary)]">
                          <input
                            type="checkbox"
                            className="mt-0.5"
                            checked={completeConfirmed}
                            disabled={!exclusiveAvailable}
                            onChange={event => setCompleteConfirmed(event.target.checked)}
                          />
                          <span>
                            {exclusiveAvailable ? t('git.bind.confirmComplete') : t('git.bind.exclusiveUnavailable')}
                          </span>
                        </label>
                      )}
                    </div>
                  )}
                </article>
              )
            })}
            {canLoadHostedDetails && (
              <div className="rounded-lg border border-[var(--border-default)] bg-[var(--bg-primary)] p-4" data-testid="change-request-hosted-details">
                <h3 className="text-body font-medium text-[var(--text-primary)]">{t('git.lookup.hostedDetailsTitle')}</h3>
                <p className="mt-1 text-helper text-[var(--text-secondary)]">{t('git.lookup.hostedDetailsHelp')}</p>
                <button
                  type="button"
                  onClick={() => void inspectHost()}
                  disabled={enabling}
                  className="mt-3 rounded-md border border-[var(--accent-blue)] px-3 py-1.5 text-nav font-medium text-[var(--accent-blue)] disabled:opacity-50"
                >
                  {enabling ? t('git.host.inspecting') : t('git.lookup.loadHostedDetails')}
                </button>
              </div>
            )}
          </div>
        </div>
      </section>
    </div>,
    document.body,
  )
}
