package changehost

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestEnvironmentCredentialSourceResolvesOnlyEnvReferences(t *testing.T) {
	source := EnvironmentCredentialSource{Lookup: func(name string) (string, bool) {
		if name == "REVIEW_TOKEN" {
			return "s3cret", true
		}
		return "", false
	}}
	secret, ok, err := source.Secret(context.Background(), "env:REVIEW_TOKEN")
	if err != nil || !ok || secret != "s3cret" {
		t.Fatalf("env credential not resolved: ok=%v err=%v", ok, err)
	}
	if _, ok, err := source.Secret(context.Background(), "env:MISSING"); err != nil || ok {
		t.Fatalf("missing env variable must report unconfigured without error: ok=%v err=%v", ok, err)
	}
	if _, _, err := source.Secret(context.Background(), "keyring:profile-1"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("keyring reference on env source must be unavailable: %v", err)
	}
}

func TestKeyringCredentialSourceRequiresLookup(t *testing.T) {
	source := KeyringCredentialSource{}
	if _, _, err := source.Secret(context.Background(), "keyring:profile-1"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("keyring source without lookup must be unavailable: %v", err)
	}
	source.Lookup = func(_ context.Context, key string) (string, bool, error) {
		return "from-keyring-" + key, true, nil
	}
	secret, ok, err := source.Secret(context.Background(), "keyring:profile-1")
	if err != nil || !ok || secret != "from-keyring-profile-1" {
		t.Fatalf("keyring credential not resolved: ok=%v err=%v", ok, err)
	}
}

func TestValidateAuthenticationSchemeEnforcesReviewedShapes(t *testing.T) {
	valid := []AuthenticationScheme{
		{HeaderName: "Authorization", ValuePrefix: "Bearer "},
		{HeaderName: "Authorization", ValuePrefix: "token "},
		{HeaderName: "PRIVATE-TOKEN"},
		{HeaderName: "X-API-Key"},
		{HeaderName: "X-Custom-Token"},
	}
	for _, scheme := range valid {
		if err := ValidateAuthenticationScheme(scheme); err != nil {
			t.Fatalf("valid scheme %+v rejected: %v", scheme, err)
		}
	}
	invalid := []AuthenticationScheme{
		{HeaderName: "Authorization"},
		{HeaderName: "Authorization", ValuePrefix: "Basic "},
		{HeaderName: "PRIVATE-TOKEN", ValuePrefix: "Token "},
		{HeaderName: "Cookie"},
		{HeaderName: "Host"},
		{HeaderName: "Bad Header"},
		{HeaderName: ""},
	}
	for _, scheme := range invalid {
		if err := ValidateAuthenticationScheme(scheme); !errors.Is(err, ErrInvalidAuthenticationScheme) {
			t.Fatalf("invalid scheme %+v accepted: %v", scheme, err)
		}
	}
}

func TestAuthenticationModeForReference(t *testing.T) {
	if mode, ok := AuthenticationModeForReference("env:REVIEW_TOKEN"); !ok || mode != AuthTokenEnvironment {
		t.Fatalf("env reference mode wrong: %v %v", mode, ok)
	}
	if mode, ok := AuthenticationModeForReference("keyring:profile-1"); !ok || mode != AuthOSKeyring {
		t.Fatalf("keyring reference mode wrong: %v %v", mode, ok)
	}
	if _, ok := AuthenticationModeForReference(""); ok {
		t.Fatal("empty reference produced an authentication mode")
	}
}

func TestHTTPClientAppliesProfileDeclaredCredentialHeader(t *testing.T) {
	var seen http.Header
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = request.Header.Clone()
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	approved := approvedGitHubHost(t)
	authentication := ResolvedAuthentication{
		Source: EnvironmentCredentialSource{Lookup: func(name string) (string, bool) {
			return "token-value", name == "REVIEW_TOKEN"
		}},
		Reference:      "env:REVIEW_TOKEN",
		Scheme:         AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
		AllowedOrigins: []string{"https://api.github.com"},
	}
	client, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, authentication)
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = transport
	if _, err := client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil); err != nil {
		t.Fatal(err)
	}
	// Failure output must never include the captured header set: it holds the
	// credential value. Report presence booleans only.
	if seen.Get("PRIVATE-TOKEN") != "token-value" {
		t.Fatalf("profile-declared header missing (present=%v)", seen.Get("PRIVATE-TOKEN") != "")
	}
	if seen.Get("Authorization") != "" {
		t.Fatalf("credential source must not set its own header (authorization_present=%v)", seen.Get("Authorization") != "")
	}
}

