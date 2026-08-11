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
		Aliases: []ChangeRequestAliasWrite{},
		Contents: []ChangeRequestContentWrite{{
			FileKey: "hosted:file-1", Purpose: "patch", Content: []byte("@@ -1 +1 @@\n-old\n+new\n"),
		}},
	}
}
