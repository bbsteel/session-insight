import type { AgentInfo, EditCall, PositionsResponse, SearchResult, SessionDetail, SessionSummary } from './types'

export async function fetchSessions(agent?: string): Promise<SessionSummary[]> {
  const params = new URLSearchParams()
  if (agent) params.set('agent', agent)

  const res = await fetch(`/api/sessions?${params}`)
  if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.status}`)
  return readJson<SessionSummary[]>(res, 'sessions')
}

export interface TruncatedOutput {
  tool_name: string
  kind: 'stdout' | 'stderr'
  turn_index: number
  content: string
}

export async function fetchToolOutputs(id: string): Promise<TruncatedOutput[]> {
  const res = await fetch(`/api/sessions/${id}/tool-outputs`)
  if (!res.ok) throw new Error(`Failed to fetch tool outputs: ${res.status}`)
  return res.json()
}

export async function fetchSession(id: string): Promise<SessionDetail> {
  const res = await fetch(`/api/sessions/${id}`)
  if (!res.ok) throw new Error(`Failed to fetch session: ${res.status}`)
  return readJson<SessionDetail>(res, 'session')
}

export async function fetchBookmarks(): Promise<SessionSummary[]> {
  const res = await fetch('/api/bookmarks')
  if (!res.ok) throw new Error(`Failed to fetch bookmarks: ${res.status}`)
  return readJson<SessionSummary[]>(res, 'bookmarks')
}

export async function addBookmark(session: Pick<SessionSummary, 'id' | 'agent_type'>): Promise<void> {
  const params = new URLSearchParams({ agent: session.agent_type })
  const res = await fetch(`/api/sessions/${session.id}/bookmark?${params}`, { method: 'PUT' })
  if (!res.ok) throw new Error(`Failed to add bookmark: ${res.status}`)
}

export async function removeBookmark(session: Pick<SessionSummary, 'id' | 'agent_type'>): Promise<void> {
  const params = new URLSearchParams({ agent: session.agent_type })
  const res = await fetch(`/api/sessions/${session.id}/bookmark?${params}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to remove bookmark: ${res.status}`)
}

export async function updateBookmarkNote(session: Pick<SessionSummary, 'id' | 'agent_type'>, note: string): Promise<void> {
  const params = new URLSearchParams({ agent: session.agent_type })
  const res = await fetch(`/api/sessions/${session.id}/bookmark/note?${params}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note }),
  })
  if (!res.ok) throw new Error(`Failed to update bookmark note: ${res.status}`)
}

/** Thrown by deleteSession when the session's agent process is still running. */
export class SessionRunningError extends Error {
  pids: number[]
  constructor(pids: number[]) {
    super('session is running')
    this.pids = pids
  }
}

export async function deleteSession(id: string): Promise<void> {
  const res = await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
  if (res.status === 409) {
    const body = await res.json().catch(() => ({ pids: [] }))
    throw new SessionRunningError(Array.isArray(body.pids) ? body.pids : [])
  }
  if (!res.ok) throw new Error(await res.text().catch(() => `Failed to delete session: ${res.status}`))
}

export async function stopSession(id: string): Promise<number> {
  const res = await fetch(`/api/sessions/${id}/stop`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text().catch(() => `Failed to stop session: ${res.status}`))
  const body = await res.json()
  return typeof body.stopped === 'number' ? body.stopped : 0
}

export async function fetchAgents(): Promise<AgentInfo[]> {
  const res = await fetch('/api/agents')
  if (!res.ok) throw new Error(`Failed to fetch agents: ${res.status}`)
  return readJson<AgentInfo[]>(res, 'agents')
}

export async function fetchRenderANSI(id: string, cols?: number, ts?: string): Promise<string> {
  const params = new URLSearchParams()
  if (cols) params.set('cols', String(cols))
  if (ts) params.set('ts', ts)
  const q = params.toString()
  const res = await fetch(q ? `/api/sessions/${id}/render?${q}` : `/api/sessions/${id}/render`)
  if (!res.ok) throw new Error(`Failed to fetch render: ${res.status}`)
  return res.text()
}

export async function fetchSessionEdits(id: string): Promise<EditCall[]> {
  const res = await fetch(`/api/sessions/${id}/edits`)
  if (!res.ok) throw new Error(`Failed to fetch edits: ${res.status}`)
  return res.json()
}

export async function fetchPositions(
  id: string,
  cols: number,
  ts?: string,
): Promise<{ status: 'building' } | { status: 'ready'; data: PositionsResponse }> {
  const params = new URLSearchParams({ cols: String(cols) })
  if (ts) params.set('ts', ts)
  const res = await fetch(`/api/sessions/${id}/positions?${params}`)
  if (res.status === 202) return { status: 'building' }
  if (!res.ok) throw new Error(`Failed to fetch positions: ${res.status}`)
  const data = await res.json() as PositionsResponse
  return { status: 'ready', data }
}

