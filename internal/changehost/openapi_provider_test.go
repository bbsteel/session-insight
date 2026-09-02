package changehost

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/model"
)

// openapiProviderFixture builds a provider over a roundTrip transport, with a
// resolver that pins review.internal to a test address.
func openapiProviderFixture(t *testing.T, profile openapi.Profile, transport http.RoundTripper) *OpenAPIProvider {
	t.Helper()
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"review.internal": {netip.MustParseAddr("192.0.2.10")},
	}}
	host := HostIdentity{
		Key: "review-host", Provider: model.ChangeProviderOpenAPI,
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal"},
	}
	approved, err := NewHostPolicy(resolver).Approve(context.Background(), host, HostApprovalOptions{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewOpenAPIProvider(host, client, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func diffOnlyProfile() openapi.Profile {
	return openapi.Profile{
		SchemaVersion:   openapi.SchemaVersion,
		ProfileID:       "profile-diff",
		ProfileRevision: 1,
		DisplayName:     "Diff Review",
		Adapter:         openapi.AdapterKind,
		HostID:          "review-host",
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal"},
		Reference: openapi.ReferenceTemplate{
			Origin:              "https://review.internal",
			PathTemplate:        "/projects/{repository}/reviews/{number}",
			RepositoryParameter: "repository",
			NumberParameter:     "number",
		},
		Authentication: openapi.Authentication{
			Scheme:              "header",
			HeaderName:          "PRIVATE-TOKEN",
			CredentialReference: "env:REVIEW_TOKEN",
		},
		Operations: openapi.ProfileOperations{
			ResolveChange: &openapi.Operation{
				Method: "GET", Origin: "https://review.internal",
				PathTemplate: "/api/projects/{repository}/reviews/{number}",
				Parameters: map[string]string{
					"repository": "reference.repository", "number": "reference.number",
				},
				Pagination: openapi.Pagination{Mode: openapi.PaginationNone},
				Response: openapi.OperationResponse{Fields: map[string]openapi.FieldSelector{
					"provider_object_id": {Pointer: "/id"},
					"title":              {Pointer: "/title"},
					"lifecycle_state":    {Pointer: "/state"},
					"head_sha":           {Pointer: "/head"},
				}},
			},
			GetDiff: &openapi.Operation{
				Method: "GET", Origin: "https://review.internal",
				PathTemplate: "/api/projects/{repository}/reviews/{number}/diff",
				Parameters: map[string]string{
					"repository": "reference.repository", "number": "reference.number",
				},
				Pagination: openapi.Pagination{Mode: openapi.PaginationNone},
				Response: openapi.OperationResponse{Fields: map[string]openapi.FieldSelector{
					"diff_text": {Pointer: ""},
				}},
			},
		},
		Capabilities: openapi.Capabilities{
			Metadata: openapi.CapabilitySupported, FileSet: openapi.CapabilitySupported,
			Patches: openapi.CapabilitySupported, Modes: openapi.CapabilityUnsupported,
			Commits: openapi.CapabilityUnsupported, ContentAnchor: "head_sha",
			RepositoryID: openapi.CapabilityUnsupported,
		},
		SpecDigest: "sha256:" + strings.Repeat("e", 64),
	}
}

const fixtureDiffText = `diff --git a/src/main.go b/src/main.go
index 1111111..2222222 100644
--- a/src/main.go
+++ b/src/main.go
@@ -1,2 +1,2 @@
-old
+new
diff --git a/src/new.go b/src/new.go
new file mode 100755
index 0000000..3333333
--- /dev/null
+++ b/src/new.go
@@ -0,0 +1 @@
+added
`

func TestOpenAPIProviderGetDiffSnapshot(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/projects/team/repo/reviews/42":
			return response(request, http.StatusOK, fmt.Sprintf(
				`{"id": 42, "title": "Diff change", "state": "open", "head": "%s"}`, headSHA), nil), nil
		case "/api/projects/team/repo/reviews/42/diff":
			return response(request, http.StatusOK, fixtureDiffText, nil), nil
		default:
			return response(request, http.StatusNotFound, `{}`, nil), nil
		}
	})
	provider := openapiProviderFixture(t, diffOnlyProfile(), transport)

	reference, ok := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	if !ok {
		t.Fatal("profile parser rejected a matching URL")
	}
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Change.Title != "Diff change" || resolved.Change.DisplayNumber != "42" {
		t.Fatalf("summary: %+v", resolved.Change)
	}

	snapshot, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Snapshot.Files) != 2 || len(snapshot.Contents) != 2 {
		t.Fatalf("diff file set: files=%d contents=%d", len(snapshot.Snapshot.Files), len(snapshot.Contents))
	}
	statuses := map[string]model.GitFileStatus{}
	for _, file := range snapshot.Snapshot.Files {
		statuses[file.DisplayPath] = file.Status
	}
	if statuses["src/main.go"] != model.GitFileModified || statuses["src/new.go"] != model.GitFileAdded {
		t.Fatalf("diff statuses: %+v", statuses)
	}
	// Diff-only platform: commits degrade, patches stay exact.
	if snapshot.Snapshot.Completeness.Commits.State == model.GitEvidenceExact {
		t.Fatal("commits must degrade on a diff-only platform")
	}
	if snapshot.Snapshot.Completeness.Patches.State != model.GitEvidenceExact {
		t.Fatal("patches must be exact from a full diff")
	}
	if snapshot.Snapshot.Content.HeadSHA != headSHA || snapshot.Snapshot.Content.Key == "" {
		t.Fatalf("content version: %+v", snapshot.Snapshot.Content)
	}
	// Fixed-version re-request resolves the same content key.
	again, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, snapshot.Snapshot.Content.Key)
	if err != nil {
		t.Fatal(err)
	}
	if again.Snapshot.Content.Key != snapshot.Snapshot.Content.Key {
		t.Fatal("fixed-version snapshot drifted")
	}
	// A mismatched requested version is not found.
	if _, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, "bogus-version"); err == nil {
		t.Fatal("mismatched requested version must be rejected")
	}
}

