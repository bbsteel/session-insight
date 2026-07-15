package analytics

import (
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
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

// TestFindingsContractStable checks the machine-facing contract the insight
// layer keys on: every finding carries a stable Code, its Metrics hold the
// raw numbers the rule computed, and EvidenceRefs point at resolvable IDs.
func TestFindingsContractStable(t *testing.T) {
	exact := model.TokenPresence{Output: model.PresenceExact}
	subTurn := model.TurnVM{TurnIndex: 3, UserMessage: "d", RequestCount: 1, Subagents: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "a", RequestCount: 5, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
			{TurnIndex: 1, UserMessage: "b", RequestCount: 5, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
			{TurnIndex: 2, UserMessage: "c", RequestCount: 260, ToolCallCount: 400, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
			subTurn,
		},
		Billing: &model.SessionBilling{
			Precision: model.PrecisionExact, BillingUnit: "aiu", BillingAmount: 1000,
			Totals: model.TokenUsage{PromptTokens: 1, Present: model.TokenPresence{Input: model.PresenceExact}},
		},
	}

	res := Compute(detail)
	byCode := map[string]Finding{}
	for _, f := range res.Findings {
		if f.Code == "" {
			t.Errorf("finding without Code: %+v", f)
		}
		byCode[f.Code] = f
	}

	amp, ok := byCode[CodeInstructionAmplification]
	if !ok {
		t.Fatalf("missing instruction_amplification, got codes %v", codesOf(res.Findings))
	}
	// 4 user messages driving 5+5+260+1=271 requests → factor 67.
	if amp.Metrics["factor"] != 67 {
		t.Errorf("amplification factor metric = %v, want 67", amp.Metrics["factor"])
	}
	if len(amp.EvidenceRefs) == 0 || amp.EvidenceRefs[0].Kind != "session" {
		t.Errorf("amplification must carry a session evidence ref: %+v", amp.EvidenceRefs)
	}

	conc, ok := byCode[CodeCostConcentration]
	if !ok || len(conc.EvidenceRefs) == 0 {
		t.Fatalf("missing cost_concentration with refs, got %+v", conc)
	}
	if r := conc.EvidenceRefs[0]; r.ID != "turn:2" || r.TurnIndex == nil || *r.TurnIndex != 2 {
		t.Errorf("concentration evidence must point at turn:2, got %+v", r)
	}

	fan, ok := byCode[CodeSubagentFanout]
	if !ok {
		t.Fatalf("missing subagent_fanout")
	}
	if fan.Metrics["subagent_count"] != 11 {
		t.Errorf("subagent_count metric = %v, want 11", fan.Metrics["subagent_count"])
	}
	// One session ref + one per subagent-spawning turn.
	if len(fan.EvidenceRefs) != 2 {
		t.Errorf("subagent_fanout refs = %d, want 2 (session + turn 3)", len(fan.EvidenceRefs))
	}
}

func codesOf(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return out
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

func TestFindingsContinuationNudge(t *testing.T) {
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "实现功能 X", RequestCount: 2},
			{TurnIndex: 1, UserMessage: "继续", RequestCount: 2, Anomalies: []string{"continuation_nudge"}},
			{TurnIndex: 2, UserMessage: "ok", RequestCount: 2, Anomalies: []string{"continuation_nudge"}},
		},
	}

	res := Compute(detail)
	var nudge *Finding
	for i, f := range res.Findings {
		if f.Title == "需要人工续跑 2 次" {
			nudge = &res.Findings[i]
		}
	}
	if nudge == nil {
		t.Fatalf("expected continuation-nudge finding, got %+v", res.Findings)
	}
	if nudge.Severity != "warn" {
		t.Errorf("nudge finding severity = %s, want warn", nudge.Severity)
	}
	if nudge.TurnIndex == nil || *nudge.TurnIndex != 1 {
		t.Errorf("nudge finding must point at first nudge turn 1, got %+v", nudge.TurnIndex)
	}
}

func TestFindingsSingleNudgeIsQuiet(t *testing.T) {
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, UserMessage: "实现功能 X", RequestCount: 2},
			{TurnIndex: 1, UserMessage: "继续", RequestCount: 2, Anomalies: []string{"continuation_nudge"}},
		},
	}

	res := Compute(detail)
	for _, f := range res.Findings {
		if f.Title == "需要人工续跑 1 次" {
			t.Errorf("single nudge must not produce a finding: %+v", f)
		}
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
