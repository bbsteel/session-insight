package analytics

import (
	"strings"
	"testing"

	"session-insight/internal/model"
)

func TestFindingsAmplificationAndConcentration(t *testing.T) {
	exact := model.TokenPresence{Output: model.PresenceExact}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "a", RequestCount: 5, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
			{TurnIndex: 1, UserMessage: "b", RequestCount: 5, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
			{TurnIndex: 2, UserMessage: "c", RequestCount: 260, ToolCallCount: 400, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
		},
		Billing: &model.SessionBilling{
			Precision:     model.PrecisionExact,
			BillingUnit:   "aiu",
			BillingAmount: 1000,
			Totals:        model.TokenUsage{PromptTokens: 1, Present: model.TokenPresence{Input: model.PresenceExact}},
		},
	}

	res := Compute(detail)
	byTitle := map[string]Finding{}
	for _, f := range res.Findings {
		byTitle[f.Title] = f
	}

	amp, ok := byTitle["指令放大 90 倍"]
	if !ok || amp.Severity != "critical" {
		t.Errorf("expected critical amplification finding, got %+v", res.Findings)
	}

	conc, ok := byTitle["消耗集中在 Turn 2"]
	if !ok || conc.TurnIndex == nil || *conc.TurnIndex != 2 {
		t.Fatalf("expected concentration finding on turn 2, got %+v", res.Findings)
	}
	if !strings.Contains(conc.Detail, "估算") {
		t.Errorf("cost-based concentration must be labelled 估算: %s", conc.Detail)
	}

	loop, ok := byTitle["Turn 2 疑似长工具循环"]
	if !ok || loop.Severity != "critical" {
		t.Errorf("expected critical tool-loop finding, got %+v", res.Findings)
	}

	// critical findings must sort first
	if len(res.Findings) > 0 && res.Findings[0].Severity != "critical" {
		t.Errorf("findings not sorted by severity: %+v", res.Findings)
	}
}

func TestFindingsContextReplay(t *testing.T) {
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "a", RequestCount: 2}},
		Billing: &model.SessionBilling{
			Precision:   model.PrecisionExact,
			BillingUnit: "aiu",
			Totals: model.TokenUsage{
				PromptTokens:    100_000,
				CacheReadTokens: 17_000_000,
				Present:         model.TokenPresence{Input: model.PresenceExact, CacheRead: model.PresenceExact},
			},
		},
	}

	res := Compute(detail)
	found := false
	for _, f := range res.Findings {
		if f.Title == "长上下文反复重放" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected context-replay finding, got %+v", res.Findings)
	}
}

func TestFindingsQuietOnHealthySession(t *testing.T) {
	exact := model.TokenPresence{Input: model.PresenceExact, Output: model.PresenceExact}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "a", RequestCount: 3, ToolCallCount: 4, TokenUsage: model.TokenUsage{PromptTokens: 100, CompletionTokens: 50, Present: exact}},
			{TurnIndex: 1, UserMessage: "b", RequestCount: 2, ToolCallCount: 2, TokenUsage: model.TokenUsage{PromptTokens: 120, CompletionTokens: 60, Present: exact}},
			{TurnIndex: 2, UserMessage: "c", RequestCount: 4, ToolCallCount: 3, TokenUsage: model.TokenUsage{PromptTokens: 110, CompletionTokens: 40, Present: exact}},
		},
	}

	res := Compute(detail)
	if len(res.Findings) != 0 {
		t.Errorf("healthy session must produce no findings, got %+v", res.Findings)
	}
}
