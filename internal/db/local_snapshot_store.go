package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

type LocalGitFileType string

const (
	LocalGitFileRegular   LocalGitFileType = "file"
	LocalGitFileSymlink   LocalGitFileType = "symlink"
	LocalGitFileSubmodule LocalGitFileType = "submodule"
	LocalGitFileBinary    LocalGitFileType = "binary"
)

// LocalGitSnapshotFileWrite is one immutable file-manifest row. RetainContent
// distinguishes an exact empty file from a hash-only entry whose bytes were
// intentionally not persisted (for example an over-limit or binary file).
type LocalGitSnapshotFileWrite struct {
	PathKey       string
	Ordinal       int
	RawPath       []byte
	DisplayPath   string
	PathEncoding  model.GitPathEncoding
	Layer         model.GitFileLayer
	FileType      LocalGitFileType
	Mode          string
	GitOID        string
	ContentHash   string
	ContentBytes  int64
	Content       []byte
	RetainContent bool
	Assessment    model.GitEvidenceAssessment
}

// LocalGitSnapshotWrite publishes one capture only after its complete
// manifest and retained content have passed validation and quota checks.
type LocalGitSnapshotWrite struct {
	BindingID         string
	Summary           model.GitSnapshotSummary
	IndexFingerprint  string
	StatusFingerprint string
	Provisional       bool
	Files             []LocalGitSnapshotFileWrite
	Quota             SourceContentQuota
}

// StoreLocalGitSnapshot atomically inserts an immutable local snapshot. A
// failed file insert, content write, or quota check leaves the prior complete
// snapshots untouched. Only obsolete, unreferenced checkpoint/final captures
// are retired; the unique baseline is never silently replaced.
func (db *DB) StoreLocalGitSnapshot(write LocalGitSnapshotWrite) error {
	if err := validateLocalGitSnapshotWrite(write); err != nil {
		return err
	}

	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin local Git snapshot connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin local Git snapshot write: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	var bindingExists int
	if err := c.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_git_bindings WHERE binding_id = ?`, write.BindingID,
	).Scan(&bindingExists); err != nil {
		return fmt.Errorf("inspect local Git snapshot binding: %w", err)
	}
	if bindingExists != 1 {
		return fmt.Errorf("local Git snapshot binding %q does not exist", write.BindingID)
	}

	assessment, err := marshalAssessment(write.Summary.Assessment)
	if err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `
		INSERT INTO session_git_snapshots(
			snapshot_id, binding_id, kind, source_revision, manifest_digest,
			head_sha, index_fingerprint, status_fingerprint, state, reason_code,
			reasons_json, provisional, capture_started_at, capture_completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		write.Summary.SnapshotID, write.BindingID, write.Summary.Kind,
		write.Summary.SourceRevision, write.Summary.ManifestDigest, write.Summary.HeadSHA,
		write.IndexFingerprint, write.StatusFingerprint, assessment.state,
		assessment.reasonCode, assessment.reasonsJSON, boolInt(write.Provisional),
		model.FormatTime(write.Summary.CaptureStartedAt), model.FormatTime(write.Summary.CaptureEndedAt),
	); err != nil {
		return fmt.Errorf("insert local Git snapshot: %w", err)
	}

	quota := write.Quota.withDefaults()
	var quotaOwner *SourceContentOwner
	for _, file := range write.Files {
		contentHash := file.ContentHash
		contentBytes := file.ContentBytes
		if file.RetainContent {
			digest := sha256.Sum256(file.Content)
			computed := hex.EncodeToString(digest[:])
			if contentHash != "" && contentHash != computed {
				return fmt.Errorf("local Git snapshot file %q content hash mismatch", file.PathKey)
			}
			contentHash = computed
			contentBytes = int64(len(file.Content))
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO session_git_snapshot_files(
				snapshot_id, path_key, ordinal, raw_path, display_path, path_encoding,
				layer, file_type, mode, git_oid, content_hash, content_bytes,
				content_state, reason_code
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			write.Summary.SnapshotID, file.PathKey, file.Ordinal, file.RawPath,
			file.DisplayPath, file.PathEncoding, file.Layer, file.FileType, file.Mode,
			file.GitOID, contentHash, contentBytes, file.Assessment.State,
			file.Assessment.ReasonCode,
		); err != nil {
			return fmt.Errorf("insert local Git snapshot file %q: %w", file.PathKey, err)
		}
		if file.RetainContent {
			purpose := "after"
			if write.Summary.Kind == model.GitSnapshotBaseline {
				purpose = "before"
			}
			owner := SourceContentOwner{
				LocalSnapshotID: write.Summary.SnapshotID,
				PathKey:         file.PathKey,
				Purpose:         purpose,
			}
			if _, err := putSourceContentReference(ctx, c, owner, file.Content, quota); err != nil {
				return fmt.Errorf("retain local Git snapshot file %q: %w", file.PathKey, err)
			}
			if quotaOwner == nil {
				ownerCopy := owner
				quotaOwner = &ownerCopy
			}
		}
	}
	if quotaOwner != nil {
		if err := checkSourceContentQuota(ctx, c, *quotaOwner, quota); err != nil {
			return err
		}
	}

	if write.Summary.Kind == model.GitSnapshotCheckpoint || write.Summary.Kind == model.GitSnapshotFinal {
		if _, err := c.ExecContext(ctx, `
			DELETE FROM session_git_snapshots
			WHERE binding_id = ? AND kind = ? AND snapshot_id <> ?
			  AND NOT EXISTS (
				SELECT 1 FROM session_git_evidence evidence
				WHERE evidence.baseline_snapshot_id = session_git_snapshots.snapshot_id
				   OR evidence.final_snapshot_id = session_git_snapshots.snapshot_id
			  )`, write.BindingID, write.Summary.Kind, write.Summary.SnapshotID,
		); err != nil {
			return fmt.Errorf("retire old local Git snapshots: %w", err)
		}
		if _, err := c.ExecContext(ctx, `
			DELETE FROM source_content_blobs
			WHERE NOT EXISTS (
				SELECT 1 FROM source_content_blob_refs refs
				WHERE refs.blob_sha = source_content_blobs.sha256
			)`); err != nil {
			return fmt.Errorf("garbage collect retired local Git snapshot content: %w", err)
		}
	}

	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit local Git snapshot write: %w", err)
	}
	committed = true
	return nil
}

