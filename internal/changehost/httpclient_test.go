package changehost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type staticAuthorization string

func (s staticAuthorization) Authorization(context.Context, HostIdentity, string) (string, bool, error) {
	return string(s), true, nil
}

type blockingAuthorization struct{}

func (blockingAuthorization) Authorization(ctx context.Context, _ HostIdentity, _ string) (string, bool, error) {
	<-ctx.Done()
	return "", false, ctx.Err()
}

func response(request *http.Request, status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: header,
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}
}

func TestHTTPClientRestrictsMethodsOriginsHeadersAndResponseSize(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Header.Get("Cookie") != "" || request.Header.Get("X-Secret") != "" {
			t.Fatalf("unsafe caller header forwarded: %v", request.Header)
		}
		return response(request, http.StatusOK, "12345", nil), nil
	})
	client, err := newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{MaxResponseBytes: 4}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{"Cookie": {"secret"}, "X-Secret": {"secret"}}
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", headers)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorOverflow {
		t.Fatalf("oversize response was not rejected: %v", err)
	}
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodPost, "https://api.github.com/repos/acme/widgets/pulls/42", nil)
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorUnsupported || requests != 1 {
		t.Fatalf("write method reached transport: %v, requests=%d", err, requests)
	}
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://evil.example/steal", nil)
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorHostNotApproved || requests != 1 {
		t.Fatalf("unapproved origin reached transport: %v, requests=%d", err, requests)
	}
}

func TestHTTPClientPreservesAllowlistedProviderMediaType(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if accept := request.Header.Get("Accept"); accept != "application/vnd.github.diff" {
			t.Fatalf("provider media type = %q", accept)
		}
		return response(request, http.StatusOK, "diff --git a/a b/a\n", nil), nil
	})
	client, err := newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("Accept", "application/vnd.github.diff")
	if _, err := client.Do(
		context.Background(), OperationGetSnapshot, http.MethodGet,
		"https://api.github.com/repos/acme/widgets/pulls/42", headers,
	); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientAllowsOnlyApprovedRedirectAndDropsAuthorization(t *testing.T) {
	var seen []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.String()+" auth="+request.Header.Get("Authorization"))
		if request.URL.Host == "github.com" {
			header := http.Header{"Location": {"https://api.github.com/repos/acme/widgets/pulls/42"}}
			return response(request, http.StatusFound, "", header), nil
		}
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	client, err := newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{}, staticAuthorization("Bearer super-secret"), transport)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://github.com/acme/widgets/pull/42", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "Bearer super-secret") || strings.Contains(seen[1], "super-secret") {
		t.Fatalf("redirect credential policy violated: %v", seen)
	}

	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := http.Header{"Location": {"https://evil.example/steal"}}
		return response(request, http.StatusFound, "", header), nil
	})
	client, err = newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{}, staticAuthorization("Bearer super-secret"), transport)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://github.com/acme/widgets/pull/42", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorHostNotApproved || strings.Contains(err.Error(), "evil.example") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe redirect was not safely rejected: %v", err)
	}
}

func TestHTTPClientReturnsBoundedRateLimitMetadata(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Retry-After", "17")
		header.Set("X-RateLimit-Remaining", "0")
		header.Set("X-RateLimit-Reset", "1800000000")
		return response(request, http.StatusTooManyRequests, `{}`, header), nil
	})
	client, err := newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorRateLimited || providerErr.RetryAfter != 17*time.Second {
		t.Fatalf("rate limit did not produce typed retry: result=%+v err=%v", result, err)
	}
	if result.Metadata.RetryAfterSeconds == nil || *result.Metadata.RetryAfterSeconds != 17 || result.Metadata.RateLimit == nil || result.Metadata.RateLimit.Remaining == nil || *result.Metadata.RateLimit.Remaining != 0 {
		t.Fatalf("rate limit metadata incomplete: %+v", result.Metadata)
	}
	if result.Body != nil {
		t.Fatalf("rate-limit response body escaped the safe boundary: %q", result.Body)
	}
}

