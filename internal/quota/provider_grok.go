package quota

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	grokCreditsEndpoint = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	grokBillingEndpoint = "https://cli-chat-proxy.grok.com/v1/billing"
	grokTokenEndpoint   = "https://auth.x.ai/oauth2/token"
)

type grokProvider struct {
	options    ProviderOptions
	definition ProviderDefinition
}

func NewGrokProvider(options ProviderOptions) QuotaProvider {
	return &grokProvider{
		options: normalizeProviderOptions(options),
		definition: ProviderDefinition{
			ID:                 ProviderGrok,
			DisplayNameKey:     "quota.provider.grok",
			DescriptionKey:     "quota.provider.grokDescription",
			DocumentationURL:   "https://docs.x.ai/docs",
			SupportsExactQuota: true,
		},
	}
}

func (p *grokProvider) Definition() ProviderDefinition {
	return p.definition
}

func (p *grokProvider) Fetch(ctx context.Context) QuotaSnapshot {
	authPath := homePath(p.options.HomeDir, ".grok", "auth.json")
	auth, err := readJSONMap(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return quotaFailure(StatusNotConfigured, "credentials_missing")
		}
		return quotaFailure(StatusInvalidData, "credentials_invalid")
	}
	authKey, credentials := selectGrokCredentials(auth)
	if credentials == nil {
		return quotaFailure(StatusNotConfigured, "access_token_missing")
	}
	if _, expired := expiresAt(credentials, p.options.Now()); expired {
		refreshToken := firstString(credentials, "refresh_token", "refreshToken")
		if refreshToken == "" {
			return quotaFailure(StatusUnauthorized, "token_expired")
		}
		refreshed, refreshErr := p.refreshAccessToken(ctx, authPath, authKey, auth, credentials, refreshToken)
		if refreshErr != nil {
			return quotaFailure(StatusUnauthorized, "token_refresh_failed")
		}
		credentials = refreshed
	}
	accessToken := firstString(credentials, "key", "access_token", "accessToken")
	if accessToken == "" {
		return quotaFailure(StatusNotConfigured, "access_token_missing")
	}
	clientVersion := p.clientVersion()
	headers := p.headers(accessToken, clientVersion)
	statusCode, payload, requestErr := requestJSON(ctx, p.options.HTTPClient, http.MethodGet, grokCreditsEndpoint, headers, nil)
	if statusCode == http.StatusUnauthorized {
		refreshToken := firstString(credentials, "refresh_token", "refreshToken")
		if refreshToken != "" {
			if refreshed, refreshErr := p.refreshAccessToken(ctx, authPath, authKey, auth, credentials, refreshToken); refreshErr == nil {
				credentials = refreshed
				accessToken = firstString(credentials, "key", "access_token", "accessToken")
				headers = p.headers(accessToken, clientVersion)
				statusCode, payload, requestErr = requestJSON(ctx, p.options.HTTPClient, http.MethodGet, grokCreditsEndpoint, headers, nil)
			}
		}
	}
	if statusCode != http.StatusOK {
		return httpFailure(statusCode, requestErr, "grok_credits")
	}
	if requestErr != nil {
		return httpFailure(statusCode, requestErr, "grok_credits")
	}
	credits, err := parseJSONObject(payload)
	if err != nil {
		return quotaFailure(StatusInvalidData, "credits_payload_invalid")
	}
	creditsConfig := mapField(credits, "config")
	if creditsConfig == nil {
		creditsConfig = credits
	}
	windows := make([]QuotaWindow, 0, 2)
	if weekly, ok := parseGrokWeeklyWindow(creditsConfig, p.options.Now()); ok {
		windows = append(windows, weekly)
	}

	monthlyStatus, monthlyPayload, monthlyErr := requestJSON(ctx, p.options.HTTPClient, http.MethodGet, grokBillingEndpoint, headers, nil)
	if monthlyStatus == http.StatusOK && monthlyErr == nil {
		if billing, parseErr := parseJSONObject(monthlyPayload); parseErr == nil {
			billingConfig := mapField(billing, "config")
			if billingConfig == nil {
				billingConfig = billing
			}
			if monthly, ok := parseGrokMonthlyWindow(billingConfig, p.options.Now()); ok {
				windows = append(windows, monthly)
			}
		}
	}
	if len(windows) == 0 {
		return quotaFailure(StatusInvalidData, "billing_windows_missing")
	}
	return QuotaSnapshot{
		Status:     StatusAvailable,
		Windows:    windows,
		SourceKind: "grok_billing_endpoint",
		PlanLabel:  firstString(creditsConfig, "plan", "plan_type", "planType"),
	}
}

func selectGrokCredentials(auth map[string]any) (string, map[string]any) {
	if firstString(auth, "key", "access_token", "accessToken") != "" {
		return "", auth
	}
	for key, value := range auth {
		credentials, ok := value.(map[string]any)
		if ok && firstString(credentials, "key", "access_token", "accessToken") != "" {
			return key, credentials
		}
	}
	return "", nil
}

func (p *grokProvider) headers(accessToken, clientVersion string) map[string]string {
	headers := bearerHeaders(accessToken)
	headers["x-grok-client-version"] = clientVersion
	headers["x-grok-client-mode"] = "cli"
	headers["User-Agent"] = "grok-cli/" + clientVersion
	return headers
}

