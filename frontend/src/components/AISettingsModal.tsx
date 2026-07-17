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
  grok: 'Grok CLI',
}

const LOCAL_AGENTS = ['claude', 'codex', 'gemini', 'grok'] as const

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

interface HeaderRow {
  key: string
  value: string
}

interface FormState {
  editingId: number | null
  name: string
  kind: 'api' | 'acp'
  baseUrl: string
  apiKey: string
  hasStoredKey: boolean
  /** Extra HTTP headers for API sources (key/value rows for the editor). */
  headers: HeaderRow[]
  agent: string
  modelId: string
  modelLabel: string
}

const emptyForm: FormState = {
  editingId: null, name: '', kind: 'api', baseUrl: '', apiKey: '',
  hasStoredKey: false, headers: [], agent: '', modelId: '', modelLabel: '',
}

function headersFromRecord(rec: Record<string, string> | undefined | null): HeaderRow[] {
  if (!rec) return []
  return Object.entries(rec)
    .filter(([k]) => k.trim() !== '')
    .map(([key, value]) => ({ key, value: value ?? '' }))
    .sort((a, b) => a.key.localeCompare(b.key))
}

function headersToRecord(rows: HeaderRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) {
    const k = row.key.trim()
    if (!k) continue
    out[k] = row.value
  }
  return out
}

// Common header names for gateways / OpenRouter / Anthropic-style endpoints.
// Picking one from the dropdown inserts an editable row (name prefilled).
const HEADER_PRESETS: { name: string; hint: string }[] = [
  { name: 'x-auth-token', hint: '部分企业网关鉴权' },
  { name: 'X-Api-Key', hint: '通用 API Key 头' },
  { name: 'api-key', hint: 'Azure / 部分国内网关' },
  { name: 'Authorization', hint: '覆盖默认 Bearer，或填非 Bearer 方案' },
  { name: 'HTTP-Referer', hint: 'OpenRouter 推荐' },
  { name: 'X-Title', hint: 'OpenRouter 应用名' },
  { name: 'anthropic-version', hint: 'Anthropic 兼容端点' },
  { name: 'OpenAI-Organization', hint: 'OpenAI 组织' },
  { name: 'OpenAI-Project', hint: 'OpenAI 项目' },
]

function headerKeyEquals(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase()
}

