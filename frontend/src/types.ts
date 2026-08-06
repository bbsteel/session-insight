/** Stable capability availability state (static or session-resolved). */
export type CapabilityState =
  | 'exact'
  | 'estimated'
  | 'missing'
  | 'not_applicable'
  | 'unsupported'

/** Baseline capability IDs (Phase 1–4 contract). */
export type CapabilityID =
  | 'discovery'
  | 'replay'
  | 'realtime'
  | 'tokens'
  | 'tool_results'
  | 'diff'
  | 'subtasks'
  | 'resume'
  | 'delete'
  | 'terminate'

/** One baseline capability declaration from GET /api/agents. */
export interface CapabilityDeclaration {
  state: CapabilityState
  reason_code?: string
  detail_key?: string
}

/** Resolved status of one capability for a single session (Phase 4). */
export interface SessionCapabilityStatus {
  state: CapabilityState
  reason_code?: string
  detail_key?: string
}

/** Advisory mutation eligibility (not a capability exact/missing overload). */
export type ActionAvailability =
  | 'available'
  | 'unavailable'
  | 'runtime_check_required'

export interface SessionActionStatus {
  availability: ActionAvailability
  reason_code?: string
}

/** Liveness quality separate from the realtime capability. */
export interface SessionLivenessStatus {
  is_live: boolean
  state: CapabilityState
  reason_code?: string
}

/** Nested payload under GET /api/sessions/{id} as agent_capabilities. */
export interface SessionCapabilities {
  agent_type: string
  adapter_revision: number
  status: Partial<Record<CapabilityID | string, SessionCapabilityStatus>>
  actions?: Partial<Record<CapabilityID | string, SessionActionStatus>>
  liveness: SessionLivenessStatus
}

export type TerminalBindingState = 'none' | 'launching' | 'active' | 'active_unknown' | 'stopped' | string

export interface SessionTerminalStatus {
  state: TerminalBindingState
  session_live: boolean
  liveness_state: CapabilityState
  terminal_id?: string
  terminal_name?: string
  instance_id?: string
  window_id?: string
  tab_id?: string
  terminal_pid?: number
  agent_pid?: number
  confidence: 'exact' | 'instance' | 'unknown' | string
  focusable: boolean
  launched_at?: string
  last_verified_at?: string
}

export interface ResumePlan {
  status: string
  agent_type: string
  session_id: string
  cwd: string
  command?: string
  supports_unsafe: boolean
  liveness: SessionLivenessStatus
  terminal: SessionTerminalStatus
}

export interface ResumeResult {
  launched: boolean
  status: string
  command: string
  terminal: SessionTerminalStatus
}

export interface TerminalFocusResult {
  tab_selected: boolean
  foreground: boolean
}

export interface AgentInfo {
  type: string
  display_name: string
  session_count: number
  live_count?: number
  /** Adapter-owned contract revision (not the Agent product version). */
  adapter_revision?: number
  /** Whether this Agent's storage was discovered on the local machine. */
  discovered?: boolean
  /** Whether the backend reader supports permanently deleting sessions of this agent. */
  can_delete?: boolean
  /** Whether SI can map a session to an exact PID and terminate it. */
  can_terminate?: boolean
  /** Adapter-owned capability map (stable IDs). Present from capability contract API. */
  capabilities?: Partial<Record<CapabilityID | string, CapabilityDeclaration>>
}

export interface SessionSummary {
  id: string
  agent_type: string
  name: string
  model_name: string
  model_provider?: string
  repository: string
  branch: string
  project: string
  cwd: string
  resume_id?: string
  /** Present only when the session source explicitly records the launching shell. */
  shell_kind?: 'powershell' | 'git-bash' | 'cmd' | 'wsl' | 'posix'
  turn_count: number
  historical_turn_count?: number
  rolled_back_turn_count?: number
  message_count: number
  is_live: boolean
  bookmarked: boolean
  bookmark_note?: string
  created_at: string
  updated_at: string
  /** Compact record completeness; omit paths. Absent = unavailable. */
  record_status?: RecordStatus
  /** True for sessions imported from a portable bundle (read-only copy). */
  imported?: boolean
  /** Hostname of the machine the bundle was exported from. */
  origin_host?: string
  /** Agent type on the origin machine (agent_type is the local imported id). */
  original_agent_type?: string
  /** Optional case label carried by the bundle. */
  case_label?: string
}

