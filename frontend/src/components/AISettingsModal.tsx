import { useEffect, useState } from 'react'
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

// AISettingsModal manages LLM provider configs: an OpenAI-compatible HTTP
// endpoint ("api") or a local agent CLI over ACP ("acp"). Model choice is
// mandatory — the form's model list comes from a live "测试连接" fetch, so a
// saved provider is always a verified one.
export default function AISettingsModal({ onClose }: Props) {
  const [providers, setProviders] = useState<LLMProvider[]>([])
  const [acpAgents, setAcpAgents] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [listError, setListError] = useState<string | null>(null)

  const [form, setForm] = useState<FormState | null>(null)
  const [models, setModels] = useState<LLMModel[] | null>(null)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const reload = () => {
    fetchLLMProviders()
      .then(data => {
        setProviders(data.providers)
        setAcpAgents(data.acp_agents ?? [])
        setListError(null)
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

  const openAdd = () => {
    setForm({ ...emptyForm, kind: acpAgents.length > 0 ? 'acp' : 'api', agent: acpAgents[0] ?? '' })
    setModels(null)
    setFormError(null)
  }

  const openEdit = (p: LLMProvider) => {
    setForm({
      editingId: p.id, name: p.name, kind: p.kind, baseUrl: p.base_url,
      apiKey: '', hasStoredKey: p.has_api_key, agent: p.agent,
      modelId: p.model_id, modelLabel: p.model_label,
    })
    setModels(null)
    setFormError(null)
  }

  const runTest = async () => {
    if (!form) return
    setTesting(true)
    setFormError(null)
    try {
      const list = await testLLMProvider({
        kind: form.kind,
        base_url: form.baseUrl,
        api_key: form.apiKey || undefined,
        agent: form.agent,
        provider_id: form.editingId ?? undefined,
      })
      setModels(list)
      if (list.length === 0) setFormError('连接成功但未返回任何模型')
    } catch (err) {
      setModels(null)
      setFormError(err instanceof Error ? err.message : String(err))
    } finally {
      setTesting(false)
    }
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

  const inputCls = 'mt-1 h-7 w-full rounded-md border border-[var(--border-default)] bg-[var(--bg-inset)] px-2 text-helper text-[var(--text-primary)] focus:border-[var(--accent-blue)] focus:outline-none'
  const btnCls = 'h-7 rounded-md border border-[var(--border-default)] px-2.5 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] disabled:opacity-50'

  return (
    <div className="fixed inset-0 z-[300] flex items-center justify-center bg-black/50" onClick={onClose}>
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
                    <li key={p.id} className="flex items-center gap-2 rounded-md border border-[var(--border-muted)] px-3 py-2">
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="truncate text-helper font-medium text-[var(--text-primary)]">{p.name}</span>
                          <span className="flex-shrink-0 rounded border border-[var(--border-muted)] bg-[var(--bg-inset)] px-1.5 text-meta text-[var(--text-secondary)]">
                            {p.kind === 'api' ? 'API' : (AGENT_LABELS[p.agent] ?? p.agent)}
                          </span>
                          {p.is_default && (
                            <span className="flex-shrink-0 rounded bg-[var(--accent-blue)]/10 px-1.5 text-meta text-[var(--accent-blue)]">默认</span>
                          )}
                        </div>
                        <div className="mt-0.5 truncate text-meta text-[var(--text-muted)]">
                          {p.model_label || p.model_id}{p.kind === 'api' && p.base_url ? ` · ${p.base_url}` : ''}
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
                      setModels(null)
                    }}
                    className={`h-7 rounded-md px-3 text-helper ${
                      form.kind === kind
                        ? 'bg-[var(--accent-blue)]/10 text-[var(--accent-blue)]'
                        : 'border border-[var(--border-default)] text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)]'
                    }`}
                  >
                    {kind === 'api' ? 'OpenAI 兼容 API' : '本机 Agent CLI'}
                  </button>
                ))}
              </div>

              <label className="block text-helper text-[var(--text-primary)]">
                名称
                <input
                  type="text"
                  value={form.name}
                  placeholder={form.kind === 'api' ? '如 DeepSeek' : '如 本机 Claude'}
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
                </>
              ) : (
                <label className="block text-helper text-[var(--text-primary)]">
                  Agent CLI
                  <select
                    value={form.agent}
                    onChange={e => { setForm({ ...form, agent: e.target.value, modelId: '', modelLabel: '' }); setModels(null) }}
                    className={inputCls}
                  >
                    {['claude', 'codex', 'gemini'].map(a => (
                      <option key={a} value={a}>
                        {AGENT_LABELS[a]}{acpAgents.includes(a) ? '' : '（本机未检测到）'}
                      </option>
                    ))}
                  </select>
                  <div className="mt-1 text-meta text-[var(--text-muted)]">
                    复用本机 CLI 的登录态，无需 API key。首次连接会自动下载适配器，可能需要一分钟左右。
                  </div>
                </label>
              )}

              <div className="flex items-center gap-2">
                <button className={btnCls} disabled={testing} onClick={() => void runTest()}>
                  {testing ? '连接中…' : '测试连接并获取模型'}
                </button>
                {models && models.length > 0 && (
                  <span className="text-meta text-[var(--accent-green,#3fb950)]">✓ 获取到 {models.length} 个模型</span>
                )}
              </div>

              {(models || form.modelId) && (
                <label className="block text-helper text-[var(--text-primary)]">
                  模型（必选）
                  {models ? (
                    <select
                      value={form.modelId}
                      onChange={e => {
                        const m = models.find(m => m.id === e.target.value)
                        setForm({ ...form, modelId: e.target.value, modelLabel: m?.label ?? e.target.value })
                      }}
                      className={inputCls}
                    >
                      <option value="">— 请选择 —</option>
                      {models.map(m => (
                        <option key={m.id} value={m.id}>{m.label && m.label !== m.id ? `${m.label} (${m.id})` : m.id}</option>
                      ))}
                    </select>
                  ) : (
                    <div className="mt-1 text-meta text-[var(--text-muted)]">
                      当前：{form.modelLabel || form.modelId}（点「测试连接」可重新选择）
                    </div>
                  )}
                </label>
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
                {!form.modelId && <span className="text-meta text-[var(--text-muted)]">先测试连接并选择模型</span>}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
