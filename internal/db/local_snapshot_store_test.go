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

func TestStoreLocalGitSnapshotPublishesManifestAndContentAtomically(t *testing.T) {
	database, bindingID := openLocalSnapshotTestDB(t, "snapshot-store", "snapshot-entry")
	defer database.Close()

	write := localSnapshotTestWrite(bindingID, "snapshot-final", model.GitSnapshotFinal, "final.txt", []byte("final content"))
	if err := database.StoreLocalGitSnapshot(write); err != nil {
		t.Fatal(err)
	}

	var snapshots, files, refs, blobs int
	for table, destination := range map[string]*int{
		"session_git_snapshots":      &snapshots,
		"session_git_snapshot_files": &files,
		"source_content_blob_refs":   &refs,
		"source_content_blobs":       &blobs,
	} {
		if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if snapshots != 1 || files != 1 || refs != 1 || blobs != 1 {
		t.Fatalf("snapshots=%d files=%d refs=%d blobs=%d", snapshots, files, refs, blobs)
	}

	var storedHash, refPurpose string
	if err := database.Conn().QueryRow(`
		SELECT file.content_hash, ref.purpose
		FROM session_git_snapshot_files file
		JOIN source_content_blob_refs ref
		  ON ref.local_snapshot_id = file.snapshot_id AND ref.path_key = file.path_key`,
	).Scan(&storedHash, &refPurpose); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("final content"))
	if storedHash != hex.EncodeToString(digest[:]) || refPurpose != "after" {
		t.Fatalf("stored hash=%q purpose=%q", storedHash, refPurpose)
	}
}

func TestLatestLocalGitSnapshotRoundTripsRetainedAndSpecialFiles(t *testing.T) {
	database, bindingID := openLocalSnapshotTestDB(t, "snapshot-read", "snapshot-read-entry")
	defer database.Close()

	write := localSnapshotTestWrite(bindingID, "snapshot-read-final", model.GitSnapshotFinal, "empty.txt", []byte{})
	specialDigest := sha256.Sum256([]byte("socket"))
	write.Files = append(write.Files, LocalGitSnapshotFileWrite{
		PathKey: hex.EncodeToString(specialDigest[:]), Ordinal: 1,
		RawPath: []byte("socket"), DisplayPath: "socket", PathEncoding: model.GitPathUTF8,
		Layer: model.GitFileLayerWorktree, FileType: LocalGitFileSpecial,
		Assessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonSnapshotObjectMissing),
	})
	if err := database.StoreLocalGitSnapshot(write); err != nil {
		t.Fatal(err)
	}

	record, ok, err := database.LatestLocalGitSnapshot(bindingID, model.GitSnapshotFinal)
	if err != nil || !ok {
		t.Fatalf("LatestLocalGitSnapshot ok=%v err=%v", ok, err)
	}
	if record.Summary.SnapshotID != "snapshot-read-final" || len(record.Files) != 2 {
		t.Fatalf("snapshot=%q files=%d", record.Summary.SnapshotID, len(record.Files))
	}
	if !record.Files[0].Retained || len(record.Files[0].Content) != 0 {
		t.Fatalf("exact empty content was not retained: %+v", record.Files[0])
	}
	if record.Files[1].FileType != LocalGitFileSpecial || record.Files[1].Retained ||
		record.Files[1].Assessment.ReasonCode != model.ReasonSnapshotObjectMissing {
		t.Fatalf("special file = %+v", record.Files[1])
	}
}

