package model

import "time"

// SessionRevision returns a monotonic revision number for cache invalidation.
// First version uses UpdatedAt.UnixNano(); all callers must use this helper
// so the implementation can be swapped without touching API contracts.
func SessionRevision(s Session) int64 {
	return s.UpdatedAt.UnixNano()
}

// LiveWindow is how recently a session must have been active to count as
// "live" (活跃中). This is a presence heuristic — "recently active", not a
// literal "process is running now" — so a single uniform window is applied to
// every agent regardless of how it records activity.
const LiveWindow = 5 * time.Minute

// IsSessionLive reports whether a session counts as live, based purely on how
// long ago it was last active. Must be evaluated at serve time (relative to
// now), never stored, so liveness decays correctly as a session goes idle.
func IsSessionLive(updatedAt time.Time) bool {
	return time.Since(updatedAt) < LiveWindow
}

type Session struct {
	ID           string    `json:"id"`
	AgentType    string    `json:"agent_type"`
	CWD          string    `json:"cwd"`
	Repository   string    `json:"repository"`
	Branch       string    `json:"branch"`
	Project      string    `json:"project"`
	Name         string    `json:"name"`
	ModelName    string    `json:"model_name"`
	PreviewText  string    `json:"preview_text"`
	TurnCount    int       `json:"turn_count"`
	MessageCount int       `json:"message_count"`
	IsLive    bool   `json:"is_live"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	PremiumRequests  int   `json:"premium_requests"`
}

type Turn struct {
	TurnIndex        int        `json:"turn_index"`
	UserMessage      string     `json:"user_message"`
	AssistantMessage string     `json:"assistant_message"`
	TokenUsage       TokenUsage `json:"token_usage"`
	ToolCallCount    int        `json:"tool_call_count"`
	ErrorCount       int        `json:"error_count"`
	DurationMs       int64      `json:"duration_ms"`
}

type EventVM struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

type ToolCallVM struct {
	Name     string `json:"name"`
	ExitCode int    `json:"exit_code"`
	Duration int64  `json:"duration_ms"`
}
type TurnVM struct {
	TurnIndex        int        `json:"turn_index"`
	UserMessage      string     `json:"user_message"`
	AssistantMessage string     `json:"assistant_message"`
	TokenUsage       TokenUsage `json:"token_usage"`
	ToolCallCount    int        `json:"tool_call_count"`
	ErrorCount       int        `json:"error_count"`
	DurationMs       int64      `json:"duration_ms"`
	Events           []EventVM  `json:"events,omitempty"`
	Anomalies        []string   `json:"anomalies,omitempty"`
	ToolNames        []string   `json:"tool_names,omitempty"`
		Subagents        []string   `json:"subagents,omitempty"`
		ToolDetails      []ToolCallVM `json:"tool_details,omitempty"`
		Skills           []string   `json:"skills,omitempty"`
}

type EditCall struct {
	TurnIndex  int    `json:"turn_index"`
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type AnomalySummary struct {
	ToolFailures    int  `json:"tool_failures"`
	DurationSpikes  int  `json:"duration_spikes"`
	MissingShutdown bool `json:"missing_shutdown"`
	TotalAnomalies  int  `json:"total_anomalies"`
}

type SessionDetail struct {
	Session
	Turns          []TurnVM       `json:"turns"`
	AnomalySummary AnomalySummary `json:"anomaly_summary"`
	Todos          []Todo         `json:"todos,omitempty"`
}

type Todo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Deps        []string `json:"deps,omitempty"`
}