export async function fetchSearch(q: string): Promise<SearchResult[]> {
  const params = new URLSearchParams({ q })
  const res = await fetch(`/api/search?${params}`)
  if (!res.ok) throw new Error(`Search failed: ${res.status}`)
  return res.json()
}

// Resolves a (possibly cwd-relative) path to an absolute existing file, or
// null — the context menu only offers "open in editor" for real files.
export async function resolveFile(path: string, cwd: string): Promise<string | null> {
  const params = new URLSearchParams({ path, cwd })
  const res = await fetch(`/api/resolve-file?${params}`)
  if (!res.ok) return null
  const data = await res.json() as { path: string }
  return data.path
}

export async function openFile(req: { path: string; cwd?: string; line?: number; search?: string }): Promise<void> {
  const res = await fetch('/api/open-file', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) throw new Error(`Failed to open file: ${res.status} ${await res.text()}`)
}

// Cheap stat-level change marker for live tail; null = unsupported for this
// session's agent (frontend then skips live polling entirely).
export async function fetchLiveRevision(id: string): Promise<number | null> {
  const res = await fetch(`/api/sessions/${id}/live-revision`)
  if (!res.ok) return null
  const data = await res.json() as { revision: number }
  return data.revision
}

export interface FsEntry {
  name: string
  is_dir: boolean
}

export async function fsList(dir: string): Promise<FsEntry[]> {
  const res = await fetch(`/api/fs/list?${new URLSearchParams({ dir })}`)
  if (!res.ok) throw new Error(`Failed to list ${dir}: ${res.status}`)
  return res.json()
}

export async function fsRead(path: string): Promise<{ path: string; content: string; truncated: boolean; size: number }> {
  const res = await fetch(`/api/fs/read?${new URLSearchParams({ path })}`)
  if (!res.ok) throw new Error(res.status === 415 ? '二进制文件无法预览' : `读取失败: ${res.status}`)
  return res.json()
}

export interface AppSettings {
  editor_command: string
  editor_command_default: string
  file_open_extensions: string
  timestamp_kinds: string
}

export async function fetchSettings(): Promise<AppSettings> {
  const res = await fetch('/api/settings')
  if (!res.ok) throw new Error(`Failed to fetch settings: ${res.status}`)
  return res.json()
}

export async function saveSettings(settings: { editor_command?: string; file_open_extensions?: string; timestamp_kinds?: string }): Promise<void> {
  const res = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  })
  if (!res.ok) throw new Error(`Failed to save settings: ${res.status}`)
}

export interface LLMModel {
  id: string
  label: string
  description?: string
}

export interface LLMProvider {
  id: number
  name: string
  kind: 'api' | 'acp'
  base_url: string
  has_api_key: boolean
  /** Extra HTTP headers for OpenAI-compatible API sources. */
  headers?: Record<string, string>
  agent: string
  model_id: string
  model_label: string
  is_default: boolean
  created_at: string
}

export interface LLMProviderInput {
  name: string
  kind: 'api' | 'acp'
  base_url?: string
  api_key?: string
  /** Extra HTTP headers (api kind only). Sent in full on every save. */
  headers?: Record<string, string>
  agent?: string
  model_id: string
  model_label?: string
}

export interface AIGeneration {
  id: number
  kind: string
  agent_type: string
  session_id: string
  provider_name: string
  model_id: string
  content: string
  // Kind-specific structured extras as a JSON string (handoff: difficulty +
  // recommended executors). Empty/absent when the model skipped it.
  metadata?: string
  created_at: string
}

// HandoffMetadata is the parsed shape of AIGeneration.metadata for handoff.
export interface HandoffMetadata {
  difficulty?: string
  difficulty_reason?: string
  recommended?: { executor: string; reason?: string }[]
}

export function parseHandoffMetadata(raw: string | undefined): HandoffMetadata | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as HandoffMetadata
    return typeof parsed === 'object' && parsed !== null ? parsed : null
  } catch {
    return null
  }
}

export type AIKind = 'summary' | 'title' | 'handoff'

export async function fetchLLMProviders(): Promise<{ providers: LLMProvider[]; acp_agents: string[] }> {
  const res = await fetch('/api/llm/providers')
  if (!res.ok) throw new Error(`获取模型源失败: ${res.status}`)
  return res.json()
}

export async function addLLMProvider(input: LLMProviderInput): Promise<number> {
  const res = await fetch('/api/llm/providers', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!res.ok) throw new Error(await res.text())
  const data = await res.json() as { id: number }
  return data.id
}

