package model

// InsightEvidence is optional reader-specific evidence for Deep Insight. The
// unified SessionDetail loses agent-specific detail that causal analysis needs
// (e.g. Copilot keeps only a subagent's display name, not its delegation
// description, model, mode, parent request, or timing). A reader that can
// recover those facts implements reader.InsightEvidenceProvider to fill this;
// readers without it leave the bundle to declare an evidence gap.
//
// Readers only preserve facts here — they must not keyword-classify an English
// delegation description into "implement/review/fix" roles. Semantic role
// assignment is the model's job, made after it cites the raw evidence.
type InsightEvidence struct {
	ModelRequests []ModelRequestEvidence `json:"model_requests,omitempty"`
	Subagents     []SubagentEvidence     `json:"subagents,omitempty"`
	Skills        []SkillEvidence        `json:"skills,omitempty"`
	ToolEvents    []ToolEvidence         `json:"tool_events,omitempty"`
}

// SubagentEvidence is one delegated subagent, with the raw facts needed to
// reconstruct its responsibility and owning task. Prompt is a bounded excerpt
// or truncated original — never the full unbounded text.
type SubagentEvidence struct {
	ToolCallID   string `json:"tool_call_id"`
	TurnIndex    int    `json:"turn_index"`
	Name         string `json:"name,omitempty"`
	Model        string `json:"model,omitempty"`
	Description  string `json:"description,omitempty"`
	Mode         string `json:"mode,omitempty"` // e.g. "sync" | "async"
	Prompt       string `json:"prompt,omitempty"`
	PromptChars  int    `json:"prompt_chars,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	RequestCount int    `json:"request_count,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
	// OverlapsOther is true when this subagent's run window overlapped at least
	// one other subagent — evidence for genuine parallelism vs. serial role
	// switching.
	OverlapsOther bool `json:"overlaps_other,omitempty"`
}

// ModelRequestEvidence attributes one model response to its owning parent or
// subagent, with model, timing, output tokens and turn.
type ModelRequestEvidence struct {
	ID           string `json:"id"`
	TurnIndex    int    `json:"turn_index"`
	ParentID     string `json:"parent_id,omitempty"` // owning tool_call_id, "" = root
	SubagentName string `json:"subagent_name,omitempty"`
	Model        string `json:"model,omitempty"`
	Timestamp    string `json:"timestamp,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
}

// SkillEvidence is one skill invocation with its owning turn.
type SkillEvidence struct {
	Name      string `json:"name"`
	TurnIndex int    `json:"turn_index"`
}

// ToolEvidence is one tool call preserved for causal analysis (repeated
// failures, timeouts, rejections keyed to a turn).
type ToolEvidence struct {
	ToolCallID string `json:"tool_call_id,omitempty"`
	TurnIndex  int    `json:"turn_index"`
	Name       string `json:"name"`
	ExitCode   int    `json:"exit_code,omitempty"`
	ErrorKind  string `json:"error_kind,omitempty"`
	ErrorMsg   string `json:"error_message,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Rejected   bool   `json:"rejected,omitempty"`
}
