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
	client, err := newHTTPClient(approvedGitHubHost(), HTTPClientConfig{MaxResponseBytes: 4}, nil, transport)
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
	client, err := newHTTPClient(approvedGitHubHost(), HTTPClientConfig{}, staticAuthorization("Bearer super-secret"), transport)
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
	client, err = newHTTPClient(approvedGitHubHost(), HTTPClientConfig{}, staticAuthorization("Bearer super-secret"), transport)
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
	client, err := newHTTPClient(approvedGitHubHost(), HTTPClientConfig{}, nil, transport)
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
}

func TestPinnedDialerRejectsUnknownTargetBeforeNetwork(t *testing.T) {
	dial := newPinnedDialer(approvedGitHubHost(), time.Millisecond)
	_, err := dial(context.Background(), "tcp", "evil.example:443")
	if err == nil || strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("unknown target was not safely rejected: %v", err)
	}
}

func TestHTTPClientFreezesApprovedOriginSlices(t *testing.T) {
	approved := approvedGitHubHost()
	transportCalls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		transportCalls++
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	approved.Host.EndpointOrigins[0] = "https://evil.example"
	approved.Endpoints[0].Origin = "https://evil.example"
	approved.Endpoints[0].Addresses[0] = approved.Endpoints[1].Addresses[0]
	_, err = client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://evil.example/steal", nil)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorHostNotApproved || transportCalls != 0 {
		t.Fatalf("mutated approval expanded client authority: %v, calls=%d", err, transportCalls)
	}
}
