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
