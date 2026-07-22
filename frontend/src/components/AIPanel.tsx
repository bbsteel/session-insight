import { useEffect, useRef, useState } from 'react'
import {
  deleteLLMProvider, fetchLatestGeneration, fetchLLMProviders, generateAI,
  ModelUnavailableError, NoProviderError,
  parseHandoffMetadata, removeSessionTitle, setSessionTitle, splitHandoffOutput,
  type AIGeneration, type AIKind, type LLMProvider,
} from '../api'
import { useI18n } from '../i18n'
import MarkdownRenderer from './MarkdownRenderer'

interface Props {
  sessionId: string
  agentType: string
  sessionName: string
  onClose: () => void
  // Fires after a title override is applied (title) or removed (null) so the
  // host view can update its own header without a refetch.
  onTitleApplied: (title: string | null) => void
}

const TABS: { kind: AIKind; labelKey: string; scenarioKey: string }[] = [
  { kind: 'summary', labelKey: 'ai.tab.summary', scenarioKey: 'ai.scenario.summary' },
  { kind: 'title', labelKey: 'ai.tab.title', scenarioKey: 'ai.scenario.title' },
  { kind: 'handoff', labelKey: 'ai.tab.handoff', scenarioKey: 'ai.scenario.handoff' },
]

// One line of the generation progress log: ms is null while the step is
// still running.
interface StageLine {
  text: string
  ms: number | null
}

interface TabState {
  generation: AIGeneration | null
  loaded: boolean // latest-generation cache fetch finished
  busy: boolean
  stages: StageLine[]
  error: string | null
  noProvider: boolean
  unavailableProviderId: number | null
}

const emptyTab: TabState = {
  generation: null, loaded: false, busy: false, stages: [], error: null,
  noProvider: false, unavailableProviderId: null,
}

// AnimatedDots cycles 1→2→3 dots for the in-flight stage line.
function AnimatedDots() {
  const [n, setN] = useState(1)
  useEffect(() => {
    const timer = window.setInterval(() => setN(v => (v % 3) + 1), 400)
    return () => window.clearInterval(timer)
  }, [])
  return <span>{'.'.repeat(n)}</span>
}

