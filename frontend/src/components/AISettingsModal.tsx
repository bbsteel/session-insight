import { useEffect, useRef, useState } from 'react'
import {
  addLLMProvider, deleteLLMProvider, fetchLLMProviders, setDefaultLLMProvider,
  testLLMProvider, updateLLMProvider,
  type LLMModel, type LLMProvider, type LLMProviderInput,
} from '../api'

interface Props {
  onClose: () => void
}

const AGENT_LABELS: Record<string, string> = {
  claude: 'Claude Code',
  codex: 'Codex CLI',
  gemini: 'Gemini CLI',
}

// Common OpenAI-compatible endpoints, one click to fill. Model names are
// offline fallbacks so the user can save without a live /models round-trip;
// a successful 测试连接 replaces them with the endpoint's real list. Entries
// with an empty models list rely on the live fetch entirely.
const API_PRESETS: { name: string; baseUrl: string; models: string[] }[] = [
  { name: 'DeepSeek', baseUrl: 'https://api.deepseek.com/v1', models: ['deepseek-chat', 'deepseek-reasoner'] },
  { name: 'Kimi', baseUrl: 'https://api.moonshot.cn/v1', models: ['kimi-k2-turbo-preview', 'kimi-k2-0905-preview', 'moonshot-v1-auto'] },
  { name: '通义千问', baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', models: ['qwen3-max', 'qwen-plus', 'qwen-turbo'] },
  { name: '智谱 GLM', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', models: ['glm-4.6', 'glm-4.5-air'] },
  { name: 'MiniMax', baseUrl: 'https://api.minimaxi.com/v1', models: ['MiniMax-M2'] },
  { name: '硅基流动', baseUrl: 'https://api.siliconflow.cn/v1', models: [] },
  { name: 'OpenRouter', baseUrl: 'https://openrouter.ai/api/v1', models: [] },
  { name: 'Groq', baseUrl: 'https://api.groq.com/openai/v1', models: [] },
  { name: 'xAI', baseUrl: 'https://api.x.ai/v1', models: ['grok-4'] },
  { name: 'Mistral', baseUrl: 'https://api.mistral.ai/v1', models: [] },
  { name: 'OpenAI', baseUrl: 'https://api.openai.com/v1', models: ['gpt-5.1', 'gpt-5-mini', 'gpt-4o'] },
  { name: 'ollama 本机', baseUrl: 'http://127.0.0.1:11434/v1', models: [] },
]

interface FormState {
  editingId: number | null
  name: string
  kind: 'api' | 'acp'
  baseUrl: string
  apiKey: string
  hasStoredKey: boolean
  agent: string
  modelId: string
  modelLabel: string
}

const emptyForm: FormState = {
  editingId: null, name: '', kind: 'api', baseUrl: '', apiKey: '',
  hasStoredKey: false, agent: '', modelId: '', modelLabel: '',
}

// One model-list fetch result, keyed per source (agent / endpoint) so a slow
// or failing source never blocks or overwrites another tab's list.
interface FetchState {
  status: 'loading' | 'ok' | 'error'
  models?: LLMModel[]
  error?: string
  preset?: boolean
}

// AISettingsModal manages LLM provider configs: an OpenAI-compatible HTTP
// endpoint ("api") or a local agent CLI over ACP ("acp"). Model choice is
// mandatory. ACP model lists load automatically per agent (backend TTL cache
// covers both successes and failures; 强制刷新 bypasses it); API lists come
// from 测试连接 or the preset fallbacks.
export default function AISettingsModal({ onClose }: Props) {
  const [providers, setProviders] = useState<LLMProvider[]>([])
  const [acpAgents, setAcpAgents] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [listError, setListError] = useState<string | null>(null)

  const [form, setForm] = useState<FormState | null>(null)
  const [fetches, setFetches] = useState<Record<string, FetchState>>({})
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  // The last auto-generated 名称, so user edits are never overwritten.
  const autoNameRef = useRef('')

  const reload = () => {
    fetchLLMProviders()
      .then(data => {
        setProviders(data.providers)
        setAcpAgents(data.acp_agents ?? [])
        setListError(null)
        // Anyone holding a provider picker (e.g. the AI panel) refreshes off
        // this — provider edits should show up without reopening.
        window.dispatchEvent(new Event('si-ai-providers-changed'))
      })
      .catch(err => setListError(err.message))
      .finally(() => setLoading(false))
  }
  useEffect(reload, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const sourceKey = (f: FormState): string =>
    f.kind === 'acp' ? `acp:${f.agent}` : `api:${f.baseUrl.trim()}`

  const fetchModels = async (f: FormState, force: boolean) => {
    const key = sourceKey(f)
    setFetches(prev => ({ ...prev, [key]: { status: 'loading' } }))
    setFormError(null)
    try {
      const list = await testLLMProvider({
        kind: f.kind,
        base_url: f.baseUrl,
        api_key: f.apiKey || undefined,
        agent: f.agent,
        provider_id: f.editingId ?? undefined,
        force,
      })
      setFetches(prev => ({ ...prev, [key]: { status: 'ok', models: list } }))
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      setFetches(prev => ({ ...prev, [key]: { status: 'error', error: message } }))
    }
  }

  // ACP model lists need no credentials, so load them as soon as an agent is
  // picked. Fetch only when this agent has no result yet — errors stay until
  // 强制刷新, mirroring the backend's failure cache.
  useEffect(() => {
    if (!form || form.kind !== 'acp' || !form.agent) return
    if (fetches[`acp:${form.agent}`]) return
    void fetchModels(form, false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form?.kind, form?.agent])

  const openAdd = () => {
    setForm({ ...emptyForm, kind: acpAgents.length > 0 ? 'acp' : 'api', agent: acpAgents[0] ?? '' })
    setFormError(null)
    autoNameRef.current = ''
  }

  const openEdit = (p: LLMProvider) => {
    setForm({
      editingId: p.id, name: p.name, kind: p.kind, baseUrl: p.base_url,
      apiKey: '', hasStoredKey: p.has_api_key, agent: p.agent,
      modelId: p.model_id, modelLabel: p.model_label,
    })
    setFormError(null)
    autoNameRef.current = ''
  }

  const applyPreset = (preset: typeof API_PRESETS[number]) => {
    if (!form) return
    const next = { ...form, baseUrl: preset.baseUrl, modelId: '', modelLabel: '' }
    if (!form.name.trim() || form.name === autoNameRef.current) {
      next.name = preset.name
      autoNameRef.current = preset.name
    }
    setForm(next)
    const key = `api:${preset.baseUrl}`
    if (preset.models.length > 0 && fetches[key]?.status !== 'ok') {
      setFetches(prev => ({
        ...prev,
        [key]: { status: 'ok', preset: true, models: preset.models.map(id => ({ id, label: id })) },
      }))
    }
    setFormError(null)
  }

  // selectModel also derives a default 名称 in provider-model form, but only
  // while the user hasn't typed their own.
  const selectModel = (id: string, f: FormState = form!) => {
    const st = fetches[sourceKey(f)]
    const m = st?.models?.find(m => m.id === id)
    const next = { ...f, modelId: id, modelLabel: m?.label ?? id }
    if (id && (!f.name.trim() || f.name === autoNameRef.current)) {
      const base = f.kind === 'acp'
        ? f.agent
        : (API_PRESETS.find(p => p.baseUrl === f.baseUrl.trim())?.name ?? f.name.trim() ?? 'api')
      const auto = `${base}-${id}`
      next.name = auto
      autoNameRef.current = auto
    }
    setForm(next)
  }

  const save = async () => {
    if (!form) return
    const input: LLMProviderInput = {
      name: form.name.trim(),
      kind: form.kind,
      base_url: form.baseUrl.trim(),
      api_key: form.apiKey,
      agent: form.agent,
      model_id: form.modelId,
      model_label: form.modelLabel,
    }
    setSaving(true)
    setFormError(null)
    try {
      if (form.editingId != null) await updateLLMProvider(form.editingId, input)
      else await addLLMProvider(input)
      setForm(null)
      reload()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const remove = async (p: LLMProvider) => {
    if (!window.confirm(`删除模型源「${p.name}」？`)) return
    try {
      await deleteLLMProvider(p.id)
      reload()
    } catch (err) {
      setListError(err instanceof Error ? err.message : String(err))
    }
  }

  const makeDefault = async (p: LLMProvider) => {
    try {
      await setDefaultLLMProvider(p.id)
      reload()
    } catch (err) {
      setListError(err instanceof Error ? err.message : String(err))
    }
  }

  const inputCls = 'mt-1 h-8 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] px-2.5 text-helper text-[var(--text-primary)] shadow-sm placeholder:text-[var(--text-muted)] hover:border-[var(--text-muted)] focus:border-[var(--accent-blue)] focus:outline-none focus:ring-1 focus:ring-[var(--accent-blue)]'
  const btnCls = 'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'
  const chipOn = 'bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] border border-[var(--accent-blue)] text-[var(--accent-blue)]'
  const chipOff = 'border border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]'

  const st = form ? fetches[sourceKey(form)] : undefined
  const listedIds = new Set((st?.models ?? []).map(m => m.id))

  return (
    <div className="fixed inset-0 z-[400] flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-xl w-[min(640px,92vw)] max-h-[84vh] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-[var(--border-default)]">
          <div className="text-sm font-medium text-[var(--text-primary)]">AI 模型源</div>
          <button onClick={onClose} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] text-lg leading-none px-1">✕</button>
        </div>

        <div className="flex-1 overflow-auto p-4">
          {loading && <div className="text-helper text-[var(--text-secondary)]">加载中…</div>}
          {listError && <div className="mb-2 text-helper text-[var(--error)]">{listError}</div>}

          {!loading && !form && (
            <>
              {providers.length === 0 ? (
                <div className="rounded-md border border-dashed border-[var(--border-default)] p-4 text-center text-helper text-[var(--text-secondary)]">
                  还没有配置模型源。支持 OpenAI 兼容 API（DeepSeek、通义、Kimi、ollama…），
                  或直接使用本机已安装的 agent CLI（无需 API key）。
                  {acpAgents.length > 0 && (
                    <div className="mt-1 text-meta text-[var(--text-muted)]">
                      本机已检测到：{acpAgents.map(a => AGENT_LABELS[a] ?? a).join('、')}
                    </div>
                  )}
                </div>
              ) : (
                <ul className="space-y-2">
                  {providers.map(p => (
                    <li
                      key={p.id}
                      className={`flex items-center gap-2 rounded-md border px-3 py-2 ${
                        p.is_default
                          ? 'border-[var(--accent-blue)] bg-[color-mix(in_srgb,var(--accent-blue)_6%,transparent)]'
                          : 'border-[var(--border-muted)]'
                      }`}
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="truncate text-helper font-medium text-[var(--text-primary)]">{p.name}</span>
                          <span className="flex-shrink-0 rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5 text-meta text-[var(--text-secondary)]">
                            {p.kind === 'api' ? 'API' : (AGENT_LABELS[p.agent] ?? p.agent)}
                          </span>
                          {p.is_default && (
                            <span className="flex-shrink-0 rounded bg-[var(--accent-blue)] px-1.5 py-px text-meta font-medium text-[var(--text-inverse)]">✓ 使用中</span>
                          )}
                        </div>
                        <div className="mt-0.5 truncate text-meta text-[var(--text-muted)]">
                          {p.model_id}{p.kind === 'api' && p.base_url ? ` · ${p.base_url}` : ''}
                        </div>
                      </div>
                      {!p.is_default && (
                        <button className={btnCls} onClick={() => void makeDefault(p)}>设为默认</button>
                      )}
                      <button className={btnCls} onClick={() => openEdit(p)}>编辑</button>
                      <button className={`${btnCls} text-[var(--error)]`} onClick={() => void remove(p)}>删除</button>
                    </li>
                  ))}
                </ul>
              )}
              <button className={`${btnCls} mt-3`} onClick={openAdd}>+ 添加模型源</button>
            </>
          )}

          {form && (
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                {(['api', 'acp'] as const).map(kind => (
                  <button
                    key={kind}
                    onClick={() => {
                      setForm({ ...form, kind, agent: kind === 'acp' ? (form.agent || acpAgents[0] || 'claude') : form.agent, modelId: '', modelLabel: '' })
                    }}
                    className={`h-7 rounded-md px-3 text-helper ${form.kind === kind ? chipOn : chipOff}`}
                  >
                    {kind === 'api' ? 'OpenAI 兼容 API' : '本机 Agent CLI'}
                  </button>
                ))}
              </div>

              {form.kind === 'api' && (
                <div>
                  <div className="text-meta text-[var(--text-muted)]">常用服务（点击填充端点）</div>
                  <div className="mt-1 flex flex-wrap items-center gap-1.5">
                    {API_PRESETS.map(preset => (
                      <button
                        key={preset.name}
                        onClick={() => applyPreset(preset)}
                        className={`h-6 rounded-full px-2.5 text-meta ${form.baseUrl.trim() === preset.baseUrl ? chipOn : chipOff}`}
                      >
                        {preset.name}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              <label className="block text-helper text-[var(--text-primary)]">
                名称
                <input
                  type="text"
                  value={form.name}
                  placeholder="选择模型后自动生成，可修改"
                  onChange={e => setForm({ ...form, name: e.target.value })}
                  className={inputCls}
                />
              </label>

              {form.kind === 'api' ? (
                <>
                  <label className="block text-helper text-[var(--text-primary)]">
                    Base URL
                    <input
                      type="text"
                      value={form.baseUrl}
                      placeholder="https://api.deepseek.com/v1"
                      onChange={e => setForm({ ...form, baseUrl: e.target.value })}
                      className={`${inputCls} font-mono`}
                    />
                  </label>
                  <label className="block text-helper text-[var(--text-primary)]">
                    API Key
                    <input
                      type="password"
                      value={form.apiKey}
                      placeholder={form.hasStoredKey ? '已保存（留空则不修改）' : 'sk-…'}
                      onChange={e => setForm({ ...form, apiKey: e.target.value })}
                      className={`${inputCls} font-mono`}
                    />
                  </label>
                  <div className="flex items-center gap-2">
                    <button className={btnCls} disabled={st?.status === 'loading' || !form.baseUrl.trim()} onClick={() => void fetchModels(form, false)}>
                      {st?.status === 'loading' ? '连接中…' : '测试连接并获取模型'}
                    </button>
                    {st?.status === 'ok' && !st.preset && (
                      <span className="text-meta text-[var(--accent-green,#3fb950)]">✓ 获取到 {st.models!.length} 个模型</span>
                    )}
                  </div>
                </>
              ) : (
                <div className="block text-helper text-[var(--text-primary)]">
                  Agent CLI
                  <div className="mt-1 flex flex-wrap items-center gap-1.5">
                    {['claude', 'codex', 'gemini'].map(a => (
                      <button
                        key={a}
                        onClick={() => {
                          if (a === form.agent) return
                          setForm({ ...form, agent: a, modelId: '', modelLabel: '' })
                        }}
                        className={`h-7 rounded-md px-2.5 text-helper ${form.agent === a ? chipOn : chipOff}`}
                      >
                        {AGENT_LABELS[a]}{acpAgents.includes(a) ? '' : '（未检测到）'}
                      </button>
                    ))}
                  </div>
                  <div className="mt-1 text-meta text-[var(--text-muted)]">
                    复用本机 CLI 的登录态，无需 API key。首次连接会自动下载适配器，可能需要一分钟左右。
                  </div>
                  <div className="mt-2 flex items-center gap-2">
                    {st?.status === 'loading' && <span className="text-meta text-[var(--text-muted)]" role="status">正在获取模型列表…</span>}
                    {st?.status === 'ok' && (
                      <span className="text-meta text-[var(--accent-green,#3fb950)]">✓ {st.models!.length} 个模型</span>
                    )}
                    {st && st.status !== 'loading' && (
                      <button className={`${btnCls} h-6 px-2 text-meta`} onClick={() => void fetchModels(form, true)}>
                        强制刷新
                      </button>
                    )}
                  </div>
                </div>
              )}

              {st?.status === 'error' && (
                <div className="whitespace-pre-wrap break-all rounded-md border border-[var(--error)] bg-[color-mix(in_srgb,var(--error)_6%,transparent)] px-2.5 py-1.5 text-helper text-[var(--error)]">
                  {st.error}
                </div>
              )}

              {(st?.status === 'ok' || form.modelId) && (
                <div className="block text-helper text-[var(--text-primary)]">
                  模型（必选）
                  {st?.preset && (
                    <span className="ml-2 text-meta text-[var(--text-muted)]">预置常见模型，建议「测试连接」获取实际列表</span>
                  )}
                  {st?.status === 'ok' && st.models!.length > 0 && (
                    <div className="mt-1 space-y-1">
                      {st.models!.map(m => (
                        <button
                          key={m.id}
                          onClick={() => selectModel(m.id)}
                          className={`flex w-full items-baseline gap-2 rounded-md border px-2.5 py-1.5 text-left ${form.modelId === m.id ? chipOn : chipOff}`}
                        >
                          <span className="flex-shrink-0 font-medium">
                            {m.label && m.label !== m.id ? m.label : m.id}
                          </span>
                          {m.label && m.label !== m.id && (
                            <span className="flex-shrink-0 font-mono text-meta opacity-70">{m.id}</span>
                          )}
                          {m.description && (
                            <span className="min-w-0 truncate text-meta text-[var(--text-muted)]">{m.description}</span>
                          )}
                        </button>
                      ))}
                    </div>
                  )}
                  <input
                    type="text"
                    value={listedIds.has(form.modelId) ? '' : form.modelId}
                    placeholder="或手动填写模型 ID（列表里没有的型号）"
                    onChange={e => selectModel(e.target.value.trim())}
                    className={`${inputCls} font-mono`}
                  />
                </div>
              )}

              {formError && <div className="whitespace-pre-wrap break-all text-helper text-[var(--error)]">{formError}</div>}

              <div className="flex items-center gap-2 border-t border-[var(--border-muted)] pt-3">
                <button
                  className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}
                  disabled={saving || !form.name.trim() || !form.modelId}
                  onClick={() => void save()}
                >
                  {saving ? '保存中…' : '保存'}
                </button>
                <button className={btnCls} onClick={() => setForm(null)}>取消</button>
                {!form.modelId && <span className="text-meta text-[var(--text-muted)]">先选择一个模型</span>}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
