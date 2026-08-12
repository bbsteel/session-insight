package changehost

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	gitHubTestBaseSHA = "1111111111111111111111111111111111111111"
	gitHubTestHeadSHA = "2222222222222222222222222222222222222222"
	gitHubTestCommit  = "3333333333333333333333333333333333333333"
)

func TestGitHubProviderCombinesFilesAndRawDiffIntoExactSnapshot(t *testing.T) {
	provider := testGitHubProvider(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repositories/10":
			return response(request, http.StatusOK, `{"id":10,"node_id":"R_target","full_name":"acme/widgets"}`, nil), nil
		case request.URL.Path == "/repos/acme/widgets/pulls/42" && request.Header.Get("Accept") == "application/vnd.github.diff":
			return response(request, http.StatusOK, githubRawDiffFixture(), nil), nil
		case request.URL.Path == "/repos/acme/widgets/pulls/42":
			if request.Header.Get("X-GitHub-Api-Version") != gitHubAPIVersion {
				t.Fatalf("GitHub API version header = %q", request.Header.Get("X-GitHub-Api-Version"))
			}
			return response(request, http.StatusOK, githubPullFixture(gitHubTestHeadSHA), http.Header{"ETag": {"etag-pr-42"}}), nil
		case strings.HasSuffix(request.URL.Path, "/files"):
			return response(request, http.StatusOK, `[{"filename":"internal/new.go","previous_filename":"internal/old.go","status":"renamed","additions":2,"deletions":1,"patch":"@@ -1 +1 @@\n-old\n+new\n"}]`, nil), nil
		case strings.HasSuffix(request.URL.Path, "/commits"):
			return response(request, http.StatusOK, `[{"sha":"`+gitHubTestCommit+`","commit":{"message":"Add exact PR capture\n\nBody","author":{"name":"Example","date":"2026-08-11T08:00:00Z"},"committer":{"name":"Example","date":"2026-08-11T08:01:00Z"}}}]`, nil), nil
		default:
			t.Fatalf("unexpected GitHub request %s accept=%s", request.URL.String(), request.Header.Get("Accept"))
			return nil, nil
		}
	})
	identity := model.ChangeRequestIdentity{
		Provider: model.ChangeProviderGitHub, HostID: "host-github-public",
		TargetRepository: &model.HostedRepositoryIdentity{
			HostID: "host-github-public", ImmutableID: "repository:10", Slug: "old-owner/old-name",
		},
		ProviderObjectID: "pull:42",
	}
	result, err := provider.GetSnapshot(context.Background(), identity, "")
	if err != nil {
		t.Fatal(err)
	}
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		t.Fatalf("snapshot contract = %v", errs)
	}
	if result.Snapshot.Identity.TargetRepository.Slug != "acme/widgets" {
		t.Fatalf("resolved target slug = %q", result.Snapshot.Identity.TargetRepository.Slug)
	}
	if result.Snapshot.SourceRepository == nil || result.Snapshot.SourceRepository.ImmutableID != "repository:20" {
		t.Fatalf("source repository = %+v", result.Snapshot.SourceRepository)
	}
	if len(result.Snapshot.Files) != 1 || result.Snapshot.Files[0].Status != model.GitFileRenamed ||
		result.Snapshot.Files[0].OldMode != "100644" || result.Snapshot.Files[0].NewMode != "100755" ||
		len(result.Contents) != 1 || !strings.Contains(string(result.Contents[0].Content), "diff --git") {
		t.Fatalf("files=%+v contents=%+v", result.Snapshot.Files, result.Contents)
	}
	if result.Snapshot.Completeness.Patches.State != model.GitEvidenceExact ||
		result.Snapshot.Completeness.Modes.State != model.GitEvidenceExact {
		t.Fatalf("completeness = %+v", result.Snapshot.Completeness)
	}
}