func TestHTTPClientRejectsInvalidResolvedAuthentication(t *testing.T) {
	approved := approvedGitHubHost(t)
	if _, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, ResolvedAuthentication{
		Source: EnvironmentCredentialSource{}, Reference: "env:REVIEW_TOKEN",
		Scheme: AuthenticationScheme{HeaderName: "Authorization"},
	}); !errors.Is(err, ErrInvalidAuthenticationScheme) {
		t.Fatalf("Authorization without reviewed prefix accepted: %v", err)
	}
	if _, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, ResolvedAuthentication{
		Source: EnvironmentCredentialSource{}, Reference: "file:/etc/passwd",
		Scheme: AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
	}); !errors.Is(err, ErrInvalidAuthenticationScheme) {
		t.Fatalf("invalid credential reference accepted: %v", err)
	}
}

func TestHTTPClientStripsCustomCredentialHeaderOnRedirect(t *testing.T) {
	// Track credential-header presence per request, never the value itself:
	// failure output must stay free of credential material.
	seen := []bool{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.Header.Get("PRIVATE-TOKEN") != "")
		if request.URL.Host == "github.com" {
			header := http.Header{"Location": {"https://api.github.com/repos/acme/widgets/pulls/42"}}
			return response(request, http.StatusFound, "", header), nil
		}
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	approved := approvedGitHubHost(t)
	client, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, ResolvedAuthentication{
		Source:         EnvironmentCredentialSource{Lookup: func(string) (string, bool) { return "s3cret", true }},
		Reference:      "env:REVIEW_TOKEN",
		Scheme:         AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
		AllowedOrigins: []string{"https://github.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = transport
	if _, err := client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://github.com/acme/widgets/pull/42", nil); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !seen[0] || seen[1] {
		t.Fatalf("redirect credential policy violated: credential header presence per hop = %v", seen)
	}
}

func TestHTTPClientCredentialOriginPolicy(t *testing.T) {
	seen := []http.Header{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.Header.Clone())
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"review.internal":     {netip.MustParseAddr("192.0.2.10")},
		"api.review.internal": {netip.MustParseAddr("192.0.2.11")},
	}}
	host := HostIdentity{
		Key: "review-host", Provider: model.ChangeProviderOpenAPI,
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal", "https://api.review.internal", "http://review.internal"},
	}
	approved, err := NewHostPolicy(resolver).Approve(context.Background(), host,
		HostApprovalOptions{AllowHTTP: true, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	authentication := ResolvedAuthentication{
		Source:         EnvironmentCredentialSource{Lookup: func(string) (string, bool) { return "tok", true }},
		Reference:      "env:REVIEW_TOKEN",
		Scheme:         AuthenticationScheme{HeaderName: "Authorization", ValuePrefix: "Bearer "},
		AllowedOrigins: []string{"https://api.review.internal", "http://review.internal"},
	}
	client, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, authentication)
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = transport

	// Allowed HTTPS origin: credential injected.
	if _, err := client.Do(context.Background(), OperationGetSnapshot, http.MethodGet, "https://api.review.internal/api/x", nil); err != nil {
		t.Fatal(err)
	}
	// Undeclared (but approved) origin: no credential.
	if _, err := client.Do(context.Background(), OperationGetSnapshot, http.MethodGet, "https://review.internal/api/x", nil); err != nil {
		t.Fatal(err)
	}
	// Plain HTTP never carries credentials, even on an allowed origin.
	if _, err := client.Do(context.Background(), OperationGetSnapshot, http.MethodGet, "http://review.internal/api/x", nil); err != nil {
		t.Fatal(err)
	}
	if seen[0].Get("Authorization") == "" {
		t.Fatal("allowed origin lost its credential")
	}
	if seen[1].Get("Authorization") != "" {
		t.Fatal("undeclared origin received the credential")
	}
	if seen[2].Get("Authorization") != "" {
		t.Fatal("credential leaked over plain HTTP")
	}
}

func TestHTTPClientProfileCredentialScopeFailsClosedWhenEmpty(t *testing.T) {
	var credentialHeaderSeen bool
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		credentialHeaderSeen = request.Header.Get("PRIVATE-TOKEN") != ""
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	client, err := NewAuthenticatedHTTPClient(approvedGitHubHost(t), HTTPClientConfig{}, ResolvedAuthentication{
		Source:    EnvironmentCredentialSource{Lookup: func(string) (string, bool) { return "tok", true }},
		Reference: "env:REVIEW_TOKEN",
		Scheme:    AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = transport
	if _, err := client.Do(context.Background(), OperationGetSnapshot, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil); err != nil {
		t.Fatal(err)
	}
	if credentialHeaderSeen {
		t.Fatal("profile authentication without an explicit origin scope must not inject credentials")
	}
}

func TestNewAuthenticatedHTTPClientRejectsInvalidAllowedOrigin(t *testing.T) {
	_, err := NewAuthenticatedHTTPClient(approvedGitHubHost(t), HTTPClientConfig{}, ResolvedAuthentication{
		Source:         EnvironmentCredentialSource{},
		Reference:      "env:REVIEW_TOKEN",
		Scheme:         AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
		AllowedOrigins: []string{"not-an-origin"},
	})
	if !errors.Is(err, ErrInvalidAuthenticationScheme) {
		t.Fatalf("invalid allowlist origin accepted: %v", err)
	}
}
