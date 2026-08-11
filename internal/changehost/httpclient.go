package changehost

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	defaultRequestTimeout   = 20 * time.Second
	defaultConnectTimeout   = 5 * time.Second
	defaultMaxResponseBytes = int64(8 << 20)
	defaultMaxConcurrent    = 4
)

var errURLOriginNotApproved = errors.New("change host URL origin was not approved")

type HTTPClientConfig struct {
	RequestTimeout   time.Duration
	ConnectTimeout   time.Duration
	MaxResponseBytes int64
	MaxConcurrent    int
	UserAgent        string
}

// AuthorizationSource supplies an in-memory Authorization value for one
// approved origin. Implementations may consult env, keyring, or an allowlisted
// provider CLI, but the resulting value must never be persisted or logged.
type AuthorizationSource interface {
	Authorization(context.Context, HostIdentity, string) (string, bool, error)
}

type HTTPResult struct {
	StatusCode   int
	Body         []byte
	ETag         string
	LastModified string
	Metadata     ResultMetadata
}

// HTTPClient is a GET/HEAD-only client scoped to one immutable ApprovedHost.
// Its production transport dials only approval-time pinned addresses, ignores
// proxy environment variables, and preserves normal TLS hostname validation.
type HTTPClient struct {
	approved      ApprovedHost
	client        *http.Client
	authorization AuthorizationSource
	maxBytes      int64
	userAgent     string
	semaphore     chan struct{}
}

func NewHTTPClient(approved ApprovedHost, config HTTPClientConfig, authorization AuthorizationSource) (*HTTPClient, error) {
	if err := ValidateApprovedHost(approved); err != nil {
		return nil, err
	}
	approved = cloneApprovedHost(approved)
	config = normalizeHTTPClientConfig(config)
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           newPinnedDialer(approved, config.ConnectTimeout),
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   config.ConnectTimeout,
		ResponseHeaderTimeout: config.RequestTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return newHTTPClient(approved, config, authorization, transport)
}

func newHTTPClient(approved ApprovedHost, config HTTPClientConfig, authorization AuthorizationSource, transport http.RoundTripper) (*HTTPClient, error) {
	if err := ValidateApprovedHost(approved); err != nil {
		return nil, err
	}
	approved = cloneApprovedHost(approved)
	config = normalizeHTTPClientConfig(config)
	result := &HTTPClient{
		approved: approved, authorization: authorization, maxBytes: config.MaxResponseBytes,
		userAgent: config.UserAgent, semaphore: make(chan struct{}, config.MaxConcurrent),
	}
	result.client = &http.Client{
		Transport: transport,
		Timeout:   config.RequestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("redirect limit exceeded")
			}
			if _, err := result.validateURL(request.URL); err != nil {
				return err
			}
			// Credentials never transit a redirect, even between approved
			// origins. Providers should request the canonical API URL directly.
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			return nil
		},
	}
	return result, nil
}

func normalizeHTTPClientConfig(config HTTPClientConfig) HTTPClientConfig {
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaultRequestTimeout
	}
	if config.ConnectTimeout <= 0 || config.ConnectTimeout > config.RequestTimeout {
		config.ConnectTimeout = defaultConnectTimeout
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaultMaxConcurrent
	}
	if config.UserAgent == "" {
		config.UserAgent = "session-insight-changehost/0.6"
	}
	return config
}

func (c *HTTPClient) Do(ctx context.Context, operation Operation, method, rawURL string, headers http.Header) (HTTPResult, error) {
	if !IsKnownOperation(operation) {
		return HTTPResult{}, &Error{Code: ErrorUnsupported, Operation: operation}
	}
	if method != http.MethodGet && method != http.MethodHead {
		return HTTPResult{}, &Error{Code: ErrorUnsupported, Operation: operation}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return HTTPResult{}, &Error{Code: ErrorHostNotApproved, Operation: operation, Cause: err}
	}
	origin, err := c.validateURL(parsed)
	if err != nil {
		return HTTPResult{}, &Error{Code: ErrorHostNotApproved, Operation: operation, Cause: err}
	}
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return HTTPResult{}, &Error{Code: ErrorUnavailable, Operation: operation, Cause: ctx.Err()}
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), nil)
	if err != nil {
		return HTTPResult{}, &Error{Code: ErrorInvalidResponse, Operation: operation, Cause: err}
	}
	copySafeRequestHeaders(request.Header, headers)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	if c.authorization != nil {
		value, configured, authErr := c.authorization.Authorization(ctx, c.approved.Host, origin)
		if authErr != nil {
			return HTTPResult{}, &Error{Code: ErrorAuthRequired, Operation: operation, Cause: authErr}
		}
		if configured {
			if value == "" || strings.ContainsAny(value, "\r\n") {
				return HTTPResult{}, &Error{Code: ErrorAuthRequired, Operation: operation}
			}
			request.Header.Set("Authorization", value)
		}
	}
	response, err := c.client.Do(request)
	if err != nil {
		if errors.Is(err, errURLOriginNotApproved) {
			return HTTPResult{}, &Error{Code: ErrorHostNotApproved, Operation: operation, Cause: err}
		}
		return HTTPResult{}, &Error{Code: ErrorUnavailable, Operation: operation, Cause: err}
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, c.maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return HTTPResult{}, &Error{Code: ErrorInvalidResponse, Operation: operation, Cause: err}
	}
	if int64(len(body)) > c.maxBytes {
		return HTTPResult{}, &Error{Code: ErrorOverflow, Operation: operation}
	}
	metadata := ResultMetadata{
		Assessment: modelAssessmentForStatus(response.StatusCode),
		PageCount:  1,
		ItemCount:  0,
		BytesRead:  int64(len(body)),
		RateLimit:  parseRateLimit(response.Header),
	}
	if seconds, ok := parseRetryAfter(response.Header.Get("Retry-After"), time.Now()); ok {
		metadata.RetryAfterSeconds = &seconds
	}
	result := HTTPResult{
		StatusCode: response.StatusCode, Body: body, ETag: response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"), Metadata: metadata,
	}
	if code, failed := responseErrorCode(response.StatusCode, metadata); failed {
		providerError := &Error{Code: code, Operation: operation}
		if metadata.RetryAfterSeconds != nil {
			providerError.RetryAfter = time.Duration(*metadata.RetryAfterSeconds) * time.Second
		}
		return result, providerError
	}
	return result, nil
}

