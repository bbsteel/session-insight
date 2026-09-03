import { useRef, useState } from 'react'
import { useI18n } from '../i18n'
import {
  activateChangeHostProfile,
  approveChangeHost,
  confirmChangeHostProfileMapping,
  importChangeHostProfile,
  probeChangeHostProfile,
  verifyChangeHostProfile,
} from '../api'
import {
  WIZARD_STEPS,
  basicsReady,
  capabilityRows,
  confirmationKey,
  initialWizardState,
  mappingComplete,
  mappingSelections,
  networkReady,
  type WizardState,
  type WizardStep,
} from '../changePlatformTypes'

const inputCls =
  'w-full h-8 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2.5 text-body text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]/30'
const primaryBtn =
  'rounded-md bg-[var(--accent-blue)] px-3.5 py-1.5 text-helper text-white transition-colors duration-fast hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] disabled:opacity-50 disabled:pointer-events-none'
const secondaryBtn =
  'rounded-md border border-[var(--border-default)] px-3.5 py-1.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] disabled:opacity-50 disabled:pointer-events-none'

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/**
 * Five-step wizard for adapting an unknown change platform through its
 * OpenAPI document (design §13): basics → network & credentials → automatic
 * analysis → mapping confirmation → test & activate.
 */
export default function ChangePlatformWizard({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useI18n()
  const [state, setState] = useState<WizardState>(initialWizardState)
  const [busy, setBusy] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const fileReadSequenceRef = useRef(0)
  const activeFileReaderRef = useRef<FileReader | null>(null)

  const patch = (partial: Partial<WizardState>) => setState(s => ({ ...s, ...partial, error: partial.error ?? '' }))
  const patchBasics = (partial: Partial<WizardState['basics']>) => setState(s => ({ ...s, basics: { ...s.basics, ...partial } }))
  const patchNetwork = (partial: Partial<WizardState['network']>) => setState(s => ({ ...s, network: { ...s.network, ...partial } }))

  const run = async (action: () => Promise<void>) => {
    setBusy(true)
    try {
      await action()
    } catch (err) {
      setState(s => ({ ...s, error: errorText(err) }))
    } finally {
      setBusy(false)
    }
  }

  const readFile = (file: File | undefined) => {
    if (!file) return
    const fileReadSequence = fileReadSequenceRef.current + 1
    fileReadSequenceRef.current = fileReadSequence
    activeFileReaderRef.current?.abort()
    const reader = new FileReader()
    activeFileReaderRef.current = reader
    reader.onload = () => {
      if (fileReadSequenceRef.current !== fileReadSequence) return
      activeFileReaderRef.current = null
      patchBasics({ document: String(reader.result ?? '') })
    }
    reader.onerror = () => {
      if (fileReadSequenceRef.current !== fileReadSequence) return
      activeFileReaderRef.current = null
      patch({ error: t('changeHosts.wizard.fileReadError') })
    }
    reader.onabort = () => {
      if (fileReadSequenceRef.current === fileReadSequence) activeFileReaderRef.current = null
    }
    reader.readAsText(file)
  }

  const stepIndex = WIZARD_STEPS.indexOf(state.step)
  const gotoStep = (step: WizardStep) => patch({ step })

  // Step 2 → 3: import the document, then approve the host origins.
  const submitNetwork = () => run(async () => {
    const imported = await importChangeHostProfile({
      displayName: state.basics.displayName,
      document: state.basics.document,
      apiBaseUrl: state.basics.apiBaseUrl,
      sampleChangeUrl: state.basics.sampleChangeUrl,
      credentialEnvName: state.network.credentialEnvName,
    })
    if (imported.requires_host_approval) {
      await approveChangeHost(imported.host.key, {
        allowHTTP: state.network.allowHttp,
        allowPrivateNetwork: state.network.allowPrivateNetwork,
      })
    }
    const probe = await probeChangeHostProfile(imported.profile.profile_id)
    patch({ imported, probe, step: 'analysis' })
  })

  // Step 4 → 5: apply confirmations and re-verify until clean.
  const submitMapping = () => run(async () => {
    const profileId = state.imported!.profile.profile_id
    await confirmChangeHostProfileMapping(profileId, mappingSelections(state.probe!.required_confirmations, state.selections))
    const probe = await verifyChangeHostProfile(profileId)
    patch({ probe })
    if (probe.verified) {
      patch({ probe, step: 'finish' })
    }
  })

  const activate = () => run(async () => {
    await activateChangeHostProfile(state.imported!.profile.profile_id)
    onDone()
  })

  const canProceed =
    state.step === 'basics' ? basicsReady(state.basics) :
    state.step === 'network' ? networkReady(state.network) :
    state.step === 'analysis' ? state.probe?.verified || (state.probe?.required_confirmations.length ?? 0) > 0 :
    state.step === 'mapping' ? mappingComplete(state.probe?.required_confirmations ?? [], state.selections) :
    state.probe?.verified === true

  const nextAction =
    state.step === 'basics' ? () => gotoStep('network') :
    state.step === 'network' ? submitNetwork :
    state.step === 'analysis' ? () => gotoStep(state.probe?.verified ? 'finish' : 'mapping') :
    state.step === 'mapping' ? submitMapping :
    activate

  return (
    <div className="fixed inset-0 z-[var(--z-modal)] flex items-center justify-center bg-black/40 p-6" role="dialog" aria-modal="true" aria-label={t('changeHosts.wizard.title')} onClick={onClose}>
      <div className="flex max-h-full w-[640px] flex-col overflow-hidden rounded-xl border border-[var(--border-default)] bg-[var(--bg-surface)] shadow-2xl" onClick={e => e.stopPropagation()}>
        <div className="flex flex-shrink-0 items-center justify-between border-b border-[var(--border-muted)] px-5 py-3">
          <h2 className="text-body font-semibold text-[var(--text-primary)]">{t('changeHosts.wizard.title')}</h2>
          <button onClick={onClose} aria-label={t('common.close')} className="flex h-7 w-7 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]">✕</button>
        </div>

        {/* Step indicator */}
        <div className="flex flex-shrink-0 gap-1 border-b border-[var(--border-muted)] px-5 py-2">
          {WIZARD_STEPS.map((step, i) => (
            <div key={step} className={`flex items-center gap-1.5 rounded px-2 py-1 text-meta ${i === stepIndex ? 'bg-[color-mix(in_srgb,var(--accent-blue)_10%,transparent)] font-medium text-[var(--accent-blue)]' : i < stepIndex ? 'text-[var(--success)]' : 'text-[var(--text-muted)]'}`}>
              <span className="tabular-nums">{i + 1}</span>
              <span>{t(`changeHosts.wizard.step.${step}`)}</span>
            </div>
          ))}
        </div>

        <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4">
          {state.step === 'basics' && (
            <>
              <label className="block space-y-1">
                <span className="text-helper text-[var(--text-secondary)]">{t('changeHosts.wizard.displayName')}</span>
                <input className={inputCls} value={state.basics.displayName} onChange={e => patchBasics({ displayName: e.target.value })} placeholder={t('changeHosts.wizard.displayNamePlaceholder')} />
              </label>
              <div className="space-y-1">
                <span className="text-helper text-[var(--text-secondary)]">{t('changeHosts.wizard.document')}</span>
                <div className="flex items-center gap-2">
                  <button className={secondaryBtn} onClick={() => fileRef.current?.click()}>{t('changeHosts.wizard.documentPick')}</button>
                  <span className="truncate text-helper text-[var(--text-muted)]">
                    {state.basics.document ? t('changeHosts.wizard.documentLoaded', { bytes: state.basics.document.length }) : t('changeHosts.wizard.documentEmpty')}
                  </span>
                  <input ref={fileRef} type="file" accept=".json,.yaml,.yml,application/json,text/yaml" className="hidden" onChange={e => readFile(e.target.files?.[0])} />
                </div>
              </div>
              <label className="block space-y-1">
                <span className="text-helper text-[var(--text-secondary)]">{t('changeHosts.wizard.apiBaseUrl')}</span>
                <input className={inputCls} value={state.basics.apiBaseUrl} onChange={e => patchBasics({ apiBaseUrl: e.target.value })} placeholder={t('changeHosts.wizard.apiBaseUrlPlaceholder')} />
              </label>
              <label className="block space-y-1">
                <span className="text-helper text-[var(--text-secondary)]">{t('changeHosts.wizard.sampleChangeUrl')}</span>
                <input className={inputCls} value={state.basics.sampleChangeUrl} onChange={e => patchBasics({ sampleChangeUrl: e.target.value })} placeholder={t('changeHosts.wizard.sampleChangeUrlPlaceholder')} />
                <span className="block text-meta text-[var(--text-muted)]">{t('changeHosts.wizard.sampleChangeUrlHelp')}</span>
              </label>
            </>
          )}

          {state.step === 'network' && (
            <>
              <label className="block space-y-1">
                <span className="text-helper text-[var(--text-secondary)]">{t('changeHosts.wizard.credentialEnvName')}</span>
                <input className={inputCls} value={state.network.credentialEnvName} onChange={e => patchNetwork({ credentialEnvName: e.target.value })} placeholder={t('changeHosts.wizard.credentialEnvNamePlaceholder')} />
                <span className="block text-meta text-[var(--text-muted)]">{t('changeHosts.wizard.credentialEnvHelp')}</span>
              </label>
              <label className="flex items-start gap-2 text-helper text-[var(--text-secondary)]">
                <input type="checkbox" className="mt-0.5" checked={state.network.allowHttp} onChange={e => patchNetwork({ allowHttp: e.target.checked })} />
                <span>{t('changeHosts.wizard.allowHttp')}</span>
              </label>
              <label className="flex items-start gap-2 text-helper text-[var(--text-secondary)]">
                <input type="checkbox" className="mt-0.5" checked={state.network.allowPrivateNetwork} onChange={e => patchNetwork({ allowPrivateNetwork: e.target.checked })} />
                <span>{t('changeHosts.wizard.allowPrivateNetwork')}</span>
              </label>
            </>
          )}

          {state.step === 'analysis' && state.imported && (
            <>
              <div className="space-y-1">
                <div className="text-helper font-medium text-[var(--text-primary)]">{t('changeHosts.wizard.originsTitle')}</div>
                <ul className="list-inside list-disc space-y-0.5 text-helper text-[var(--text-secondary)]">
                  {state.imported.endpoint_origins.map(origin => <li key={origin} className="font-mono">{origin}</li>)}
                </ul>
              </div>
              <div className="text-helper text-[var(--text-secondary)]">
                {t('changeHosts.wizard.candidateCount', { count: state.imported.candidate_count })}
              </div>
              {state.probe && (state.probe.warnings.length > 0 || state.probe.required_confirmations.length > 0) && (
                <div className="rounded-md border border-[var(--border-muted)] px-3 py-2 text-helper text-[var(--warning)]">
                  {state.probe.warnings.map(warning => <div key={warning}>{t(`changeHosts.failure.${warning}`)}</div>)}
                  {state.probe.required_confirmations.length > 0 && (
                    <div>{t('changeHosts.pendingConfirmations', { count: state.probe.required_confirmations.length })}</div>
                  )}
                </div>
              )}
              {state.probe?.verified && (
                <div className="rounded-md bg-[color-mix(in_srgb,var(--success)_12%,transparent)] px-3 py-2 text-helper text-[var(--success)]">
                  {t('changeHosts.wizard.analysisVerified')}
                </div>
              )}
            </>
          )}

          {state.step === 'mapping' && state.probe && (
            <>
              <div className="text-helper text-[var(--text-muted)]">{t('changeHosts.wizard.mappingHelp')}</div>
              {state.probe.required_confirmations.map(confirmation => {
                const key = confirmationKey(confirmation.role, confirmation.field)
                return (
                  <fieldset key={key} className="space-y-1.5 rounded-lg border border-[var(--border-default)] p-3">
                    <legend className="px-1 text-helper font-medium text-[var(--text-primary)]">
                      {t(`changeHosts.field.${confirmation.field}`)}
                      <span className="ml-1.5 text-meta text-[var(--text-muted)]">{confirmation.role}</span>
                    </legend>
                    {confirmation.candidates.map(candidate => (
                      <label key={candidate.pointer} className="flex items-center gap-2 text-helper text-[var(--text-secondary)]">
                        <input
                          type="radio"
                          name={key}
                          checked={state.selections[key] === candidate.pointer}
                          readOnly
                          onClick={() => setState(s => ({ ...s, selections: { ...s.selections, [key]: candidate.pointer } }))}
                        />
                        <span className="font-mono">{candidate.pointer}</span>
                        <span className="text-meta text-[var(--text-muted)]">
                          {candidate.shape} · {Math.round(candidate.confidence * 100)}%
                        </span>
                      </label>
                    ))}
                  </fieldset>
                )
              })}
            </>
          )}

          {state.step === 'finish' && state.probe && (
            <>
              <table className="w-full text-helper">
                <tbody>
                  {capabilityRows(state.probe.capabilities).map(row => (
                    <tr key={row.id} className="border-b border-[var(--border-muted)] last:border-0">
                      <td className="py-1.5 text-[var(--text-secondary)]">{t(`changeHosts.capability.${row.id}`)}</td>
                      <td className={`py-1.5 text-right ${row.supported ? 'text-[var(--success)]' : 'text-[var(--text-muted)]'}`}>
                        {t(`changeHosts.supported.${row.supported ? 'supported' : 'unsupported'}`)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {state.probe.warnings.length > 0 && (
                <div className="rounded-md border border-[var(--border-muted)] px-3 py-2 text-helper text-[var(--warning)]">
                  {state.probe.warnings.map(warning => <div key={warning}>{t(`changeHosts.failure.${warning}`)}</div>)}
                </div>
              )}
              <div className="text-helper text-[var(--text-muted)]">{t('changeHosts.wizard.finishHelp')}</div>
            </>
          )}

          {state.error && (
            <div className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-surface)] px-3 py-2 text-helper text-[var(--warning)]" role="alert">
              {state.error}
            </div>
          )}
        </div>

        <div className="flex flex-shrink-0 items-center justify-between border-t border-[var(--border-muted)] px-5 py-3">
          <button className={secondaryBtn} disabled={busy || stepIndex === 0} onClick={() => gotoStep(WIZARD_STEPS[stepIndex - 1])}>
            {t('changeHosts.wizard.back')}
          </button>
          <button className={primaryBtn} disabled={busy || !canProceed} onClick={nextAction}>
            {state.step === 'finish' ? t('changeHosts.wizard.activate') :
             state.step === 'analysis' && state.probe?.verified ? t('changeHosts.wizard.continue') :
             state.step === 'analysis' ? t('changeHosts.wizard.toMapping') :
             t('changeHosts.wizard.next')}
          </button>
        </div>
      </div>
    </div>
  )
}