// In-flow picker (not a floating overlay): expands inside the Headers card so it
// never paints under "测试连接" / the model list. Name left, hint right.
function HeaderPresetPicker({
  usedKeys,
  onPick,
  triggerClassName,
}: {
  usedKeys: string[]
  onPick: (name: string) => void
  triggerClassName: string
}) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDoc = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('pointerdown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('pointerdown', onDoc)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div ref={rootRef} className="w-full">
      <button
        type="button"
        className={`${triggerClassName} flex w-full items-center justify-between`}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen(v => !v)}
      >
        <span>添加常见 Header…</span>
        <span className="opacity-70" aria-hidden>{open ? '▴' : '▾'}</span>
      </button>
      {open && (
        <ul
          role="listbox"
          className="mt-1 max-h-36 overflow-y-auto rounded-md border border-[var(--border-default)] bg-[var(--bg-surface)] py-0.5"
        >
          {HEADER_PRESETS.map(preset => {
            const used = usedKeys.some(k => headerKeyEquals(k, preset.name))
            return (
              <li key={preset.name} role="option" aria-selected={used} aria-disabled={used || undefined}>
                <button
                  type="button"
                  disabled={used}
                  title={used ? `${preset.name} 已添加` : undefined}
                  onClick={() => {
                    if (used) return
                    onPick(preset.name)
                    setOpen(false)
                  }}
                  className="flex w-full items-baseline justify-between gap-4 px-2.5 py-1.5 text-left disabled:cursor-not-allowed disabled:opacity-45 hover:bg-[var(--bg-surface-hover)]"
                >
                  <span className="min-w-0 truncate font-mono text-helper text-[var(--text-primary)]">
                    {preset.name}
                  </span>
                  <span className="flex-shrink-0 text-right text-meta text-[var(--text-muted)]">
                    {used ? '已添加' : preset.hint}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
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
        headers: f.kind === 'api' ? headersToRecord(f.headers) : undefined,
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

  // New sources always open on OpenAI-compatible API; users can switch to ACP.
  const openAdd = () => {
    setForm({ ...emptyForm, kind: 'api', agent: acpAgents[0] ?? '' })
    setFormError(null)
    autoNameRef.current = ''
  }

  const openEdit = (p: LLMProvider) => {
    setForm({
      editingId: p.id, name: p.name, kind: p.kind, baseUrl: p.base_url,
      apiKey: '', hasStoredKey: p.has_api_key,
      headers: headersFromRecord(p.headers),
      agent: p.agent,
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

  // Restore the empty "选择服务…" state after picking a preset.
  const clearApiPreset = () => {
    if (!form) return
    const next = { ...form, baseUrl: '', modelId: '', modelLabel: '' }
    if (!form.name.trim() || form.name === autoNameRef.current) {
      next.name = ''
      autoNameRef.current = ''
    }
    setForm(next)
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

  // model_id is globally unique across saved providers.
  const takenModelIds = new Set(
    providers.filter(p => p.id !== form?.editingId).map(p => p.model_id),
  )

  const save = async () => {
    if (!form) return
    if (takenModelIds.has(form.modelId)) {
      setFormError(`model_id「${form.modelId}」已被其他模型源占用，不可重复配置`)
      return
    }
    const input: LLMProviderInput = {
      name: form.name.trim(),
      kind: form.kind,
      base_url: form.baseUrl.trim(),
      api_key: form.apiKey,
      headers: form.kind === 'api' ? headersToRecord(form.headers) : undefined,
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
      {/* Fixed shell height: internal sections scroll so adding headers / models
          never resizes the dialog or fights nested overflow regions. */}
      <div
        className="bg-[var(--bg-surface)] border border-[var(--border-default)] rounded-lg shadow-xl w-[min(680px,94vw)] h-[min(760px,88vh)] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex flex-shrink-0 items-center justify-between px-4 py-2.5 border-b border-[var(--border-default)]">
          <div className="text-sm font-medium text-[var(--text-primary)]">AI 模型源</div>
          <button onClick={onClose} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] text-lg leading-none px-1">✕</button>
        </div>

        <div className="flex min-h-0 flex-1 flex-col overflow-hidden p-4">
          {loading && <div className="text-helper text-[var(--text-secondary)]">加载中…</div>}
          {listError && <div className="mb-2 flex-shrink-0 text-helper text-[var(--error)]">{listError}</div>}

          {!loading && !form && (
            <div className="flex min-h-0 flex-1 flex-col">
              <div className="min-h-0 flex-1 overflow-y-auto">
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
              </div>
              <button className={`${btnCls} mt-3 flex-shrink-0 self-start`} onClick={openAdd}>+ 添加模型源</button>
            </div>
          )}

          {form && (
            <div className="flex min-h-0 flex-1 flex-col">
              {/*
                Layout: fixed shell → connection block (no outer nested scroll) →
                model list takes leftover height → footer pinned.
                Headers grow only inside their own max-height box so the dialog
                never jumps when adding rows.
              */}
              <div className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-hidden">
                <div className="flex flex-shrink-0 flex-col gap-2.5">
                  <div className="flex items-center gap-2">
                    {(['api', 'acp'] as const).map(kind => (
                      <button
                        key={kind}
                        type="button"
                        onClick={() => {
                          setForm({
                            ...form,
                            kind,
                            agent: kind === 'acp'
                              ? (acpAgents.includes(form.agent) ? form.agent : (acpAgents[0] ?? ''))
                              : form.agent,
                            modelId: '',
                            modelLabel: '',
                          })
                        }}
                        className={`h-7 rounded-md px-3 text-helper ${form.kind === kind ? chipOn : chipOff}`}
                      >
                        {kind === 'api' ? 'OpenAI 兼容 API' : '本机 Agent CLI'}
                      </button>
                    ))}
                  </div>

                  {form.kind === 'api' ? (
                    <label className="block text-helper text-[var(--text-primary)]">
                      常用大模型服务
                      <select
                        className={`${inputCls} font-mono`}
                        value={API_PRESETS.find(p => p.baseUrl === form.baseUrl.trim())?.baseUrl ?? ''}
                        aria-label="选择常用大模型服务以填充端点"
                        onChange={e => {
                          const v = e.target.value
                          if (!v) {
                            clearApiPreset()
                            return
                          }
                          const preset = API_PRESETS.find(p => p.baseUrl === v)
                          if (preset) applyPreset(preset)
                        }}
                      >
                        <option value="">选择服务以填充端点…</option>
                        {API_PRESETS.map(preset => (
                          <option key={preset.name} value={preset.baseUrl}>
                            {preset.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ) : (
                    <div className="block text-helper text-[var(--text-primary)]">
                      Agent CLI
                      <div className="mt-1 flex flex-wrap items-center gap-1.5">
                        {LOCAL_AGENTS.map(a => {
                          const detected = acpAgents.includes(a)
                          const selected = form.agent === a
                          return (
                            <button
                              key={a}
                              type="button"
                              disabled={!detected}
                              title={detected ? undefined : '本机未检测到该 CLI，无法选用'}
                              onClick={() => {
                                if (!detected || selected) return
                                setForm({ ...form, agent: a, modelId: '', modelLabel: '' })
                              }}
                              className={`h-7 rounded-md px-2.5 text-helper ${
                                !detected
                                  ? `cursor-not-allowed opacity-50 ${selected ? chipOn : 'border border-[var(--border-muted)] text-[var(--text-muted)]'}`
                                  : selected ? chipOn : chipOff
                              }`}
                            >
                              {AGENT_LABELS[a]}{detected ? '' : '（未检测到）'}
                            </button>
                          )
                        })}
                      </div>
                      <div className="mt-1 text-meta text-[var(--text-muted)]">
                        复用本机 CLI 登录态，无需 API key。
                      </div>
                    </div>
                  )}

                  {form.kind === 'api' && (
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

                      {/* Headers: in-flow preset list (no floating overlay that covers siblings). */}
                      <div className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] p-2">
                        <div className="flex flex-wrap items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="text-helper text-[var(--text-primary)]">自定义 Headers</div>
                            <div className="text-meta text-[var(--text-muted)]">可选 · 同名头覆盖默认 Authorization</div>
                          </div>
                          <button
                            type="button"
                            className={`${btnCls} h-7 flex-shrink-0 bg-[var(--bg-surface)] px-2 text-meta`}
                            onClick={() => setForm({ ...form, headers: [...form.headers, { key: '', value: '' }] })}
                          >
                            + 自定义
                          </button>
                        </div>
                        <div className="mt-1.5">
                          <HeaderPresetPicker
                            usedKeys={form.headers.map(h => h.key)}
                            triggerClassName={`${btnCls} h-7 bg-[var(--bg-surface)] px-2 font-mono text-meta`}
                            onPick={name => {
                              if (form.headers.some(h => headerKeyEquals(h.key, name))) return
                              setForm({
                                ...form,
                                headers: [...form.headers, { key: name, value: '' }],
                              })
                            }}
                          />
                        </div>
                        {form.headers.length === 0 ? (
                          <div className="mt-1.5 text-meta text-[var(--text-muted)]">未添加请求头</div>
                        ) : (
                          <div className="mt-1.5 max-h-[6.75rem] space-y-1.5 overflow-y-auto pr-0.5">
                            {form.headers.map((row, idx) => (
                              <div key={idx} className="flex items-center gap-1.5">
                                <input
                                  type="text"
                                  value={row.key}
                                  placeholder="Header 名"
                                  onChange={e => {
                                    const headers = form.headers.slice()
                                    headers[idx] = { ...headers[idx], key: e.target.value }
                                    setForm({ ...form, headers })
                                  }}
                                  className={`${inputCls} mt-0 h-7 min-w-0 flex-1 bg-[var(--bg-surface)] font-mono`}
                                  aria-label={`Header 名称 ${idx + 1}`}
                                />
                                <input
                                  type="text"
                                  value={row.value}
                                  placeholder="值"
                                  onChange={e => {
                                    const headers = form.headers.slice()
                                    headers[idx] = { ...headers[idx], value: e.target.value }
                                    setForm({ ...form, headers })
                                  }}
                                  className={`${inputCls} mt-0 h-7 min-w-0 flex-[1.4] bg-[var(--bg-surface)] font-mono`}
                                  aria-label={`Header 值 ${idx + 1}`}
                                />
                                <button
                                  type="button"
                                  className={`${btnCls} h-7 flex-shrink-0 bg-[var(--bg-surface)] px-2 text-[var(--error)]`}
                                  title="删除此 header"
                                  onClick={() => setForm({ ...form, headers: form.headers.filter((_, i) => i !== idx) })}
                                >
                                  ✕
                                </button>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>

                      <div className="flex h-7 flex-wrap items-center gap-2">
                        <button className={btnCls} disabled={st?.status === 'loading' || !form.baseUrl.trim()} onClick={() => void fetchModels(form, false)}>
                          {st?.status === 'loading' ? '连接中…' : '测试连接并获取模型'}
                        </button>
                        {st?.status === 'ok' && !st.preset && (
                          <span className="text-meta text-[var(--accent-green,#3fb950)]">✓ 获取到 {st.models!.length} 个模型</span>
                        )}
                      </div>
                    </>
                  )}

                  {form.kind === 'acp' && (
                    <div className="flex h-7 flex-wrap items-center gap-2">
                      {st?.status === 'loading' && <span className="text-meta text-[var(--text-muted)]" role="status">正在获取模型列表…</span>}
                      {st?.status === 'ok' && (
                        <span className="text-meta text-[var(--accent-green,#3fb950)]">✓ {st.models!.length} 个模型</span>
                      )}
                      {st && st.status !== 'loading' && (
                        <button type="button" className={`${btnCls} h-6 px-2 text-meta`} onClick={() => void fetchModels(form, true)}>
                          强制刷新
                        </button>
                      )}
                    </div>
                  )}

                  {st?.status === 'error' && (
                    <div className="max-h-14 overflow-y-auto whitespace-pre-wrap break-all rounded-md border border-[var(--error)] bg-[color-mix(in_srgb,var(--error)_6%,transparent)] px-2.5 py-1.5 text-helper text-[var(--error)]">
                      {st.error}
                    </div>
                  )}
                </div>

                {/* Model list absorbs remaining height — only this region scrolls long lists. */}
                <div className="flex min-h-0 flex-1 flex-col text-helper text-[var(--text-primary)]">
                  <div className="flex flex-shrink-0 items-baseline gap-2">
                    <span>模型（必选）</span>
                    {st?.preset && (
                      <span className="text-meta text-[var(--text-muted)]">预置常见模型，建议「测试连接」获取实际列表</span>
                    )}
                  </div>
                  <div className="mt-1 min-h-0 flex-1 overflow-y-auto rounded-md border border-[var(--border-muted)] bg-[var(--bg-inset)] p-1.5">
                    {st?.status === 'loading' && (
                      <div className="px-2 py-3 text-meta text-[var(--text-muted)]">正在获取模型列表…</div>
                    )}
                    {st?.status === 'ok' && st.models!.length > 0 && (
                      <div className="space-y-1">
                        {st.models!.map(m => {
                          const taken = takenModelIds.has(m.id)
                          return (
                            <button
                              key={m.id}
                              type="button"
                              disabled={taken}
                              onClick={() => !taken && selectModel(m.id)}
                              title={taken ? '该 model_id 已被其他模型源占用' : undefined}
                              className={`flex w-full items-baseline gap-2 rounded-md border px-2.5 py-1.5 text-left ${
                                taken
                                  ? 'cursor-not-allowed border-[var(--border-muted)] opacity-50'
                                  : form.modelId === m.id ? chipOn : chipOff
                              }`}
                            >
                              <span className="flex-shrink-0 font-medium">
                                {m.label && m.label !== m.id ? m.label : m.id}
                              </span>
                              {m.label && m.label !== m.id && (
                                <span className="flex-shrink-0 font-mono text-meta opacity-70">{m.id}</span>
                              )}
                              {taken && (
                                <span className="flex-shrink-0 text-meta text-[var(--text-muted)]">已占用</span>
                              )}
                              {m.description && !taken && (
                                <span className="min-w-0 truncate text-meta text-[var(--text-muted)]">{m.description}</span>
                              )}
                            </button>
                          )
                        })}
                      </div>
                    )}
                    {st?.status === 'ok' && st.models!.length === 0 && (
                      <div className="px-2 py-3 text-meta text-[var(--text-muted)]">未返回模型，可手动填写下方 ID</div>
                    )}
                    {!st && (
                      <div className="px-2 py-3 text-meta text-[var(--text-muted)]">
                        {form.kind === 'api' ? '测试连接后显示模型列表，或手动填写 ID' : '选择 Agent 后自动获取模型列表'}
                      </div>
                    )}
                    {st?.status === 'error' && (
                      <div className="px-2 py-3 text-meta text-[var(--text-muted)]">获取失败，可手动填写下方 ID 或强制刷新</div>
                    )}
                  </div>
                  <input
                    type="text"
                    value={listedIds.has(form.modelId) ? '' : form.modelId}
                    placeholder="或手动填写模型 ID（列表里没有的型号）"
                    onChange={e => selectModel(e.target.value.trim())}
                    className={`${inputCls} flex-shrink-0 font-mono`}
                  />
                  {form.modelId && takenModelIds.has(form.modelId) && (
                    <div className="mt-1 flex-shrink-0 text-meta text-[var(--error)]">
                      model_id「{form.modelId}」已被其他模型源占用
                    </div>
                  )}
                </div>
              </div>

              {/* 名称在两个 tab 都放在最下方（保存前），便于先配连接/模型再改展示名。 */}
              <label className="mt-2 block flex-shrink-0 text-helper text-[var(--text-primary)]">
                名称
                <input
                  type="text"
                  value={form.name}
                  placeholder="选择模型后自动生成，可修改"
                  onChange={e => setForm({ ...form, name: e.target.value })}
                  className={inputCls}
                />
              </label>

              {formError && (
                <div className="mt-2 max-h-12 flex-shrink-0 overflow-y-auto whitespace-pre-wrap break-all text-helper text-[var(--error)]">
                  {formError}
                </div>
              )}

              <div className="mt-2 flex flex-shrink-0 items-center gap-2 border-t border-[var(--border-muted)] pt-3">
                <button
                  type="button"
                  className={`${btnCls} border-[var(--accent-blue)] text-[var(--accent-blue)]`}
                  disabled={saving || !form.name.trim() || !form.modelId || takenModelIds.has(form.modelId)}
                  onClick={() => void save()}
                >
                  {saving ? '保存中…' : '保存'}
                </button>
                <button type="button" className={btnCls} onClick={() => setForm(null)}>取消</button>
                {!form.modelId && <span className="text-meta text-[var(--text-muted)]">先选择一个模型</span>}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
