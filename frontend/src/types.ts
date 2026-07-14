export interface AgentInfo {
  type: string
  display_name: string
  session_count: number
  live_count?: number
  /** Whether the backend reader supports permanently deleting sessions of this agent. */
  can_delete?: boolean
}

export interface SessionSummary {
  id: string
  agent_type: string
  name: string
  model_name: string
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
  created_at: string
  updated_at: string
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
}

export interface MiniMapPosition {
  kind: 'turn' | 'user' | 'error' | 'compaction' | 'edit' | 'fold' | 'trunc' | 'tool'
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
  created_at: string
  updated_at: string
  model_name: string
  turns: TurnVM[]
  historical_turn_count?: number
  rolled_back_turn_count?: number
  rollback_groups?: RollbackGroupVM[]
  todos?: { id: string; title: string; description: string; status: string; deps?: string[] }[]
  billing?: SessionBillingSummary
}
