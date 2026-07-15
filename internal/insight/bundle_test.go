package insight

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/analytics"
	"github.com/bbsteel/session-insight/internal/model"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// mkDetail builds a session that trips several findings so the bundle has
// findings, referenced turns and gaps to exercise.
func mkDetail() *model.SessionDetail {
	exact := model.TokenPresence{Output: model.PresenceExact}
	turns := []model.TurnVM{
		{TurnIndex: 0, UserMessage: "实现功能", RequestCount: 5, AssistantMessage: "开始实现", TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
		{TurnIndex: 1, UserMessage: "继续", RequestCount: 5, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: exact}},
		{TurnIndex: 2, UserMessage: "修好并发问题", RequestCount: 260, ToolCallCount: 400, AssistantMessage: "修复完成",
			TokenUsage:  model.TokenUsage{CompletionTokens: 10, Present: exact},
			ToolDetails: []model.ToolCallVM{{Name: "bash", ExitCode: 1, ErrorMessage: "race detected"}}},
	}
	return &model.SessionDetail{
		Session: model.Session{AgentType: "copilot", ModelName: "gpt-5"},
		Turns:   turns,
		Billing: &model.SessionBilling{
			Precision: model.PrecisionExact, BillingUnit: "aiu", BillingAmount: 12.5,
			Totals: model.TokenUsage{PromptTokens: 1, Present: model.TokenPresence{Input: model.PresenceExact}},
		},
	}
}

func TestBundleReferentialIntegrity(t *testing.T) {
	detail := mkDetail()
	res := analytics.Compute(detail)
	if len(res.Findings) == 0 {
		t.Fatal("test fixture must produce findings")
	}

	b := BuildBundle(detail, res, nil)

	factIDs := map[string]bool{}
	for _, f := range b.Facts {
		factIDs[f.ID] = true
		if f.Source == "" {
			t.Errorf("fact %s missing Source", f.ID)
		}
	}
	// Every finding evidence ref of kind session/turn must resolve to a fact.
	for _, fb := range b.Findings {
		for _, ref := range fb.EvidenceRefs {
			if ref.Kind == "session" || ref.Kind == "turn" {
				if !factIDs[ref.ID] {
					t.Errorf("finding %s references %s but no fact exposes it", fb.Code, ref.ID)
				}
			}
		}
	}
	// Detail text must NOT leak into the bundle — only Code/Metrics/refs.
	if strings.Contains(mustJSON(t, b), "点击跳转") {
		t.Error("finding Detail prose leaked into the bundle")
	}
	// session:summary must always exist.
	if !factIDs["session:summary"] {
		t.Error("bundle missing mandatory session:summary fact")
	}
}

func TestBundleDeclaresSubagentGap(t *testing.T) {
	detail := mkDetail()
	// Add 10 subagents so the fan-out finding fires, but provide no reader
	// evidence: the bundle must declare the gap, not fabricate detail.
	detail.Turns[2].Subagents = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	res := analytics.Compute(detail)

	b := BuildBundle(detail, res, nil)
	found := false
	for _, g := range b.EvidenceGaps {
		if strings.Contains(g, "subagent") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a subagent evidence gap, got %v", b.EvidenceGaps)
	}
}

func TestBundleUsesReaderEvidence(t *testing.T) {
	detail := mkDetail()
	detail.Turns[2].Subagents = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	res := analytics.Compute(detail)

	ev := &model.InsightEvidence{
		Subagents: []model.SubagentEvidence{
			{ToolCallID: "call-1", TurnIndex: 2, Name: "reviewer", Model: "gpt-5", Description: "review concurrency", Mode: "sync", RequestCount: 3, OutputTokens: 900},
		},
	}
	b := BuildBundle(detail, res, ev)

	var sub *EvidenceFact
	for i := range b.Facts {
		if b.Facts[i].ID == "subagent:call-1" {
			sub = &b.Facts[i]
		}
	}
	if sub == nil {
		t.Fatalf("expected subagent:call-1 fact, got %v", factIDsOf(b))
	}
	if sub.Kind != "subagent" || !strings.Contains(sub.Statement, "reviewer") {
		t.Errorf("subagent fact malformed: %+v", sub)
	}
	// With reader evidence present, the generic gap must not appear.
	for _, g := range b.EvidenceGaps {
		if strings.Contains(g, "未提供 subagent 深层证据") {
			t.Errorf("gap should be gone when reader evidence exists: %v", b.EvidenceGaps)
		}
	}
}

func TestBundleTurnBudgetElision(t *testing.T) {
	detail := mkDetail()
	// Turn 2 (400 tool calls) trips the tool-loop finding, so turn:2 is a
	// mandatory referenced fact. Pad the session with many filler turns whose
	// summed cost blows a small budget, forcing older non-referenced turns to
	// be elided while turn:2 survives.
	for i := 3; i < 40; i++ {
		detail.Turns = append(detail.Turns, model.TurnVM{
			TurnIndex:    i,
			UserMessage:  strings.Repeat("填充说明 ", 20),
			RequestCount: 1,
		})
	}
	res := analytics.Compute(detail)

	b := buildBundleWithBudget(detail, res, nil, 900)
	ids := factIDsOf(b)
	if !ids["turn:2"] {
		t.Error("referenced turn:2 must survive budget elision")
	}
	elisionGap := false
	for _, g := range b.EvidenceGaps {
		if strings.Contains(g, "预算裁剪") {
			elisionGap = true
		}
	}
	if !elisionGap {
		t.Errorf("expected an elision gap when turns are dropped, got %v", b.EvidenceGaps)
	}
}

func factIDsOf(b Bundle) map[string]bool {
	out := map[string]bool{}
	for _, f := range b.Facts {
		out[f.ID] = true
	}
	return out
}
