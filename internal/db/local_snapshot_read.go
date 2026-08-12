package db

import (
	"database/sql"
	"fmt"

	"github.com/bbsteel/session-insight/internal/model"
)

// LocalGitSnapshotFileRecord is the storage projection used by the local Git
// derivation orchestrator. Retained distinguishes an exact empty blob from a
// hash-only or unavailable manifest row.
type LocalGitSnapshotFileRecord struct {
	PathKey      string
	Ordinal      int
	RawPath      []byte
	DisplayPath  string
	PathEncoding model.GitPathEncoding
	Layer        model.GitFileLayer
	FileType     LocalGitFileType
	Mode         string
	GitOID       string
	ContentHash  string
	ContentBytes int64
	Content      []byte
	Retained     bool
	Assessment   model.GitEvidenceAssessment
}

// LocalGitSnapshotRecord reconstructs one immutable local capture. It remains
// a DB-owned projection so persistence does not depend on the Git runner.
type LocalGitSnapshotRecord struct {
	Summary           model.GitSnapshotSummary
	IndexFingerprint  string
	StatusFingerprint string
	Provisional       bool
	Files             []LocalGitSnapshotFileRecord
}

// SessionRepositoryBindingID resolves the storage-private binding ID behind a
// Session-scoped API repository key, preserving pre-v35 IDs when present.
func (db *DB) SessionRepositoryBindingID(rootAgentType, rootSessionID, repositoryEntryKey string) (string, bool, error) {
	var bindingID string
	err := db.conn.QueryRow(`
		SELECT binding_id FROM session_git_bindings
		WHERE agent_type = ? AND session_id = ? AND repository_entry_key = ?`,
		rootAgentType, rootSessionID, repositoryEntryKey,
	).Scan(&bindingID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve Session repository binding: %w", err)
	}
	return bindingID, true, nil
}

// LatestLocalGitSnapshot returns the newest retained snapshot of one kind for
// a binding, including any source bytes retained under the local quota.
func (db *DB) LatestLocalGitSnapshot(bindingID string, kind model.GitSnapshotKind) (LocalGitSnapshotRecord, bool, error) {
	if bindingID == "" || (kind != model.GitSnapshotBaseline && kind != model.GitSnapshotCheckpoint && kind != model.GitSnapshotFinal) {
		return LocalGitSnapshotRecord{}, false, fmt.Errorf("local Git snapshot binding and kind are required")
	}
	var snapshotID string
	err := db.conn.QueryRow(`
		SELECT snapshot_id FROM session_git_snapshots
		WHERE binding_id = ? AND kind = ?
		ORDER BY capture_completed_at DESC, snapshot_id DESC LIMIT 1`, bindingID, kind,
	).Scan(&snapshotID)
	if err == sql.ErrNoRows {
		return LocalGitSnapshotRecord{}, false, nil
	}
	if err != nil {
		return LocalGitSnapshotRecord{}, false, fmt.Errorf("find latest local Git snapshot: %w", err)
	}

	var record LocalGitSnapshotRecord
	var state, reason, reasonsJSON, startedAt, completedAt string
	var provisional int
	if err := db.conn.QueryRow(`
		SELECT snapshot_id, kind, source_revision, manifest_digest, head_sha,
		       index_fingerprint, status_fingerprint, state, reason_code,
		       reasons_json, provisional, capture_started_at, capture_completed_at
		FROM session_git_snapshots
		WHERE binding_id = ? AND snapshot_id = ?`, bindingID, snapshotID,
	).Scan(
		&record.Summary.SnapshotID, &record.Summary.Kind, &record.Summary.SourceRevision,
		&record.Summary.ManifestDigest, &record.Summary.HeadSHA, &record.IndexFingerprint,
		&record.StatusFingerprint, &state, &reason, &reasonsJSON, &provisional,
		&startedAt, &completedAt,
	); err != nil {
		return LocalGitSnapshotRecord{}, false, fmt.Errorf("read local Git snapshot: %w", err)
	}
	var errParse error
	if record.Summary.Assessment, errParse = decodeStoredAssessment(state, reason, reasonsJSON); errParse != nil {
		return LocalGitSnapshotRecord{}, false, errParse
	}
	if record.Summary.CaptureStartedAt, errParse = parseStoredTime(startedAt); errParse != nil {
		return LocalGitSnapshotRecord{}, false, errParse
	}
	if record.Summary.CaptureEndedAt, errParse = parseStoredTime(completedAt); errParse != nil {
		return LocalGitSnapshotRecord{}, false, errParse
	}
	record.Provisional = provisional != 0
	record.Files = []LocalGitSnapshotFileRecord{}

	purpose := "after"
	if kind == model.GitSnapshotBaseline {
		purpose = "before"
	}
	rows, err := db.conn.Query(`
		SELECT file.path_key, file.ordinal, file.raw_path, file.display_path,
		       file.path_encoding, file.layer, file.file_type, file.mode,
		       file.git_oid, file.content_hash, file.content_bytes,
		       file.content_state, file.reason_code,
		       ref.blob_sha, blob.content
		FROM session_git_snapshot_files file
		LEFT JOIN source_content_blob_refs ref
		  ON ref.local_snapshot_id = file.snapshot_id
		 AND ref.path_key = file.path_key AND ref.purpose = ?
		LEFT JOIN source_content_blobs blob ON blob.sha256 = ref.blob_sha
		WHERE file.snapshot_id = ?
		ORDER BY file.ordinal`, purpose, snapshotID)
	if err != nil {
		return LocalGitSnapshotRecord{}, false, fmt.Errorf("query local Git snapshot files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file LocalGitSnapshotFileRecord
		var contentState, contentReason string
		var blobSHA sql.NullString
		var content []byte
		if err := rows.Scan(
			&file.PathKey, &file.Ordinal, &file.RawPath, &file.DisplayPath,
			&file.PathEncoding, &file.Layer, &file.FileType, &file.Mode,
			&file.GitOID, &file.ContentHash, &file.ContentBytes,
			&contentState, &contentReason, &blobSHA, &content,
		); err != nil {
			return LocalGitSnapshotRecord{}, false, fmt.Errorf("scan local Git snapshot file: %w", err)
		}
		state := model.GitEvidenceState(contentState)
		if state == model.GitEvidenceExact {
			file.Assessment = model.ExactGitEvidence()
		} else {
			file.Assessment = model.NonExactGitEvidence(state, model.GitEvidenceReasonCode(contentReason))
		}
		file.Retained = blobSHA.Valid
		if file.Retained {
			file.Content = append([]byte(nil), content...)
		}
		record.Files = append(record.Files, file)
	}
	if err := rows.Err(); err != nil {
		return LocalGitSnapshotRecord{}, false, fmt.Errorf("iterate local Git snapshot files: %w", err)
	}
	return record, true, nil
}
