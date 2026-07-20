// Package llm talks to user-configured model sources. Two kinds:
//
//   - "api": an OpenAI-compatible HTTP endpoint (DeepSeek, 通义, Kimi,
//     ollama, one-api, ...).
//   - "acp": a local agent CLI used as a model source. Claude / Codex /
//     Gemini are driven over the Agent Client Protocol (same path Zed uses).
//     Grok has no ACP server, so it runs headless (`grok models` /
//     `grok --prompt-file`). Model lists come from the CLI at runtime and
//     the chosen model is set explicitly — generation never relies on
//     whatever default the CLI happens to have.
package llm

import (
	"context"
	"fmt"
	"strings"
)

// Config identifies one model source plus the user's explicit model choice.
type Config struct {
	Kind    string // "api" | "acp"
	BaseURL string // api: endpoint base, e.g. https://api.deepseek.com/v1
	APIKey  string // api: bearer token
	// Headers are extra HTTP headers for kind="api" (e.g. OpenRouter Referer,
	// gateway api-key). Applied after Authorization so a custom Authorization
	// entry can override the Bearer derived from APIKey.
	Headers map[string]string
	Agent   string // acp: "claude" | "codex" | "gemini" | "grok"
	ModelID string // the explicitly selected model
}

// Model is one selectable model advertised by a provider.
type Model struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// ModelUnavailableError reports a saved ACP model choice that the agent no
// longer advertises. Callers can distinguish it from transport failures and
// offer a safe delete/reconfigure action without parsing the message text.
type ModelUnavailableError struct {
	ModelID   string
	Agent     string
	Available []string
}

func (e *ModelUnavailableError) Error() string {
	return fmt.Sprintf("模型「%s」已无法由 %s 使用；请刷新模型源并改选以下型号之一：%s",
		e.ModelID, LocalAgentLabel(e.Agent), strings.Join(e.Available, "、"))
}

// StatusFunc receives coarse progress stages ("下载适配器", "连接模型", ...)
// so the UI can show what a long-running generation is doing.
type StatusFunc func(stage string)

// Client is one live connection strategy for a provider kind.
type Client interface {
	// ListModels asks the provider what models it offers.
	ListModels(ctx context.Context) ([]Model, error)
	// Generate runs one prompt against cfg.ModelID and returns the full
	// text output. onStatus may be nil.
	Generate(ctx context.Context, prompt string, onStatus StatusFunc) (string, error)
}

// SystemPromptGenerator is an optional Client capability: run a generation with
// a separate immutable system instruction and an untrusted user payload. The
// API path implements a real system role; sources that cannot (ACP) are driven
// through Generate with a concatenated prompt instead, which the caller treats
// as a weaker boundary. onStatus may be nil.
type SystemPromptGenerator interface {
	GenerateWithSystem(ctx context.Context, system, user string, onStatus StatusFunc) (string, error)
}

// New returns the client for cfg.Kind.
func New(cfg Config) (Client, error) {
	switch cfg.Kind {
	case "api":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("api provider requires base_url")
		}
		return &openAIClient{cfg: cfg}, nil
	case "acp":
		if cfg.Agent == "" {
			return nil, fmt.Errorf("acp provider requires agent")
		}
		if AgentBinary(cfg.Agent) == "" {
			return nil, fmt.Errorf("unsupported acp agent %q", cfg.Agent)
		}
		// Grok: headless CLI (no ACP). Others: ACP stdio adapters.
		if cfg.Agent == "grok" {
			return &grokCLIClient{cfg: cfg}, nil
		}
		return &acpClient{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q", cfg.Kind)
	}
}
