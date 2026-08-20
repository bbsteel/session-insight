package quota

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	kimiUsageEndpoint = "https://api.kimi.com/coding/v1/usages"
	kimiTokenEndpoint = "https://auth.kimi.com/api/oauth/token"
	kimiClientID      = "17e5f671-d194-4dfb-9706-5516cb48c098"
)

type kimiProvider struct {
	options    ProviderOptions
	definition ProviderDefinition
}

func NewKimiProvider(options ProviderOptions) QuotaProvider {
	return &kimiProvider{
		options: normalizeProviderOptions(options),
		definition: ProviderDefinition{
			ID:                 ProviderKimi,
			DisplayNameKey:     "quota.provider.kimi",
			DescriptionKey:     "quota.provider.kimiDescription",
			DocumentationURL:   "https://www.kimi.com/code/docs/en/kimi-code/membership.html",
			SupportsExactQuota: true,
		},
	}
}

func (p *kimiProvider) Definition() ProviderDefinition {
	return p.definition
}

func (p *kimiProvider) Fetch(ctx context.Context) QuotaSnapshot {
	credentialPath, credentials, credentialErr := p.loadCredentials()
	if credentialErr != nil && !os.IsNotExist(credentialErr) {
		return quotaFailure(StatusInvalidData, "credentials_invalid")
	}
	accessToken := firstString(credentials, "access_token", "accessToken", "token")
	if accessToken == "" {
		accessToken = strings.TrimSpace(os.Getenv("KIMI_CODING_API_KEY"))
		if accessToken == "" {
			accessToken = strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
		}
	}
	if accessToken == "" {
		refreshToken := firstString(credentials, "refresh_token", "refreshToken")
		if refreshToken != "" {
			refreshed, refreshErr := p.refreshAccessToken(ctx, credentialPath, credentials, refreshToken)
			if refreshErr == nil {
				credentials = refreshed
				accessToken = firstString(credentials, "access_token", "accessToken")
			}
		}
		if accessToken == "" {
			return quotaFailure(StatusNotConfigured, "credentials_missing")
		}
	}
	if _, expired := expiresAt(credentials, p.options.Now()); expired {
		refreshToken := firstString(credentials, "refresh_token", "refreshToken")
		if refreshToken == "" {
			return quotaFailure(StatusUnauthorized, "token_expired")
		}
		refreshed, refreshErr := p.refreshAccessToken(ctx, credentialPath, credentials, refreshToken)
		if refreshErr != nil {
			return quotaFailure(StatusUnauthorized, "token_refresh_failed")
		}
		credentials = refreshed
		accessToken = firstString(credentials, "access_token", "accessToken")
	}

	statusCode, payload, requestErr := p.fetchUsage(ctx, accessToken)
	if statusCode == http.StatusUnauthorized {
		refreshToken := firstString(credentials, "refresh_token", "refreshToken")
		if refreshToken != "" {
			if refreshed, refreshErr := p.refreshAccessToken(ctx, credentialPath, credentials, refreshToken); refreshErr == nil {
				credentials = refreshed
				accessToken = firstString(credentials, "access_token", "accessToken")
				statusCode, payload, requestErr = p.fetchUsage(ctx, accessToken)
			}
		}
	}
	if statusCode != http.StatusOK {
		return httpFailure(statusCode, requestErr, "kimi_usage")
	}
	if requestErr != nil {
		return httpFailure(statusCode, requestErr, "kimi_usage")
	}
	object, err := parseJSONObject(payload)
	if err != nil {
		return quotaFailure(StatusInvalidData, "usage_payload_invalid")
	}
	windows := parseKimiWindows(object, p.options.Now())
	if len(windows) == 0 {
		return quotaFailure(StatusInvalidData, "usage_windows_missing")
	}
	return QuotaSnapshot{
		Status:     StatusAvailable,
		Windows:    windows,
		SourceKind: "kimi_code_usage_endpoint",
		PlanLabel:  firstString(object, "plan", "plan_type", "planType"),
	}
}

func (p *kimiProvider) loadCredentials() (string, map[string]any, error) {
	for _, path := range []string{
		homePath(p.options.HomeDir, ".kimi-code", "credentials", "kimi-code.json"),
		homePath(p.options.HomeDir, ".kimi", "credentials", "kimi-code.json"),
	} {
		credentials, err := readJSONMap(path)
		if err == nil {
			return path, credentials, nil
		}
		if !os.IsNotExist(err) {
			return path, nil, err
		}
	}
	return "", nil, os.ErrNotExist
}