func TestOpenAPIProviderLinkHeaderPagination(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	profile := diffOnlyProfile()
	profile.Operations.ListFiles = &openapi.Operation{
		Method: "GET", Origin: "https://review.internal",
		PathTemplate: "/api/projects/{repository}/reviews/{number}/files",
		Parameters: map[string]string{
			"repository": "reference.repository", "number": "reference.number",
		},
		Pagination: openapi.Pagination{Mode: openapi.PaginationLinkHeader, PageParameter: "page"},
		Response: openapi.OperationResponse{
			ItemsPointer: "/values",
			Fields: map[string]openapi.FieldSelector{
				"path":   {Pointer: "/path"},
				"status": {Pointer: "/status"},
			},
		},
	}
	profile.Operations.GetDiff = nil
	profile.Capabilities.Patches = openapi.CapabilityUnsupported
	pageRequests := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/projects/team/repo/reviews/42":
			return response(request, http.StatusOK, fmt.Sprintf(
				`{"id": 42, "title": "Paged", "state": "open", "head": "%s"}`, headSHA), nil), nil
		case "/api/projects/team/repo/reviews/42/files":
			pageRequests++
			if request.URL.Query().Get("page") == "2" {
				return response(request, http.StatusOK, `{"values": [{"path": "b.go", "status": "added"}]}`, nil), nil
			}
			header := http.Header{"Link": {`<https://review.internal/api/projects/team/repo/reviews/42/files?page=2>; rel="next"`}}
			return response(request, http.StatusOK, `{"values": [{"path": "a.go", "status": "modified"}]}`, header), nil
		default:
			return response(request, http.StatusNotFound, `{}`, nil), nil
		}
	})
	provider := openapiProviderFixture(t, profile, transport)
	reference, ok := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	if !ok {
		t.Fatal("parse failed")
	}
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Snapshot.Files) != 2 || pageRequests != 2 {
		t.Fatalf("link-header pagination: files=%d pages=%d", len(snapshot.Snapshot.Files), pageRequests)
	}
}

