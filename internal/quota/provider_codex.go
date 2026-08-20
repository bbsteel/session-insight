package quota

import (
	"context"
	"net/http"
	"os"
	"strings"
)

const codexUsageEndpoint = "https://chatgpt.com/backend-api/wham/usage"

type codexProvider struct {
	options    ProviderOptions
	definition ProviderDefinition
}

func NewCodexProvider(options ProviderOptions) QuotaProvider {
	return &codexProvider{
		options: normalizeProviderOptions(options),
		definition: ProviderDefinition{
			ID:                 ProviderCodex,
			DisplayNameKey:     "quota.provider.codex",
			DescriptionKey:     "quota.provider.codexDescription",
			DocumentationURL:   "https://help.openai.com/en/articles/20001106-codex-rate-card",
			QuotaURL:           codexUsageEndpoint,
			SupportsExactQuota: true,
		},
	}
}

func (p *codexProvider) Definition() ProviderDefinition {
	return p.definition
}

func (p *codexProvider) Fetch(ctx context.Context) QuotaSnapshot {
	credentialPath := homePath(p.options.HomeDir, ".codex", "auth.json")
	auth, err := readJSONMap(credentialPath)
	if err != nil {
		if os.IsNotExist(err) {
			return quotaFailure(StatusNotConfigured, "credentials_missing")
		}
		return quotaFailure(StatusInvalidData, "credentials_invalid")
	}
	tokens := mapField(auth, "tokens")
	accessToken := firstString(tokens, "access_token", "accessToken")
	if accessToken == "" {
		accessToken = firstString(auth, "access_token", "accessToken")
	}
	if accessToken == "" {
		return quotaFailure(StatusNotConfigured, "access_token_missing")
	}
	headers := bearerHeaders(accessToken)
	headers["Content-Type"] = "application/json"
	headers["User-Agent"] = "codex-cli"
	if accountID := firstString(tokens, "account_id", "accountId"); accountID != "" {
		headers["chatgpt-account-id"] = accountID
	}
	statusCode, payload, requestErr := requestJSON(ctx, p.options.HTTPClient, http.MethodGet, codexUsageEndpoint, headers, nil)
	if statusCode != http.StatusOK {
		return httpFailure(statusCode, requestErr, "codex_usage")
	}
	if requestErr != nil {
		return httpFailure(statusCode, requestErr, "codex_usage")
	}
	object, err := parseJSONObject(payload)
	if err != nil {
		return quotaFailure(StatusInvalidData, "usage_payload_invalid")
	}
	rateLimit := mapField(object, "rate_limit", "rateLimit")
	if rateLimit == nil {
		rateLimit = object
	}
	windows := make([]QuotaWindow, 0, 2)
	for _, definition := range []struct {
		id   string
		keys []string
	}{
		{id: "primary", keys: []string{"primary_window", "primaryWindow", "primary"}},
		{id: "secondary", keys: []string{"secondary_window", "secondaryWindow", "secondary"}},
	} {
		windowObject := mapField(rateLimit, definition.keys...)
		if windowObject == nil {
			continue
		}
		windowSecondsKey := firstExistingKey(windowObject, "window_seconds", "windowSeconds")
		windowMinutesKey := firstExistingKey(windowObject, "window_minutes", "windowMinutes")
		window, ok, parseErr := ParseJSONWindowObject(windowObject, JSONWindowRule{
			ID:                    definition.id,
			UsedPercentPath:       firstExistingKey(windowObject, "used_percent", "usedPercent"),
			ResetAtPath:           firstExistingKey(windowObject, "reset_at", "resetAt"),
			ResetAfterSecondsPath: firstExistingKey(windowObject, "reset_after_seconds", "resetAfterSeconds"),
			WindowSecondsPath:     firstExistingKey(windowObject, "window_seconds", "windowSeconds", "window_minutes", "windowMinutes"),
			Unit:                  "percent",
		}, p.options.Now())
		if parseErr != nil {
			return quotaFailure(StatusInvalidData, "usage_window_invalid")
		}
		if ok && (window.RemainingPercent != nil || window.UsedPercent != nil) {
			if window.WindowSeconds != nil && windowSecondsKey == "" && windowMinutesKey != "" {
				seconds := *window.WindowSeconds * 60
				window.WindowSeconds = &seconds
			}
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return quotaFailure(StatusInvalidData, "usage_windows_missing")
	}
	return QuotaSnapshot{
		Status:     StatusAvailable,
		Windows:    windows,
		SourceKind: "codex_usage_endpoint",
		PlanLabel:  strings.TrimSpace(firstString(object, "plan_type", "planType", "plan")),
	}
}

func firstExistingKey(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if _, ok := object[key]; ok {
			return key
		}
	}
	return ""
}