export interface TokenUsage {
  prompt_tokens: number
  completion_tokens: number
  reasoning_tokens?: number
  cache_read_tokens: number
  cache_write_tokens: number
  premium_requests: number
}

export interface SessionBillingSummary {
  precision: string
  billing_unit?: string
  billing_amount?: number
  totals: TokenUsage
}

export interface TurnVM {
  turn_index: number
  user_message: string
  assistant_message: string
  token_usage: TokenUsage
  request_count?: number
  tool_call_count: number
  error_count: number
  duration_ms: number
tool_names?: string[]
  tool_details?: { name: string; exit_code: number; duration_ms: number; error_kind?: string; error_message?: string; timed_out?: boolean; timeout_seconds?: number; rejected?: boolean; tool_kind?: string }[]
  subagents?: string[]
  skills?: string[]
  anomalies?: string[]
  rolled_back?: boolean
  original_turn_index?: number
}

export interface RollbackGroupVM {
  after_turn_index: number
  timestamp: string
  turns: TurnVM[]
}

export interface EditCall {
  turn_index: number
  file_path: string
  old_string: string
  new_string: string
  replace_all?: boolean
}

export interface SearchResult {
  session_id: string
  agent_type: string
  project: string
  name: string
  updated_at: string
  match: string
  /** True when hit comes from a source_missing tombstone (historical index). */
  source_missing?: boolean
  stale?: boolean
}

/** Mutually exclusive record completeness states (independent of capability). */
export type RecordCompletenessState =
  | 'complete'
  | 'degraded'
  | 'metadata_only'
  | 'source_missing'
  | 'parser_unsupported'

export type SourceFileState = 'present' | 'missing' | 'unreadable' | 'unsupported'

export interface SessionSourceFile {
  role: string
  path: string
  state: SourceFileState | string
  updated_at?: string
  size_bytes?: number
}

export interface ParseWarning {
  code: string
  severity: string
  affects_completeness: boolean
  impacts?: string[]
  count: number
  source_role?: string
  first_record?: number
}

export interface WarningSummary {
  total: number
  info?: number
  warning?: number
  error?: number
  impact_counts?: Record<string, number>
}

export interface SessionProvenance {
  state: RecordCompletenessState | string
  reason_code?: string
  captured_at: string
  source_updated_at?: string
  adapter_revision: number
  sources: SessionSourceFile[]
  warning_summary: WarningSummary
  warnings: ParseWarning[]
  last_successful_at?: string
  missing_since?: string
}

/** Compact list projection — no absolute paths. */
export interface RecordStatus {
  state: RecordCompletenessState | string
  warning_count: number
  captured_at: string
}

export interface MiniMapPosition {
  kind: 'turn' | 'user' | 'assistant' | 'error' | 'compaction' | 'edit' | 'fold' | 'trunc' | 'tool'
  position_key: string
  turn_index: number
  line_start: number
  line_end?: number
  label: string
  severity?: string
  payload?: Record<string, unknown>
}

export interface PositionsResponse {
  session_id: string
  agent_type: string
  revision: number
  cols: number
  total_lines: number
  positions: MiniMapPosition[]
}

/** Provenance of a session imported from a portable bundle (SessionDetail.import_info). */
export interface ImportInfo {
  original_agent_type: string
  original_session_id: string
  origin_host: string
  bundle_id: string
  case_label?: string
  redacted?: boolean
  imported_at: string
}

export interface SessionDetail {
  id: string
  agent_type: string
  name: string
  repository: string
  branch: string
  cwd: string
  turn_count: number
  message_count: number
  is_live: boolean
  bookmarked: boolean
  bookmark_note?: string
  created_at: string
  updated_at: string
  model_name: string
  turns: TurnVM[]
  historical_turn_count?: number
  rolled_back_turn_count?: number
  rollback_groups?: RollbackGroupVM[]
  todos?: { id: string; title: string; description: string; status: string; deps?: string[] }[]
  billing?: SessionBillingSummary
  /** Phase 4 resolved Agent capabilities for this session (optional for degrade path). */
  agent_capabilities?: SessionCapabilities
  resume_id?: string
  /** Independent record completeness / source inventory (v0.5.1). */
  provenance?: SessionProvenance
  /** False for metadata/source-missing envelopes without replayable body. */
  record_available?: boolean
  /** Present for sessions imported from a portable bundle. */
  import_info?: ImportInfo
}
