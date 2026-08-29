package openapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeJSON(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func inferenceTestContext() InferenceContext {
	return InferenceContext{
		SampleNumber:         "1234",
		SampleRepositorySlug: "team/repo",
		DisplayOrigin:        "https://review.internal",
	}
}

func candidateFor(candidates []FieldCandidate, field string) *FieldCandidate {
	for i := range candidates {
		if candidates[i].Field == field {
			return &candidates[i]
		}
	}
	return nil
}

func TestInferChangeFields(t *testing.T) {
	response := decodeJSON(t, `{
		"id": 8842,
		"number": 1234,
		"title": "Add retry budget",
		"state": "open",
		"web_url": "https://review.internal/projects/team/repo/pulls/1234",
		"repository": {"slug": "team/repo", "id": 77},
		"source": {"latestCommit": "`+"0123456789abcdef0123456789abcdef01234567"+`", "branch": "feature/retry"},
		"destination": {"branch": "main"},
		"updated_at": "2026-08-20T10:00:00Z"
	}`)
	candidates := InferChangeFields(response, inferenceTestContext())

	title := candidateFor(candidates, "title")
	if title == nil || title.Pointer != "/title" || title.Confidence < ConfidenceAutoPick {
		t.Fatalf("title: %+v", title)
	}
	number := candidateFor(candidates, "display_number")
	if number == nil || number.Pointer != "/number" || number.Confidence < ConfidenceAutoPick {
		t.Fatalf("display_number: %+v", number)
	}
	state := candidateFor(candidates, "lifecycle_state")
	if state == nil || state.Confidence < ConfidenceAutoPick || state.Transform == nil || state.Transform.Name != TransformChangeStatus {
		t.Fatalf("lifecycle_state: %+v", state)
	}
	head := candidateFor(candidates, "head_sha")
	if head == nil || head.Pointer != "/source/latestCommit" || head.Confidence < ConfidenceAutoPick {
		t.Fatalf("head_sha: %+v", head)
	}
	webURL := candidateFor(candidates, "web_url")
	if webURL == nil || webURL.Confidence < ConfidenceAutoPick {
		t.Fatalf("web_url: %+v", webURL)
	}
	slug := candidateFor(candidates, "target_repository_slug")
	if slug == nil || slug.Confidence < ConfidenceAutoPick {
		t.Fatalf("target_repository_slug: %+v", slug)
	}
	// updated_at must never reach a confident content-anchor mapping.
	native := candidateFor(candidates, "native_version")
	if native != nil && native.Confidence >= ConfidenceConfirmPick {
		t.Fatalf("updated_at must not mint a confident content version: %+v", native)
	}
}

func TestInferChangeFieldsCrossValidationDiscreditsWrongValues(t *testing.T) {
	// The "number" field holds a value that does not match the sample URL.
	response := decodeJSON(t, `{"number": 9999, "title": "x", "state": "open"}`)
	candidates := InferChangeFields(response, inferenceTestContext())
	if number := candidateFor(candidates, "display_number"); number != nil {
		t.Fatalf("non-matching number must not be a display_number candidate: %+v", number)
	}
}

func TestInferListFields(t *testing.T) {
	files := decodeJSON(t, `{"values": [
		{"path": "src/main.go", "status": "modified", "diff": "@@ -1 +1 @@", "oldMode": "100644", "newMode": "100755"},
		{"path": "src/util.go", "status": "added", "diff": "@@ -0,0 +1 @@"}
	]}`)
	itemsPointer, candidates := InferListFields(files, OperationListFiles, inferenceTestContext())
	if itemsPointer != "/values" {
		t.Fatalf("items pointer: %q", itemsPointer)
	}
	path := candidateFor(candidates, "path")
	if path == nil || path.Pointer != "/path" || path.Confidence < ConfidenceAutoPick {
		t.Fatalf("path: %+v", path)
	}
	status := candidateFor(candidates, "status")
	if status == nil || status.Confidence < ConfidenceAutoPick || status.Transform.Name != TransformFileStatus {
		t.Fatalf("status: %+v", status)
	}
	// newMode exists only on the first element: intersection drops it.
	if mode := candidateFor(candidates, "new_mode"); mode != nil {
		t.Fatalf("partially-present field must not survive intersection: %+v", mode)
	}

	commits := decodeJSON(t, `[
		{"sha": "`+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"+`", "message": "first", "author": "Ana", "authoredAt": "2026-08-01T09:00:00Z"},
		{"sha": "`+"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"+`", "message": "second", "author": "Ben", "authoredAt": "2026-08-02T09:00:00Z"}
	]`)
	itemsPointer, candidates = InferListFields(commits, OperationListCommits, inferenceTestContext())
	if itemsPointer != "" {
		t.Fatalf("root array items pointer must be empty, got %q", itemsPointer)
	}
	sha := candidateFor(candidates, "sha")
	if sha == nil || sha.Confidence < ConfidenceAutoPick || sha.Transform.Name != TransformGitSHA {
		t.Fatalf("sha: %+v", sha)
	}
	subject := candidateFor(candidates, "subject")
	if subject == nil || subject.Pointer != "/message" {
		t.Fatalf("subject: %+v", subject)
	}
}

func TestInferListFieldsRejectsNonListResponses(t *testing.T) {
	itemsPointer, candidates := InferListFields(decodeJSON(t, `{"id": 1}`), OperationListFiles, inferenceTestContext())
	if itemsPointer != "" || candidates != nil {
		t.Fatal("single object must not produce list fields")
	}
}

func TestFieldCandidatesAreSanitized(t *testing.T) {
	response := decodeJSON(t, `{"title": "secret project name", "state": "open"}`)
	candidates := InferChangeFields(response, inferenceTestContext())
	encoded, err := json.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsRawValue(string(encoded), "secret project name") {
		t.Fatalf("candidates must never embed raw response values: %s", encoded)
	}
}

func containsRawValue(encoded, raw string) bool {
	return raw != "" && strings.Contains(encoded, raw)
}
