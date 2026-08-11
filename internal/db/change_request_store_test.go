package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	testChangeBaseSHA = "1111111111111111111111111111111111111111"
	testChangeHeadSHA = "2222222222222222222222222222222222222222"
	testChangeCommit  = "3333333333333333333333333333333333333333"
)

func TestStoreChangeRequestSnapshotPublishesFixedVersionAndReverseAliases(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")

	write := testChangeRequestSnapshotWrite()
	changeKey, err := database.StoreChangeRequestSnapshot(write)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(changeKey, "change-") {
		t.Fatalf("change key = %q", changeKey)
	}
	for table, want := range map[string]int{
		"hosted_repositories":        2,
		"change_request_identities":  1,
		"change_request_snapshots":   1,
		"change_request_files":       1,
		"change_request_commits":     1,
		"change_request_cache_heads": 1,
		"source_content_blob_refs":   1,
		"source_content_blobs":       1,
	} {
		var got int
		if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
	var aliases int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM change_request_aliases`).Scan(&aliases); err != nil {
		t.Fatal(err)
	}
	if aliases < 5 {
		t.Fatalf("reverse aliases = %d, want at least 5", aliases)
	}

	write.Snapshot.MetadataRevision = "metadata-2"
	write.Snapshot.Title = "Updated title"
	write.Snapshot.ETag = "etag-2"
	write.SyncStartedAt = write.SyncStartedAt.Add(time.Minute)
	write.Snapshot.FetchedAt = write.Snapshot.FetchedAt.Add(time.Minute)
	if _, err := database.StoreChangeRequestSnapshot(write); err != nil {
		t.Fatal(err)
	}
	var title, metadataRevision string
	if err := database.Conn().QueryRow(`
		SELECT title, metadata_revision FROM change_request_snapshots WHERE snapshot_id = ?`,
		write.Snapshot.SnapshotID,
	).Scan(&title, &metadataRevision); err != nil {
		t.Fatal(err)
	}
	if title != "Updated title" || metadataRevision != "metadata-2" {
		t.Fatalf("title=%q metadata_revision=%q", title, metadataRevision)
	}
	assertTableCount(t, database, "change_request_files", 1)
	assertTableCount(t, database, "source_content_blob_refs", 1)
}

func TestStoreChangeRequestSnapshotRejectsContentIdentityRewrite(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")

	write := testChangeRequestSnapshotWrite()
	if _, err := database.StoreChangeRequestSnapshot(write); err != nil {
		t.Fatal(err)
	}
	rewritten := write
	rewritten.Snapshot.Content.HeadSHA = testChangeCommit
	rewritten.Snapshot.Content.Key = "github:content-2"
	if _, err := database.StoreChangeRequestSnapshot(rewritten); err == nil {
		t.Fatal("snapshot ID accepted a different content identity")
	}

	var version, head string
	if err := database.Conn().QueryRow(`
		SELECT content_version_key, head_sha FROM change_request_snapshots WHERE snapshot_id = ?`,
		write.Snapshot.SnapshotID,
	).Scan(&version, &head); err != nil {
		t.Fatal(err)
	}
	if version != string(write.Snapshot.Content.Key) || head != testChangeHeadSHA {
		t.Fatalf("retained version=%q head=%q", version, head)
	}
	assertTableCount(t, database, "source_content_blob_refs", 1)
}

func TestStoreChangeRequestSnapshotRejectsPayloadAndCompletenessDrift(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")
	write := testChangeRequestSnapshotWrite()
	if _, err := database.StoreChangeRequestSnapshot(write); err != nil {
		t.Fatal(err)
	}

	drift := testChangeRequestSnapshotWrite()
	drift.Snapshot.Files[0].DisplayPath = "internal/drift.go"
	drift.Contents[0].Content = []byte("different patch")
	if _, err := database.StoreChangeRequestSnapshot(drift); err == nil {
		t.Fatal("fixed snapshot accepted different file/content payload")
	}
	partial := testChangeRequestSnapshotWrite()
	partial.Snapshot.Completeness.Patches = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
	if _, err := database.StoreChangeRequestSnapshot(partial); err == nil {
		t.Fatal("fixed snapshot accepted a different completeness claim")
	}

	var path string
	if err := database.Conn().QueryRow(`SELECT display_path FROM change_request_files`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "internal/evidence.go" {
		t.Fatalf("stored fixed payload drifted to %q", path)
	}
}

func TestStoreChangeRequestSnapshotScopesForkAliasesAndPreventsCacheRollback(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")
	original := testChangeRequestSnapshotWrite()
	changeKey, err := database.StoreChangeRequestSnapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := CanonicalHostedRepositoryKey(*original.Snapshot.SourceRepository)
	if err != nil {
		t.Fatal(err)
	}
	var branchRepository string
	if err := database.Conn().QueryRow(`
		SELECT repository_id FROM change_request_aliases
		WHERE change_id = ? AND alias_kind = 'branch' AND alias_value = ?`,
		changeKey, original.Snapshot.SourceRef,
	).Scan(&branchRepository); err != nil {
		t.Fatal(err)
	}
	if branchRepository != sourceID {
		t.Fatalf("source branch repository = %q, want %q", branchRepository, sourceID)
	}

	newer := testChangeRequestSnapshotWrite()
	newer.Snapshot.SnapshotID = "snapshot-pr-42-newer"
	newer.Snapshot.Content.Key = "github:provider-pr-42:content-2"
	newer.Snapshot.Content.HeadSHA = testChangeCommit
	newer.Snapshot.FetchedAt = original.Snapshot.FetchedAt.Add(time.Minute)
	newer.SyncStartedAt = original.SyncStartedAt.Add(time.Minute)
	if _, err := database.StoreChangeRequestSnapshot(newer); err != nil {
		t.Fatal(err)
	}
	lateOlderRequest := original
	lateOlderRequest.Snapshot.FetchedAt = newer.Snapshot.FetchedAt.Add(time.Minute)
	if _, err := database.StoreChangeRequestSnapshot(lateOlderRequest); err != nil {
		t.Fatal(err)
	}
	var head string
	if err := database.Conn().QueryRow(`SELECT snapshot_id FROM change_request_cache_heads WHERE change_id = ?`, changeKey).Scan(&head); err != nil {
		t.Fatal(err)
	}
	if head != newer.Snapshot.SnapshotID {
		t.Fatalf("cache head rolled back to %q", head)
	}
	var originalState, newerState string
	if err := database.Conn().QueryRow(`SELECT cache_state FROM change_request_snapshots WHERE snapshot_id = ?`, original.Snapshot.SnapshotID).Scan(&originalState); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`SELECT cache_state FROM change_request_snapshots WHERE snapshot_id = ?`, newer.Snapshot.SnapshotID).Scan(&newerState); err != nil {
		t.Fatal(err)
	}
	if originalState != "stale" || newerState != "current" {
		t.Fatalf("cache states original=%q newer=%q", originalState, newerState)
	}
}

func TestStoreChangeRequestSnapshotPreventsMutableMetadataRollback(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")

	original := testChangeRequestSnapshotWrite()
	if _, err := database.StoreChangeRequestSnapshot(original); err != nil {
		t.Fatal(err)
	}
	newer := original
	newer.SyncStartedAt = original.SyncStartedAt.Add(2 * time.Minute)
	newer.Snapshot.FetchedAt = original.Snapshot.FetchedAt.Add(2 * time.Minute)
	newer.Snapshot.MetadataRevision = "metadata-newer"
	newer.Snapshot.Title = "Newest metadata"
	if _, err := database.StoreChangeRequestSnapshot(newer); err != nil {
		t.Fatal(err)
	}
	lateOlder := original
	lateOlder.SyncStartedAt = original.SyncStartedAt.Add(time.Minute)
	lateOlder.Snapshot.FetchedAt = newer.Snapshot.FetchedAt.Add(time.Minute)
	lateOlder.Snapshot.MetadataRevision = "metadata-older"
	lateOlder.Snapshot.Title = "Late old metadata"
	if _, err := database.StoreChangeRequestSnapshot(lateOlder); err != nil {
		t.Fatal(err)
	}

	var title, revision string
	if err := database.Conn().QueryRow(`
		SELECT title, metadata_revision FROM change_request_snapshots WHERE snapshot_id = ?`,
		original.Snapshot.SnapshotID,
	).Scan(&title, &revision); err != nil {
		t.Fatal(err)
	}
	if title != newer.Snapshot.Title || revision != newer.Snapshot.MetadataRevision {
		t.Fatalf("metadata rolled back to title=%q revision=%q", title, revision)
	}
}

func TestStoreHistoricalChangeRequestSnapshotDoesNotReplaceCurrentHead(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")

	current := testChangeRequestSnapshotWrite()
	changeKey, err := database.StoreChangeRequestSnapshot(current)
	if err != nil {
		t.Fatal(err)
	}
	historical := testChangeRequestSnapshotWrite()
	historical.Snapshot.SnapshotID = "snapshot-pr-42-historical"
	historical.Snapshot.Content.Key = "github:provider-pr-42:historical"
	historical.Snapshot.Content.HeadSHA = testChangeCommit
	historical.SyncStartedAt = current.SyncStartedAt.Add(time.Minute)
	historical.Snapshot.FetchedAt = current.Snapshot.FetchedAt.Add(time.Minute)
	historical.UpdateCacheHead = false
	if _, err := database.StoreChangeRequestSnapshot(historical); err != nil {
		t.Fatal(err)
	}

	var head, currentState, historicalState string
	if err := database.Conn().QueryRow(`SELECT snapshot_id FROM change_request_cache_heads WHERE change_id = ?`, changeKey).Scan(&head); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`SELECT cache_state FROM change_request_snapshots WHERE snapshot_id = ?`, current.Snapshot.SnapshotID).Scan(&currentState); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`SELECT cache_state FROM change_request_snapshots WHERE snapshot_id = ?`, historical.Snapshot.SnapshotID).Scan(&historicalState); err != nil {
		t.Fatal(err)
	}
	if head != current.Snapshot.SnapshotID || currentState != "current" || historicalState != "stale" {
		t.Fatalf("head=%q current=%q historical=%q", head, currentState, historicalState)
	}
}

func TestStoreChangeRequestSnapshotRejectsURLOutsideApprovedOrigins(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")
	write := testChangeRequestSnapshotWrite()
	write.Snapshot.WebURL = "https://unapproved.example/acme/widgets/pull/42"
	if _, err := database.StoreChangeRequestSnapshot(write); err == nil {
		t.Fatal("snapshot accepted a web URL outside approved endpoints")
	}
	assertTableCount(t, database, "change_request_snapshots", 0)
}

func TestStoreChangeRequestSnapshotQuotaFailureRollsBackEverything(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")

	write := testChangeRequestSnapshotWrite()
	write.Quota = SourceContentQuota{MaxFileBytes: 64, MaxSessionBytes: 64, MaxChangeRequestBytes: 3, MaxGlobalBytes: 64}
	if _, err := database.StoreChangeRequestSnapshot(write); !errors.Is(err, ErrSourceContentQuotaExceeded) {
		t.Fatalf("quota error = %v", err)
	}
	for _, table := range []string{
		"hosted_repositories", "change_request_identities", "change_request_snapshots",
		"change_request_files", "change_request_aliases", "source_content_blobs", "source_content_blob_refs",
	} {
		assertTableCount(t, database, table, 0)
	}
}

func TestStoreGenericChangeRequestIsOfflineAndIdempotent(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	reference := model.ChangeRequestReference{
		Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://review.example",
		NormalizedURL: "https://review.example/team/repo/reviews/7",
	}
	digest := sha256.Sum256([]byte(reference.NormalizedURL))
	identity := model.ChangeRequestIdentity{Provider: model.ChangeProviderGeneric, GenericOpaqueID: "generic-" + hex.EncodeToString(digest[:])}
	first, err := database.StoreGenericChangeRequest(reference, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.StoreGenericChangeRequest(reference, identity)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("generic keys differ: %q %q", first, second)
	}
	assertTableCount(t, database, "change_hosts", 0)
	assertTableCount(t, database, "change_request_identities", 1)
	assertTableCount(t, database, "change_request_aliases", 1)
	assertTableCount(t, database, "change_request_snapshots", 0)
}

func TestStoreGenericChangeRequestRejectsReferencesOutsideParserContract(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for name, reference := range map[string]model.ChangeRequestReference{
		"http": {
			Provider: model.ChangeProviderGeneric, DisplayOrigin: "http://review.example",
			NormalizedURL: "http://review.example/team/repo/reviews/7",
		},
		"root": {
			Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://review.example",
			NormalizedURL: "https://review.example/",
		},
		"origin-mismatch": {
			Provider: model.ChangeProviderGeneric, DisplayOrigin: "https://other.example",
			NormalizedURL: "https://review.example/team/repo/reviews/7",
		},
	} {
		t.Run(name, func(t *testing.T) {
			digest := sha256.Sum256([]byte(reference.NormalizedURL))
			identity := model.ChangeRequestIdentity{
				Provider:        model.ChangeProviderGeneric,
				GenericOpaqueID: "generic-" + hex.EncodeToString(digest[:]),
			}
			if _, err := database.StoreGenericChangeRequest(reference, identity); err == nil {
				t.Fatal("unsafe generic reference was accepted")
			}
		})
	}
	assertTableCount(t, database, "change_request_identities", 0)
	assertTableCount(t, database, "change_request_aliases", 0)
}

func TestStoreChangeRequestSnapshotRequiresRetainedExactPatch(t *testing.T) {
	write := testChangeRequestSnapshotWrite()
	write.Contents = []ChangeRequestContentWrite{}
	if err := validateChangeRequestSnapshotWrite(write); err == nil {
		t.Fatal("exact patch accepted without retained content")
	}
	write = testChangeRequestSnapshotWrite()
	write.Snapshot.Files[0].PatchAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
	if err := validateChangeRequestSnapshotWrite(write); err == nil {
		t.Fatal("non-exact patch accepted authoritative retained content")
	}
}

func insertChangeHost(t *testing.T, database *DB, hostID, provider, hostname string) {
	t.Helper()
	origin := "https://" + hostname
	if _, err := database.Conn().Exec(`
		INSERT INTO change_hosts(
			host_id, provider, hostname, display_origin, endpoint_origins_json,
			lifecycle, state, approved_at
		) VALUES (?, ?, ?, ?, ?, 'approved', 'exact', '2026-08-11T00:00:00Z')`,
		hostID, provider, hostname, origin, `["`+origin+`"]`,
	); err != nil {
		t.Fatal(err)
	}
}

func testChangeRequestSnapshotWrite() ChangeRequestSnapshotWrite {
	target := &model.HostedRepositoryIdentity{HostID: "host-github-public", ImmutableID: "repo-target-1", Slug: "acme/widgets"}
	source := &model.HostedRepositoryIdentity{HostID: "host-github-public", ImmutableID: "repo-fork-1", Slug: "contributor/widgets"}
	additions, deletions := 5, 1
	now := time.Date(2026, 8, 11, 5, 6, 7, 0, time.UTC)
	return ChangeRequestSnapshotWrite{
		Snapshot: model.ChangeRequestSnapshot{
			SnapshotID: "snapshot-pr-42",
			Identity: model.ChangeRequestIdentity{
				Provider: model.ChangeProviderGitHub, HostID: target.HostID,
				TargetRepository: target, ProviderObjectID: "provider-pr-42",
			},
			Content: model.ChangeRequestContentVersion{
				Key: "github:provider-pr-42:content-1", BaseRefSHA: testChangeBaseSHA,
				DiffBaseSHA: testChangeBaseSHA, HeadSHA: testChangeHeadSHA,
				FileManifestDigest: "sha256:" + strings.Repeat("a", 64),
			},
			MetadataRevision: "metadata-1", Kind: model.ChangeRequestPullRequest,
			DisplayNumber: "42", LifecycleState: model.ChangeLifecycleOpen,
			Title: "Add evidence", WebURL: "https://github.example/acme/widgets/pull/42",
			SourceRepository: source, SourceRef: "feat/evidence", TargetRef: "main",
			Files: []model.GitFileChange{{
				Ordinal: 0, Key: "hosted:file-1", Layer: model.GitFileLayerHosted,
				DisplayPath: "internal/evidence.go", PathEncoding: model.GitPathUTF8,
				Status: model.GitFileModified, OldMode: "100644", NewMode: "100644",
				Additions: &additions, Deletions: &deletions,
				StatusAssessment: model.ExactGitEvidence(), PatchAssessment: model.ExactGitEvidence(),
				Evidence: []model.GitEvidenceLink{},
			}},
			Commits: []model.GitCandidateCommit{{
				Ordinal: 0, SHA: testChangeCommit, Subject: "Add evidence",
				Relation: model.GitCommitChangeMembership, Assessment: model.ExactGitEvidence(),
				Evidence: []model.GitEvidenceLink{},
			}},
			Completeness: model.ChangeRequestCompleteness{
				Metadata: model.ExactGitEvidence(), FileSet: model.ExactGitEvidence(),
				Patches: model.ExactGitEvidence(), Modes: model.ExactGitEvidence(),
				Commits: model.ExactGitEvidence(),
			},
			ETag: "etag-1", FetchedAt: now,
		},
		SyncStartedAt:   now.Add(-time.Second),
		UpdateCacheHead: true,
		Aliases:         []ChangeRequestAliasWrite{},
		Contents: []ChangeRequestContentWrite{{
			FileKey: "hosted:file-1", Purpose: "patch", Content: []byte("@@ -1 +1 @@\n-old\n+new\n"),
		}},
	}
}
