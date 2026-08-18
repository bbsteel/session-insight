package changehost

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

var ErrUnsafeGenericReference = errors.New("unsafe generic change reference")

// GenericParser recognizes an exact HTTPS URL whose path looks like a pull,
// merge, or review request. It does not guess a hosted provider and never
// grants network access to the referenced host.
type GenericParser struct{}

func (GenericParser) Kind() model.ChangeProviderKind { return model.ChangeProviderGeneric }

func (GenericParser) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	normalized, origin, pathValue, err := normalizeGenericURL(raw)
	if err != nil || pathValue == "/" {
		return model.ChangeRequestReference{}, false
	}
	slug, number, ok := changeRequestPathIdentity(pathValue)
	if !ok {
		return model.ChangeRequestReference{}, false
	}
	return model.ChangeRequestReference{
		Provider:             model.ChangeProviderGeneric,
		DisplayOrigin:        origin,
		TargetRepositorySlug: slug,
		DisplayNumber:        number,
		NormalizedURL:        normalized,
	}, true
}

func (GenericParser) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	normalized, origin, pathValue, err := normalizeGenericURL(raw)
	if err != nil || pathValue == "/" {
		return model.HostedRepositoryReference{}, false
	}
	return model.HostedRepositoryReference{
		Provider:        model.ChangeProviderGeneric,
		DisplayOrigin:   origin,
		Slug:            strings.TrimPrefix(pathValue, "/"),
		SanitizedRemote: normalized,
	}, true
}

// GenericIdentity derives a stable local-only identity from the exact
// sanitized URL. The opaque digest is never treated as a provider identity.
func GenericIdentity(ref model.ChangeRequestReference) (model.ChangeRequestIdentity, error) {
	if ref.Provider != model.ChangeProviderGeneric {
		return model.ChangeRequestIdentity{}, ErrUnsafeGenericReference
	}
	normalized, origin, pathValue, err := normalizeGenericURL(ref.NormalizedURL)
	if err != nil || pathValue == "/" || normalized != ref.NormalizedURL || origin != ref.DisplayOrigin {
		return model.ChangeRequestIdentity{}, ErrUnsafeGenericReference
	}
	sum := sha256.Sum256([]byte(normalized))
	return model.ChangeRequestIdentity{
		Provider:        model.ChangeProviderGeneric,
		GenericOpaqueID: "generic-" + hex.EncodeToString(sum[:]),
	}, nil
}

func normalizeGenericURL(raw string) (normalized, origin, pathValue string, err error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t") {
		return "", "", "", ErrUnsafeGenericReference
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", "", ErrUnsafeGenericReference
	}
	if u.Opaque != "" || u.ForceQuery || u.Path == "" || strings.ContainsRune(u.Path, '\x00') {
		return "", "", "", ErrUnsafeGenericReference
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", "", "", ErrUnsafeGenericReference
		}
	}
	hostname := strings.ToLower(u.Hostname())
	if hostname == "" {
		return "", "", "", ErrUnsafeGenericReference
	}
	port := u.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", "", "", ErrUnsafeGenericReference
		}
	}
	if port == "443" {
		port = ""
	}
	u.Scheme = "https"
	if port == "" {
		u.Host = hostname
		if strings.Contains(hostname, ":") {
			u.Host = "[" + hostname + "]"
		}
	} else {
		u.Host = net.JoinHostPort(hostname, port)
	}
	normalized = u.String()
	originURL := &url.URL{Scheme: u.Scheme, Host: u.Host}
	return normalized, originURL.String(), u.EscapedPath(), nil
}