func TestHTTPClientRejectsForgedAndRevokedApproval(t *testing.T) {
	if _, err := NewHTTPClient(&ApprovedHost{}, HTTPClientConfig{}, nil); err == nil {
		t.Fatal("forged zero-value approval created a network client")
	}
	approved := approvedGitHubHost(t)
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, `{}`, nil), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	approved.Revoke()
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorHostRevoked {
		t.Fatalf("revoked approval remained usable: %v", err)
	}
	if _, err := NewHTTPClient(approved, HTTPClientConfig{}, nil); err == nil {
		t.Fatal("revoked approval created a new client")
	}
}

func TestApprovalRevocationCancelsInFlightRequest(t *testing.T) {
	approved := approvedGitHubHost(t)
	started := make(chan struct{})
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client, err := newHTTPClient(approved, HTTPClientConfig{RequestTimeout: time.Second}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, requestErr := client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil)
		done <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach transport")
	}
	approved.Revoke()
	select {
	case requestErr := <-done:
		var providerErr *Error
		if !errors.As(requestErr, &providerErr) || providerErr.Code != ErrorHostRevoked {
			t.Fatalf("in-flight revoke returned wrong error: %v", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel in-flight request")
	}
}

func TestHTTPClientTotalDeadlineCoversQueueAndAuthorization(t *testing.T) {
	transportCalls := 0
	client, err := newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{RequestTimeout: 25 * time.Millisecond, ConnectTimeout: 10 * time.Millisecond, MaxConcurrent: 1}, nil, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		transportCalls++
		return response(request, http.StatusOK, `{}`, nil), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	client.semaphore <- struct{}{}
	started := time.Now()
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil)
	<-client.semaphore
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorUnavailable || transportCalls != 0 {
		t.Fatalf("queued request escaped total deadline: %v, calls=%d", err, transportCalls)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("queued request exceeded total deadline: %s", elapsed)
	}

	client, err = newHTTPClient(approvedGitHubHost(t), HTTPClientConfig{RequestTimeout: 25 * time.Millisecond, ConnectTimeout: 10 * time.Millisecond}, blockingAuthorization{}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		transportCalls++
		return response(request, http.StatusOK, `{}`, nil), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil)
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorAuthRequired || transportCalls != 0 {
		t.Fatalf("blocking authorization escaped total deadline: %v, calls=%d", err, transportCalls)
	}
}

func TestHTTPClientRejectsUnboundedConfigAndEnvironmentProxy(t *testing.T) {
	approved := approvedGitHubHost(t)
	for _, config := range []HTTPClientConfig{
		{RequestTimeout: maximumRequestTimeout + time.Second},
		{RequestTimeout: time.Minute, ConnectTimeout: maximumConnectTimeout + time.Second},
		{MaxResponseBytes: maximumResponseBytes + 1},
		{MaxConcurrent: maximumConcurrent + 1},
		{UserAgent: "unsafe\r\nheader"},
	} {
		if _, err := NewHTTPClient(approved, config, nil); !errors.Is(err, ErrInvalidHTTPClientConfig) {
			t.Fatalf("unbounded config accepted: %+v, err=%v", config, err)
		}
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client, err := NewHTTPClient(approved, HTTPClientConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("production client consulted environment proxy settings")
	}
}

func TestPinnedDialerRejectsUnknownTargetBeforeNetwork(t *testing.T) {
	dial := newPinnedDialer(approvedGitHubHost(t), time.Millisecond)
	_, err := dial(context.Background(), "tcp", "evil.example:443")
	if err == nil || strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("unknown target was not safely rejected: %v", err)
	}
}

func TestHTTPClientFreezesApprovedOriginSlices(t *testing.T) {
	approved := approvedGitHubHost(t)
	transportCalls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		transportCalls++
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	approved.host.EndpointOrigins[0] = "https://evil.example"
	approved.endpoints[0].Origin = "https://evil.example"
	approved.endpoints[0].Addresses[0] = approved.endpoints[1].Addresses[0]
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://evil.example/steal", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorHostNotApproved || transportCalls != 0 {
		t.Fatalf("mutated approval expanded client authority: %v, calls=%d", err, transportCalls)
	}
}
