package changehost

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	publicGitHubOrigin = "https://github.com"
	publicGitLabOrigin = "https://gitlab.com"
)

// PublicGitHubHost returns the fixed public SaaS origin set. The separate API
// origin is explicit so approval cannot silently expand when requests begin.
func PublicGitHubHost() HostIdentity {
	return HostIdentity{
		Key: "host-github-public", Provider: model.ChangeProviderGitHub,
		DisplayOrigin:   publicGitHubOrigin,
		EndpointOrigins: []string{publicGitHubOrigin, "https://api.github.com"},
	}
}

// PublicGitLabHost uses one origin for both web and /api/v4 endpoints.
func PublicGitLabHost() HostIdentity {
	return HostIdentity{
		Key: "host-gitlab-public", Provider: model.ChangeProviderGitLab,
		DisplayOrigin:   publicGitLabOrigin,
		EndpointOrigins: []string{publicGitLabOrigin},
	}
}

type GitHubParser struct{}

func (GitHubParser) Kind() model.ChangeProviderKind { return model.ChangeProviderGitHub }

func (GitHubParser) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	segments, ok := parsePublicWebPath(raw, "github.com")
	if !ok || len(segments) != 4 || segments[2] != "pull" {
		return model.ChangeRequestReference{}, false
	}
	number, ok := positiveDisplayNumber(segments[3])
	if !ok {
		return model.ChangeRequestReference{}, false
	}
	slug := strings.Join(segments[:2], "/")
	return model.ChangeRequestReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: publicGitHubOrigin,
		TargetRepositorySlug: slug, DisplayNumber: number,
		NormalizedURL: publicGitHubOrigin + "/" + slug + "/pull/" + number,
	}, true
}

func (GitHubParser) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	slug, ok := parsePublicGitRemote(raw, "github.com")
	if !ok || len(strings.Split(slug, "/")) != 2 {
		return model.HostedRepositoryReference{}, false
	}
	return model.HostedRepositoryReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: publicGitHubOrigin,
		Slug: slug, SanitizedRemote: publicGitHubOrigin + "/" + slug,
	}, true
}

type GitLabParser struct{}

func (GitLabParser) Kind() model.ChangeProviderKind { return model.ChangeProviderGitLab }

func (GitLabParser) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	segments, ok := parsePublicWebPath(raw, "gitlab.com")
	if !ok || len(segments) < 5 {
		return model.ChangeRequestReference{}, false
	}
	marker := len(segments) - 3
	if segments[marker] != "-" || segments[marker+1] != "merge_requests" {
		return model.ChangeRequestReference{}, false
	}
	number, ok := positiveDisplayNumber(segments[marker+2])
	if !ok {
		return model.ChangeRequestReference{}, false
	}
	slug := strings.Join(segments[:marker], "/")
	return model.ChangeRequestReference{
		Provider: model.ChangeProviderGitLab, DisplayOrigin: publicGitLabOrigin,
		TargetRepositorySlug: slug, DisplayNumber: number,
		NormalizedURL: publicGitLabOrigin + "/" + slug + "/-/merge_requests/" + number,
	}, true
}

func (GitLabParser) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	slug, ok := parsePublicGitRemote(raw, "gitlab.com")
	if !ok || len(strings.Split(slug, "/")) < 2 {
		return model.HostedRepositoryReference{}, false
	}
	return model.HostedRepositoryReference{
		Provider: model.ChangeProviderGitLab, DisplayOrigin: publicGitLabOrigin,
		Slug: slug, SanitizedRemote: publicGitLabOrigin + "/" + slug,
	}, true
}

func parsePublicWebPath(raw, hostname string) ([]string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t") {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), hostname) ||
		parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Opaque != "" || parsed.ForceQuery || parsed.RawPath != "" {
		return nil, false
	}
	return safeRepositorySegments(parsed.Path)
}

func parsePublicGitRemote(raw, hostname string) (string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t") {
		return "", false
	}
	var pathValue string
	if strings.HasPrefix(raw, "git@"+hostname+":") {
		pathValue = strings.TrimPrefix(raw, "git@"+hostname+":")
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(parsed.Hostname(), hostname) || parsed.RawQuery != "" ||
			parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery || parsed.RawPath != "" {
			return "", false
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil || parsed.Port() != "" {
				return "", false
			}
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return "", false
			}
			if password, present := parsed.User.Password(); present || password != "" {
				return "", false
			}
			if parsed.Port() != "" && parsed.Port() != "22" {
				return "", false
			}
		default:
			return "", false
		}
		pathValue = strings.TrimPrefix(parsed.Path, "/")
	}
	pathValue = strings.TrimSuffix(strings.TrimSuffix(pathValue, "/"), ".git")
	segments, ok := safeRepositorySegments("/" + pathValue)
	if !ok || len(segments) < 2 {
		return "", false
	}
	return strings.Join(segments, "/"), true
}

func safeRepositorySegments(pathValue string) ([]string, bool) {
	if pathValue == "" || pathValue == "/" || strings.ContainsRune(pathValue, '\x00') {
		return nil, false
	}
	trimmed := strings.Trim(pathValue, "/")
	if trimmed == "" || len(trimmed) > 4096 {
		return nil, false
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || len(segment) > 255 {
			return nil, false
		}
	}
	return segments, true
}

func positiveDisplayNumber(raw string) (string, bool) {
	if raw == "" || strings.HasPrefix(raw, "+") || strings.HasPrefix(raw, "0") && raw != "0" {
		return "", false
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 1 {
		return "", false
	}
	return strconv.FormatInt(number, 10), true
}
