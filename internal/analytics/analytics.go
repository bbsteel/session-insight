// Package analytics computes session-level insight from the unified data
// model. Everything here is agent-agnostic: it consumes model types only and
// must never import a reader. Agent-specific knowledge (field semantics,
// billing sources) belongs behind the reader boundary; this package reacts
// only to what the data declares about itself (presence, precision).
package analytics

import (
	"strings"

	"session-insight/internal/model"
)

type TurnToken struct {
	TurnIndex  int   `json:"turn_index"`
	Tokens     int64 `json:"tokens"`
	Duration   int64 `json:"duration_ms"`
	ToolCount  int   `json:"tool_count"`
	ErrorCount int   `json:"error_count"`
}

// Result keeps the JSON contract previously served inline by the API layer,
// extended with the session bill.
type Result struct {
	TotalTokens      int64                 `json:"total_tokens"`
	PromptTokens     int64                 `json:"prompt_tokens"`
	CompletionTokens int64                 `json:"completion_tokens"`
	CacheReadTokens  int64                 `json:"cache_read_tokens"`
	CacheHitRate     float64               `json:"cache_hit_rate"`
	TotalTools       int                   `json:"total_tools"`
	TotalErrors      int                   `json:"total_errors"`
	AnomalyCount     int                   `json:"anomaly_count"`
	HealthScore      int                   `json:"health_score"`
	HealthGrade      string                `json:"health_grade"`
	TurnCount        int                   `json:"turn_count"`
	TokenEfficiency  float64               `json:"token_efficiency"`
	Timeline         []TurnToken           `json:"timeline"`
	ToolFreq         map[string]int        `json:"tool_freq"`
	ToolSuccess      map[string]int        `json:"tool_success"`
	ToolTotal        map[string]int        `json:"tool_total"`
	SkillFreq        map[string]int        `json:"skill_freq"`
	TodoCount        int                   `json:"todo_count"`
	Todos            []model.Todo          `json:"todos"`
	TodoDone         int                   `json:"todo_done"`
	ContextWindow    int                   `json:"context_window"`
	ContextPeak      int64                 `json:"context_peak"`
	PressurePct      float64               `json:"pressure_pct"`
	Billing          *model.SessionBilling `json:"billing,omitempty"`
}

