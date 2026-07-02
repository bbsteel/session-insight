export interface AgentInfo {
  type: string
  display_name: string
  session_count: number
  live_count?: number
}

export interface SessionSummary {
  id: string
  agent_type: string
  name: string
  repository: string
  branch: string
  project: string
  cwd: string
  resume_id?: string
  preview_text: string
  turn_count: number
  message_count: number
  is_live: boolean
  created_at: string
  updated_at: string
}

export interface TokenUsage {
  prompt_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  premium_requests: number
}

export interface TurnVM {
  turn_index: number
  user_message: string
  assistant_message: string
  token_usage: TokenUsage
  tool_call_count: number
  error_count: number
  duration_ms: number
tool_names?: string[]
  tool_details?: { name: string; exit_code: number; duration_ms: number }[]
  subagents?: string[]
  skills?: string[]
  anomalies?: string[]
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
  kind: 'turn' | 'user' | 'error' | 'compaction' | 'edit'
  position_key: string
  turn_index: number
  line_start: number
  line_end?: number
  label: string
  severity?: string
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
  turn_count: number
  message_count: number
  is_live: boolean
  created_at: string
  updated_at: string
  model_name: string
  turns: TurnVM[]
  todos?: { id: string; title: string; description: string; status: string; deps?: string[] }[]
}