export async function updateLLMProvider(id: number, input: LLMProviderInput): Promise<void> {
  const res = await fetch(`/api/llm/providers/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!res.ok) throw new Error(await res.text())
}

export async function deleteLLMProvider(id: number): Promise<void> {
  const res = await fetch(`/api/llm/providers/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(await res.text())
}

export async function setDefaultLLMProvider(id: number): Promise<void> {
  const res = await fetch('/api/llm/providers/default', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider_id: id }),
  })
  if (!res.ok) throw new Error(await res.text())
}

// Validates a (possibly unsaved) provider config by fetching its model list.
// provider_id lets a saved provider refresh models without re-entering the
// key. ACP model lists are served from a backend TTL cache; force bypasses it.
// Listing a model does not prove generation will succeed.
export async function testLLMProvider(input: Partial<LLMProviderInput> & { provider_id?: number; force?: boolean }): Promise<LLMModel[]> {
  const res = await fetch('/api/llm/providers/test', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!res.ok) throw new Error(await res.text())
  const data = await res.json() as { models: LLMModel[] }
  return data.models
}

export async function fetchLatestGeneration(kind: AIKind, sessionId: string, agent: string): Promise<AIGeneration | null> {
  const params = new URLSearchParams({ agent })
  const res = await fetch(`/api/sessions/${sessionId}/ai/${kind}/latest?${params}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`获取生成记录失败: ${res.status}`)
  return res.json()
}

// Thrown when generation is attempted with no provider configured (HTTP 412):
// callers show the "去配置模型" guidance instead of a plain error.
export class NoProviderError extends Error {}

// Runs one generation over SSE (POST + streamed response body — EventSource
// can't POST, so the stream is parsed by hand). onStatus receives coarse
// stage strings ("启动适配器", "请求模型", ...). providerId 0/undefined means
// the server-side default provider.
export async function generateAI(
  sessionId: string,
  kind: AIKind,
  onStatus: (stage: string) => void,
  signal?: AbortSignal,
  providerId?: number,
): Promise<AIGeneration> {
  const res = await fetch(`/api/sessions/${sessionId}/ai/${kind}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(providerId ? { provider_id: providerId } : {}),
    signal,
  })
  if (res.status === 412) throw new NoProviderError(await res.text())
  if (!res.ok || !res.body) throw new Error(await res.text() || `生成失败: ${res.status}`)

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let result: AIGeneration | null = null
  for (;;) {
    const { done, value } = await reader.read()
    if (value) buf += decoder.decode(value, { stream: true })
    // SSE frames are separated by a blank line; parse every complete frame.
    for (let idx = buf.indexOf('\n\n'); idx >= 0; idx = buf.indexOf('\n\n')) {
      const frame = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      let event = ''
      let data = ''
      for (const line of frame.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7).trim()
        else if (line.startsWith('data: ')) data += line.slice(6)
      }
      if (!event || !data) continue
      if (event === 'status') onStatus((JSON.parse(data) as { stage: string }).stage)
      else if (event === 'error') throw new Error((JSON.parse(data) as { message: string }).message)
      else if (event === 'done') result = JSON.parse(data) as AIGeneration
    }
    if (done) break
  }
  if (!result) throw new Error('生成中断：服务未返回结果')
  return result
}

// ---- Deep Insight (原因洞察) ----

export interface InsightEvidenceRef {
  evidence_id: string
  kind: string
  statement: string
  turn_index?: number
}

export interface InsightItem {
  title: string
  finding_codes?: string[]
  confidence: 'high' | 'medium' | 'low'
  cause: { statement: string; epistemic_status: string; causal_strength: string; evidence_ids?: string[]; confounders?: string[] }
  impact: { statement: string; evidence_ids?: string[] }
  counter_evidence_ids?: string[]
  alternatives?: { statement: string; evidence_ids?: string[]; opposing_evidence_ids?: string[]; assessment: string }[]
  recommendations?: string[]
  caveats?: string[]
}

export interface InsightOutput {
  schema_version: number
  summary: string
  insights: InsightItem[]
  evidence_gaps?: string[]
}

// Parsed shape of an insight generation's metadata JSON.
export interface InsightMetadata {
  output?: InsightOutput
  cited_evidence?: InsightEvidenceRef[]
  evidence_gaps?: string[]
  warnings?: string[]
  parse_failed?: boolean
}

export interface InsightFreshness {
  stale: boolean
  reasons: string[]
  source_revision: number
  current_revision: number
  source_fingerprint: string
  prompt_version: string
}

export interface InsightResult {
  generation: AIGeneration
  freshness: InsightFreshness
}

// SendPreview is the pre-flight privacy disclosure before the first send to a
// model target; the caller shows it and re-runs with confirm=true on approval.
export interface SendPreview {
  needs_confirmation: true
  target_fingerprint: string
  target_label: string
  data_categories: string[]
  fact_count: number
  char_count: number
  truncated_count: number
  redacted_count: number
  note: string
}

