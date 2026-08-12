package changehost

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	gitLabTestStartSHA = "1111111111111111111111111111111111111111"
	gitLabTestBaseSHA  = "2222222222222222222222222222222222222222"
	gitLabTestHeadSHA  = "3333333333333333333333333333333333333333"
	gitLabTestCommit   = "4444444444444444444444444444444444444444"
)

func TestGitLabProviderCapturesExactFixedSnapshotFromDiffsEndpoint(t *testing.T) {
	requests := make([]string, 0)
	provider := testGitLabProvider(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.URL.RequestURI())
		switch {
		case request.URL.Path == "/api/v4/projects/10":
			return response(request, http.StatusOK, `{"id":10,"path_with_namespace":"acme/widgets"}`, nil), nil
		case request.URL.Path == "/api/v4/projects/20":
			return response(request, http.StatusOK, `{"id":20,"path_with_namespace":"contributor/widgets"}`, nil), nil
		case request.URL.Path == "/api/v4/projects/10/merge_requests/7":
			return response(request, http.StatusOK, gitLabMergeRequestFixture(gitLabTestHeadSHA), http.Header{"ETag": {"etag-mr-7"}}), nil
		case strings.HasSuffix(request.URL.Path, "/diffs"):
			return response(request, http.StatusOK, `[{
				"old_path":"internal/old.go","new_path":"internal/new.go",
				"a_mode":"100644","b_mode":"100755","diff":"@@ -1 +1 @@\n-old\n+new\n",
				"new_file":false,"renamed_file":true,"deleted_file":false,
				"collapsed":false,"too_large":false
			}]`, nil), nil
		case strings.HasSuffix(request.URL.Path, "/commits"):
			return response(request, http.StatusOK, `[{"id":"`+gitLabTestCommit+`","title":"Add exact MR capture","author_name":"Example","authored_date":"2026-08-11T08:00:00Z","committed_date":"2026-08-11T08:01:00Z"}]`, nil), nil
		default:
			t.Fatalf("unexpected GitLab request %s", request.URL.String())
			return nil, nil
		}
	})
	identity := model.ChangeRequestIdentity{
		Provider: model.ChangeProviderGitLab, HostID: "host-gitlab-public",
		TargetRepository: &model.HostedRepositoryIdentity{
			HostID: "host-gitlab-public", ImmutableID: "project:10", Slug: "acme/widgets",
		},
		ProviderObjectID: "merge_request:7",
	}
	result, err := provider.GetSnapshot(context.Background(), identity, "")
	if err != nil {
		t.Fatal(err)
	}
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		t.Fatalf("snapshot contract = %v", errs)
	}
	if result.Snapshot.Content.BaseRefSHA != gitLabTestStartSHA ||
		result.Snapshot.Content.DiffBaseSHA != gitLabTestBaseSHA ||
		result.Snapshot.Content.HeadSHA != gitLabTestHeadSHA {
		t.Fatalf("content version = %+v", result.Snapshot.Content)
	}
	if result.Snapshot.SourceRepository == nil || result.Snapshot.SourceRepository.ImmutableID != "project:20" {
		t.Fatalf("source repository = %+v", result.Snapshot.SourceRepository)
	}
	if len(result.Snapshot.Files) != 1 || result.Snapshot.Files[0].Status != model.GitFileRenamed ||
		result.Snapshot.Files[0].OldMode != "100644" || result.Snapshot.Files[0].NewMode != "100755" ||
		len(result.Contents) != 1 || !strings.Contains(string(result.Contents[0].Content), "+new") {
		t.Fatalf("captured files=%+v contents=%+v", result.Snapshot.Files, result.Contents)
	}
	if result.Snapshot.Completeness.Patches.State != model.GitEvidenceExact ||
		result.Snapshot.Completeness.Modes.State != model.GitEvidenceExact {
		t.Fatalf("completeness = %+v", result.Snapshot.Completeness)
	}
	for _, request := range requests {
		if strings.Contains(request, "/changes") {
			t.Fatalf("provider used deprecated GitLab changes endpoint: %s", request)
		}
	}
}

