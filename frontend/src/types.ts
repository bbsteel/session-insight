export interface SessionSummary {
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
}
