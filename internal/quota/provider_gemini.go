package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	geminiQuotaEndpoint = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	geminiTokenEndpoint = "https://oauth2.googleapis.com/token"
)

type geminiProvider struct {
	options    ProviderOptions
	definition ProviderDefinition
}

func NewGeminiProvider(options ProviderOptions) QuotaProvider {
	return &geminiProvider{
		options: normalizeProviderOptions(options),
		definition: ProviderDefinition{
			ID:                 ProviderGemini,
			DisplayNameKey:     "quota.provider.gemini",
			DescriptionKey:     "quota.provider.geminiDescription",
			DocumentationURL:   "https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/quota-and-pricing.md",
			QuotaURL:           geminiQuotaEndpoint,
			SupportsExactQuota: true,
		},
	}
}

func (p *geminiProvider) Definition() ProviderDefinition {
	return p.definition
}

func (p *geminiProvider) Fetch(ctx context.Context) QuotaSnapshot {
	credentialPath := homePath(p.options.HomeDir, ".gemini", "oauth_creds.json")
	credentials, credentialErr := readJSONMap(credentialPath)
	accessToken := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_ACCESS_TOKEN"))
	if credentialErr == nil {
		accessToken = firstString(credentials, "access_token", "accessToken")
		if accessToken == "" {
			return quotaFailure(StatusNotConfigured, "access_token_missing")
		}
		if _, expired := expiresAt(credentials, p.options.Now()); expired {
			refreshToken := firstString(credentials, "refresh_token", "refreshToken")
			if refreshToken == "" {
				return quotaFailure(StatusUnauthorized, "token_expired")
			}
			refreshed, refreshErr := p.refreshAccessToken(ctx, credentials, refreshToken)
			if refreshErr != nil {
				return quotaFailure(StatusUnauthorized, "token_refresh_failed")
			}
			credentials = refreshed
			accessToken = firstString(credentials, "access_token", "accessToken")
		}
	} else if !os.IsNotExist(credentialErr) {
		return quotaFailure(StatusInvalidData, "credentials_invalid")
	}
	if accessToken == "" {
		return quotaFailure(StatusNotConfigured, "credentials_missing")
	}
	projectID := p.projectID(credentials)
	if projectID == "" {
		return quotaFailure(StatusNotConfigured, "project_id_missing")
	}
	requestPayload, err := json.Marshal(map[string]string{
		"project":   projectID,
		"userAgent": "gemini-cli",
	})
	if err != nil {
		return quotaFailure(StatusInvalidData, "request_build_failed")
	}
	headers := bearerHeaders(accessToken)
	headers["Content-Type"] = "application/json"
	statusCode, payload, requestErr := requestJSON(ctx, p.options.HTTPClient, http.MethodPost, geminiQuotaEndpoint, headers, requestPayload)
	if statusCode != http.StatusOK {
		return httpFailure(statusCode, requestErr, "gemini_quota")
	}
	if requestErr != nil {
		return httpFailure(statusCode, requestErr, "gemini_quota")
	}
	object, err := parseJSONObject(payload)
	if err != nil {
		return quotaFailure(StatusInvalidData, "quota_payload_invalid")
	}
	rawBuckets, ok := object["buckets"].([]any)
	if !ok {
		return quotaFailure(StatusInvalidData, "quota_buckets_missing")
	}
	windows := make([]QuotaWindow, 0, len(rawBuckets))
	for index, rawBucket := range rawBuckets {
		bucket, ok := rawBucket.(map[string]any)
		if !ok {
			continue
		}
		modelID := firstString(bucket, "modelId", "model_id", "tokenType", "token_type")
		if modelID == "" {
			modelID = "bucket_" + strconv.Itoa(index)
		}
		window, hasValue, parseErr := ParseJSONWindowObject(bucket, JSONWindowRule{
			ID:                    modelID,
			RemainingFractionPath: firstExistingKey(bucket, "remainingFraction", "remaining_fraction"),
			RemainingAmountPath:   firstExistingKey(bucket, "remainingAmount", "remaining_amount"),
			ResetAtPath:           firstExistingKey(bucket, "resetTime", "reset_time", "resetAt", "reset_at"),
			Unit:                  firstString(bucket, "tokenType", "token_type"),
		}, p.options.Now())
		if parseErr != nil {
			return quotaFailure(StatusInvalidData, "quota_bucket_invalid")
		}
		if hasValue && (window.RemainingPercent != nil || window.RemainingAmount != nil) {
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return quotaFailure(StatusInvalidData, "quota_windows_missing")
	}
	return QuotaSnapshot{
		Status:     StatusAvailable,
		Windows:    windows,
		SourceKind: "gemini_code_assist_quota_endpoint",
	}
}

func (p *geminiProvider) refreshAccessToken(ctx context.Context, credentials map[string]any, refreshToken string) (map[string]any, error) {
	clientID := strings.TrimSpace(os.Getenv("GEMINI_OAUTH_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GEMINI_OAUTH_CLIENT_SECRET"))
	if clientID == "" || clientSecret == "" {
		return nil, errGeminiOAuthClientMissing
	}
	statusCode, payload, requestErr := requestFormJSON(ctx, p.options.HTTPClient, geminiTokenEndpoint, url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
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
	updated := make(map[string]any, len(credentials)+2)
	for key, value := range credentials {
		updated[key] = value
	}
	updated["access_token"] = newAccessToken
	if refreshedRefreshToken := firstString(refreshed, "refresh_token", "refreshToken"); refreshedRefreshToken != "" {
		updated["refresh_token"] = refreshedRefreshToken
	}
	if expiresIn, ok, numberErr := numberField(refreshed, "expires_in", "expiresIn"); numberErr == nil && ok {
		updated["expiry_date"] = p.options.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	}
	if p.options.PersistRefreshedCredentials {
		_ = writeJSONMapAtomic(homePath(p.options.HomeDir, ".gemini", "oauth_creds.json"), updated)
	}
	return updated, nil
}

var errMissingAccessToken = &missingTokenError{}

type missingTokenError struct{}

func (*missingTokenError) Error() string { return "oauth response did not contain an access token" }

var errGeminiOAuthClientMissing = &geminiOAuthClientMissingError{}

type geminiOAuthClientMissingError struct{}

func (*geminiOAuthClientMissingError) Error() string {
	return "GEMINI_OAUTH_CLIENT_ID and GEMINI_OAUTH_CLIENT_SECRET are required for token refresh"
}

func (p *geminiProvider) projectID(credentials map[string]any) string {
	for _, key := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID", "GOOGLE_CLOUD_QUOTA_PROJECT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	if value := firstString(credentials, "project_id", "projectId", "project"); value != "" {
		return value
	}
	for _, path := range []string{
		homePath(p.options.HomeDir, ".gemini", "projects.json"),
	} {
		if object, err := readJSONMap(path); err == nil {
			if value := firstString(object, "project_id", "projectId", "project"); value != "" {
				return value
			}
		}
	}
	return ""
}
