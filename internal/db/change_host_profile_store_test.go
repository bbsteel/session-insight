package db

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/model"
)

func openV43TestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// validStoreProfile builds a fully valid profile document for one host.
func validStoreProfile(t *testing.T, profileID, hostID string, revision int) string {
	t.Helper()
	encoded, err := openapi.EncodeProfile(openapi.Profile{
		SchemaVersion:   openapi.SchemaVersion,
		ProfileID:       profileID,
		ProfileRevision: revision,
		DisplayName:     "Internal Review",
		Adapter:         openapi.AdapterKind,
		HostID:          hostID,
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
				Method:       "GET",
				Origin:       "https://review.internal",
				PathTemplate: "/api/projects/{repository}/reviews/{number}",
				Parameters: map[string]string{
					"repository": "reference.repository",
					"number":     "reference.number",
				},
				Pagination: openapi.Pagination{Mode: openapi.PaginationNone},
				Response: openapi.OperationResponse{
					Fields: map[string]openapi.FieldSelector{
						"provider_object_id": {Pointer: "/id"},
						"title":              {Pointer: "/title"},
						"lifecycle_state":    {Pointer: "/state"},
						"head_sha":           {Pointer: "/head/sha"},
					},
				},
			},
		},
		Capabilities: openapi.Capabilities{
			Metadata:      openapi.CapabilitySupported,
			FileSet:       openapi.CapabilityUnsupported,
			Patches:       openapi.CapabilityUnsupported,
			Modes:         openapi.CapabilityUnsupported,
			Commits:       openapi.CapabilityUnsupported,
			ContentAnchor: "head_sha",
			RepositoryID:  openapi.CapabilityUnsupported,
		},
		SpecDigest: "sha256:" + strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func storeApprovedOpenAPIHost(t *testing.T, database *DB, hostID string) {
	t.Helper()
	if err := database.StoreChangeHostPreview(ChangeHostRecord{
		HostID: hostID, Provider: model.ChangeProviderOpenAPI,
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.ApproveChangeHost(hostID, false, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func profileRecord(profileID, hostID string, revision int, lifecycle openapi.ProfileLifecycle) ChangeHostProfileRecord {
	return ChangeHostProfileRecord{
		ProfileID: profileID, HostID: hostID, ProfileRevision: revision,
		SchemaVersion: openapi.SchemaVersion, DisplayName: "Internal Review",
		Lifecycle: lifecycle, SpecDigest: "sha256:" + strings.Repeat("b", 64),
		SpecVersion: "openapi:3.0.3",
	}
}

func TestV43OpenAPIProviderAndProfileLifecycle(t *testing.T) {
	database := openV43TestDB(t)
	storeApprovedOpenAPIHost(t, database, "review-company-internal")

	record := profileRecord("profile-1", "review-company-internal", 1, openapi.ProfileDraft)
	record.ProfileJSON = validStoreProfile(t, "profile-1", "review-company-internal", 1)
	if err := database.CreateChangeHostProfileDraft(record); err != nil {
		t.Fatal(err)
	}
	stored, exists, err := database.ChangeHostProfile("profile-1")
	if err != nil || !exists {
		t.Fatalf("profile not stored: exists=%v err=%v", exists, err)
	}
	if stored.Lifecycle != openapi.ProfileDraft || stored.VerifiedAt != nil {
		t.Fatalf("fresh profile must be an unverified draft: %+v", stored.Lifecycle)
	}
	if _, exists, err := database.ActiveChangeHostProfile("review-company-internal"); err != nil || exists {
		t.Fatalf("no profile may be active before activation: exists=%v err=%v", exists, err)
	}

	// Draft -> verified -> active, with the single-active invariant preserved
	// when a second revision is activated.
	if err := database.MarkChangeHostProfileVerified("profile-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := database.ActivateChangeHostProfile("profile-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second := profileRecord("profile-2", "review-company-internal", 2, openapi.ProfileDraft)
	second.ProfileJSON = validStoreProfile(t, "profile-2", "review-company-internal", 2)
	if err := database.CreateChangeHostProfileDraft(second); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkChangeHostProfileVerified("profile-2", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := database.ActivateChangeHostProfile("profile-2", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	active, exists, err := database.ActiveChangeHostProfile("review-company-internal")
	if err != nil || !exists {
		t.Fatalf("no active profile after activation: exists=%v err=%v", exists, err)
	}
	if active.ProfileID != "profile-2" {
		t.Fatalf("activation did not switch to the newer revision: %+v", active.ProfileID)
	}
	previous, _, _ := database.ChangeHostProfile("profile-1")
	if previous.Lifecycle != openapi.ProfileRevoked {
		t.Fatalf("previous active profile must be revoked atomically: %+v", previous.Lifecycle)
	}

	// Degraded and revoked transitions.
	if err := database.MarkChangeHostProfileDegraded("profile-2", "change_profile_schema_drift", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	degraded, _, _ := database.ChangeHostProfile("profile-2")
	if degraded.Lifecycle != openapi.ProfileDegraded || degraded.LastFailureCode != "change_profile_schema_drift" {
		t.Fatalf("degradation not recorded: %+v", degraded.Lifecycle)
	}
	if err := database.RevokeChangeHostProfile("profile-2", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestV43ProfileStoreGuards(t *testing.T) {
	database := openV43TestDB(t)
	storeApprovedOpenAPIHost(t, database, "review-company-internal")

	// An active profile's mapping cannot be edited in place.
	record := profileRecord("profile-1", "review-company-internal", 1, openapi.ProfileDraft)
	record.ProfileJSON = validStoreProfile(t, "profile-1", "review-company-internal", 1)
	if err := database.CreateChangeHostProfileDraft(record); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkChangeHostProfileVerified("profile-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := database.ActivateChangeHostProfile("profile-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, err := database.conn.Exec(`UPDATE change_host_profiles SET profile_json = '{}' WHERE profile_id = 'profile-1'`)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("active profile mapping must be immutable: %v", err)
	}
	_, err = database.conn.Exec(`UPDATE change_host_profiles SET host_id = 'other' WHERE profile_id = 'profile-1'`)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("profile identity must be immutable: %v", err)
	}

	// Only one active revision per host survives even a direct write.
	_, err = database.conn.Exec(`
		INSERT INTO change_host_profiles(
			profile_id, host_id, profile_revision, schema_version, display_name,
			lifecycle, profile_json, inference_report_json, spec_digest, spec_version,
			created_at, updated_at, verified_at, activated_at
		) VALUES ('profile-raw', 'review-company-internal', 99, 1, 'Raw', 'active',
			'{}', '{}', 'sha256:` + strings.Repeat("c", 64) + `', 'openapi:3.0.3',
			'2026-08-25T00:00:00Z', '2026-08-25T00:00:00Z',
			'2026-08-25T00:00:00Z', '2026-08-25T00:00:00Z')`)
	if err == nil {
		t.Fatal("second active profile for one host must violate the unique index")
	}

	// Activation requires an approved host and a verified revision.
	unverified := profileRecord("profile-3", "review-company-internal", 3, openapi.ProfileDraft)
	unverified.ProfileJSON = validStoreProfile(t, "profile-3", "review-company-internal", 3)
	if err := database.CreateChangeHostProfileDraft(unverified); err != nil {
		t.Fatal(err)
	}
	if err := database.ActivateChangeHostProfile("profile-3", time.Now().UTC()); !errors.Is(err, ErrChangeHostProfileConflict) {
		t.Fatalf("draft activation must conflict: %v", err)
	}

	// Deleting a profile referenced by a historical snapshot is forbidden.
	database.conn.Exec(`INSERT INTO hosted_repositories(repository_id, host_id, provider_immutable_id, slug)
		VALUES ('repo-1', 'review-company-internal', 'immutable-1', 'team/repo')`)
	database.conn.Exec(`INSERT INTO change_request_identities(change_id, provider, host_id, target_repository_id, provider_object_id)
		VALUES ('change-1', 'openapi', 'review-company-internal', 'repo-1', 'pr-1')`)
	if _, err := database.conn.Exec(`INSERT INTO change_request_snapshots(
			snapshot_id, change_id, content_version_key, metadata_revision, kind,
			display_number, lifecycle_state, web_url, completeness_json, fetched_at,
			profile_id, profile_revision
		) VALUES ('snap-1', 'change-1', 'cv-1', 'rev-1', 'pull_request',
			'1', 'open', 'https://review.internal/projects/team/repo/reviews/1',
			'{"metadata":{"state":"exact","reasons":[]},"file_set":{"state":"unavailable","reason_code":"change_request_partial","reasons":["change_request_partial"]},"patches":{"state":"unavailable","reason_code":"change_request_partial","reasons":["change_request_partial"]},"modes":{"state":"unavailable","reason_code":"change_request_partial","reasons":["change_request_partial"]},"commits":{"state":"unavailable","reason_code":"change_request_partial","reasons":["change_request_partial"]}}',
			'2026-08-25T00:00:00Z', 'profile-1', 1)`); err != nil {
		t.Fatal(err)
	}
	// profile-1 is active, so it is not deletable through the store anyway;
	// revoke it first to reach the snapshot-reference guard.
	if err := database.RevokeChangeHostProfile("profile-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteChangeHostProfile("profile-1"); !errors.Is(err, ErrChangeHostProfileReferenced) {
		t.Fatalf("snapshot-referenced profile must not be deletable: %v", err)
	}
	if err := database.DeleteChangeHostProfile("profile-3"); err != nil {
		t.Fatal(err)
	}
}

func TestV43DraftAllowsIncompleteMappingButVerifyDoesNot(t *testing.T) {
	database := openV43TestDB(t)
	storeApprovedOpenAPIHost(t, database, "review-company-internal")

	incomplete := profileRecord("profile-1", "review-company-internal", 1, openapi.ProfileDraft)
	incomplete.ProfileJSON = `{
		"schema_version": 1, "profile_id": "profile-1", "profile_revision": 1,
		"display_name": "Internal Review", "adapter": "openapi",
		"host_id": "review-company-internal", "display_origin": "https://review.internal",
		"endpoint_origins": ["https://review.internal"],
		"reference": {"origin": "https://review.internal", "path_template": "/projects/{repository}/reviews/{number}", "repository_parameter": "repository", "number_parameter": "number"},
		"authentication": {"scheme": "header", "header_name": "PRIVATE-TOKEN", "value_prefix": "", "credential_reference": "env:REVIEW_TOKEN"},
		"operations": {}, "capabilities": {}, "limits": {},
		"spec_digest": "sha256:` + strings.Repeat("b", 64) + `"
	}`
	if err := database.CreateChangeHostProfileDraft(incomplete); err != nil {
		t.Fatalf("draft with pending mappings must be storable: %v", err)
	}
	err := database.MarkChangeHostProfileVerified("profile-1", time.Now().UTC())
	if err == nil {
		t.Fatal("incomplete mapping reached verified")
	}
	issues, ok := err.(openapi.ValidationIssues)
	if !ok {
		t.Fatalf("verify must surface structured validation issues, got %T: %v", err, err)
	}
	found := false
	for _, issue := range issues {
		if issue.Code == openapi.IssueMappingIncomplete {
			found = true
		}
	}
	if !found {
		t.Fatalf("verify must report the missing required mapping: %+v", issues)
	}
}

func TestV43CredentialReferenceStorageStaysInternal(t *testing.T) {
	database := openV43TestDB(t)
	storeApprovedOpenAPIHost(t, database, "review-company-internal")

	if err := database.SetChangeHostCredentialReference("review-company-internal", "not-a-reference"); err == nil {
		t.Fatal("invalid credential reference accepted")
	}
	if err := database.SetChangeHostCredentialReference("review-company-internal", "env:REVIEW_TOKEN"); err != nil {
		t.Fatal(err)
	}
	record, exists, err := database.ChangeHost("review-company-internal")
	if err != nil || !exists {
		t.Fatal(err)
	}
	if record.CredentialReference != "env:REVIEW_TOKEN" {
		t.Fatalf("credential reference not stored: %q", record.CredentialReference)
	}
	if err := database.SetChangeHostCredentialReference("review-company-internal", ""); err != nil {
		t.Fatal(err)
	}
	record, _, _ = database.ChangeHost("review-company-internal")
	if record.CredentialReference != "" {
		t.Fatalf("credential reference not cleared: %q", record.CredentialReference)
	}
}

func TestV43MigrationIsIdempotentAndPreservesRows(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeApprovedOpenAPIHost(t, database, "review-company-internal")
	record := profileRecord("profile-1", "review-company-internal", 1, openapi.ProfileDraft)
	record.ProfileJSON = validStoreProfile(t, "profile-1", "review-company-internal", 1)
	if err := database.CreateChangeHostProfileDraft(record); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.conn.QueryRow(`SELECT COUNT(*) FROM change_host_profiles`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("profile row missing: count=%d err=%v", count, err)
	}
	// Reopen the same database: every migration must no-op on the v43 shape.
	database.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after v43: %v", err)
	}
	defer reopened.Close()
	stored, exists, err := reopened.ChangeHostProfile("profile-1")
	if err != nil || !exists || stored.Lifecycle != openapi.ProfileDraft {
		t.Fatalf("profile did not survive reopen: exists=%v lifecycle=%v err=%v", exists, stored.Lifecycle, err)
	}
}