func validateLocalGitSnapshotWrite(write LocalGitSnapshotWrite) error {
	if !validLocalSnapshotID(write.BindingID) || !validLocalSnapshotID(write.Summary.SnapshotID) {
		return fmt.Errorf("local Git snapshot binding and snapshot IDs are required")
	}
	if write.Summary.Kind != model.GitSnapshotBaseline && write.Summary.Kind != model.GitSnapshotCheckpoint && write.Summary.Kind != model.GitSnapshotFinal {
		return fmt.Errorf("invalid local Git snapshot kind %q", write.Summary.Kind)
	}
	if !validLocalSnapshotID(write.Summary.SourceRevision) || write.Summary.CaptureStartedAt.IsZero() || write.Summary.CaptureEndedAt.IsZero() || write.Summary.CaptureEndedAt.Before(write.Summary.CaptureStartedAt) {
		return fmt.Errorf("local Git snapshot requires a source revision and valid capture window")
	}
	if write.Summary.HeadSHA != "" && !isLowerHex(write.Summary.HeadSHA, 40, 64) {
		return fmt.Errorf("local Git snapshot has invalid HEAD object ID")
	}
	for _, item := range []struct {
		field  string
		digest string
	}{
		{field: "manifest", digest: write.Summary.ManifestDigest},
		{field: "index", digest: write.IndexFingerprint},
		{field: "status", digest: write.StatusFingerprint},
	} {
		if item.digest != "" && !validSHA256Digest(item.digest) {
			return fmt.Errorf("local Git snapshot has invalid %s fingerprint", item.field)
		}
	}
	if validation := model.ValidateGitEvidenceAssessment(write.Summary.Assessment); !validation.OK() {
		return fmt.Errorf("validate local Git snapshot assessment: %+v", validation.Issues)
	}
	if write.Files == nil {
		return fmt.Errorf("local Git snapshot files must be an explicit array")
	}
	seen := make(map[string]bool, len(write.Files))
	for index, file := range write.Files {
		if file.Ordinal != index || !isLowerHex(file.PathKey, 64) || seen[file.PathKey] {
			return fmt.Errorf("local Git snapshot file %d has invalid ordinal or path key", index)
		}
		seen[file.PathKey] = true
		if len(file.RawPath) == 0 || strings.ContainsRune(string(file.RawPath), '\x00') ||
			file.DisplayPath == "" || strings.ContainsRune(file.DisplayPath, '\x00') || file.ContentBytes < 0 {
			return fmt.Errorf("local Git snapshot file %q has invalid path or size", file.PathKey)
		}
		if file.PathEncoding != model.GitPathUTF8 && file.PathEncoding != model.GitPathBytesB64 {
			return fmt.Errorf("local Git snapshot file %q has invalid path encoding", file.PathKey)
		}
		if file.Layer != model.GitFileLayerTree && file.Layer != model.GitFileLayerIndex && file.Layer != model.GitFileLayerWorktree {
			return fmt.Errorf("local Git snapshot file %q has invalid layer", file.PathKey)
		}
		switch file.FileType {
		case LocalGitFileRegular, LocalGitFileSymlink, LocalGitFileSubmodule, LocalGitFileBinary:
		default:
			return fmt.Errorf("local Git snapshot file %q has invalid file type", file.PathKey)
		}
		if file.GitOID != "" && !isLowerHex(file.GitOID, 40) && !isLowerHex(file.GitOID, 64) {
			return fmt.Errorf("local Git snapshot file %q has invalid Git object ID", file.PathKey)
		}
		if file.ContentHash != "" && !isLowerHex(file.ContentHash, 64) {
			return fmt.Errorf("local Git snapshot file %q has invalid content hash", file.PathKey)
		}
		if file.RetainContent && file.ContentBytes != int64(len(file.Content)) {
			return fmt.Errorf("local Git snapshot file %q content size mismatch", file.PathKey)
		}
		if !file.RetainContent && file.Content != nil {
			return fmt.Errorf("local Git snapshot file %q supplies content without retention", file.PathKey)
		}
		if validation := model.ValidateGitEvidenceAssessment(file.Assessment); !validation.OK() {
			return fmt.Errorf("validate local Git snapshot file %q assessment: %+v", file.PathKey, validation.Issues)
		}
	}
	return nil
}

func validLocalSnapshotID(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func validSHA256Digest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && isLowerHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func isLowerHex(value string, lengths ...int) bool {
	matchedLength := false
	for _, length := range lengths {
		if len(value) == length {
			matchedLength = true
			break
		}
	}
	if !matchedLength {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
