package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func parserTestReference() ReferenceTemplate {
	return ReferenceTemplate{
		Origin:              "https://review.internal",
		PathTemplate:        "/projects/{repository}/reviews/{number}",
		RepositoryParameter: "repository",
		NumberParameter:     "number",
	}
}

func TestMatchReferenceTemplate(t *testing.T) {
	reference := parserTestReference()
	parsed, ok := MatchReferenceTemplate(reference, "review-host", "https://review.internal/projects/team/sub/repo/reviews/1234")
	if !ok {
		t.Fatal("valid change URL rejected")
	}
	if parsed.Provider != model.ChangeProviderOpenAPI || parsed.HostID != "review-host" ||
		parsed.TargetRepositorySlug != "team/sub/repo" || parsed.DisplayNumber != "1234" {
		t.Fatalf("parsed reference wrong: %+v", parsed)
	}
	if parsed.NormalizedURL != "https://review.internal/projects/team/sub/repo/reviews/1234" {
		t.Fatalf("normalized URL: %q", parsed.NormalizedURL)
	}

	// Query strings are stripped from the normalized form but a query token
	// never reaches the reference.
	parsed, ok = MatchReferenceTemplate(reference, "review-host", "https://review.internal/projects/team/repo/reviews/99?token=secret&tab=diff")
	if !ok {
		t.Fatal("URL with query must still parse (query is dropped)")
	}
	if strings.Contains(parsed.NormalizedURL, "token") || parsed.DisplayNumber != "99" {
		t.Fatalf("query leaked into the reference: %+v", parsed)
	}

	for _, raw := range []string{
		"http://review.internal/projects/team/repo/reviews/1",       // wrong scheme
		"https://other.internal/projects/team/repo/reviews/1",       // wrong host
		"https://review.internal/projects/team/repo/pulls/1",        // literal mismatch
		"https://user@review.internal/projects/team/repo/reviews/1", // userinfo
		"https://review.internal/projects/team/repo/reviews/1#frag",
		"https://review.internal/projects/team/repo/reviews",
		"https://review.internal/projects//reviews/1",
		"https://review.internal/projects/../reviews/1",
	} {
		if _, ok := MatchReferenceTemplate(reference, "review-host", raw); ok {
			t.Fatalf("unsafe or mismatching URL accepted: %q", raw)
		}
	}
}

func TestMatchReferenceTemplatePercentDecodesOnlyParameters(t *testing.T) {
	reference := parserTestReference()
	parsed, ok := MatchReferenceTemplate(reference, "h", "https://review.internal/projects/te%20am/repo/reviews/12")
	if !ok {
		t.Fatal("escaped repository segment rejected")
	}
	if parsed.TargetRepositorySlug != "te am/repo" {
		t.Fatalf("parameter decoding wrong: %q", parsed.TargetRepositorySlug)
	}
	if parsed.NormalizedURL != "https://review.internal/projects/te%20am/repo/reviews/12" {
		t.Fatalf("normalized URL must re-escape the value: %q", parsed.NormalizedURL)
	}
}

func decodeDoc(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestEvalPointerAndSelector(t *testing.T) {
	doc := decodeDoc(t, `{"source": {"latestCommit": {"id": "0123456789abcdef0123456789abcdef01234567"}}, "id": 42, "state": "OPEN"}`)
	sha, err := EvalSelector(doc, FieldSelector{Pointer: "/source/latestCommit/id", Transform: &FieldTransform{Name: TransformGitSHA}})
	if err != nil || sha != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("head sha: %q %v", sha, err)
	}
	id, err := EvalSelector(doc, FieldSelector{Pointer: "/id", Transform: &FieldTransform{Name: TransformIntegerToStr}})
	if err != nil || id != "42" {
		t.Fatalf("id: %q %v", id, err)
	}
	lower, err := EvalSelector(doc, FieldSelector{Pointer: "/state", Transform: &FieldTransform{Name: TransformLowercase}})
	if err != nil || lower != "open" {
		t.Fatalf("lowercase: %q %v", lower, err)
	}
	if _, err := EvalSelector(doc, FieldSelector{Pointer: "/missing"}); err == nil {
		t.Fatal("missing path must error (drift signal)")
	}
	if _, err := EvalSelector(doc, FieldSelector{Pointer: "/source"}); err == nil {
		t.Fatal("non-scalar value must error")
	}
}

