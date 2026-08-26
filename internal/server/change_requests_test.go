package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

func TestGitEvidenceAPIReturnsHonestMissingEnvelopeAndETag(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "missing-git", false)
	server := New(database, nil)

	request := httptest.NewRequest("GET", "/api/sessions/missing-git/git-evidence?agent=codex", nil)
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope model.SessionGitEvidenceEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Assessment.State != model.GitEvidenceMissing || envelope.Repositories == nil || len(envelope.Repositories) != 0 {
		t.Fatalf("unexpected missing envelope: %+v", envelope)
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing Git evidence ETag")
	}
	conditional := httptest.NewRequest("GET", "/api/sessions/missing-git/git-evidence?agent=codex", nil)
	conditional.Header.Set("If-None-Match", etag)
	response = httptest.NewRecorder()
	server.Mux.ServeHTTP(response, conditional)
	if response.Code != http.StatusNotModified {
		t.Fatalf("conditional status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGenericResolvePersistsOfflineReverseIdentity(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)
	response := serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", `{
		"reference":"https://code.example/team/repo/reviews/7"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var resolved changeRequestResolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved.Matches) != 1 || resolved.Matches[0].Change.Identity.Provider != model.ChangeProviderGeneric ||
		resolved.Matches[0].LinkedSessions == nil || resolved.Matches[0].CandidateSessions == nil {
		t.Fatalf("unexpected generic resolve response: %+v", resolved)
	}
	changeKey := resolved.Matches[0].Change.ChangeKey
	response = serveChangeRequestAPI(server, "GET", "/api/change-requests/"+changeKey+"/sessions", "")
	if response.Code != http.StatusOK {
		t.Fatalf("reverse status=%d body=%s", response.Code, response.Body.String())
	}
	var reverse changeRequestSessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &reverse); err != nil {
		t.Fatal(err)
	}
	if reverse.LinkedSessions == nil || reverse.CandidateSessions == nil || len(reverse.LinkedSessions) != 0 {
		t.Fatalf("unexpected generic reverse response: %+v", reverse)
	}
}

func TestAutomaticResolveWithoutApprovalReturnsTypedLocalState(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)
	response := serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", `{
		"reference":"https://github.com/acme/widgets/pull/42"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var resolved changeRequestResolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Matches == nil || resolved.CreationSessions == nil || len(resolved.Matches) != 0 ||
		resolved.Assessment.ReasonCode != model.ReasonChangeRequestNotFound {
		t.Fatalf("unexpected unapproved resolve state: %+v", resolved)
	}
	response = serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", `{
		"reference":"https://github.com/acme/widgets/pull/42",
		"include_hosted_details":true
	}`)
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Assessment.ReasonCode != model.ReasonChangeHostNotApproved {
		t.Fatalf("hosted details did not require approval: %+v", resolved)
	}
}

func TestAutomaticResolveFindsLocalCreationEvidenceWithoutHostApproval(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "creator", false)
	reference := model.ChangeRequestReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
		NormalizedURL: "https://github.com/acme/widgets/pull/42",
	}
	evidence := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-api", Reference: reference,
		CommandKind: "github_cli_pr_create", ToolName: "exec", EventID: "invoke",
		RecordedAt:     time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC),
		SourceRevision: "sha256:source", Assessment: model.ExactGitEvidence(),
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence("codex", "creator", evidence.SourceRevision, []model.ChangeRequestCreationEvidence{evidence}); err != nil {
		t.Fatal(err)
	}
	server := New(database, nil)
	response := serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", `{
		"reference":"https://github.com/acme/widgets/pull/42"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var resolved changeRequestResolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Assessment.State != model.GitEvidenceExact || len(resolved.CreationSessions) != 1 ||
		resolved.CreationSessions[0].RootSessionID != "creator" || len(resolved.Matches) != 0 {
		t.Fatalf("unexpected local creation match: %+v", resolved)
	}
	if !strings.Contains(response.Body.String(), `"matches":[]`) {
		t.Fatalf("matches did not serialize as empty array: %s", response.Body.String())
	}

	response = serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", `{
		"reference":"https://github.com/acme/widgets/pull/42",
		"include_hosted_details":true
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("hosted status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved.CreationSessions) != 1 || resolved.CreationSessions[0].RootSessionID != "creator" {
		t.Fatalf("hosted details dropped local creation matches: %+v", resolved)
	}
}