func (p *kimiProvider) fetchUsage(ctx context.Context, accessToken string) (int, []byte, error) {
	headers := bearerHeaders(accessToken)
	headers["User-Agent"] = "kimi-cli"
	headers["X-Msh-Platform"] = "kimi_cli"
	return requestJSON(ctx, p.options.HTTPClient, http.MethodGet, kimiUsageEndpoint, headers, nil)
}

func (p *kimiProvider) refreshAccessToken(ctx context.Context, credentialPath string, credentials map[string]any, refreshToken string) (map[string]any, error) {
	statusCode, payload, requestErr := requestFormJSON(ctx, p.options.HTTPClient, kimiTokenEndpoint, url.Values{
		"client_id":     {kimiClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, map[string]string{
		"User-Agent":      "kimi-cli",
		"X-Msh-Platform":  "kimi_cli",
		"X-Msh-Version":   "session-insight",
		"X-Msh-Device-Id": kimiDeviceID(p.options.HomeDir),
	})
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
	updated["access_token"] = newAccessToken
	if refreshedToken := firstString(refreshed, "refresh_token", "refreshToken"); refreshedToken != "" {
		updated["refresh_token"] = refreshedToken
	}
	if expiresIn, ok, numberErr := numberField(refreshed, "expires_in", "expiresIn"); numberErr == nil && ok {
		updated["expires_at"] = p.options.Now().Add(timeDurationSeconds(expiresIn)).Unix()
	}
	if p.options.PersistRefreshedCredentials && credentialPath != "" {
		_ = writeJSONMapAtomic(credentialPath, updated)
	}
	return updated, nil
}

func kimiDeviceID(homeDir string) string {
	for _, path := range []string{
		homePath(homeDir, ".kimi-code", "device_id"),
		homePath(homeDir, ".kimi", "device_id"),
	} {
		if content, err := os.ReadFile(path); err == nil {
			if value := strings.TrimSpace(string(content)); value != "" {
				return value
			}
		}
	}
	return "session-insight"
}

func parseKimiWindows(root map[string]any, now time.Time) []QuotaWindow {
	data := mapField(root, "data")
	if data == nil {
		data = root
	}
	windows := make([]QuotaWindow, 0, 4)
	if usage := mapField(data, "usage"); usage != nil {
		if window, ok := parseKimiWindow(usage, "usage", now); ok {
			windows = append(windows, window)
		}
	}
	for _, limitsKey := range []string{"limits", "windows"} {
		rawLimits, ok := data[limitsKey].([]any)
		if !ok {
			continue
		}
		for index, rawLimit := range rawLimits {
			limit, ok := rawLimit.(map[string]any)
			if !ok {
				continue
			}
			detail := mapField(limit, "detail", "window")
			if detail == nil {
				detail = limit
			}
			windowID := firstString(limit, "id", "name", "type", "window_type")
			if windowID == "" {
				windowID = firstString(detail, "id", "name", "type", "window_type")
			}
			if windowID == "" {
				windowID = "limit_" + strconv.Itoa(index)
			}
			if window, ok := parseKimiWindow(detail, windowID, now); ok {
				windows = append(windows, window)
			}
		}
	}
	return windows
}

func parseKimiWindow(object map[string]any, id string, now time.Time) (QuotaWindow, bool) {
	window, hasValue, err := ParseJSONWindowObject(object, JSONWindowRule{
		ID:                    id,
		RemainingFractionPath: firstExistingKey(object, "remaining_fraction", "remainingFraction", "remaining_ratio", "remainingRatio"),
		UsedAmountPath:        firstExistingKey(object, "used", "used_amount", "usedAmount"),
		RemainingAmountPath:   firstExistingKey(object, "remaining", "remaining_amount", "remainingAmount"),
		LimitAmountPath:       firstExistingKey(object, "limit", "limit_amount", "limitAmount"),
		ResetAtPath:           firstExistingKey(object, "resetTime", "reset_time", "resetAt", "reset_at"),
		ResetAfterSecondsPath: firstExistingKey(object, "reset_in", "resetIn", "ttl"),
		Unit:                  firstString(object, "unit", "type", "window_type"),
	}, now)
	if err != nil || !hasValue {
		return QuotaWindow{}, false
	}
	return window, window.RemainingPercent != nil || window.RemainingAmount != nil || window.UsedAmount != nil
}

func timeDurationSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