func TestGitHubProviderDiscardsCaptureWhenHeadMoves(t *testing.T) {
	pullCalls := 0
	provider := testGitHubProvider(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repositories/10":
			return response(request, http.StatusOK, `{"id":10,"full_name":"acme/widgets"}`, nil), nil
		case request.URL.Path == "/repos/acme/widgets/pulls/42" && request.Header.Get("Accept") == "application/vnd.github.diff":
			return response(request, http.StatusOK, githubRawDiffFixture(), nil), nil
		case request.URL.Path == "/repos/acme/widgets/pulls/42":
			pullCalls++
			head := gitHubTestHeadSHA
			if pullCalls == 2 {
				head = "4444444444444444444444444444444444444444"
			}
			return response(request, http.StatusOK, githubPullFixture(head), nil), nil
		case strings.HasSuffix(request.URL.Path, "/files"):
			return response(request, http.StatusOK, `[{"filename":"internal/new.go","previous_filename":"internal/old.go","status":"renamed","additions":2,"deletions":1,"patch":"@@ -1 +1 @@\n-old\n+new\n"}]`, nil), nil
		case strings.HasSuffix(request.URL.Path, "/commits"):
			return response(request, http.StatusOK, `[]`, nil), nil
		default:
			t.Fatalf("unexpected GitHub request %s", request.URL.String())
			return nil, nil
		}
	})
	identity := model.ChangeRequestIdentity{
		Provider: model.ChangeProviderGitHub, HostID: "host-github-public",
		TargetRepository: &model.HostedRepositoryIdentity{HostID: "host-github-public", ImmutableID: "repository:10", Slug: "acme/widgets"},
		ProviderObjectID: "pull:42",
	}
	_, err := provider.GetSnapshot(context.Background(), identity, "")
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorCaptureRaced || providerErr.Operation != OperationGetSnapshot {
		t.Fatalf("head race error = %v", err)
	}
}

func TestGitHubDiffParserHandlesQuotedPathsAndModes(t *testing.T) {
	sections, err := parseGitHubDiff([]byte("diff --git \"a/old\\tname.go\" \"b/new\\tname.go\"\nold mode 100644\nnew mode 100755\nrename from \"old\\tname.go\"\nrename to \"new\\tname.go\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || sections[0].OldPath != "old\tname.go" || sections[0].NewPath != "new\tname.go" ||
		sections[0].OldMode != "100644" || sections[0].NewMode != "100755" {
		t.Fatalf("sections = %+v", sections)
	}
}

func TestGitHubFilesDegradeModesWhenRawDiffUnavailable(t *testing.T) {
	files, contents, completeness, _, err := githubFiles(
		"pull:42",
		[]githubFile{{Filename: "a.go", Status: "modified", Additions: 1, Deletions: 1, Patch: "@@ -1 +1 @@\n-a\n+b\n"}},
		nil, false, false, false, []githubCommit{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(contents) != 1 || completeness.FileSet.State != model.GitEvidenceExact ||
		completeness.Patches.State != model.GitEvidenceExact || completeness.Modes.State == model.GitEvidenceExact {
		t.Fatalf("files=%+v contents=%+v completeness=%+v", files, contents, completeness)
	}
}

func testGitHubProvider(t *testing.T, transport roundTripFunc) *GitHubProvider {
	t.Helper()
	approved := approvedGitHubHost(t)
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewGitHubProvider(PublicGitHubHost(), client)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func githubPullFixture(head string) string {
	return `{
		"number":42,"title":"Add exact PR capture","state":"open","draft":false,"merged":false,"merged_at":null,
		"html_url":"https://github.com/acme/widgets/pull/42","updated_at":"2026-08-11T08:02:00Z","merge_commit_sha":"9999999999999999999999999999999999999999",
		"head":{"ref":"feature","sha":"` + head + `","repo":{"id":20,"node_id":"R_source","full_name":"contributor/widgets"}},
		"base":{"ref":"main","sha":"` + gitHubTestBaseSHA + `","repo":{"id":10,"node_id":"R_target","full_name":"acme/widgets"}}
	}`
}

func githubRawDiffFixture() string {
	return "diff --git a/internal/old.go b/internal/new.go\n" +
		"similarity index 90%\nold mode 100644\nnew mode 100755\n" +
		"rename from internal/old.go\nrename to internal/new.go\n" +
		"--- a/internal/old.go\n+++ b/internal/new.go\n@@ -1 +1 @@\n-old\n+new\n"
}