// AIPanel drives the three phase-1 generations for one session. Summary and
// handoff open on the last saved result (regenerate on demand); title
// produces a draft the user must explicitly apply — nothing renames a
// session without confirmation.
export default function AIPanel({ sessionId, agentType, sessionName, onClose, onTitleApplied }: Props) {
  const { locale, t } = useI18n()
  const [tab, setTab] = useState<AIKind>('summary')
  const [states, setStates] = useState<Record<AIKind, TabState>>({
    summary: emptyTab, title: emptyTab, handoff: emptyTab,
  })
  const [copied, setCopied] = useState(false)
  const [titleApplied, setTitleApplied] = useState(false)
  const [providers, setProviders] = useState<LLMProvider[]>([])
  // 0 = use the server-side default provider.
  const [providerId, setProviderId] = useState(0)
  const abortRef = useRef<AbortController | null>(null)
  const progressLogRef = useRef<HTMLDivElement | null>(null)

  const patch = (kind: AIKind, p: Partial<TabState>) =>
    setStates(prev => ({ ...prev, [kind]: { ...prev[kind], ...p } }))
  const st = states[tab]
  const defaultProvider = providers.find(p => p.is_default)
  const handoff = tab === 'handoff' && st.generation
    ? splitHandoffOutput(st.generation.content)
    : null
  const displayedContent = handoff ? handoff.content : st.generation?.content ?? ''

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  useEffect(() => () => abortRef.current?.abort(), [])

  // Keep the bounded progress log pinned to the newest stage as backend SSE
  // events arrive. requestAnimationFrame waits for React to paint the new row
  // before measuring its scroll height.
  useEffect(() => {
    const log = progressLogRef.current
    if (!log || st.stages.length === 0) return
    const frame = requestAnimationFrame(() => { log.scrollTop = log.scrollHeight })
    return () => cancelAnimationFrame(frame)
  }, [st.stages, st.generation?.created_at])

  // Provider list for the generation model picker; kept fresh after the
  // settings modal saves changes.
  useEffect(() => {
    const load = () => {
      fetchLLMProviders()
        .then(data => setProviders(data.providers))
        .catch(() => {})
    }
    load()
    window.addEventListener('si-ai-providers-changed', load)
    return () => window.removeEventListener('si-ai-providers-changed', load)
  }, [])

  // Lazily load the cached latest generation the first time a tab is shown.
  useEffect(() => {
    const st = states[tab]
    if (st.loaded || st.busy) return
    let cancelled = false
    fetchLatestGeneration(tab, sessionId, agentType)
      .then(gen => { if (!cancelled) patch(tab, { generation: gen, loaded: true }) })
      .catch(() => { if (!cancelled) patch(tab, { loaded: true }) })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, sessionId, agentType])

  const generate = async (kind: AIKind) => {
    const ac = new AbortController()
    abortRef.current = ac
    let lines: StageLine[] = []
    let lastAt = performance.now()
    const finalize = () => {
      if (lines.length > 0 && lines[lines.length - 1].ms == null) {
        lines = [...lines]
        lines[lines.length - 1] = { ...lines[lines.length - 1], ms: performance.now() - lastAt }
      }
      return lines
    }
    const onStage = (stage: string) => {
      finalize()
      lines = [...lines, { text: stage, ms: null }]
      lastAt = performance.now()
      patch(kind, { stages: lines })
    }
    patch(kind, { busy: true, stages: [], error: null, noProvider: false, unavailableProviderId: null })
    setTitleApplied(false)
    try {
      const gen = await generateAI(sessionId, kind, onStage, ac.signal, providerId, locale)
      patch(kind, { generation: gen, busy: false, loaded: true, stages: finalize() })
    } catch (err) {
      if (ac.signal.aborted) return
      if (err instanceof NoProviderError) patch(kind, { busy: false, noProvider: true })
      else if (err instanceof ModelUnavailableError) {
        patch(kind, {
          busy: false, stages: finalize(), error: err.message,
          unavailableProviderId: err.providerId,
        })
      }
      else patch(kind, { busy: false, stages: finalize(), error: err instanceof Error ? err.message : String(err) })
    }
  }

  const copy = (text: string) => {
    void navigator.clipboard.writeText(text)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  // Broadcast a title override so the sidebar renames the session instantly
  // (optimistic local patch) instead of waiting for the SSE-triggered refetch.
  const broadcastTitle = (title: string | null) => {
    window.dispatchEvent(new CustomEvent('si-title-override', {
      detail: { agentType, sessionId, title },
    }))
  }

  const applyTitle = async (title: string) => {
    try {
      await setSessionTitle(sessionId, agentType, title)
      setTitleApplied(true)
      onTitleApplied(title)
      broadcastTitle(title)
    } catch (err) {
      patch('title', { error: err instanceof Error ? err.message : String(err) })
    }
  }

  const restoreTitle = async () => {
    try {
      await removeSessionTitle(sessionId, agentType)
      setTitleApplied(false)
      onTitleApplied(null)
      broadcastTitle(null)
    } catch (err) {
      patch('title', { error: err instanceof Error ? err.message : String(err) })
    }
  }

  const removeUnavailableProvider = async () => {
    const id = st.unavailableProviderId
    if (id == null) return
    const provider = providers.find(p => p.id === id)
    const label = provider?.name ?? `#${id}`
    if (!window.confirm(t('ai.deleteUnavailableConfirm', { name: label }))) return
    try {
      await deleteLLMProvider(id)
      const next = providers.filter(p => p.id !== id)
      setProviders(next)
      if (providerId === id) setProviderId(0)
      patch(tab, {
        error: null, unavailableProviderId: null, noProvider: next.length === 0,
      })
      window.dispatchEvent(new Event('si-ai-providers-changed'))
    } catch (err) {
      patch(tab, { error: t('ai.deleteProviderFailed', { error: err instanceof Error ? err.message : String(err) }) })
    }
  }

  const btnCls = 'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'

  return (
    <div className="fixed inset-0 z-[300] flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-xl w-[min(900px,94vw)] h-[min(720px,88vh)] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="px-4 py-2.5 border-b border-[var(--border-default)]">
          <div className="relative flex items-center justify-center">
            <div className="max-w-[calc(100%-180px)] truncate text-center text-body font-bold text-[var(--text-primary)]" title={sessionName}>{sessionName}</div>
            <div className="absolute right-0 top-1/2 flex -translate-y-1/2 items-center gap-1">
              <button
                onClick={() => window.dispatchEvent(new Event('si-open-ai-settings'))}
                className="h-7 rounded-md px-2 text-helper text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
                title={t('ai.configureProviders')}
              >
                ⚙ {t('ai.providers')}
              </button>
              <button onClick={onClose} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] text-lg leading-none px-1">✕</button>
            </div>
          </div>
          <div className="mt-1.5 flex items-center gap-3">
            <span className="text-sm font-medium text-[var(--text-primary)]">✨ AI</span>
            <div className="flex items-center gap-1">
              {TABS.map(tabDef => (
                <button
                  key={tabDef.kind}
                  onClick={() => setTab(tabDef.kind)}
                  className={`h-7 rounded-md px-2.5 text-helper ${
                    tab === tabDef.kind
                      ? 'bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] text-[var(--accent-blue)]'
                      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
                  }`}
                >
                  {t(tabDef.labelKey)}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="flex-shrink-0 border-b border-[var(--border-muted)] px-4 py-2">
          <div className="flex h-[108px] items-start gap-3">
            <div className="flex shrink-0 flex-col gap-2 pt-1">
              {providers.length > 0 && (
                <select
                  value={providerId}
                  onChange={e => setProviderId(Number(e.target.value))}
                  disabled={st.busy}
                  className="h-7 max-w-[220px] rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-1.5 text-helper text-[var(--text-secondary)] focus:outline-none focus:border-[var(--accent-blue)]"
                  title={t('ai.providerPicker')}
                >
                  <option value={0}>
                    {defaultProvider ? t('ai.defaultProvider', { name: defaultProvider.name }) : t('ai.noDefaultProvider')}
                  </option>
                  {providers.map(p => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
              )}
              <div className="flex items-center gap-2">
                <button className={btnCls} disabled={st.busy} onClick={() => void generate(tab)}>
                  {st.busy ? t('ai.generating') : st.generation ? t('ai.regenerate') : t(tab === 'summary' ? 'ai.generateSummary' : tab === 'title' ? 'ai.generateTitle' : 'ai.generateHandoff')}
                </button>
                {!st.busy && st.generation && tab === 'summary' && (
                  <button className={btnCls} onClick={() => copy(displayedContent)}>
                    {copied ? t('ai.copied') : t('common.copy')}
                  </button>
                )}
              </div>
            </div>

            {(st.stages.length > 0 || st.generation) && (
              <div ref={progressLogRef} className="min-w-0 flex-1 self-stretch overflow-y-auto rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-2 font-mono text-helper text-[var(--text-secondary)]">
                {st.stages.map((line, i) => (
                  <div key={i} className="leading-5">
                    {line.ms != null ? (
                      <>
                        <span className="text-[var(--success)]">✓</span> {line.text}...
                        <span className="ml-1 text-meta text-[var(--text-muted)]">{(line.ms / 1000).toFixed(1)}s</span>
                      </>
                    ) : (
                      <span className="text-[var(--text-primary)]">
                        <span className="text-[var(--accent-blue)]">›</span> {line.text}<AnimatedDots />
                      </span>
                    )}
                  </div>
                ))}
                {!st.busy && st.generation && (
                  <div className="mt-1 border-t border-[var(--border-muted)] pt-1 text-meta text-[var(--text-muted)]">
                    <span className="font-semibold text-[var(--text-secondary)]">{st.generation.model_id}</span> {t('ai.generatedAt', { time: st.generation.created_at })}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        <div className="flex-1 overflow-auto p-4">
          {st.noProvider && (
            <div className="rounded-md border border-dashed border-[var(--border-default)] p-4 text-center">
              <div className="text-helper text-[var(--text-primary)]">{t('ai.noProvider')}</div>
              <div className="mt-1 text-meta text-[var(--text-muted)]">{t('ai.noProviderHelp')}</div>
              <button
                className={`${btnCls} mt-3 border-[var(--accent-blue)] text-[var(--accent-blue)]`}
                onClick={() => window.dispatchEvent(new Event('si-open-ai-settings'))}
              >
                {t('ai.configure')}
              </button>
            </div>
          )}

          {st.error && (
            <div className="mb-3 flex items-start gap-2 rounded-md border border-[var(--error)] bg-[color-mix(in_srgb,var(--error)_6%,transparent)] px-3 py-2">
              <div className="min-w-0 flex-1 whitespace-pre-wrap break-all text-helper text-[var(--error)]">{st.error}</div>
              {st.unavailableProviderId != null && (
                <button
                  className={`${btnCls} flex-shrink-0 border-[var(--error)] text-[var(--error)]`}
                  onClick={() => void removeUnavailableProvider()}
                >
                  {t('ai.deleteProvider')}
                </button>
              )}
            </div>
          )}

          {!st.noProvider && !st.generation && !st.busy && st.loaded && !st.error && (
            <div className="pt-8 text-center text-helper text-[var(--text-muted)]">
              {t(`ai.empty.${tab}`)}
            </div>
          )}

          {!st.busy && st.generation && tab === 'title' && (
            <div className="mx-auto max-w-[480px] pt-6 text-center">
              <div className="text-meta text-[var(--text-muted)]">{t('ai.titleDraft')}</div>
              <div className="mt-2 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-4 py-3 text-body font-medium text-[var(--text-primary)]">
                {st.generation.content}
              </div>
              <div className="mt-4 flex items-center justify-center gap-2">
                <button
                  className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}
                  disabled={titleApplied}
                  onClick={() => void applyTitle(st.generation!.content)}
                >
                  {titleApplied ? t('ai.titleApplied') : t('ai.applyTitle')}
                </button>
                <button className={btnCls} onClick={() => void restoreTitle()}>{t('ai.restoreTitle')}</button>
              </div>
              <div className="mt-2 text-meta text-[var(--text-muted)]">{t('ai.titleLocalOnly')}</div>
            </div>
          )}

          {!st.busy && st.generation && tab !== 'title' && (
            <>
              {tab === 'handoff' && (() => {
                // A cached pre-fix generation may carry a restarted envelope
                // in content while its saved metadata still describes the
                // discarded first draft. Prefer metadata recovered alongside
                // the selected content; new normalized generations fall back
                // to the server-stored metadata.
                const meta = handoff?.metadata ?? parseHandoffMetadata(st.generation!.metadata)
                if (!meta) return null
                return (
                  <div className="mb-3 rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] px-3 py-2">
                    <div className="text-helper font-semibold text-[var(--text-primary)]">{t('ai.assessment')}</div>
                    {meta.difficulty && (
                      <div className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-1.5 text-helper text-[var(--text-secondary)]">
                        <span>{t('ai.difficulty')}</span>
                        <div>
                          <span className={`font-medium ${
                          meta.difficulty === '困难' || meta.difficulty === 'hard' ? 'text-[var(--error)]'
                            : meta.difficulty === '中等' || meta.difficulty === 'medium' ? 'text-[var(--warning)]'
                            : 'text-[var(--success)]'
                        }`}>
                          {meta.difficulty}
                        </span>
                          {meta.difficulty_reason && <span className="ml-1.5 text-meta text-[var(--text-muted)]">{meta.difficulty_reason}</span>}
                        </div>
                      </div>
                    )}
                    {meta.recommended && meta.recommended.length > 0 && (
                      <div className="mt-1 text-helper text-[var(--text-secondary)]">
                        {t('ai.recommendedAgent')}
                        {meta.recommended.map((r, i) => (
                          <span key={i}>
                            {i > 0 && ', '}
                            <span className="font-medium text-[var(--text-primary)]" title={r.reason || undefined}>{r.executor}</span>
                          </span>
                        ))}
                      </div>
                    )}
                    <div className="mt-1.5 text-meta text-[var(--text-muted)]">{t('ai.assessmentCopyHelp')}</div>
                  </div>
                )
              })()}
              {tab === 'handoff' && (
                <div className="mb-2 flex justify-end">
                  <button className={btnCls} onClick={() => copy(displayedContent)}>
                    {copied ? t('ai.copied') : t('ai.copyHandoff')}
                  </button>
                </div>
              )}
              <div className="prose-custom min-w-0 text-helper text-[var(--text-primary)]">
                <MarkdownRenderer content={displayedContent} />
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
