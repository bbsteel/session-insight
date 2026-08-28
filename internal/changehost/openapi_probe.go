package changehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
)

// openapi_probe.go: the read-only probe executor (design §7.4). It runs the
// top-scored operation candidates against the approved host and feeds the
// responses into the pure inference engine. Probe responses live in memory
// only; the report carries pointers, shapes, and confidence — never raw
// values, tokens, or full URLs.

// ProbeOpenAPICandidates runs the probe plan for every role. The client is
// already scoped to the approved host and carries the profile's credential;
// candidates whose path parameters cannot bind were filtered out during
// scoring.
func ProbeOpenAPICandidates(
	ctx context.Context,
	client *HTTPClient,
	grouped map[openapi.OperationID][]openapi.OperationCandidate,
	sample openapi.SampleReference,
	inferenceContext openapi.InferenceContext,
) map[openapi.OperationID][]openapi.RoleOutcome {
	outcomes := map[openapi.OperationID][]openapi.RoleOutcome{}
	for _, role := range openapi.OperationIDs() {
		candidates := grouped[role]
		for _, candidate := range candidates {
			outcomes[role] = append(outcomes[role], probeOne(ctx, client, role, candidate, sample, inferenceContext))
		}
	}
	return outcomes
}

func probeOne(
	ctx context.Context,
	client *HTTPClient,
	role openapi.OperationID,
	candidate openapi.OperationCandidate,
	sample openapi.SampleReference,
	inferenceContext openapi.InferenceContext,
) openapi.RoleOutcome {
	outcome := openapi.RoleOutcome{Candidate: candidate}
	requestURL, err := BuildProbeURL(candidate, sample)
	if err != nil {
		outcome.RejectReason = string(openapi.IssueProfileContractInvalid)
		return outcome
	}
	result, err := client.Do(ctx, OperationGetSnapshot, "GET", requestURL, nil)
	if err != nil {
		var providerError *Error
		if errors.As(err, &providerError) {
			outcome.StatusCode = statusCodeForError(providerError.Code)
		}
		outcome.RejectReason = string(openapi.IssueProbeFailed)
		return outcome
	}
	outcome.StatusCode = result.StatusCode

	if role == openapi.OperationGetDiff {
		body := strings.TrimSpace(string(result.Body))
		if strings.HasPrefix(body, "diff --git") || strings.Contains(body, "\n@@ ") || strings.HasPrefix(body, "@@ ") {
			return outcome
		}
		outcome.RejectReason = string(openapi.IssueProbeFailed)
		return outcome
	}

	var body any
	if err := json.Unmarshal(result.Body, &body); err != nil {
		outcome.RejectReason = string(openapi.IssueProbeFailed)
		return outcome
	}
	if strings.Contains(result.Link, `rel="next"`) {
		outcome.LinkNext = true
	}
	switch role {
	case openapi.OperationResolveChange, openapi.OperationResolveRepository:
		outcome.Fields = openapi.InferChangeFields(body, inferenceContext)
	case openapi.OperationListFiles, openapi.OperationListCommits:
		itemsPointer, fields := openapi.InferListFields(body, role, inferenceContext)
		outcome.ItemsPointer = itemsPointer
		outcome.Fields = fields
		if itemsPointer == "" && !isRootArray(body) {
			outcome.RejectReason = string(openapi.IssueMappingIncomplete)
			return outcome
		}
		outcome.CursorPointer, outcome.CursorParam = detectCursorPagination(body)
	}
	return outcome
}

// BuildProbeURL substitutes the sample reference values into the candidate's
// path template. Repository slugs keep their inner slashes; every segment is
// percent-escaped individually.
func BuildProbeURL(candidate openapi.OperationCandidate, sample openapi.SampleReference) (string, error) {
	base := strings.TrimSuffix(candidate.BaseURL, "/")
	if base == "" {
		return "", fmt.Errorf("probe candidate has no base URL")
	}
	path := candidate.Operation.Path
	for name, binding := range candidate.Bindings {
		var value string
		switch binding {
		case "reference.repository":
			value = escapeSlugSegments(sample.RepositorySlug)
		case "reference.number":
			value = sample.Number
		default:
			continue
		}
		path = strings.ReplaceAll(path, "{"+name+"}", value)
	}
	if strings.ContainsAny(path, "{}") {
		return "", fmt.Errorf("probe path still has unbound parameters")
	}
	parsed, err := url.Parse(base + path)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("probe URL is invalid")
	}
	return parsed.String(), nil
}

// escapeSlugSegments escapes each slug segment without flattening the
// hierarchy separators.
func escapeSlugSegments(slug string) string {
	segments := strings.Split(slug, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func isRootArray(body any) bool {
	_, ok := body.([]any)
	return ok
}

// detectCursorPagination looks for a next-cursor field beside the items
// array in a wrapper response.
func detectCursorPagination(body any) (pointer string, parameter string) {
	object, ok := body.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, key := range []string{"next_cursor", "nextCursor", "next", "cursor", "nextPageCursor"} {
		if value, ok := object[key].(string); ok && value != "" {
			return "/" + key, "cursor"
		}
	}
	return "", ""
}

func statusCodeForError(code ErrorCode) int {
	switch code {
	case ErrorAuthRequired:
		return 401
	case ErrorNotFound:
		return 404
	case ErrorRateLimited:
		return 429
	case ErrorUnavailable:
		return 503
	default:
		return 0
	}
}
