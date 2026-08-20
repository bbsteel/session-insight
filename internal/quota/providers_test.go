package quota

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type quotaRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip quotaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func quotaJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func quotaTestOptions(homeDir string, roundTrip quotaRoundTripFunc) ProviderOptions {
	return ProviderOptions{
		HomeDir: homeDir,
		HTTPClient: &http.Client{
			Transport: roundTrip,
		},
		Now: func() time.Time {
			return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
		},
	}
}

func writeQuotaCredential(t *testing.T, homeDir string, relativePath string, content string) {
	t.Helper()
	path := filepath.Join(homeDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexProviderNormalizesRateLimitWindows(t *testing.T) {
	homeDir := t.TempDir()
	writeQuotaCredential(t, homeDir, ".codex/auth.json", `{"tokens":{"access_token":"codex-token","account_id":"account"}}`)
	options := quotaTestOptions(homeDir, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != codexUsageEndpoint {
			t.Fatalf("unexpected endpoint %s", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer codex-token" {
			t.Fatalf("missing bearer token")
		}
		if request.Header.Get("chatgpt-account-id") != "account" {
			t.Fatalf("missing account id")
		}
		return quotaJSONResponse(http.StatusOK, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_after_seconds":3600},"secondary_window":{"used_percent":80,"reset_at":"2026-08-21T00:00:00Z"}}}`), nil
	})

	snapshot := NewCodexProvider(options).Fetch(context.Background())
	if snapshot.Status != StatusAvailable || len(snapshot.Windows) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if got := *snapshot.Windows[0].RemainingPercent; got != 75 {
		t.Fatalf("primary remaining percent = %v, want 75", got)
	}
	if got := *snapshot.Windows[0].ResetAt; !got.Equal(options.Now().Add(time.Hour)) {
		t.Fatalf("primary reset = %s", got)
	}
	if snapshot.PlanLabel != "pro" {
		t.Fatalf("plan label = %q", snapshot.PlanLabel)
	}
}

func TestClaudeProviderParsesSubscriptionWindows(t *testing.T) {
	homeDir := t.TempDir()
	writeQuotaCredential(t, homeDir, ".claude/.credentials.json", `{"claudeAiOauth":{"accessToken":"claude-token"}}`)
	options := quotaTestOptions(homeDir, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != claudeUsageEndpoint {
			t.Fatalf("unexpected endpoint %s", request.URL)
		}
		if request.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Fatalf("missing Claude OAuth beta header")
		}
		return quotaJSONResponse(http.StatusOK, `{"five_hour":{"utilization":12.5,"resets_at":"2026-08-20T17:00:00Z"},"seven_day":{"utilization":40}}`), nil
	})

	snapshot := NewClaudeProvider(options).Fetch(context.Background())
	if snapshot.Status != StatusAvailable || len(snapshot.Windows) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if got := *snapshot.Windows[0].RemainingPercent; got != 87.5 {
		t.Fatalf("remaining percent = %v, want 87.5", got)
	}
}

func TestGeminiProviderParsesQuotaBuckets(t *testing.T) {
	homeDir := t.TempDir()
	writeQuotaCredential(t, homeDir, ".gemini/oauth_creds.json", `{"access_token":"gemini-token","project_id":"test-project"}`)
	options := quotaTestOptions(homeDir, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != geminiQuotaEndpoint {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		return quotaJSONResponse(http.StatusOK, `{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":0.62,"remainingAmount":"620000","resetTime":"2026-08-20T18:00:00Z","tokenType":"TOKENS"}]}`), nil
	})

	snapshot := NewGeminiProvider(options).Fetch(context.Background())
	if snapshot.Status != StatusAvailable || len(snapshot.Windows) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	window := snapshot.Windows[0]
	if window.ID != "gemini-2.5-pro" || *window.RemainingPercent != 62 || *window.RemainingAmount != 620000 {
		t.Fatalf("unexpected Gemini window: %+v", window)
	}
}

func TestKimiProviderParsesUsageAndLimitWindows(t *testing.T) {
	homeDir := t.TempDir()
	writeQuotaCredential(t, homeDir, ".kimi-code/credentials/kimi-code.json", `{"access_token":"kimi-token"}`)
	options := quotaTestOptions(homeDir, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != kimiUsageEndpoint {
			t.Fatalf("unexpected endpoint %s", request.URL)
		}
		return quotaJSONResponse(http.StatusOK, `{"data":{"usage":{"limit":100,"remaining":40,"reset_in":1800},"limits":[{"type":"weekly","detail":{"limit":1000,"used":250,"resetAt":"2026-08-24T00:00:00Z"}}]}}`), nil
	})

	snapshot := NewKimiProvider(options).Fetch(context.Background())
	if snapshot.Status != StatusAvailable || len(snapshot.Windows) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if got := *snapshot.Windows[0].RemainingPercent; got != 40 {
		t.Fatalf("usage remaining percent = %v, want 40", got)
	}
	if snapshot.Windows[0].Unit != "percent" {
		t.Fatalf("usage unit = %q, want percent", snapshot.Windows[0].Unit)
	}
	if got := *snapshot.Windows[1].RemainingPercent; got != 75 {
		t.Fatalf("limit remaining percent = %v, want 75", got)
	}
	if snapshot.Windows[1].Unit != "" {
		t.Fatalf("limit unit = %q, want empty", snapshot.Windows[1].Unit)
	}
}

