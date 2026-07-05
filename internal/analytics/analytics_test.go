package analytics

import (
	"testing"

	"session-insight/internal/model"
)

func copilotLikeDetail() *model.SessionDetail {
	return &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, TokenUsage: model.TokenUsage{
				CompletionTokens: 42,
				Present:          model.TokenPresence{Output: model.PresenceExact},
			}},
		},
	}
}

func TestComputeHeadlineFromBilling(t *testing.T) {
	detail := copilotLikeDetail()
	detail.Billing = &model.SessionBilling{
		Precision:     model.PrecisionExact,
		BillingUnit:   "aiu",
		BillingAmount: 1.5,
		Totals: model.TokenUsage{
			PromptTokens:     1000,
			CompletionTokens: 500,
			CacheReadTokens:  9000,
			Present: model.TokenPresence{
				Input:     model.PresenceExact,
				Output:    model.PresenceExact,
				CacheRead: model.PresenceExact,
			},
		},
	}

	res := Compute(detail)
	if res.PromptTokens != 1000 || res.CompletionTokens != 500 {
		t.Errorf("headline must come from the bill, got prompt=%d completion=%d", res.PromptTokens, res.CompletionTokens)
	}
	if res.CacheHitRate != 90.0 {
		t.Errorf("expected cache rate 90%% (9000/10000), got %v", res.CacheHitRate)
	}
	if res.Billing == nil || res.Billing.BillingAmount != 1.5 {
		t.Errorf("bill must pass through, got %+v", res.Billing)
	}
}

func TestComputeNoFakeCacheRateWhenInputMissing(t *testing.T) {
	detail := copilotLikeDetail()
	detail.Billing = &model.SessionBilling{Precision: model.PrecisionMissing}

	res := Compute(detail)
	if res.CacheHitRate != 0 {
		t.Errorf("cache rate must not be derived from missing buckets, got %v", res.CacheHitRate)
	}
	if res.CompletionTokens != 42 {
		t.Errorf("headline should fall back to turn sums, got %d", res.CompletionTokens)
	}
	if res.Billing == nil || res.Billing.Precision != model.PrecisionMissing {
		t.Errorf("missing bill must pass through, got %+v", res.Billing)
	}
}

func TestComputeAttributesCostByRequests(t *testing.T) {
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, RequestCount: 30, TokenUsage: model.TokenUsage{CompletionTokens: 10, Present: model.TokenPresence{Output: model.PresenceExact}}},
			{TurnIndex: 1, RequestCount: 10, TokenUsage: model.TokenUsage{CompletionTokens: 999, Present: model.TokenPresence{Output: model.PresenceExact}}},
		},
		Billing: &model.SessionBilling{
			Precision:     model.PrecisionExact,
			BillingUnit:   "aiu",
			BillingAmount: 100,
			Totals: model.TokenUsage{
				PromptTokens: 1, Present: model.TokenPresence{Input: model.PresenceExact},
			},
		},
	}

	res := Compute(detail)
	if res.CostPrecision != model.PrecisionEstimated {
		t.Fatalf("expected estimated cost precision, got %q", res.CostPrecision)
	}
	// Weighted by requests (30:10), not by tokens (10:999).
	if res.Timeline[0].EstCost != 75 || res.Timeline[1].EstCost != 25 {
		t.Errorf("cost attribution mismatch: %v / %v", res.Timeline[0].EstCost, res.Timeline[1].EstCost)
	}
	if res.Timeline[0].Requests != 30 {
		t.Errorf("timeline requests missing: %+v", res.Timeline[0])
	}
}

func TestComputeNoCostAttributionWithoutBilledAmount(t *testing.T) {
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, RequestCount: 3, TokenUsage: model.TokenUsage{
				PromptTokens: 10, CompletionTokens: 5,
				Present: model.TokenPresence{Input: model.PresenceExact, Output: model.PresenceExact},
			}},
		},
	}

	res := Compute(detail)
	// Claude-like sessions have exact tokens but no billed unit: nothing to
	// spread, so no estimated costs may appear.
	if res.CostPrecision != "" || res.Timeline[0].EstCost != 0 {
		t.Errorf("unexpected cost attribution: precision=%q est=%v", res.CostPrecision, res.Timeline[0].EstCost)
	}
}

func TestComputeBuildsBillingFromExactTurns(t *testing.T) {
	exact := model.TokenPresence{
		Input:     model.PresenceExact,
		Output:    model.PresenceExact,
		CacheRead: model.PresenceExact,
	}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{
			{TurnIndex: 0, TokenUsage: model.TokenUsage{PromptTokens: 100, CompletionTokens: 50, CacheReadTokens: 300, Present: exact}},
			{TurnIndex: 1, TokenUsage: model.TokenUsage{PromptTokens: 200, CompletionTokens: 70, CacheReadTokens: 500, Present: exact}},
		},
	}

	res := Compute(detail)
	if res.Billing == nil || res.Billing.Precision != model.PrecisionExact {
		t.Fatalf("expected exact bill built from per-turn usage, got %+v", res.Billing)
	}
	if res.Billing.Totals.PromptTokens != 300 || res.Billing.Totals.CacheReadTokens != 800 {
		t.Errorf("bill totals mismatch: %+v", res.Billing.Totals)
	}
	if res.CacheHitRate <= 0 {
		t.Errorf("expected real cache rate from exact turn data, got %v", res.CacheHitRate)
	}
}