func TestOpenAPIProviderContractAndCapabilities(t *testing.T) {
	provider := openapiProviderFixture(t, diffOnlyProfile(), roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, `{}`, nil), nil
	}))
	if errs := ValidateProvider(provider); len(errs) != 0 {
		t.Fatalf("provider contract: %v", errs)
	}
	capabilities := provider.Capabilities()
	if capabilities.Operations[CapabilitySnapshotPatches].State != CapabilitySupported ||
		capabilities.Operations[CapabilitySnapshotCommits].State != CapabilityUnsupported ||
		capabilities.Operations[CapabilityDiscoverHead].ReasonCode != CapabilityReasonProviderUnsupported ||
		capabilities.Operations[CapabilityParseRemote].ReasonCode != CapabilityReasonEndpointUnsupported {
		t.Fatalf("capability projection: %+v", capabilities.Operations)
	}
	// Unsupported discovery fails with a stable code, never a guess.
	if _, err := provider.DiscoverForCommit(context.Background(), model.HostedRepositoryIdentity{}, strings.Repeat("a", 40)); err == nil {
		t.Fatal("unsupported discovery must fail")
	}
}

func TestOpenAPIProviderCursorHeaderPagination(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	profile := diffOnlyProfile()
	profile.Operations.ListFiles = &openapi.Operation{
		Method: "GET", Origin: "https://review.internal",
		PathTemplate: "/api/projects/{repository}/reviews/{number}/files",
		Parameters: map[string]string{
			"repository": "reference.repository", "number": "reference.number",
		},
		Pagination: openapi.Pagination{
			Mode:             openapi.PaginationCursorHeader,
			CursorParameter:  "cursor",
			NextCursorHeader: "X-Next-Cursor",
		},
		Response: openapi.OperationResponse{
			ItemsPointer: "/values",
			Fields: map[string]openapi.FieldSelector{
				"path":   {Pointer: "/path"},
				"status": {Pointer: "/status"},
			},
		},
	}
	profile.Operations.GetDiff = nil
	profile.Capabilities.Patches = openapi.CapabilityUnsupported
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/projects/team/repo/reviews/42":
			return response(request, http.StatusOK, fmt.Sprintf(
				`{"id": 42, "title": "Paged", "state": "open", "head": "%s"}`, headSHA), nil), nil
		case "/api/projects/team/repo/reviews/42/files":
			if request.URL.Query().Get("cursor") == "next-2" {
				return response(request, http.StatusOK, `{"values": [{"path": "b.go", "status": "added"}]}`, nil), nil
			}
			header := http.Header{"X-Next-Cursor": {"next-2"}}
			return response(request, http.StatusOK, `{"values": [{"path": "a.go", "status": "modified"}]}`, header), nil
		default:
			return response(request, http.StatusNotFound, `{}`, nil), nil
		}
	})
	provider := openapiProviderFixture(t, profile, transport)
	reference, ok := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	if !ok {
		t.Fatal("parse failed")
	}
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Snapshot.Files) != 2 {
		t.Fatalf("cursor-header pagination: files=%d", len(snapshot.Snapshot.Files))
	}
}

func TestFilesFromUnifiedDiffIgnoresMarkerInPatchBody(t *testing.T) {
	raw := []byte("diff --git a/fixture.txt b/fixture.txt\nindex 1111111..2222222 100644\n--- a/fixture.txt\n+++ b/fixture.txt\n@@ -1 +1 @@\n-diff --git a/old.txt b/old.txt\n+diff --git a/new.txt b/new.txt\n")
	files, _, _, err := filesFromUnifiedDiff(model.ChangeProviderOpenAPI, "team/repo", "1", raw, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].DisplayPath != "fixture.txt" {
		t.Fatalf("patch-body marker produced phantom files: %+v", files)
	}
}

func TestFilesFromUnifiedDiffRejectsTraversalPaths(t *testing.T) {
	raw := []byte("diff --git a/ok.go b/../../evil.go\nindex 1..2 100644\n--- a/ok.go\n+++ b/../../evil.go\n@@ -1 +1 @@\n-a\n+b\n")
	if _, _, _, err := filesFromUnifiedDiff(model.ChangeProviderOpenAPI, "team/repo", "1", raw, 100); err == nil {
		t.Fatal("traversal path in diff accepted")
	}
}

