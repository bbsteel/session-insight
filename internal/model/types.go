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
	ResumeID     string    `json:"resume_id,omitempty"`
	PreviewText  string    `json:"preview_text"`
	TurnCount    int       `json:"turn_count"`
	MessageCount int       `json:"message_count"`
	IsLive       bool      `json:"is_live"`
	Bookmarked   bool      `json:"bookmarked"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Presence marks whether a token bucket was actually reported by the agent.
// Zero value ("") means the data is unavailable from the source; "exact" means
// the agent reported it; "n/a" means the concept does not exist for this agent
// (e.g. cache_write where the provider's cache is automatic and free).
// The distinction drives UI behavior: unavailable → estimation path with an
// "estimated" badge, n/a → render "—" and exclude from derived metrics.
type Presence string

const (
	PresenceMissing Presence = ""
	PresenceExact   Presence = "exact"
	PresenceNA      Presence = "n/a"
)

type TokenPresence struct {
	Input      Presence `json:"input,omitempty"`
	Output     Presence `json:"output,omitempty"`
	CacheRead  Presence `json:"cache_read,omitempty"`
	CacheWrite Presence `json:"cache_write,omitempty"`
	Reasoning  Presence `json:"reasoning,omitempty"`
}

// TokenUsage holds the canonical token buckets defined in
// docs/superpowers/specs/2026-07-05-usage-accounting-design.md.
// PromptTokens, CacheReadTokens and CacheWriteTokens are mutually exclusive;
// true total input = the sum of the three. ReasoningTokens is an
// informational subset of CompletionTokens and must never be added to it.
// Readers are responsible for converting each agent's native semantics
// (inclusive vs exclusive) into these buckets.
type TokenUsage struct {
	PromptTokens     int64         `json:"prompt_tokens"`
	CompletionTokens int64         `json:"completion_tokens"`
	ReasoningTokens  int64         `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int64         `json:"cache_read_tokens"`
	CacheWriteTokens int64         `json:"cache_write_tokens"`
	PremiumRequests  int           `json:"premium_requests"`
	Present          TokenPresence `json:"present"`
}

// ModelUsage is one model's share of a session bill.
type ModelUsage struct {
	Model         string     `json:"model"`
	Requests      int        `json:"requests"`
	BillingAmount float64    `json:"billing_amount,omitempty"`
	Usage         TokenUsage `json:"usage"`
}

// SessionBilling is the session-level bill in the agent's native billing
// unit ("aiu", "premium_requests", "usd"; empty when the agent has no billed
// unit). Precision is "exact" when the agent reported the bill, "estimated"
// when analytics derived it, "missing" when the source data is absent (e.g.
// a killed Copilot session never wrote session.shutdown).
type SessionBilling struct {
	Precision     string       `json:"precision"`
	BillingUnit   string       `json:"billing_unit,omitempty"`
	BillingAmount float64      `json:"billing_amount,omitempty"`
	Totals        TokenUsage   `json:"totals"`
	ByModel       []ModelUsage `json:"by_model,omitempty"`
}

const (
	PrecisionExact     = "exact"
	PrecisionEstimated = "estimated"
	PrecisionMissing   = "missing"
)

// AddUsage accumulates o into u, widening presence per bucket.
func (u *TokenUsage) AddUsage(o TokenUsage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.ReasoningTokens += o.ReasoningTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.PremiumRequests += o.PremiumRequests
	MergePresence(&u.Present, o.Present)
}

// MergePresence widens dst so a bucket counts as present when any
// contributing source reported it; "n/a" only sticks when no source has data.
func MergePresence(dst *TokenPresence, src TokenPresence) {
	pick := func(d *Presence, s Presence) {
		if s == PresenceExact || (*d == PresenceMissing && s != PresenceMissing) {
			*d = s
		}
	}
	pick(&dst.Input, src.Input)
	pick(&dst.Output, src.Output)
	pick(&dst.CacheRead, src.CacheRead)
	pick(&dst.CacheWrite, src.CacheWrite)
	pick(&dst.Reasoning, src.Reasoning)
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
	// RequestCount is the number of API responses observed in this turn.
	// It is the attribution weight for spreading a session-level bill onto
	// turns: each request replays the whole context, so request count is the
	// dominant cost driver for agents without per-turn billing data.
	RequestCount  int          `json:"request_count"`
	ToolCallCount int          `json:"tool_call_count"`
	ErrorCount    int          `json:"error_count"`
	DurationMs    int64        `json:"duration_ms"`
	Events        []EventVM    `json:"events,omitempty"`
	Anomalies     []string     `json:"anomalies,omitempty"`
	ToolNames     []string     `json:"tool_names,omitempty"`
	Subagents     []string     `json:"subagents,omitempty"`
	ToolDetails   []ToolCallVM `json:"tool_details,omitempty"`
	Skills        []string     `json:"skills,omitempty"`
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
	Turns          []TurnVM        `json:"turns"`
	AnomalySummary AnomalySummary  `json:"anomaly_summary"`
	Todos          []Todo          `json:"todos,omitempty"`
	Billing        *SessionBilling `json:"billing,omitempty"`
}

type Todo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Deps        []string `json:"deps,omitempty"`
}
