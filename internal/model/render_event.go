package model

import "time"

type RenderEvent struct {
	EventID       string `json:"event_id"`
	ParentEventID string `json:"parent_event_id,omitempty"`
	StreamID      string `json:"stream_id,omitempty"`
	Seq           int    `json:"seq,omitempty"`
	Depth         int    `json:"depth"`

	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	TurnIndex int       `json:"turn_index"`

	Text     string `json:"text,omitempty"`
	Language string `json:"language,omitempty"`

	ToolName   string         `json:"tool_name,omitempty"`
	ToolInput  map[string]any `json:"tool_input,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`

	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`

	TokenUsage *RenderTokenUsage `json:"token_usage,omitempty"`

	Subtype string         `json:"subtype,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`

	Model     string         `json:"model,omitempty"`
	AgentType string         `json:"agent_type,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type RenderTokenUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
}