func TestSessionChangeRequestsIncludeRecordedReferences(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "grok", "recorder", false)
	recordedAt := time.Date(2026, 8, 24, 3, 54, 31, 0, time.UTC)
	created := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-grok",
		Reference: model.ChangeRequestReference{
			Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
			TargetRepositorySlug: "acme/widgets", DisplayNumber: "163",
			NormalizedURL: "https://github.com/acme/widgets/pull/163",
		},
		CommandKind: "github_cli_pr_create", ToolName: "Run", EventID: "invoke",
		TurnIndex: 3, RecordedAt: recordedAt,
		SourceRevision: "index:grok:recorder:1", Assessment: model.ExactGitEvidence(),
	}
	mentioned := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-mention-grok",
		Reference: model.ChangeRequestReference{
			Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
			TargetRepositorySlug: "acme/widgets", DisplayNumber: "94",
			NormalizedURL: "https://github.com/acme/widgets/pull/94",
		},
		CommandKind: "change_request_url", ToolName: "message", EventID: "assistant",
		TurnIndex: 3, RecordedAt: recordedAt.Add(-time.Hour),
		SourceRevision: "index:grok:recorder:1", Assessment: model.ExactGitEvidence(),
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence(
		"grok", "recorder", "index:grok:recorder:1",
		[]model.ChangeRequestCreationEvidence{created, mentioned},
	); err != nil {
		t.Fatal(err)
	}
	server := New(database, nil)
	response := serveChangeRequestAPI(server, "GET", "/api/sessions/recorder/change-requests?agent=grok", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Links   []model.SessionChangeRequestLink `json:"links"`
		Derived []sessionRecordedChangeReference `json:"derived"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Links) != 0 {
		t.Fatalf("unexpected explicit links: %+v", body.Links)
	}
	if len(body.Derived) != 2 ||
		body.Derived[0].Kind != "mentioned" || body.Derived[0].Reference.DisplayNumber != "94" ||
		body.Derived[1].Kind != "created" || body.Derived[1].Reference.DisplayNumber != "163" {
		t.Fatalf("unexpected derived references: %+v", body.Derived)
	}
	if !body.Derived[1].RecordedAt.Equal(recordedAt) || body.Derived[1].ToolName != "Run" || body.Derived[1].TurnIndex != 3 {
		t.Fatalf("derived entry lost provenance: %+v", body.Derived[1])
	}
}

func TestDeriveRecordedChangeReferencesCreatedSupersedesMention(t *testing.T) {
	reference := model.ChangeRequestReference{
		Provider: model.ChangeProviderGitHub, DisplayOrigin: "https://github.com",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "42",
		NormalizedURL: "https://github.com/acme/widgets/pull/42",
	}
	recordedAt := time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC)
	base := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-base", Reference: reference, ToolName: "exec", EventID: "invoke",
		RecordedAt: recordedAt, SourceRevision: "sha256:one", Assessment: model.ExactGitEvidence(),
	}
	mention := base
	mention.EvidenceID = "cr-mention"
	mention.CommandKind = "change_request_url"
	mention.ToolName = "message"
	mention.RecordedAt = recordedAt.Add(-time.Hour)
	created := base
	created.EvidenceID = "cr-create"
	created.CommandKind = "github_cli_pr_create"

	derived := deriveRecordedChangeReferences([]model.ChangeRequestCreationEvidence{mention, created})
	if len(derived) != 1 || derived[0].Kind != "created" {
		t.Fatalf("creation did not supersede mention: %+v", derived)
	}

	mentionOnly := deriveRecordedChangeReferences([]model.ChangeRequestCreationEvidence{mention})
	if len(mentionOnly) != 1 || mentionOnly[0].Kind != "mentioned" {
		t.Fatalf("standalone mention not kept: %+v", mentionOnly)
	}
}

func TestResolveChangeRequestFailsClosedOnMalformedInput(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)
	cases := []struct {
		name string
		body string
		code string
	}{
		{name: "empty reference", body: `{"reference":""}`, code: "invalid_request"},
		{name: "unknown field", body: `{"reference":"https://github.com/acme/widgets/pull/42","extra":true}`, code: "invalid_request"},
		{name: "invalid json", body: `{`, code: "invalid_request"},
		{name: "unclaimed reference", body: `{"reference":"not-a-url"}`, code: "change_alias_ambiguous"},
	}
	for _, test := range cases {
		response := serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", test.body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", test.name, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), test.code) {
			t.Fatalf("%s code=%s body=%s", test.name, test.code, response.Body.String())
		}
	}
}

func TestResolveFindsGenericReviewURLWithoutHostApproval(t *testing.T) {
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "grok", "mentioned", false)
	reference := model.ChangeRequestReference{
		Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://gitee.com",
		TargetRepositorySlug: "acme/widgets", DisplayNumber: "12",
		NormalizedURL: "https://gitee.com/acme/widgets/pulls/12",
	}
	evidence := model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-gitee", Reference: reference,
		CommandKind: "change_request_url", ToolName: "message", EventID: "assistant",
		RecordedAt:     time.Date(2026, 8, 11, 16, 17, 21, 0, time.UTC),
		SourceRevision: "index:grok:mentioned:1", Assessment: model.ExactGitEvidence(),
	}
	if err := database.ReplaceSessionChangeRequestCreationEvidence("grok", "mentioned", evidence.SourceRevision, []model.ChangeRequestCreationEvidence{evidence}); err != nil {
		t.Fatal(err)
	}
	server := New(database, nil)
	response := serveChangeRequestAPI(server, "POST", "/api/change-requests/resolve", `{
		"reference":"https://gitee.com/acme/widgets/pulls/12"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var resolved changeRequestResolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Assessment.State != model.GitEvidenceExact || resolved.Reference.Provider != model.ChangeProviderGeneric ||
		len(resolved.CreationSessions) != 1 || resolved.CreationSessions[0].RootSessionID != "mentioned" {
		t.Fatalf("unexpected generic URL resolve: %+v", resolved)
	}
}

func TestExclusiveChangeRequestBindSelectsAuthorityAndServesPatch(t *testing.T) {
	server, changeKey, version, fileKey := seededChangeRequestAPIServer(t)
	bindBody := fmt.Sprintf(`{
		"change_key":%q,
		"repository_entry_key":"repo-entry",
		"content_version_key":%q,
		"relationship":"exclusive",
		"confirmation":{"complete_delivery":true,"content_version_key":%q}
	}`, changeKey, version, version)
	response := serveChangeRequestAPI(
		server, "POST", "/api/sessions/session-pr/change-requests/bind?agent=codex", bindBody,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("bind status=%d body=%s", response.Code, response.Body.String())
	}
	var link model.SessionChangeRequestLink
	if err := json.Unmarshal(response.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.Relationship != model.ChangeRelationshipExclusive || link.ConfirmationRevision == "" {
		t.Fatalf("unexpected stored link: %+v", link)
	}

	response = serveChangeRequestAPI(server, "GET", "/api/sessions/session-pr/git-evidence?agent=codex", "")
	if response.Code != http.StatusOK {
		t.Fatalf("evidence status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope model.SessionGitEvidenceEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Repositories) != 1 || envelope.Repositories[0].Authority != model.GitAuthorityHostedChange ||
		envelope.Repositories[0].AuthoritySelection == nil || envelope.Repositories[0].AuthoritySelection.LinkID != link.LinkID {
		t.Fatalf("hosted authority not selected: %+v", envelope)
	}

	response = serveChangeRequestAPI(
		server, "GET", "/api/sessions/session-pr/git-evidence/files/"+fileKey+"/patch?agent=codex&repository=repo-entry", "",
	)
	if response.Code != http.StatusOK || response.Body.String() != "@@ -1 +1 @@\n-old\n+new\n" {
		t.Fatalf("patch status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe patch headers: %+v", response.Header())
	}

	response = serveChangeRequestAPI(server, "GET", "/api/change-requests/"+changeKey+"/sessions", "")
	var reverse changeRequestSessionsResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reverse) != nil || len(reverse.LinkedSessions) != 1 {
		t.Fatalf("reverse status=%d body=%s", response.Code, response.Body.String())
	}

	response = serveChangeRequestAPI(
		server, "DELETE", "/api/sessions/session-pr/change-requests/"+link.LinkID+"?agent=codex", "",
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(server, "GET", "/api/sessions/session-pr/git-evidence?agent=codex", "")
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Repositories[0].Authority != model.GitAuthorityNone || !envelope.Repositories[0].Stale {
		t.Fatalf("deleted selected link did not demote authority: %+v", envelope.Repositories[0])
	}
}

func seededChangeRequestAPIServer(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	database := openCollabAPIDB(t)
	seedCollabSession(t, database, "codex", "session-pr", false)
	if err := database.ReplaceCollaborationGraph(apiCollabGraph("codex", "session-pr", 7)); err != nil {
		t.Fatal(err)
	}
	evidence := model.SessionGitEvidence{
		RootAgentType: "codex", RootSessionID: "session-pr", RepositoryEntryKey: "repo-entry",
		Revision:   1,
		Assessment: model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonBaselineNotCaptured),
		Repository: model.GitRepositoryBinding{
			RepositoryEntryKey: "repo-entry", WorktreeRoot: "/workspace/project",
			CommonRootID: "common-root", WorktreeID: "worktree-id",
			Assessment: model.ExactGitEvidence(),
		},
		Files: []model.GitFileChange{}, CandidateCommits: []model.GitCandidateCommit{},
		ChangeRequests: []model.SessionChangeRequestLink{}, Authority: model.GitAuthorityNone,
		GeneratedAt: time.Date(2026, 8, 11, 6, 0, 0, 0, time.UTC),
	}
	if err := database.ReplaceSessionGitEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_hosts(
			host_id, provider, hostname, display_origin, endpoint_origins_json,
			lifecycle, state, approved_at
		) VALUES (
			'host-github-public','github','github.com','https://github.com',
			'["https://github.com","https://api.github.com"]',
			'approved','exact','2026-08-11T00:00:00Z'
		)`); err != nil {
		t.Fatal(err)
	}
	target := &model.HostedRepositoryIdentity{
		HostID: "host-github-public", ImmutableID: "repository:101", Slug: "acme/widgets",
	}
	additions, deletions := 1, 1
	const (
		baseSHA = "1111111111111111111111111111111111111111"
		headSHA = "2222222222222222222222222222222222222222"
		version = "github:pull:42:fixed-version"
		fileKey = "hosted:file-1"
	)
	fetchedAt := time.Date(2026, 8, 11, 6, 1, 0, 0, time.UTC)
	write := db.ChangeRequestSnapshotWrite{
		Snapshot: model.ChangeRequestSnapshot{
			SnapshotID: "snapshot-pr-42",
			Identity: model.ChangeRequestIdentity{
				Provider: model.ChangeProviderGitHub, HostID: target.HostID,
				TargetRepository: target, ProviderObjectID: "pull:42",
			},
			Content: model.ChangeRequestContentVersion{
				Key: version, BaseRefSHA: baseSHA, DiffBaseSHA: baseSHA,
				HeadSHA: headSHA, FileManifestDigest: "sha256:" + strings.Repeat("a", 64),
			},
			MetadataRevision: "metadata-1", Kind: model.ChangeRequestPullRequest,
			DisplayNumber: "42", LifecycleState: model.ChangeLifecycleOpen,
			Title: "Add evidence", WebURL: "https://github.com/acme/widgets/pull/42",
			SourceRepository: target, SourceRef: "feat/evidence", TargetRef: "main",
			Files: []model.GitFileChange{{
				Ordinal: 0, Key: fileKey, Layer: model.GitFileLayerHosted,
				DisplayPath: "internal/evidence.go", PathEncoding: model.GitPathUTF8,
				Status: model.GitFileModified, OldMode: "100644", NewMode: "100644",
				Additions: &additions, Deletions: &deletions,
				StatusAssessment: model.ExactGitEvidence(), PatchAssessment: model.ExactGitEvidence(),
				Evidence: []model.GitEvidenceLink{},
			}},
			Commits: []model.GitCandidateCommit{},
			Completeness: model.ChangeRequestCompleteness{
				Metadata: model.ExactGitEvidence(), FileSet: model.ExactGitEvidence(),
				Patches: model.ExactGitEvidence(), Modes: model.ExactGitEvidence(),
				Commits: model.ExactGitEvidence(),
			},
			ETag: "etag-1", FetchedAt: fetchedAt,
		},
		SyncStartedAt: fetchedAt.Add(-time.Second), UpdateCacheHead: true,
		Aliases: []db.ChangeRequestAliasWrite{},
		Contents: []db.ChangeRequestContentWrite{{
			FileKey: fileKey, Purpose: "patch", Content: []byte("@@ -1 +1 @@\n-old\n+new\n"),
		}},
	}
	changeKey, err := database.StoreChangeRequestSnapshot(write)
	if err != nil {
		t.Fatal(err)
	}
	return New(database, nil), changeKey, version, fileKey
}

func serveChangeRequestAPI(server *Server, method, target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	return response
}