func TestGitLabProviderDiscardsCaptureWhenRevisionMoves(t *testing.T) {
	mergeRequestCalls := 0
	provider := testGitLabProvider(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/api/v4/projects/10":
			return response(request, http.StatusOK, `{"id":10,"path_with_namespace":"acme/widgets"}`, nil), nil
		case request.URL.Path == "/api/v4/projects/20":
			return response(request, http.StatusOK, `{"id":20,"path_with_namespace":"contributor/widgets"}`, nil), nil
		case request.URL.Path == "/api/v4/projects/10/merge_requests/7":
			mergeRequestCalls++
			head := gitLabTestHeadSHA
			if mergeRequestCalls == 2 {
				head = "5555555555555555555555555555555555555555"
			}
			return response(request, http.StatusOK, gitLabMergeRequestFixture(head), nil), nil
		case strings.HasSuffix(request.URL.Path, "/diffs"):
			return response(request, http.StatusOK, `[{"old_path":"a.go","new_path":"a.go","a_mode":"100644","b_mode":"100644","diff":"@@ -1 +1 @@\n-a\n+b\n"}]`, nil), nil
		case strings.HasSuffix(request.URL.Path, "/commits"):
			return response(request, http.StatusOK, `[]`, nil), nil
		default:
			t.Fatalf("unexpected GitLab request %s", request.URL.String())
			return nil, nil
		}
	})
	identity := model.ChangeRequestIdentity{
		Provider: model.ChangeProviderGitLab, HostID: "host-gitlab-public",
		TargetRepository: &model.HostedRepositoryIdentity{HostID: "host-gitlab-public", ImmutableID: "project:10", Slug: "acme/widgets"},
		ProviderObjectID: "merge_request:7",
	}
	_, err := provider.GetSnapshot(context.Background(), identity, "")
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != ErrorCaptureRaced || providerErr.Operation != OperationGetSnapshot {
		t.Fatalf("revision race error = %v", err)
	}
}

func TestGitLabProviderDegradesOnlyTruncatedPatchDimension(t *testing.T) {
	diffs := []gitLabDiff{{
		OldPath: "large.bin", NewPath: "large.bin", AMode: "100644", BMode: "100644", TooLarge: true,
	}}
	files, contents, completeness, _, err := gitLabFiles("merge_request:7", diffs, []gitLabCommit{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(contents) != 0 || completeness.FileSet.State != model.GitEvidenceExact ||
		completeness.Modes.State != model.GitEvidenceExact || completeness.Patches.ReasonCode != model.ReasonChangeRequestOverflow {
		t.Fatalf("files=%+v contents=%+v completeness=%+v", files, contents, completeness)
	}
}

func testGitLabProvider(t *testing.T, transport roundTripFunc) *GitLabProvider {
	t.Helper()
	host := PublicGitLabHost()
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"gitlab.com": {netip.MustParseAddr("8.8.8.8")},
	}}
	approved, err := NewHostPolicy(resolver).Approve(context.Background(), host, HostApprovalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewGitLabProvider(host, client)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func gitLabMergeRequestFixture(head string) string {
	return `{
		"id":70,"iid":7,"project_id":10,"source_project_id":20,"target_project_id":10,
		"title":"Add exact MR capture","state":"opened","draft":false,
		"web_url":"https://gitlab.com/acme/widgets/-/merge_requests/7",
		"sha":"` + head + `","source_branch":"feature","target_branch":"main",
		"merge_commit_sha":null,"squash_commit_sha":null,"updated_at":"2026-08-11T08:02:00Z",
		"diff_refs":{"start_sha":"` + gitLabTestStartSHA + `","base_sha":"` + gitLabTestBaseSHA + `","head_sha":"` + head + `"}
	}`
}