func (p *grokProvider) clientVersion() string {
	versionPath := homePath(p.options.HomeDir, ".grok", "version.json")
	version, err := readJSONMap(versionPath)
	if err == nil {
		if value := firstString(version, "version"); value != "" {
			return value
		}
	}
	return "session-insight"
}

func (p *grokProvider) refreshAccessToken(ctx context.Context, authPath, authKey string, auth map[string]any, credentials map[string]any, refreshToken string) (map[string]any, error) {
	clientID := firstString(credentials, "oidc_client_id", "oidcClientId", "client_id", "clientId")
	if clientID == "" {
		return nil, errMissingClientID
	}
	statusCode, payload, requestErr := requestFormJSON(ctx, p.options.HTTPClient, grokTokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}, map[string]string{"User-Agent": "session-insight-quota/1"})
	if requestErr != nil || statusCode != http.StatusOK {
		if requestErr == nil {
			requestErr = unexpectedHTTPStatus(statusCode)
		}
		return nil, requestErr
	}
	refreshed, err := parseJSONObject(payload)
	if err != nil {
		return nil, err
	}
	newAccessToken := firstString(refreshed, "access_token", "accessToken")
	if newAccessToken == "" {
		return nil, errMissingAccessToken
	}
	updated := make(map[string]any, len(credentials)+3)
	for key, value := range credentials {
		updated[key] = value
	}
	updated["key"] = newAccessToken
	if refreshedToken := firstString(refreshed, "refresh_token", "refreshToken"); refreshedToken != "" {
		updated["refresh_token"] = refreshedToken
	}
	if expiresIn, ok, numberErr := numberField(refreshed, "expires_in", "expiresIn"); numberErr == nil && ok {
		updated["expires_at"] = p.options.Now().Add(timeDurationSeconds(expiresIn)).UTC().Format(time.RFC3339)
	}
	if p.options.PersistRefreshedCredentials {
		if authKey == "" {
			_ = writeJSONMapAtomic(authPath, updated)
		} else {
			updatedAuth := make(map[string]any, len(auth)+1)
			for key, value := range auth {
				updatedAuth[key] = value
			}
			updatedAuth[authKey] = updated
			_ = writeJSONMapAtomic(authPath, updatedAuth)
		}
	}
	return updated, nil
}

func parseGrokWeeklyWindow(object map[string]any, now time.Time) (QuotaWindow, bool) {
	usedPercent, hasUsedPercent, err := numberField(object, "creditUsagePercent", "credit_usage_percent")
	if err != nil {
		return QuotaWindow{}, false
	}
	period := mapField(object, "currentPeriod", "current_period")
	resetValue := ""
	if period != nil {
		resetValue = firstString(period, "end", "resetAt", "reset_at")
	}
	if resetValue == "" {
		resetValue = firstString(object, "billingPeriodEnd", "billing_period_end")
	}
	if !hasUsedPercent && resetValue == "" {
		return QuotaWindow{}, false
	}
	if !hasUsedPercent {
		usedPercent = 0
	}
	if usedPercent < 0 || usedPercent > 100 {
		return QuotaWindow{}, false
	}
	remainingPercent := 100 - usedPercent
	window := QuotaWindow{ID: "weekly", UsedPercent: &usedPercent, RemainingPercent: &remainingPercent, Unit: "percent"}
	if resetValue != "" {
		resetAt, parseErr := timestampValue(resetValue)
		if parseErr != nil {
			return QuotaWindow{}, false
		}
		window.ResetAt = &resetAt
	}
	return window, true
}

func parseGrokMonthlyWindow(object map[string]any, _ time.Time) (QuotaWindow, bool) {
	limit, hasLimit, limitErr := grokNumberField(object, "monthlyLimit", "monthly_limit")
	used, hasUsed, usedErr := grokNumberField(object, "used", "usedAmount", "used_amount")
	if limitErr != nil || usedErr != nil || !hasLimit || !hasUsed || limit <= 0 {
		return QuotaWindow{}, false
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	remainingPercent := clampPercent(100 * remaining / limit)
	usedPercent := 100 - remainingPercent
	window := QuotaWindow{
		ID:               "monthly",
		RemainingPercent: &remainingPercent,
		UsedPercent:      &usedPercent,
		RemainingAmount:  &remaining,
		UsedAmount:       &used,
		LimitAmount:      &limit,
		Unit:             firstString(object, "unit", "currency"),
	}
	if resetValue := firstString(object, "billingPeriodEnd", "billing_period_end"); resetValue != "" {
		resetAt, err := timestampValue(resetValue)
		if err != nil {
			return QuotaWindow{}, false
		}
		window.ResetAt = &resetAt
	}
	return window, true
}

func grokNumberField(object map[string]any, keys ...string) (float64, bool, error) {
	value, ok := firstValue(object, keys...)
	if !ok {
		return 0, false, nil
	}
	if nested, ok := value.(map[string]any); ok {
		value, ok = firstValue(nested, "val", "value")
		if !ok {
			return 0, false, nil
		}
	}
	number, err := numberValue(value)
	return number, true, err
}

var errMissingClientID = &missingClientIDError{}

type missingClientIDError struct{}

func (*missingClientIDError) Error() string { return "oauth credentials did not contain a client id" }