func TestApplyTransformSet(t *testing.T) {
	cases := []struct {
		name      string
		value     any
		transform FieldTransform
		want      string
		wantErr   bool
	}{
		{"boolean", true, FieldTransform{Name: TransformBoolean}, "true", false},
		{"rfc3339", "2026-08-20T10:00:00+02:00", FieldTransform{Name: TransformRFC3339Time}, "2026-08-20T08:00:00Z", false},
		{"unix time", float64(1755703200), FieldTransform{Name: TransformUnixTime}, "2025-08-20T15:20:00Z", false},
		{"enum map hit", "opened", FieldTransform{Name: TransformEnumMap, Mapping: map[string]string{"opened": "open"}}, "open", false},
		{"enum map miss", "???", FieldTransform{Name: TransformEnumMap, Mapping: map[string]string{"opened": "open"}}, "", true},
		{"change status fold", "IN_REVIEW", FieldTransform{Name: TransformChangeStatus}, "open", false},
		{"change status unknown", "weird", FieldTransform{Name: TransformChangeStatus}, "unknown", false},
		{"file status fold", "ADD", FieldTransform{Name: TransformFileStatus}, "added", false},
		{"file status unknown is drift", "touched", FieldTransform{Name: TransformFileStatus}, "", true},
		{"repository slug trim", "/team/repo.git", FieldTransform{Name: TransformRepositorySlug}, "team/repo", false},
		{"git sha reject", "not-a-sha", FieldTransform{Name: TransformGitSHA}, "", true},
		{"coalesce null", nil, FieldTransform{Name: TransformCoalesce}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyTransform(tc.value, tc.transform, "/x")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected drift error, got %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestEvalItems(t *testing.T) {
	wrapped := decodeDoc(t, `{"values": [{"a": 1}, {"a": 2}]}`)
	items, err := EvalItems(wrapped, "/values")
	if err != nil || len(items) != 2 {
		t.Fatalf("wrapped items: %v %v", items, err)
	}
	root := decodeDoc(t, `[{"a": 1}]`)
	items, err = EvalItems(root, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("root items: %v %v", items, err)
	}
	if _, err := EvalItems(decodeDoc(t, `{"values": {"a": 1}}`), "/values"); err == nil {
		t.Fatal("non-array items must error")
	}
}

func TestMatchReferenceTemplateRejectsDecodedControlCharacters(t *testing.T) {
	reference := parserTestReference()
	for _, raw := range []string{
		"https://review.internal/projects/team/repo/reviews/12%0Aabc",
		"https://review.internal/projects/te%0Dam/repo/reviews/12",
		"https://review.internal/projects/team/repo/reviews/%00",
	} {
		if _, ok := MatchReferenceTemplate(reference, "h", raw); ok {
			t.Fatalf("control character accepted: %q", raw)
		}
	}
}

func TestExpandOperationPathUsesDeclaredParameterNames(t *testing.T) {
	// Profiles may declare non-default parameter names; expansion must follow
	// the operation's bindings, not fixed names.
	expanded, ok := ExpandOperationPath(
		"/api/repos/{repo}/changes/{id}",
		map[string]string{"repo": "reference.repository", "id": "reference.number"},
		"team/repo", "42", nil,
	)
	if !ok || expanded != "/api/repos/team/repo/changes/42" {
		t.Fatalf("declared-name expansion: %q ok=%v", expanded, ok)
	}
	// operation.* bindings resolve through the prior-operation resolver.
	expanded, ok = ExpandOperationPath(
		"/api/changes/{oid}/files",
		map[string]string{"oid": "operation.resolve_change.provider_object_id"},
		"team/repo", "42",
		func(operationID, field string) (string, bool) {
			if operationID == "resolve_change" && field == "provider_object_id" {
				return "8842", true
			}
			return "", false
		},
	)
	if !ok || expanded != "/api/changes/8842/files" {
		t.Fatalf("operation output expansion: %q ok=%v", expanded, ok)
	}
	// An unresolvable operation.* binding fails closed.
	if _, ok := ExpandOperationPath(
		"/api/changes/{oid}/files",
		map[string]string{"oid": "operation.resolve_change.provider_object_id"},
		"team/repo", "42", nil,
	); ok {
		t.Fatal("unresolvable operation binding must fail closed")
	}
	// Reference templates with custom declared names match and normalize.
	reference := ReferenceTemplate{
		Origin:              "https://review.internal",
		PathTemplate:        "/r/{repo}/c/{id}",
		RepositoryParameter: "repo",
		NumberParameter:     "id",
	}
	parsed, ok := MatchReferenceTemplate(reference, "h", "https://review.internal/r/team/repo/c/42")
	if !ok || parsed.TargetRepositorySlug != "team/repo" || parsed.DisplayNumber != "42" {
		t.Fatalf("custom parameter names rejected: %+v ok=%v", parsed, ok)
	}
	if parsed.NormalizedURL != "https://review.internal/r/team/repo/c/42" {
		t.Fatalf("normalized URL: %q", parsed.NormalizedURL)
	}
	// Traversal disguised as a change number is rejected.
	if _, ok := MatchReferenceTemplate(parserTestReference(), "h", "https://review.internal/projects/team/repo/reviews/.."); ok {
		t.Fatal("traversal change number accepted")
	}
}

func TestNormalizeOriginDropsDefaultPorts(t *testing.T) {
	cases := map[string]string{
		"https://REVIEW.internal:443":  "https://review.internal",
		"http://review.internal:80":    "http://review.internal",
		"http://127.0.0.1:46371":       "http://127.0.0.1:46371",
		"https://review.internal:8443": "https://review.internal:8443",
		"https://[2001:db8::1]:443":    "https://[2001:db8::1]",
	}
	for raw, want := range cases {
		got, ok := NormalizeOrigin(raw)
		if !ok || got != want {
			t.Fatalf("NormalizeOrigin(%q) = %q ok=%v, want %q", raw, got, ok, want)
		}
	}
}