// driftTrackingProvider builds a provider whose drift reporter counts calls.
func driftTrackingProvider(t *testing.T, profile openapi.Profile, transport http.RoundTripper) (*OpenAPIProvider, *int) {
	t.Helper()
	driftCalls := 0
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"review.internal": {netip.MustParseAddr("192.0.2.10")},
	}}
	host := HostIdentity{
		Key: "review-host", Provider: model.ChangeProviderOpenAPI,
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal"},
	}
	approved, err := NewHostPolicy(resolver).Approve(context.Background(), host, HostApprovalOptions{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newHTTPClient(approved, HTTPClientConfig{}, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewOpenAPIProvider(host, client, profile, func(string) { driftCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	return provider, &driftCalls
}

func TestOpenAPIProviderTransientFailuresNeverDegrade(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	profile := diffOnlyProfile()
	profile.Operations.ListFiles = &openapi.Operation{
		Method: "GET", Origin: "https://review.internal",
		PathTemplate: "/api/projects/{repository}/reviews/{number}/files",
		Parameters: map[string]string{
			"repository": "reference.repository", "number": "reference.number",
		},
		Pagination: openapi.Pagination{Mode: openapi.PaginationNone},
		Response: openapi.OperationResponse{
			ItemsPointer: "/values",
			Fields: map[string]openapi.FieldSelector{
				"path":   {Pointer: "/path"},
				"status": {Pointer: "/status"},
			},
		},
	}
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/files") {
					return response(request, status, `{}`, nil), nil
				}
				return response(request, http.StatusOK, fmt.Sprintf(
					`{"id": 42, "title": "x", "state": "open", "head": "%s"}`, headSHA), nil), nil
			})
			provider, driftCalls := driftTrackingProvider(t, profile, transport)
			reference, ok := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
			if !ok {
				t.Fatal("parse failed")
			}
			resolved, err := provider.Resolve(context.Background(), reference)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, ""); err == nil {
				t.Fatal("failing files endpoint must fail the snapshot")
			}
			if *driftCalls != 0 {
				t.Fatalf("transient failure degraded the profile: %d drift reports", *driftCalls)
			}
		})
	}

	// A shape change (files items pointer vanishes) IS drift.
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/files") {
			return response(request, http.StatusOK, `{"items": []}`, nil), nil
		}
		return response(request, http.StatusOK, fmt.Sprintf(
			`{"id": 42, "title": "x", "state": "open", "head": "%s"}`, headSHA), nil), nil
	})
	provider, driftCalls := driftTrackingProvider(t, profile, transport)
	reference, _ := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, ""); err == nil {
		t.Fatal("shape change must fail the snapshot")
	}
	if *driftCalls == 0 {
		t.Fatal("shape change must degrade the profile")
	}
}

func TestOpenAPIProviderHonorsDeclaredMethodAndHeaders(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	profile := diffOnlyProfile()
	profile.Operations.GetDiff.Method = "GET"
	profile.Operations.GetDiff.Headers = map[string]string{"Accept": "text/x-diff", "X-Api-Track": "v2"}
	seen := http.Header{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/diff") {
			seen = request.Header.Clone()
			return response(request, http.StatusOK, fixtureDiffText, nil), nil
		}
		return response(request, http.StatusOK, fmt.Sprintf(
			`{"id": 42, "title": "x", "state": "open", "head": "%s"}`, headSHA), nil), nil
	})
	provider := openapiProviderFixture(t, profile, transport)
	reference, _ := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, ""); err != nil {
		t.Fatal(err)
	}
	if seen.Get("X-Api-Track") != "v2" || seen.Get("Accept") != "text/x-diff" {
		t.Fatalf("declared headers not applied: present_track=%v accept=%q", seen.Get("X-Api-Track") != "", seen.Get("Accept"))
	}
	// Note: credential-bearing headers are rejected at profile validation, so
	// the client denylist never sees them through a validated profile.
}

