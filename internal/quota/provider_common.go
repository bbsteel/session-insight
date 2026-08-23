package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultQuotaHTTPTimeout = 15 * time.Second
	maxQuotaResponseBytes   = 4 << 20
)

// ProviderOptions contains process-local dependencies shared by all quota
// adapters. HomeDir is injectable so parser tests never need real credentials.
type ProviderOptions struct {
	HomeDir                     string
	HTTPClient                  *http.Client
	Now                         func() time.Time
	PersistRefreshedCredentials bool
}

// DefaultProviderOptions uses the current user's local configuration and a
// bounded HTTP client. Network requests are always made by the Go backend, so
// credentials never cross the browser boundary.
func DefaultProviderOptions() ProviderOptions {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	return ProviderOptions{
		HomeDir:                     homeDir,
		HTTPClient:                  &http.Client{Timeout: defaultQuotaHTTPTimeout},
		Now:                         time.Now,
		PersistRefreshedCredentials: true,
	}
}

func normalizeProviderOptions(options ProviderOptions) ProviderOptions {
	if options.HomeDir == "" {
		options.HomeDir = DefaultProviderOptions().HomeDir
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: defaultQuotaHTTPTimeout}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return options
}

func homePath(homeDir string, parts ...string) string {
	if homeDir == "" {
		return ""
	}
	return filepath.Join(append([]string{homeDir}, parts...)...)
}

func readJSONMap(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxQuotaResponseBytes))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("credential JSON is not an object")
	}
	return object, nil
}

func writeJSONMapAtomic(path string, object map[string]any) error {
	parent := filepath.Dir(path)
	file, err := os.CreateTemp(parent, ".session-insight-quota-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(object); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func requestJSON(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string, body []byte) (int, []byte, error) {
	requestBody := io.Reader(nil)
	if len(body) > 0 {
		requestBody = strings.NewReader(string(body))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		return 0, nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	bodyReader := io.LimitReader(response.Body, maxQuotaResponseBytes+1)
	responseBody, err := io.ReadAll(bodyReader)
	if err != nil {
		return response.StatusCode, nil, err
	}
	if len(responseBody) > maxQuotaResponseBytes {
		return response.StatusCode, nil, fmt.Errorf("quota response exceeds %d bytes", maxQuotaResponseBytes)
	}
	return response.StatusCode, responseBody, nil
}

func requestFormJSON(ctx context.Context, client *http.Client, endpoint string, form url.Values, headers map[string]string) (int, []byte, error) {
	if headers == nil {
		headers = make(map[string]string)
	}
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	headers["Accept"] = "application/json"
	return requestJSON(ctx, client, http.MethodPost, endpoint, headers, []byte(form.Encode()))
}

func unexpectedHTTPStatus(statusCode int) error {
	return fmt.Errorf("upstream returned HTTP %d", statusCode)
}

func parseJSONObject(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("upstream payload is not an object")
	}
	return object, nil
}

func quotaFailure(status SnapshotStatus, reasonCode string) QuotaSnapshot {
	return QuotaSnapshot{Status: status, ReasonCode: reasonCode}
}

func httpFailure(statusCode int, requestErr error, prefix string) QuotaSnapshot {
	if requestErr != nil {
		return quotaFailure(StatusNetworkError, prefix+"_network_error")
	}
	switch {
	case statusCode == http.StatusUnauthorized:
		return quotaFailure(StatusUnauthorized, prefix+"_unauthorized")
	case statusCode == http.StatusForbidden:
		return quotaFailure(StatusUnauthorized, prefix+"_forbidden")
	case statusCode == http.StatusTooManyRequests:
		return quotaFailure(StatusRateLimited, prefix+"_rate_limited")
	case statusCode >= 500:
		return quotaFailure(StatusNetworkError, prefix+"_upstream_error")
	default:
		return quotaFailure(StatusInvalidData, fmt.Sprintf("%s_http_%d", prefix, statusCode))
	}
}

func mapValue(object map[string]any, key string) (any, bool) {
	value, ok := object[key]
	return value, ok && value != nil
}

func firstValue(object map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := mapValue(object, key); ok {
			return value, true
		}
	}
	return nil, false
}

func firstString(object map[string]any, keys ...string) string {
	value, ok := firstValue(object, keys...)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func mapField(object map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			if child, ok := value.(map[string]any); ok {
				return child
			}
		}
	}
	return nil
}

func numberField(object map[string]any, keys ...string) (float64, bool, error) {
	value, ok := firstValue(object, keys...)
	if !ok {
		return 0, false, nil
	}
	number, err := numberValue(value)
	if err != nil {
		return 0, true, err
	}
	return number, true, nil
}

func expiresAt(object map[string]any, now time.Time) (time.Time, bool) {
	value, ok := firstValue(object, "expires_at", "expiresAt", "expiry_date", "expiryDate")
	if !ok {
		return time.Time{}, false
	}
	parsed, err := timestampValue(value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, !parsed.After(now.Add(time.Minute))
}

func bearerHeaders(token string) map[string]string {
	return map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + token,
		"User-Agent":    "session-insight-quota/1",
	}
}
