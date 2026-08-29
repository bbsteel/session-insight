package model

import (
	"strings"
	"testing"
)

func TestParseCredentialReferenceAcceptsKeyringAndEnvironment(t *testing.T) {
	for _, raw := range []string{
		"keyring:profile-01J4Z8EXAMPLE",
		"keyring:host.review.internal:token",
		"env:INTERNAL_REVIEW_TOKEN",
		"env:_private_token2",
	} {
		reference, ok := ParseCredentialReference(raw)
		if !ok {
			t.Fatalf("ParseCredentialReference(%q) rejected a valid reference", raw)
		}
		if string(reference) != raw {
			t.Fatalf("ParseCredentialReference(%q) rewrote the reference to %q", raw, reference)
		}
	}

	envRef, _ := ParseCredentialReference("env:INTERNAL_REVIEW_TOKEN")
	if envRef.Scheme() != CredentialSchemeEnvironment || envRef.Name() != "INTERNAL_REVIEW_TOKEN" {
		t.Fatalf("env reference scheme/name wrong: %q %q", envRef.Scheme(), envRef.Name())
	}
	keyringRef, _ := ParseCredentialReference("keyring:profile-01J")
	if keyringRef.Scheme() != CredentialSchemeKeyring || keyringRef.Name() != "profile-01J" {
		t.Fatalf("keyring reference scheme/name wrong: %q %q", keyringRef.Scheme(), keyringRef.Name())
	}
}

func TestParseCredentialReferenceRejectsUnsafeValues(t *testing.T) {
	for _, raw := range []string{
		"",
		"file:/etc/passwd",
		"keyring:",
		"keyring:has space",
		"keyring:has/slash",
		"keyring:with\x00nul",
		"env:",
		"env:1STARTS_WITH_DIGIT",
		"env:HAS-DASH",
		"env:has space",
		" env:LEADING_SPACE",
		"env:TRAILING_SPACE ",
		"keyring:" + strings.Repeat("a", 300),
		"env:" + strings.Repeat("A", 200),
	} {
		if reference, ok := ParseCredentialReference(raw); ok {
			t.Fatalf("ParseCredentialReference(%q) accepted %q", raw, reference)
		}
	}
	if _, ok := ParseCredentialReference("env:" + strings.Repeat("A", 100) + strings.Repeat("B", 100) + strings.Repeat("C", 100) + strings.Repeat("D", 100) + strings.Repeat("E", 100) + strings.Repeat("F", 20)); ok {
		t.Fatal("overlong reference accepted")
	}
}

func TestValidateChangeRequestReferenceRequiresHostIDForOpenAPI(t *testing.T) {
	base := ChangeRequestReference{
		Provider:             ChangeProviderOpenAPI,
		DisplayOrigin:        "https://review.internal",
		TargetRepositorySlug: "team/repo",
		DisplayNumber:        "1234",
		NormalizedURL:        "https://review.internal/projects/team/repo/pulls/1234",
	}
	if validation := ValidateChangeRequestReference(base); validation.OK() {
		t.Fatal("openapi reference without host_id passed validation")
	}
	withHost := base
	withHost.HostID = "review-company-internal"
	if validation := ValidateChangeRequestReference(withHost); !validation.OK() {
		t.Fatalf("openapi reference with host_id failed validation: %+v", validation.Issues)
	}
	builtIn := ChangeRequestReference{
		Provider:             ChangeProviderGitHub,
		HostID:               "host-github-public",
		DisplayOrigin:        "https://github.com",
		TargetRepositorySlug: "acme/widgets",
		DisplayNumber:        "42",
		NormalizedURL:        "https://github.com/acme/widgets/pull/42",
	}
	if validation := ValidateChangeRequestReference(builtIn); !validation.OK() {
		t.Fatalf("github reference with fixed host_id failed validation: %+v", validation.Issues)
	}
}

func TestIsKnownChangeProviderKindIncludesOpenAPI(t *testing.T) {
	if !IsKnownChangeProviderKind(ChangeProviderOpenAPI) {
		t.Fatal("openapi must be a known change provider kind")
	}
	if ChangeProviderOpenAPI != "openapi" {
		t.Fatalf("openapi kind string drifted: %q", ChangeProviderOpenAPI)
	}
}
