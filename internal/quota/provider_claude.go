package quota

import (
	"context"
	"net/http"
	"os"
)

const claudeUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"

type claudeProvider struct {
	options    ProviderOptions
	definition ProviderDefinition
}

func NewClaudeProvider(options ProviderOptions) QuotaProvider {
	return &claudeProvider{
		options: normalizeProviderOptions(options),
		definition: ProviderDefinition{
			ID:                 ProviderClaude,
			DisplayNameKey:     "quota.provider.claude",
			DescriptionKey:     "quota.provider.claudeDescription",
			DocumentationURL:   "https://docs.anthropic.com/en/docs/claude-code",
			SupportsExactQuota: true,
		},
	}
}

func (p *claudeProvider) Definition() ProviderDefinition {
	return p.definition
}

func (p *claudeProvider) Fetch(ctx context.Context) QuotaSnapshot {
	credentialPath := ""
	for _, candidate := range []string{
		homePath(p.options.HomeDir, ".claude", ".credentials.json"),
		homePath(p.options.HomeDir, ".config", "claude", ".credentials.json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			credentialPath = candidate
			break
		}
	}
	if credentialPath == "" {
		return quotaFailure(StatusNotConfigured, "credentials_missing")
	}
	credentials, err := readJSONMap(credentialPath)
	if err != nil {
		return quotaFailure(StatusInvalidData, "credentials_invalid")
	}
	oauth := mapField(credentials, "claudeAiOauth", "claude_ai_oauth", "oauth")
	if oauth == nil {
		oauth = credentials
	}
	accessToken := firstString(oauth, "accessToken", "access_token")
	if accessToken == "" {
		return quotaFailure(StatusNotConfigured, "access_token_missing")
	}
	statusCode, payload, requestErr := requestJSON(ctx, p.options.HTTPClient, http.MethodGet, claudeUsageEndpoint, map[string]string{
		"Accept":         "application/json",
		"Authorization":  "Bearer " + accessToken,
		"anthropic-beta": "oauth-2025-04-20",
		"User-Agent":     "session-insight-quota/1",
	}, nil)
	if statusCode != http.StatusOK {
		return httpFailure(statusCode, requestErr, "claude_usage")
	}
	if requestErr != nil {
		return httpFailure(statusCode, requestErr, "claude_usage")
	}
	object, err := parseJSONObject(payload)
	if err != nil {
		return quotaFailure(StatusInvalidData, "usage_payload_invalid")
	}
	usage := mapField(object, "usage")
	if usage == nil {
		usage = object
	}
	windows := make([]QuotaWindow, 0, 4)
	for _, windowDefinition := range []struct {
		id   string
		keys []string
	}{
		{id: "five_hour", keys: []string{"five_hour", "fiveHour"}},
		{id: "seven_day", keys: []string{"seven_day", "sevenDay"}},
		{id: "seven_day_sonnet", keys: []string{"seven_day_sonnet", "sevenDaySonnet"}},
		{id: "seven_day_opus", keys: []string{"seven_day_opus", "sevenDayOpus"}},
		{id: "seven_day_cowork", keys: []string{"seven_day_cowork", "sevenDayCowork"}},
	} {
		windowObject := mapField(usage, windowDefinition.keys...)
		if windowObject == nil {
			continue
		}
		window, ok, parseErr := ParseJSONWindowObject(windowObject, JSONWindowRule{
			ID:              windowDefinition.id,
			UsedPercentPath: firstExistingKey(windowObject, "utilization", "used_percent", "usedPercent"),
			ResetAtPath:     firstExistingKey(windowObject, "resets_at", "reset_at", "resetAt"),
			Unit:            "percent",
		}, p.options.Now())
		if parseErr != nil {
			return quotaFailure(StatusInvalidData, "usage_window_invalid")
		}
		if ok && window.UsedPercent != nil {
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return quotaFailure(StatusInvalidData, "usage_windows_missing")
	}
	return QuotaSnapshot{
		Status:     StatusAvailable,
		Windows:    windows,
		SourceKind: "claude_oauth_usage_endpoint",
		PlanLabel:  firstString(object, "plan", "plan_type", "planType"),
	}
}
