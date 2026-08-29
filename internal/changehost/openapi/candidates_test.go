package openapi

import (
	"testing"
)

func TestAnalyzeSampleURL(t *testing.T) {
	sample, ok := AnalyzeSampleURL("https://review.internal/projects/team/repo/pulls/1234")
	if !ok {
		t.Fatal("sample URL rejected")
	}
	if sample.Origin != "https://review.internal" || sample.Number != "1234" || sample.RepositorySlug != "team/repo" {
		t.Fatalf("sample analysis wrong: %+v", sample)
	}

	gitlab, ok := AnalyzeSampleURL("https://gitlab.example.com/group/sub/repo/-/merge_requests/42")
	if !ok {
		t.Fatal("gitlab-style URL rejected")
	}
	if gitlab.RepositorySlug != "group/sub/repo" || gitlab.Number != "42" {
		t.Fatalf("gitlab sample analysis wrong: %+v", gitlab)
	}

	for _, raw := range []string{
		"",
		"https://review.internal/",
		"https://review.internal/projects/team/repo/pulls", // no number
		"https://review.internal/projects/team/repo/pulls/1234?token=x",
		"https://user:pw@review.internal/projects/team/repo/pulls/1234",
		"https://review.internal/../pulls/1",
		"ftp://review.internal/pulls/1",
	} {
		if _, ok := AnalyzeSampleURL(raw); ok {
			t.Fatalf("unsafe sample URL accepted: %q", raw)
		}
	}
}

func TestScoreOperationsRanksDomainShapes(t *testing.T) {
	doc, err := ParseDocument([]byte(swagger2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	sample, ok := AnalyzeSampleURL("https://review.internal/projects/team/repo/pulls/1234")
	if !ok {
		t.Fatal("sample URL rejected")
	}
	candidates := ScoreOperations(doc, sample, "https://review.internal/api")
	if len(candidates) == 0 {
		t.Fatal("no candidates scored")
	}
	grouped := TopCandidatesPerRole(candidates)

	resolve := grouped[OperationResolveChange]
	if len(resolve) == 0 || resolve[0].Operation.ID != "getReview" {
		t.Fatalf("resolve_change winner wrong: %+v", resolve)
	}
	if resolve[0].Bindings["repository"] != "reference.repository" || resolve[0].Bindings["number"] != "reference.number" {
		t.Fatalf("bindings wrong: %+v", resolve[0].Bindings)
	}

	diff := grouped[OperationGetDiff]
	if len(diff) == 0 || diff[0].Operation.ID != "getReviewDiff" {
		t.Fatalf("get_diff winner wrong: %+v", diff)
	}

	// The commits listing in the 3.1 fixture wins list_commits.
	doc31, err := ParseDocument([]byte(openapi31Fixture))
	if err != nil {
		t.Fatal(err)
	}
	candidates31 := ScoreOperations(doc31, sample, "https://api.review.internal/v1")
	grouped31 := TopCandidatesPerRole(candidates31)
	commits := grouped31[OperationListCommits]
	if len(commits) == 0 || commits[0].Operation.ID != "listReviewCommits" {
		t.Fatalf("list_commits winner wrong: %+v", commits)
	}
	// Per-role probe plans are capped.
	for role, group := range grouped31 {
		if len(group) > maxProbeCandidatesPerRole {
			t.Fatalf("role %s exceeds the probe cap", role)
		}
	}
}

func TestScoreOperationsSkipsUnbindableOperations(t *testing.T) {
	doc, err := ParseDocument([]byte(`{
	  "openapi": "3.0.3",
	  "paths": {
	    "/reviews/{number}/secret/{token}": {
	      "get": {
	        "operationId": "getReviewWithTokenPath",
	        "parameters": [
	          {"name": "number", "in": "path", "required": true, "schema": {"type": "integer"}},
	          {"name": "token", "in": "path", "required": true, "schema": {"type": "string"}}
	        ],
	        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {
	          "type": "object", "properties": {"title": {"type": "string"}, "state": {"type": "string"}, "id": {"type": "integer"}}
	        }}}}}
	      }
	    }
	  }
	}`))
	if err != nil {
		t.Fatal(err)
	}
	sample, _ := AnalyzeSampleURL("https://review.internal/pulls/1234")
	for _, candidate := range ScoreOperations(doc, sample, "https://review.internal") {
		if candidate.Operation.ID == "getReviewWithTokenPath" {
			t.Fatal("operation with an unbindable required path parameter must not be probed")
		}
	}
}