// Compute derives all session analytics from a SessionDetail.
func Compute(detail *model.SessionDetail) Result {
	var turnTotals model.TokenUsage
	var maxCumulative, cumul int64
	var totalTools, totalErrors int
	timeline := make([]TurnToken, 0, len(detail.Turns))
	toolFreq := make(map[string]int)
	toolSuccess := make(map[string]int)
	toolTotal := make(map[string]int)
	skillFreq := make(map[string]int)

	for _, t := range detail.Turns {
		turnTotals.AddUsage(t.TokenUsage)
		tok := t.TokenUsage.PromptTokens + t.TokenUsage.CompletionTokens
		totalTools += t.ToolCallCount
		totalErrors += t.ErrorCount
		cumul += tok
		if cumul > maxCumulative {
			maxCumulative = cumul
		}

		timeline = append(timeline, TurnToken{
			TurnIndex:  t.TurnIndex,
			Tokens:     tok,
			Duration:   t.DurationMs,
			ToolCount:  t.ToolCallCount,
			ErrorCount: t.ErrorCount,
		})

		for _, name := range t.ToolNames {
			toolFreq[name]++
		}
		for _, td := range t.ToolDetails {
			toolTotal[td.Name]++
			if td.ExitCode == 0 {
				toolSuccess[td.Name]++
			}
		}
		for _, name := range t.Skills {
			skillFreq[name]++
		}
	}

	billing := resolveBilling(detail, turnTotals)

	// Headline token numbers come from the bill when the agent reported one
	// (session-level aggregates are authoritative for agents like Copilot
	// whose per-turn data only covers output), otherwise from turn sums.
	headline := turnTotals
	if billing != nil && billing.Totals.Present.Input == model.PresenceExact {
		headline = billing.Totals
	}

	cacheRate := 0.0
	// A cache rate needs both sides of the fraction to be real data.
	if headline.Present.Input == model.PresenceExact && headline.Present.CacheRead == model.PresenceExact &&
		headline.PromptTokens+headline.CacheReadTokens > 0 {
		cacheRate = float64(headline.CacheReadTokens) / float64(headline.PromptTokens+headline.CacheReadTokens) * 100
	}

	pressurePct := 0.0
	ctxWindow := int64(estimateContext(detail.ModelName))
	if ctxWindow > 0 && maxCumulative > 0 {
		pressurePct = float64(maxCumulative) / float64(ctxWindow) * 100
	}

	healthScore := 100
	healthScore -= detail.AnomalySummary.ToolFailures * 5
	healthScore -= detail.AnomalySummary.DurationSpikes * 5
	if detail.AnomalySummary.MissingShutdown {
		healthScore -= 20
	}
	if healthScore < 0 {
		healthScore = 0
	}
	healthGrade := "A"
	switch {
	case healthScore > 90:
		healthGrade = "A"
	case healthScore > 75:
		healthGrade = "B"
	case healthScore > 60:
		healthGrade = "C"
	case healthScore > 40:
		healthGrade = "D"
	default:
		healthGrade = "F"
	}

	totalTokens := headline.PromptTokens + headline.CompletionTokens
	tokenEfficiency := 0.0
	if totalTokens > 0 && len(detail.Turns) > 0 {
		tokenEfficiency = float64(totalTokens) / float64(len(detail.Turns))
	}

	return Result{
		TotalTokens:      totalTokens,
		PromptTokens:     headline.PromptTokens,
		CompletionTokens: headline.CompletionTokens,
		CacheReadTokens:  headline.CacheReadTokens,
		CacheHitRate:     cacheRate,
		TotalTools:       totalTools,
		TotalErrors:      totalErrors,
		AnomalyCount:     detail.AnomalySummary.TotalAnomalies,
		HealthScore:      healthScore,
		HealthGrade:      healthGrade,
		TurnCount:        len(detail.Turns),
		TokenEfficiency:  tokenEfficiency,
		Timeline:         timeline,
		ToolFreq:         toolFreq,
		ToolSuccess:      toolSuccess,
		ToolTotal:        toolTotal,
		SkillFreq:        skillFreq,
		TodoCount:        len(detail.Todos),
		Todos:            detail.Todos,
		TodoDone:         countDone(detail.Todos),
		ContextWindow:    estimateContext(detail.ModelName),
		ContextPeak:      maxCumulative,
		PressurePct:      pressurePct,
		Billing:          billing,
	}
}

// resolveBilling prefers the reader-provided bill. When the reader gave none
// but turns carry exact per-turn usage (Claude/Codex), the turn sums form an
// exact bill with no billed unit. A reader-declared "missing" bill (e.g.
// Copilot session killed before session.shutdown) passes through untouched so
// the UI can say so instead of showing zeros.
func resolveBilling(detail *model.SessionDetail, turnTotals model.TokenUsage) *model.SessionBilling {
	if detail.Billing != nil {
		return detail.Billing
	}
	if turnTotals.Present.Input == model.PresenceExact {
		return &model.SessionBilling{
			Precision: model.PrecisionExact,
			Totals:    turnTotals,
		}
	}
	return nil
}

func countDone(todos []model.Todo) int {
	n := 0
	for _, t := range todos {
		if t.Status == "done" {
			n++
		}
	}
	return n
}

func estimateContext(modelName string) int {
	switch {
	case strings.Contains(modelName, "gpt-5"):
		return 256000
	case strings.Contains(modelName, "gpt-4"):
		return 128000
	case strings.Contains(modelName, "claude"):
		return 200000
	case strings.Contains(modelName, "gemini"):
		return 1000000
	case strings.Contains(modelName, "deepseek"):
		return 131072
	default:
		return 128000
	}
}