export function parseInsightMetadata(raw: string | undefined): InsightMetadata | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as InsightMetadata
    return typeof parsed === 'object' && parsed !== null ? parsed : null
  } catch {
    return null
  }
}

export async function fetchLatestInsight(sessionId: string, agent: string): Promise<InsightResult | null> {
  const params = new URLSearchParams({ agent })
  const res = await fetch(`/api/sessions/${sessionId}/ai/insight/latest?${params}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`获取洞察失败: ${res.status}`)
  return res.json()
}

export async function revokeInsightTargets(): Promise<void> {
  const res = await fetch('/api/insight/targets/revoke', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  })
  if (!res.ok) throw new Error(await res.text())
}

// Reasons the server refuses to start an insight generation, surfaced so the
// UI can explain rather than show a raw status code.
export class InsightBlockedError extends Error {
  constructor(public reason: 'session_active' | 'session_changing' | 'no_findings' | 'not_found', message: string) {
    super(message)
  }
}

// generateInsight runs a Deep Insight generation. Without confirm it may return
// a SendPreview (first send to an unconfirmed target); with confirm=true it
// streams SSE and resolves to the generation plus its freshness. The 200 JSON
// preview and the SSE stream are distinguished by response Content-Type.
export async function generateInsight(
  sessionId: string,
  onStatus: (stage: string) => void,
  signal?: AbortSignal,
  providerId?: number,
  confirm?: boolean,
): Promise<InsightResult | SendPreview> {
  const res = await fetch(`/api/sessions/${sessionId}/ai/insight`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider_id: providerId || 0, confirm_target: !!confirm }),
    signal,
  })
  if (res.status === 412) throw new NoProviderError(await res.text())
  if (res.status === 404) throw new InsightBlockedError('not_found', '会话不存在')
  if (res.status === 409) {
    const t = (await res.text()).trim()
    throw new InsightBlockedError(t.includes('active') ? 'session_active' : 'session_changing', t)
  }
  if (res.status === 422) throw new InsightBlockedError('no_findings', '没有可分析的初步 Finding')
  if (!res.ok || !res.body) throw new Error(await res.text() || `分析失败: ${res.status}`)

  // A JSON body (not an event stream) is the send-confirmation preview.
  if ((res.headers.get('content-type') || '').includes('application/json')) {
    return res.json() as Promise<SendPreview>
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let result: InsightResult | null = null
  for (;;) {
    const { done, value } = await reader.read()
    if (value) buf += decoder.decode(value, { stream: true })
    for (let idx = buf.indexOf('\n\n'); idx >= 0; idx = buf.indexOf('\n\n')) {
      const frame = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      let event = ''
      let data = ''
      for (const line of frame.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7).trim()
        else if (line.startsWith('data: ')) data += line.slice(6)
      }
      if (!event || !data) continue
      if (event === 'status') onStatus((JSON.parse(data) as { stage: string }).stage)
      else if (event === 'error') throw new Error((JSON.parse(data) as { message: string }).message)
      else if (event === 'done') result = JSON.parse(data) as InsightResult
    }
    if (done) break
  }
  if (!result) throw new Error('分析中断：服务未返回结果')
  return result
}

export async function setSessionTitle(sessionId: string, agent: string, title: string): Promise<void> {
  const params = new URLSearchParams({ agent })
  const res = await fetch(`/api/sessions/${sessionId}/title?${params}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
  })
  if (!res.ok) throw new Error(await res.text())
}

export async function removeSessionTitle(sessionId: string, agent: string): Promise<void> {
  const params = new URLSearchParams({ agent })
  const res = await fetch(`/api/sessions/${sessionId}/title?${params}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(await res.text())
}

// Subscribe to the backend's sessions_changed SSE stream (fed by the file
// watcher). The event is a bare ping — callers refetch /api/sessions
// themselves. EventSource auto-reconnects, so a backend restart self-heals.
// Returns a disposer.
export function watchSessionsChanged(
  onChange: () => void,
  onConnectionChange?: (connected: boolean) => void,
): () => void {
  const es = new EventSource('/api/events')
  es.addEventListener('sessions_changed', onChange)
  // onopen 在首连和每次自动重连成功时触发，onerror 在断开/重试期间触发；
  // 调用方据此展示断连提示，并在重连后补拉断线期间可能错过的 ping。
  es.onopen = () => onConnectionChange?.(true)
  es.onerror = () => onConnectionChange?.(false)
  return () => es.close()
}

async function readJson<T>(res: Response, label: string): Promise<T> {
  const contentType = res.headers.get('content-type') || ''
  if (!contentType.includes('application/json')) {
    throw new Error(`Failed to fetch ${label}: expected JSON, got ${contentType || 'unknown content type'}`)
  }
  return res.json()
}
