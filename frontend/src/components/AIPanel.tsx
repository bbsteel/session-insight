import { useEffect, useRef, useState } from 'react'
import {
  fetchLatestGeneration, generateAI, NoProviderError, removeSessionTitle, setSessionTitle,
  type AIGeneration, type AIKind,
} from '../api'
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

const TABS: { kind: AIKind; label: string }[] = [
  { kind: 'summary', label: '总结' },
  { kind: 'title', label: '标题' },
  { kind: 'handoff', label: '交接' },
]

interface TabState {
  generation: AIGeneration | null
  loaded: boolean // latest-generation cache fetch finished
  busy: boolean
  stage: string
  error: string | null
  noProvider: boolean
}

const emptyTab: TabState = { generation: null, loaded: false, busy: false, stage: '', error: null, noProvider: false }

// AIPanel drives the three phase-1 generations for one session. Summary and
// handoff open on the last saved result (regenerate on demand); title
// produces a draft the user must explicitly apply — nothing renames a
// session without confirmation.
export default function AIPanel({ sessionId, agentType, sessionName, onClose, onTitleApplied }: Props) {
  const [tab, setTab] = useState<AIKind>('summary')
  const [states, setStates] = useState<Record<AIKind, TabState>>({
    summary: emptyTab, title: emptyTab, handoff: emptyTab,
  })
  const [copied, setCopied] = useState(false)
  const [titleApplied, setTitleApplied] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  const patch = (kind: AIKind, p: Partial<TabState>) =>
    setStates(prev => ({ ...prev, [kind]: { ...prev[kind], ...p } }))

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  useEffect(() => () => abortRef.current?.abort(), [])

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
    patch(kind, { busy: true, stage: '准备中…', error: null, noProvider: false })
    setTitleApplied(false)
    try {
      const gen = await generateAI(sessionId, kind, stage => patch(kind, { stage }), ac.signal)
      patch(kind, { generation: gen, busy: false, loaded: true })
    } catch (err) {
      if (ac.signal.aborted) return
      if (err instanceof NoProviderError) patch(kind, { busy: false, noProvider: true })
      else patch(kind, { busy: false, error: err instanceof Error ? err.message : String(err) })
    }
  }

  const copy = (text: string) => {
    void navigator.clipboard.writeText(text)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  const applyTitle = async (title: string) => {
    try {
      await setSessionTitle(sessionId, agentType, title)
      setTitleApplied(true)
      onTitleApplied(title)
    } catch (err) {
      patch('title', { error: err instanceof Error ? err.message : String(err) })
    }
  }

  const restoreTitle = async () => {
    try {
      await removeSessionTitle(sessionId, agentType)
      setTitleApplied(false)
      onTitleApplied(null)
    } catch (err) {
      patch('title', { error: err instanceof Error ? err.message : String(err) })
    }
  }

  const st = states[tab]
  const btnCls = 'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'

  return (
    <div className="fixed inset-0 z-[300] flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-xl w-[min(760px,92vw)] h-[min(600px,84vh)] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-[var(--border-default)]">
          <div className="flex items-center gap-3 min-w-0">
            <span className="text-sm font-medium text-[var(--text-primary)]">AI</span>
            <div className="flex items-center gap-1">
              {TABS.map(t => (
                <button
                  key={t.kind}
                  onClick={() => setTab(t.kind)}
                  className={`h-7 rounded-md px-2.5 text-helper ${
                    tab === t.kind
                      ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                      : 'text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'
                  }`}
                >
                  {t.label}
                </button>
              ))}
            </div>
            <span className="truncate text-meta text-[var(--text-muted)]">{sessionName}</span>
          </div>
          <button onClick={onClose} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] text-lg leading-none px-1">✕</button>
        </div>

        <div className="flex flex-shrink-0 items-center gap-2 border-b border-[var(--border-muted)] px-4 py-2">
          <button className={btnCls} disabled={st.busy} onClick={() => void generate(tab)}>
            {st.busy ? '生成中…' : st.generation ? '重新生成' : (tab === 'summary' ? '生成总结' : tab === 'title' ? '生成标题' : '生成交接提示词')}
          </button>
          {st.busy && <span className="text-meta text-[var(--text-muted)]" role="status">{st.stage}</span>}
          {!st.busy && st.generation && (
            <>
              {tab !== 'title' && (
                <button className={btnCls} onClick={() => copy(st.generation!.content)}>
                  {copied ? '已复制 ✓' : '复制'}
                </button>
              )}
              <span className="ml-auto text-meta text-[var(--text-muted)]">
                {st.generation.model_id} · {st.generation.created_at}
              </span>
            </>
          )}
        </div>

        <div className="flex-1 overflow-auto p-4">
          {st.noProvider && (
            <div className="rounded-md border border-dashed border-[var(--border-default)] p-4 text-center">
              <div className="text-helper text-[var(--text-primary)]">还没有配置 AI 模型</div>
              <div className="mt-1 text-meta text-[var(--text-muted)]">支持 OpenAI 兼容 API，或直接复用本机 claude / codex / gemini CLI</div>
              <button
                className={`${btnCls} mt-3 border-[var(--accent-blue)] text-[var(--accent-blue)]`}
                onClick={() => window.dispatchEvent(new Event('si-open-ai-settings'))}
              >
                去配置模型源
              </button>
            </div>
          )}
          {st.error && <div className="mb-3 whitespace-pre-wrap break-all text-helper text-[var(--error)]">{st.error}</div>}

          {!st.noProvider && !st.generation && !st.busy && st.loaded && !st.error && (
            <div className="pt-8 text-center text-helper text-[var(--text-muted)]">
              {tab === 'summary' && '为这个会话生成一份 markdown 总结：做了什么、关键结论与决策、遗留问题。'}
              {tab === 'title' && '为这个会话生成一个简短中文标题，确认后替换侧边栏的显示名（不改动 agent 原始日志）。'}
              {tab === 'handoff' && '生成一段自包含的交接提示词，粘贴给新会话即可无缝接手这里的工作。'}
            </div>
          )}

          {st.generation && tab === 'title' && (
            <div className="mx-auto max-w-[480px] pt-6 text-center">
              <div className="text-meta text-[var(--text-muted)]">标题草稿</div>
              <div className="mt-2 rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-4 py-3 text-body font-medium text-[var(--text-primary)]">
                {st.generation.content}
              </div>
              <div className="mt-4 flex items-center justify-center gap-2">
                <button
                  className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}
                  disabled={titleApplied}
                  onClick={() => void applyTitle(st.generation!.content)}
                >
                  {titleApplied ? '已应用 ✓' : '应用为显示标题'}
                </button>
                <button className={btnCls} onClick={() => void restoreTitle()}>恢复原始标题</button>
              </div>
              <div className="mt-2 text-meta text-[var(--text-muted)]">只影响本应用的显示，agent 日志文件不会被修改</div>
            </div>
          )}

          {st.generation && tab !== 'title' && (
            <MarkdownRenderer content={st.generation.content} />
          )}
        </div>
      </div>
    </div>
  )
}
