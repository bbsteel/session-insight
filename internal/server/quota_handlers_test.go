package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/quota"
)

type quotaHandlerTestProvider struct{}

func (quotaHandlerTestProvider) Definition() quota.ProviderDefinition {
	return quota.ProviderDefinition{
		ID:                 quota.ProviderCodex,
		DisplayNameKey:     "quota.provider.codex",
		DescriptionKey:     "quota.provider.codexDescription",
		QuotaStrategyKey:   "quota.provider.codexStrategy",
		DocumentationURL:   "https://example.com/docs",
		SupportsExactQuota: true,
	}
}

func (quotaHandlerTestProvider) Fetch(context.Context) quota.QuotaSnapshot {
	remainingPercent := 73.0
	return quota.QuotaSnapshot{
		Status:  quota.StatusAvailable,
		Windows: []quota.QuotaWindow{{ID: "primary", RemainingPercent: &remainingPercent}},
	}
}

func TestCodingQuotaAPIReturnsDefinitionsAndSafeSnapshots(t *testing.T) {
	server := New(nil, nil)
	server.SetCodingQuotaManager(quota.NewManager([]quota.QuotaProvider{quotaHandlerTestProvider{}}, quota.ManagerOptions{}))

	request := httptest.NewRequest(http.MethodGet, "/api/coding-quotas", nil)
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Providers []struct {
			ID               string `json:"id"`
			QuotaStrategyKey string `json:"quota_strategy_key"`
			Snapshot         struct {
				Status  string `json:"status"`
				Windows []struct {
					RemainingPercent float64 `json:"remaining_percent"`
				} `json:"windows"`
			} `json:"snapshot"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Providers) != 1 || payload.Providers[0].ID != "codex" {
		t.Fatalf("unexpected providers: %+v", payload.Providers)
	}
	if payload.Providers[0].QuotaStrategyKey != "quota.provider.codexStrategy" {
		t.Fatalf("unexpected quota strategy key: %q", payload.Providers[0].QuotaStrategyKey)
	}
	if strings.Contains(response.Body.String(), "quota_url") {
		t.Fatalf("removed quota query URL leaked into response: %s", response.Body.String())
	}
	if payload.Providers[0].Snapshot.Status != string(quota.StatusAvailable) || payload.Providers[0].Snapshot.Windows[0].RemainingPercent != 73 {
		t.Fatalf("unexpected snapshot: %+v", payload.Providers[0].Snapshot)
	}
	if body := response.Body.String(); body == "" || containsQuotaSecret(body) {
		t.Fatalf("unsafe response body: %s", body)
	}
}

func containsQuotaSecret(body string) bool {
	for _, secret := range []string{"access_token", "Authorization", "refresh_token"} {
		if strings.Contains(body, secret) {
			return true
		}
	}
	return false
}
