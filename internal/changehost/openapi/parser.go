package openapi

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/bbsteel/session-insight/internal/model"
)

// parser.go: profile-driven change-URL parsing (design §5.2). Matching is
// exact on scheme/host/port and literal segments; only declared parameter
// segments are percent-decoded. Userinfo, fragments, traversal, control
// characters, and query payloads are rejected or stripped, so the normalized
// reference never carries token material.

// MatchReferenceTemplate parses one change URL against the profile's
// reference template. The repository placeholder spans one or more path
// segments (slugs keep their hierarchy); literal head/tail segments match
// exactly. The returned reference carries the profile host ID and never any
// query or fragment data.
func MatchReferenceTemplate(reference ReferenceTemplate, hostID, raw string) (model.ChangeRequestReference, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t\x00") {
		return model.ChangeRequestReference{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return model.ChangeRequestReference{}, false
	}
	origin, ok := normalizeOriginValue(reference.Origin)
	if !ok {
		return model.ChangeRequestReference{}, false
	}
	rawOrigin, ok := normalizeOriginValue((&url.URL{Scheme: u.Scheme, Host: u.Host}).String())
	if !ok || rawOrigin != origin {
		return model.ChangeRequestReference{}, false
	}

	templateSegments := strings.Split(strings.Trim(reference.PathTemplate, "/"), "/")
	trimmedPath := strings.Trim(u.EscapedPath(), "/")
	if trimmedPath == "" {
		return model.ChangeRequestReference{}, false
	}
	urlSegments := strings.Split(trimmedPath, "/")

	repositoryPlaceholder := "{" + reference.RepositoryParameter + "}"
	repositoryIndex := -1
	for i, segment := range templateSegments {
		if segment == repositoryPlaceholder {
			if repositoryIndex >= 0 {
				return model.ChangeRequestReference{}, false
			}
			repositoryIndex = i
		}
	}
	if repositoryIndex < 0 {
		return model.ChangeRequestReference{}, false
	}
	headLength := repositoryIndex
	tailLength := len(templateSegments) - repositoryIndex - 1
	if len(urlSegments) < headLength+tailLength+1 {
		return model.ChangeRequestReference{}, false
	}

	number := ""
	matchFixed := func(templateSegment, urlSegment string) bool {
		if strings.HasPrefix(templateSegment, "{") && strings.HasSuffix(templateSegment, "}") {
			name := templateSegment[1 : len(templateSegment)-1]
			if name != reference.NumberParameter {
				return false
			}
			decoded, err := url.PathUnescape(urlSegment)
			if err != nil || !isSafeParameterValue(decoded) {
				return false
			}
			number = decoded
			return true
		}
		// Literal segments match exactly — no decoding, no case folding.
		return templateSegment == urlSegment
	}
	for i := 0; i < headLength; i++ {
		if !matchFixed(templateSegments[i], urlSegments[i]) {
			return model.ChangeRequestReference{}, false
		}
	}
	for i := 0; i < tailLength; i++ {
		if !matchFixed(templateSegments[repositoryIndex+1+i], urlSegments[len(urlSegments)-tailLength+i]) {
			return model.ChangeRequestReference{}, false
		}
	}

	repositorySegments := urlSegments[headLength : len(urlSegments)-tailLength]
	decodedRepository := make([]string, 0, len(repositorySegments))
	for _, segment := range repositorySegments {
		decoded, err := url.PathUnescape(segment)
		if err != nil || !isSafeParameterValue(decoded) || decoded == "." || decoded == ".." {
			return model.ChangeRequestReference{}, false
		}
		decodedRepository = append(decodedRepository, decoded)
	}
	repository := strings.Join(decodedRepository, "/")
	if number == "" {
		return model.ChangeRequestReference{}, false
	}
	// The normalized URL carries the actual values, canonicalized through the
	// template — never query, fragment, or unrelated path data.
	expanded, ok := ExpandReferenceParameters(reference.PathTemplate, reference.RepositoryParameter, reference.NumberParameter, repository, number)
	if !ok {
		return model.ChangeRequestReference{}, false
	}
	normalized := origin + expanded
	return model.ChangeRequestReference{
		Provider:             model.ChangeProviderOpenAPI,
		HostID:               hostID,
		DisplayOrigin:        origin,
		TargetRepositorySlug: repository,
		DisplayNumber:        number,
		NormalizedURL:        normalized,
	}, true
}

// isSafeParameterValue rejects empty values, slashes, and any control
// character — decoded percent escapes must not smuggle newlines or NULs into
// persisted references.
func isSafeParameterValue(value string) bool {
	if value == "" || strings.Contains(value, "/") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ExpandReferenceParameters substitutes reference values into a path
// template, using the profile's declared parameter names. Repository slugs
// keep their hierarchy: each segment is escaped individually. Every declared
// placeholder must resolve; unbound placeholders fail closed.
func ExpandReferenceParameters(pathTemplate, repositoryParameter, numberParameter, repository, number string) (string, bool) {
	result := pathTemplate
	replacements := map[string]string{
		repositoryParameter: escapePathSegments(repository),
		numberParameter:     url.PathEscape(number),
	}
	for name, value := range replacements {
		result = strings.ReplaceAll(result, "{"+name+"}", value)
	}
	if strings.ContainsAny(result, "{}") {
		return "", false
	}
	return result, true
}

// ExpandOperationPath substitutes values into an operation path template via
// the operation's parameter bindings: each {placeholder} name resolves
// through its binding (reference.repository / reference.number). operation.*
// bindings need a prior operation's output and are not resolvable here.
func ExpandOperationPath(pathTemplate string, parameters map[string]string, repository, number string) (string, bool) {
	result := pathTemplate
	for name, binding := range parameters {
		var value string
		switch binding {
		case "reference.repository":
			value = escapePathSegments(repository)
		case "reference.number":
			value = url.PathEscape(number)
		default:
			continue
		}
		result = strings.ReplaceAll(result, "{"+name+"}", value)
	}
	if strings.ContainsAny(result, "{}") {
		return "", false
	}
	return result, true
}

func escapePathSegments(value string) string {
	segments := strings.Split(value, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
