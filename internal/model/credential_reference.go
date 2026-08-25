package model

import "strings"

// CredentialReferenceScheme identifies where a secret lives. The reference
// itself is safe to persist; the secret it points to is never stored in the
// database, logged, or projected into API DTOs.
type CredentialReferenceScheme string

const (
	// CredentialSchemeKeyring names an OS keyring entry, e.g.
	// "keyring:profile-01J...". The key is opaque to SessionInsight.
	CredentialSchemeKeyring CredentialReferenceScheme = "keyring"
	// CredentialSchemeEnvironment names an environment variable, e.g.
	// "env:INTERNAL_REVIEW_TOKEN".
	CredentialSchemeEnvironment CredentialReferenceScheme = "env"
)

const (
	credentialSchemeKeyringPrefix = "keyring:"
	credentialSchemeEnvPrefix     = "env:"
	maxCredentialReferenceLength  = 512
)

// CredentialReference is a validated, persistable pointer to a secret. It
// carries no secret material and therefore may appear in profile storage, but
// must still not be projected into credential-safe API DTOs.
type CredentialReference string

// ParseCredentialReference validates a keyring:/env: reference. Empty input is
// invalid: callers represent "no credential" with the empty CredentialReference
// value and never parse one.
func ParseCredentialReference(raw string) (CredentialReference, bool) {
	if raw == "" || len(raw) > maxCredentialReferenceLength {
		return "", false
	}
	if strings.TrimSpace(raw) != raw || strings.ContainsRune(raw, '\x00') {
		return "", false
	}
	if name, ok := strings.CutPrefix(raw, credentialSchemeEnvPrefix); ok {
		if !validEnvironmentVariableName(name) {
			return "", false
		}
		return CredentialReference(raw), true
	}
	if key, ok := strings.CutPrefix(raw, credentialSchemeKeyringPrefix); ok {
		if !validKeyringKey(key) {
			return "", false
		}
		return CredentialReference(raw), true
	}
	return "", false
}

// Scheme returns which credential store the reference points at.
func (r CredentialReference) Scheme() CredentialReferenceScheme {
	raw := string(r)
	if strings.HasPrefix(raw, credentialSchemeEnvPrefix) {
		return CredentialSchemeEnvironment
	}
	if strings.HasPrefix(raw, credentialSchemeKeyringPrefix) {
		return CredentialSchemeKeyring
	}
	return ""
}

// Name returns the scheme-specific key (environment variable name or keyring
// key). It is still reference metadata, not secret material.
func (r CredentialReference) Name() string {
	raw := string(r)
	if name, ok := strings.CutPrefix(raw, credentialSchemeEnvPrefix); ok {
		return name
	}
	if key, ok := strings.CutPrefix(raw, credentialSchemeKeyringPrefix); ok {
		return key
	}
	return ""
}

// validEnvironmentVariableName enforces the portable subset so a reference
// cannot smuggle shell syntax into later diagnostics or tooling.
func validEnvironmentVariableName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// validKeyringKey accepts the printable, path-free subset used for profile
// credential keys (for example "profile-01J...").
func validKeyringKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}