func TestStoreLocalGitSnapshotFailurePreservesPreviousCheckpoint(t *testing.T) {
	database, bindingID := openLocalSnapshotTestDB(t, "snapshot-rollback", "snapshot-rollback-entry")
	defer database.Close()

	first := localSnapshotTestWrite(bindingID, "checkpoint-one", model.GitSnapshotCheckpoint, "first.txt", []byte("first"))
	if err := database.StoreLocalGitSnapshot(first); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		CREATE TRIGGER reject_snapshot_file BEFORE INSERT ON session_git_snapshot_files
		WHEN NEW.display_path = 'fail.txt' BEGIN
			SELECT RAISE(ABORT, 'injected snapshot failure');
		END`); err != nil {
		t.Fatal(err)
	}

	failed := localSnapshotTestWrite(bindingID, "checkpoint-two", model.GitSnapshotCheckpoint, "fail.txt", []byte("second"))
	if err := database.StoreLocalGitSnapshot(failed); err == nil {
		t.Fatal("expected snapshot replacement to fail")
	}

	var snapshotID, displayPath string
	if err := database.Conn().QueryRow(`
		SELECT snapshot.snapshot_id, file.display_path
		FROM session_git_snapshots snapshot
		JOIN session_git_snapshot_files file ON file.snapshot_id = snapshot.snapshot_id
		WHERE snapshot.kind = 'checkpoint'`,
	).Scan(&snapshotID, &displayPath); err != nil {
		t.Fatal(err)
	}
	if snapshotID != "checkpoint-one" || displayPath != "first.txt" {
		t.Fatalf("retained snapshot=%q path=%q", snapshotID, displayPath)
	}
	assertTableCount(t, database, "source_content_blobs", 1)
	assertTableCount(t, database, "source_content_blob_refs", 1)
}

func TestStoreLocalGitSnapshotQuotaFailureRollsBackAggregate(t *testing.T) {
	database, bindingID := openLocalSnapshotTestDB(t, "snapshot-quota", "snapshot-quota-entry")
	defer database.Close()

	write := localSnapshotTestWrite(bindingID, "snapshot-over-quota", model.GitSnapshotFinal, "large.txt", []byte("too large"))
	write.Quota = SourceContentQuota{MaxFileBytes: 64, MaxSessionBytes: 4, MaxChangeRequestBytes: 64, MaxGlobalBytes: 64}
	if err := database.StoreLocalGitSnapshot(write); !errors.Is(err, ErrSourceContentQuotaExceeded) {
		t.Fatalf("quota error = %v", err)
	}
	assertTableCount(t, database, "session_git_snapshots", 0)
	assertTableCount(t, database, "session_git_snapshot_files", 0)
	assertTableCount(t, database, "source_content_blobs", 0)
	assertTableCount(t, database, "source_content_blob_refs", 0)
}

func TestStoreLocalGitSnapshotRetiresOnlyUnreferencedCheckpoint(t *testing.T) {
	database, bindingID := openLocalSnapshotTestDB(t, "snapshot-retire", "snapshot-retire-entry")
	defer database.Close()

	first := localSnapshotTestWrite(bindingID, "checkpoint-old", model.GitSnapshotCheckpoint, "old.txt", []byte("old content"))
	second := localSnapshotTestWrite(bindingID, "checkpoint-new", model.GitSnapshotCheckpoint, "new.txt", []byte("new content"))
	if err := database.StoreLocalGitSnapshot(first); err != nil {
		t.Fatal(err)
	}
	if err := database.StoreLocalGitSnapshot(second); err != nil {
		t.Fatal(err)
	}

	var snapshotID string
	if err := database.Conn().QueryRow(`SELECT snapshot_id FROM session_git_snapshots WHERE kind='checkpoint'`).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if snapshotID != "checkpoint-new" {
		t.Fatalf("remaining checkpoint = %q", snapshotID)
	}
	assertTableCount(t, database, "source_content_blobs", 1)
	assertTableCount(t, database, "source_content_blob_refs", 1)
}

func openLocalSnapshotTestDB(t *testing.T, sessionID, repositoryEntryKey string) (*DB, string) {
	t.Helper()
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	insertTestSession(t, database, "codex", sessionID)
	if err := database.ReplaceSessionGitEvidence(testSessionGitEvidence(sessionID, repositoryEntryKey, "seed.go")); err != nil {
		database.Close()
		t.Fatal(err)
	}
	return database, CanonicalSessionRepositoryBindingID("codex", sessionID, repositoryEntryKey)
}

func localSnapshotTestWrite(bindingID, snapshotID string, kind model.GitSnapshotKind, path string, content []byte) LocalGitSnapshotWrite {
	pathDigest := sha256.Sum256([]byte(path))
	manifestDigest := sha256.Sum256([]byte(snapshotID))
	now := time.Date(2026, 8, 11, 4, 5, 6, 0, time.UTC)
	return LocalGitSnapshotWrite{
		BindingID: bindingID,
		Summary: model.GitSnapshotSummary{
			SnapshotID: snapshotID, Kind: kind,
			ManifestDigest:   "sha256:" + hex.EncodeToString(manifestDigest[:]),
			SourceRevision:   "sha256:" + strings.Repeat("c", 64),
			CaptureStartedAt: now, CaptureEndedAt: now.Add(time.Second),
			Assessment: model.ExactGitEvidence(),
		},
		IndexFingerprint:  "sha256:" + strings.Repeat("d", 64),
		StatusFingerprint: "sha256:" + strings.Repeat("e", 64),
		Files: []LocalGitSnapshotFileWrite{{
			PathKey: hex.EncodeToString(pathDigest[:]), Ordinal: 0,
			RawPath: []byte(path), DisplayPath: path, PathEncoding: model.GitPathUTF8,
			Layer: model.GitFileLayerWorktree, FileType: LocalGitFileRegular,
			ContentBytes: int64(len(content)), Content: content, RetainContent: true, Assessment: model.ExactGitEvidence(),
		}},
	}
}
