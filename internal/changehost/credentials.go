package changehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

var (
	// ErrCredentialUnavailable is returned when a credential reference cannot
	// be resolved to a secret (missing keyring entry, unset variable).
	ErrCredentialUnavailable = errors.New("change host credential unavailable")
	// ErrInvalidAuthenticationScheme rejects a header/prefix pair outside the
	// reviewed credential-injection contract.
	ErrInvalidAuthenticationScheme = errors.New("invalid change host authentication scheme")
)

// CredentialSource resolves a validated credential reference to the raw
// secret. Sources return only the secret: the verified profile — never the
// source — decides the header name and value prefix. Secrets must never be
// persisted, logged, or included in errors.
type CredentialSource interface {
	Secret(ctx context.Context, reference model.CredentialReference) (secret string, ok bool, err error)
}

// EnvironmentCredentialSource resolves env: references through a lookup
// function (os.LookupEnv by default, overridable in tests). keyring:
// references need a keyring-backed source and are reported as unavailable.
type EnvironmentCredentialSource struct {
	Lookup func(name string) (string, bool)
}

func (s EnvironmentCredentialSource) Secret(_ context.Context, reference model.CredentialReference) (string, bool, error) {
	if reference.Scheme() != model.CredentialSchemeEnvironment {
		return "", false, fmt.Errorf("%w: unsupported credential scheme %q", ErrCredentialUnavailable, reference.Scheme())
	}
	lookup := s.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	secret, ok := lookup(reference.Name())
	if !ok || secret == "" {
		return "", false, nil
	}
	return secret, true, nil
}

// KeyringSecretLookup reads one secret from the OS keyring. The wiring point
// is defined here so the import flow can attach a real keyring implementation
// without changing the credential contract.
type KeyringSecretLookup func(ctx context.Context, key string) (secret string, ok bool, err error)

// KeyringCredentialSource resolves keyring: references through a lookup the
// host integration supplies.
type KeyringCredentialSource struct {
	Lookup KeyringSecretLookup
}

func (s KeyringCredentialSource) Secret(ctx context.Context, reference model.CredentialReference) (string, bool, error) {
	if reference.Scheme() != model.CredentialSchemeKeyring {
		return "", false, fmt.Errorf("%w: unsupported credential scheme %q", ErrCredentialUnavailable, reference.Scheme())
	}
	if s.Lookup == nil {
		return "", false, ErrCredentialUnavailable
	}
	return s.Lookup(ctx, reference.Name())
}

// AuthenticationScheme is the reviewed header placement for a resolved
// secret. The allowed shapes are exactly the V1 contract:
//
//	Authorization: Bearer <secret> | Authorization: token <secret>
//	PRIVATE-TOKEN: <secret> | X-API-Key: <secret>
//
// plus custom security-scheme headers declared by the imported document,
// which must also be bare header tokens without a prefix.
type AuthenticationScheme struct {
	HeaderName  string
	ValuePrefix string
}

// ValidateAuthenticationScheme enforces the reviewed header/prefix pairs.
func ValidateAuthenticationScheme(scheme AuthenticationScheme) error {
	if scheme.HeaderName == "" || !isTokenHeaderName(scheme.HeaderName) {
		return ErrInvalidAuthenticationScheme
	}
	if strings.ContainsAny(scheme.ValuePrefix, "\r\n") || len(scheme.ValuePrefix) > 32 {
		return ErrInvalidAuthenticationScheme
	}
	switch {
	case strings.EqualFold(scheme.HeaderName, "Authorization"):
		if scheme.ValuePrefix != "Bearer " && scheme.ValuePrefix != "token " {
			return ErrInvalidAuthenticationScheme
		}
	case strings.EqualFold(scheme.HeaderName, "Cookie"),
		strings.EqualFold(scheme.HeaderName, "Host"),
		strings.EqualFold(scheme.HeaderName, "Content-Length"):
		return ErrInvalidAuthenticationScheme
	default:
		if scheme.ValuePrefix != "" {
			return ErrInvalidAuthenticationScheme
		}
	}
	return nil
}

// ResolvedAuthentication binds a credential source and reference to the
// header scheme a verified profile declared.
type ResolvedAuthentication struct {
	Source    CredentialSource
	Reference model.CredentialReference
	Scheme    AuthenticationScheme
	// AllowedOrigins is the profile's explicit per-operation credential scope.
	// NewAuthenticatedHTTPClient injects credentials only for these origins.
	// An empty list is intentionally fail-closed. Credentials are never injected
	// on plain-HTTP requests regardless.
	AllowedOrigins []string
}

// validateResolvedAuthentication rejects partial or invalid bindings before a
// client is constructed.
func validateResolvedAuthentication(authentication ResolvedAuthentication) error {
	if authentication.Source == nil {
		return ErrInvalidAuthenticationScheme
	}
	if _, ok := model.ParseCredentialReference(string(authentication.Reference)); !ok {
		return ErrInvalidAuthenticationScheme
	}
	for _, origin := range authentication.AllowedOrigins {
		if _, ok := canonicalCredentialOrigin(origin); !ok {
			return ErrInvalidAuthenticationScheme
		}
	}
	return ValidateAuthenticationScheme(authentication.Scheme)
}

// allowsCredentialOrigin reports whether the credential may be injected for
// one request origin: HTTPS only, and within the declared allowlist.
func allowsCredentialOrigin(authentication *clientAuthentication, origin string) bool {
	if authentication == nil {
		return false
	}
	if strings.HasPrefix(origin, "http://") {
		return false
	}
	if authentication.allowAnyApprovedOrigin {
		return true
	}
	for _, allowed := range authentication.allowedOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

// canonicalCredentialOrigin normalizes an origin for allowlist comparison,
// matching the HTTP client's origin derivation.
func canonicalCredentialOrigin(raw string) (string, bool) {
	origin, err := endpointOrigin(raw)
	if err != nil {
		return "", false
	}
	return origin, true
}

// AuthenticationModeForReference projects the credential store kind into the
// existing authentication-mode vocabulary. The reference itself stays out of
// every DTO; only the mode and a configured flag may be exposed.
func AuthenticationModeForReference(reference model.CredentialReference) (AuthenticationMode, bool) {
	switch reference.Scheme() {
	case model.CredentialSchemeEnvironment:
		return AuthTokenEnvironment, true
	case model.CredentialSchemeKeyring:
		return AuthOSKeyring, true
	default:
		return "", false
	}
}

func isTokenHeaderName(name string) bool {
	if len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_|~", r):
		default:
			return false
		}
	}
	return true
}