func (c *HTTPClient) validateURL(u *url.URL) (string, error) {
	if u == nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("invalid change host URL")
	}
	origin, err := endpointOrigin(u.String())
	if err != nil {
		return "", err
	}
	if _, ok := c.approved.endpoint(origin); !ok {
		return "", errURLOriginNotApproved
	}
	return origin, nil
}

func copySafeRequestHeaders(destination, source http.Header) {
	for _, name := range []string{"If-None-Match", "If-Modified-Since", "Accept"} {
		if value := source.Get(name); value != "" && !strings.ContainsAny(value, "\r\n") {
			destination.Set(name, value)
		}
	}
}

func modelAssessmentForStatus(status int) GitEvidenceAssessment {
	if status >= 200 && status < 300 || status == http.StatusNotModified {
		return exactAssessment()
	}
	return partialAssessment()
}

func responseErrorCode(status int, metadata ResultMetadata) (ErrorCode, bool) {
	switch {
	case status >= 200 && status < 300, status == http.StatusNotModified:
		return "", false
	case status == http.StatusUnauthorized:
		return ErrorAuthRequired, true
	case status == http.StatusForbidden:
		if metadata.RetryAfterSeconds != nil || metadata.RateLimit != nil && metadata.RateLimit.Remaining != nil && *metadata.RateLimit.Remaining == 0 {
			return ErrorRateLimited, true
		}
		return ErrorAuthRequired, true
	case status == http.StatusNotFound:
		return ErrorNotFound, true
	case status == http.StatusTooManyRequests:
		return ErrorRateLimited, true
	case status >= 500:
		return ErrorUnavailable, true
	default:
		return ErrorInvalidResponse, true
	}
}

func exactAssessment() GitEvidenceAssessment {
	return model.ExactGitEvidence()
}

func partialAssessment() GitEvidenceAssessment {
	return model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
}

func parseRetryAfter(raw string, now time.Time) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && seconds >= 0 {
		return seconds, true
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	seconds := int64(0)
	if delay > 0 {
		seconds = int64((delay + time.Second - 1) / time.Second)
	}
	return seconds, true
}

func parseRateLimit(header http.Header) *RateLimit {
	var rate RateLimit
	if raw := firstHeader(header, "X-RateLimit-Remaining", "RateLimit-Remaining"); raw != "" {
		if remaining, err := strconv.Atoi(raw); err == nil && remaining >= 0 {
			rate.Remaining = &remaining
		}
	}
	if raw := firstHeader(header, "X-RateLimit-Reset", "RateLimit-Reset"); raw != "" {
		if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil && epoch >= 0 {
			reset := time.Unix(epoch, 0).UTC()
			rate.ResetAt = &reset
		} else if reset, err := http.ParseTime(raw); err == nil {
			reset = reset.UTC()
			rate.ResetAt = &reset
		}
	}
	if rate.Remaining == nil && rate.ResetAt == nil {
		return nil
	}
	return &rate
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func newPinnedDialer(approved ApprovedHost, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	type pinnedTarget struct {
		addresses []net.IP
		port      string
		counter   atomic.Uint64
	}
	targets := make(map[string]*pinnedTarget, len(approved.Endpoints))
	for _, endpoint := range approved.Endpoints {
		dialAddress, err := endpointDialAddress(endpoint.Origin)
		if err != nil {
			continue
		}
		_, port, err := net.SplitHostPort(dialAddress)
		if err != nil {
			continue
		}
		target := &pinnedTarget{port: port, addresses: make([]net.IP, 0, len(endpoint.Addresses))}
		for _, address := range endpoint.Addresses {
			target.addresses = append(target.addresses, net.IP(address.AsSlice()))
		}
		targets[dialAddress] = target
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("dial target was not approved")
		}
		address = net.JoinHostPort(strings.ToLower(host), port)
		target := targets[address]
		if target == nil || len(target.addresses) == 0 {
			return nil, errors.New("dial target was not approved")
		}
		index := target.counter.Add(1) - 1
		pinned := net.JoinHostPort(target.addresses[index%uint64(len(target.addresses))].String(), target.port)
		connection, err := dialer.DialContext(ctx, network, pinned)
		if err != nil {
			return nil, fmt.Errorf("approved endpoint dial failed: %w", err)
		}
		return connection, nil
	}
}