func TestGrokProviderParsesWeeklyAndMonthlyBilling(t *testing.T) {
	homeDir := t.TempDir()
	writeQuotaCredential(t, homeDir, ".grok/auth.json", `{"default":{"key":"grok-token"}}`)
	options := quotaTestOptions(homeDir, func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case grokCreditsEndpoint:
			return quotaJSONResponse(http.StatusOK, `{"config":{"creditUsagePercent":25,"currentPeriod":{"end":"2026-08-27T00:00:00Z"}}}`), nil
		case grokBillingEndpoint:
			return quotaJSONResponse(http.StatusOK, `{"config":{"monthlyLimit":{"val":"100"},"used":{"val":"30"},"billingPeriodEnd":"2026-09-01T00:00:00Z"}}`), nil
		default:
			t.Fatalf("unexpected endpoint %s", request.URL)
			return nil, nil
		}
	})

	snapshot := NewGrokProvider(options).Fetch(context.Background())
	if snapshot.Status != StatusAvailable || len(snapshot.Windows) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if got := *snapshot.Windows[0].RemainingPercent; got != 75 {
		t.Fatalf("weekly remaining percent = %v, want 75", got)
	}
	if got := *snapshot.Windows[1].RemainingPercent; got != 70 {
		t.Fatalf("monthly remaining percent = %v, want 70", got)
	}
}

func TestManagerKeepsLastGoodWindowsWhenRefreshFails(t *testing.T) {
	var fetchCount atomic.Int32
	provider := &testQuotaProvider{
		definition: ProviderDefinition{ID: ProviderCodex, SupportsExactQuota: true},
		fetch: func(context.Context) QuotaSnapshot {
			if fetchCount.Add(1) == 1 {
				remaining := 80.0
				return QuotaSnapshot{Status: StatusAvailable, Windows: []QuotaWindow{{ID: "primary", RemainingPercent: &remaining}}}
			}
			return quotaFailure(StatusRateLimited, "rate_limited")
		},
	}
	manager := NewManager([]QuotaProvider{provider}, ManagerOptions{
		SuccessTTL: time.Hour,
		ErrorTTL:   time.Minute,
	})

	first := manager.Fetch(context.Background(), true)[0]
	second := manager.Fetch(context.Background(), true)[0]
	third := manager.Fetch(context.Background(), false)[0]
	if first.Status != StatusAvailable || second.Status != StatusRateLimited || !second.Stale {
		t.Fatalf("unexpected refresh snapshots: %+v / %+v", first, second)
	}
	if len(second.Windows) != 1 || *second.Windows[0].RemainingPercent != 80 {
		t.Fatalf("stale window was not retained: %+v", second)
	}
	if third.Status != StatusRateLimited || fetchCount.Load() != 2 {
		t.Fatalf("error cache was not used: %+v, fetches=%d", third, fetchCount.Load())
	}
}

func TestUnsupportedProviderDoesNotAttemptNetworkAccess(t *testing.T) {
	provider := newUnsupportedProvider(ProviderQwen, "name", "description", "https://example.com", "quota_endpoint_unavailable")
	snapshot := provider.Fetch(context.Background())
	if snapshot.Status != StatusUnsupported || snapshot.ReasonCode != "quota_endpoint_unavailable" {
		t.Fatalf("unexpected unsupported snapshot: %+v", snapshot)
	}
}

type testQuotaProvider struct {
	definition ProviderDefinition
	fetch      func(context.Context) QuotaSnapshot
}

func (provider *testQuotaProvider) Definition() ProviderDefinition {
	return provider.definition
}

func (provider *testQuotaProvider) Fetch(ctx context.Context) QuotaSnapshot {
	return provider.fetch(ctx)
}
