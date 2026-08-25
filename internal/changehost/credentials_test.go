package changehost

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
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
		Reference: "env:REVIEW_TOKEN",
		Scheme:    AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
	}
	client, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, authentication)
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = transport
	if _, err := client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://api.github.com/repos/acme/widgets/pulls/42", nil); err != nil {
		t.Fatal(err)
	}
	if seen.Get("PRIVATE-TOKEN") != "token-value" {
		t.Fatalf("profile-declared header missing: %v", seen)
	}
	if seen.Get("Authorization") != "" {
		t.Fatalf("credential source must not set its own header: %v", seen)
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
	seen := []string{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.Host+" token="+request.Header.Get("PRIVATE-TOKEN"))
		if request.URL.Host == "github.com" {
			header := http.Header{"Location": {"https://api.github.com/repos/acme/widgets/pulls/42"}}
			return response(request, http.StatusFound, "", header), nil
		}
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	approved := approvedGitHubHost(t)
	client, err := NewAuthenticatedHTTPClient(approved, HTTPClientConfig{}, ResolvedAuthentication{
		Source:    EnvironmentCredentialSource{Lookup: func(string) (string, bool) { return "s3cret", true }},
		Reference: "env:REVIEW_TOKEN",
		Scheme:    AuthenticationScheme{HeaderName: "PRIVATE-TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = transport
	if _, err := client.Do(context.Background(), OperationResolveChange, http.MethodGet, "https://github.com/acme/widgets/pull/42", nil); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "s3cret") || strings.Contains(seen[1], "s3cret") {
		t.Fatalf("redirect credential policy violated: %v", seen)
	}
}