func TestFilesFromUnifiedDiffRespectsFileLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "diff --git a/f%d.go b/f%d.go\nindex 1..2 100644\n--- a/f%d.go\n+++ b/f%d.go\n@@ -1 +1 @@\n-a\n+b\n", i, i, i, i)
	}
	files, _, overflow, err := filesFromUnifiedDiff(model.ChangeProviderOpenAPI, "team/repo", "1", []byte(b.String()), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !overflow || len(files) != 2 {
		t.Fatalf("diff file limit: files=%d overflow=%v", len(files), overflow)
	}
}

func TestFetchPagesRejectsMalformedCursor(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	profile := diffOnlyProfile()
	profile.Operations.ListFiles = &openapi.Operation{
		Method: "GET", Origin: "https://review.internal",
		PathTemplate: "/api/projects/{repository}/reviews/{number}/files",
		Parameters: map[string]string{
			"repository": "reference.repository", "number": "reference.number",
		},
		Pagination: openapi.Pagination{
			Mode:              openapi.PaginationCursorBody,
			CursorParameter:   "cursor",
			NextCursorPointer: "/next_cursor",
		},
		Response: openapi.OperationResponse{
			ItemsPointer: "/values",
			Fields: map[string]openapi.FieldSelector{
				"path":   {Pointer: "/path"},
				"status": {Pointer: "/status"},
			},
		},
	}
	profile.Operations.GetDiff = nil
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/files") {
			// Cursor is a NUMBER, not a string: drift, not a clean end of list.
			return response(request, http.StatusOK, `{"values": [{"path": "a.go", "status": "added"}], "next_cursor": 7}`, nil), nil
		}
		return response(request, http.StatusOK, fmt.Sprintf(
			`{"id": 42, "title": "x", "state": "open", "head": "%s"}`, headSHA), nil), nil
	})
	provider := openapiProviderFixture(t, profile, transport)
	reference, _ := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, ""); err == nil {
		t.Fatal("malformed cursor must fail the snapshot instead of truncating silently")
	}
}

func TestFetchPagesFollowsLinkNextURLVerbatim(t *testing.T) {
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	profile := diffOnlyProfile()
	profile.Operations.ListFiles = &openapi.Operation{
		Method: "GET", Origin: "https://review.internal",
		PathTemplate: "/api/projects/{repository}/reviews/{number}/files",
		Parameters: map[string]string{
			"repository": "reference.repository", "number": "reference.number",
		},
		Pagination: openapi.Pagination{Mode: openapi.PaginationLinkHeader},
		Response: openapi.OperationResponse{
			ItemsPointer: "/values",
			Fields: map[string]openapi.FieldSelector{
				"path":   {Pointer: "/path"},
				"status": {Pointer: "/status"},
			},
		},
	}
	profile.Operations.GetDiff = nil
	continuationHit := false
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/projects/team/repo/reviews/42/files":
			header := http.Header{"Link": {`<https://review.internal/api/continuations/abc-9?opaque=1>; rel="next"`}}
			return response(request, http.StatusOK, `{"values": [{"path": "a.go", "status": "modified"}]}`, header), nil
		case "/api/continuations/abc-9":
			continuationHit = true
			if request.URL.Query().Get("opaque") != "1" {
				t.Error("continuation query lost")
			}
			return response(request, http.StatusOK, `{"values": [{"path": "b.go", "status": "added"}]}`, nil), nil
		default:
			return response(request, http.StatusOK, fmt.Sprintf(
				`{"id": 42, "title": "x", "state": "open", "head": "%s"}`, headSHA), nil), nil
		}
	})
	provider := openapiProviderFixture(t, profile, transport)
	reference, _ := provider.ParseReference("https://review.internal/projects/team/repo/reviews/42")
	resolved, err := provider.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.GetSnapshot(context.Background(), resolved.Change.Identity, "")
	if err != nil {
		t.Fatal(err)
	}
	if !continuationHit || len(snapshot.Snapshot.Files) != 2 {
		t.Fatalf("link continuation: hit=%v files=%d", continuationHit, len(snapshot.Snapshot.Files))
	}
}
