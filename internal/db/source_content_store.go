package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	defaultSourceContentFileBytes          int64 = 1 << 20
	defaultSourceContentSessionBytes       int64 = 20 << 20
	defaultSourceContentChangeRequestBytes int64 = 20 << 20
	defaultSourceContentGlobalBytes        int64 = 1 << 30
)

var ErrSourceContentQuotaExceeded = errors.New("source content quota exceeded")

// SourceContentQuota is based on deduplicated raw bytes for one Session or
// Change Request and deduplicated stored bytes globally. Zero fields select
// the production default, which also keeps tests able to inject small limits.
type SourceContentQuota struct {
	MaxFileBytes          int64
	MaxSessionBytes       int64
	MaxChangeRequestBytes int64
	MaxGlobalBytes        int64
}

var DefaultSourceContentQuota = SourceContentQuota{
	MaxFileBytes:          defaultSourceContentFileBytes,
	MaxSessionBytes:       defaultSourceContentSessionBytes,
	MaxChangeRequestBytes: defaultSourceContentChangeRequestBytes,
	MaxGlobalBytes:        defaultSourceContentGlobalBytes,
}

// SourceContentOwner uses three nullable-owner columns in SQLite so every
// reference has a real foreign key. Exactly one owner ID must be supplied.
type SourceContentOwner struct {
	LocalSnapshotID  string
	EvidenceID       string
	ChangeSnapshotID string
	PathKey          string
	Purpose          string
}

// PutSourceContent deduplicates content, attaches one owner reference, checks
// quotas, and garbage-collects a replaced final reference in one immediate
// transaction.
func (db *DB) PutSourceContent(owner SourceContentOwner, content []byte, quota SourceContentQuota) (string, error) {
	if err := owner.validate(); err != nil {
		return "", err
	}
	quota = quota.withDefaults()

	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("pin source content connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return "", fmt.Errorf("begin source content write: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	sha, err := putSourceContentReference(ctx, c, owner, content, quota)
	if err != nil {
		return "", err
	}
	if err := checkSourceContentQuota(ctx, c, owner, quota); err != nil {
		return "", err
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return "", fmt.Errorf("commit source content write: %w", err)
	}
	committed = true
	return sha, nil
}

// putSourceContentReference writes one blob/reference pair on an existing
// transaction. Aggregate stores use it so metadata, manifests, and retained
// content become visible together. The caller performs aggregate quota checks
// after attaching every reference.
func putSourceContentReference(ctx context.Context, c *sql.Conn, owner SourceContentOwner, content []byte, quota SourceContentQuota) (string, error) {
	if err := owner.validate(); err != nil {
		return "", err
	}
	quota = quota.withDefaults()
	if int64(len(content)) > quota.MaxFileBytes {
		return "", fmt.Errorf("%w: file bytes %d exceed %d", ErrSourceContentQuotaExceeded, len(content), quota.MaxFileBytes)
	}
	digest := sha256.Sum256(content)
	sha := hex.EncodeToString(digest[:])

	if _, err := c.ExecContext(ctx, `
		INSERT INTO source_content_blobs(sha256, content, raw_bytes, stored_bytes, codec)
		VALUES (?, ?, ?, ?, 'raw')
		ON CONFLICT(sha256) DO UPDATE SET last_accessed_at = datetime('now')`,
		sha, content, len(content), len(content),
	); err != nil {
		return "", fmt.Errorf("store source content blob: %w", err)
	}

	clause, args := owner.refPredicate()
	var refID int64
	var oldSHA string
	err = c.QueryRowContext(ctx,
		`SELECT ref_id, blob_sha FROM source_content_blob_refs WHERE `+clause,
		args...,
	).Scan(&refID, &oldSHA)
	switch err {
	case nil:
		if _, err := c.ExecContext(ctx,
			`UPDATE source_content_blob_refs SET blob_sha = ?, created_at = datetime('now') WHERE ref_id = ?`,
			sha, refID,
		); err != nil {
			return "", fmt.Errorf("replace source content reference: %w", err)
		}
	case sql.ErrNoRows:
		if _, err := c.ExecContext(ctx, `
			INSERT INTO source_content_blob_refs(
				blob_sha, local_snapshot_id, evidence_id, change_snapshot_id, path_key, purpose
			) VALUES (?, ?, ?, ?, ?, ?)`,
			sha, nullString(owner.LocalSnapshotID), nullString(owner.EvidenceID),
			nullString(owner.ChangeSnapshotID), owner.PathKey, owner.Purpose,
		); err != nil {
			return "", fmt.Errorf("attach source content reference: %w", err)
		}
	default:
		return "", fmt.Errorf("inspect source content reference: %w", err)
	}

	if oldSHA != "" && oldSHA != sha {
		if err := deleteBlobIfOrphaned(ctx, c, oldSHA); err != nil {
			return "", err
		}
	}
	return sha, nil
}

// DeleteSourceContentRef removes one owner/path/purpose reference and deletes
// its blob only when no local or hosted owner still references it.
func (db *DB) DeleteSourceContentRef(owner SourceContentOwner) error {
	if err := owner.validate(); err != nil {
		return err
	}
	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin source content delete: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin source content delete: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	clause, args := owner.refPredicate()
	var sha string
	err = c.QueryRowContext(ctx,
		`SELECT blob_sha FROM source_content_blob_refs WHERE `+clause,
		args...,
	).Scan(&sha)
	if err == sql.ErrNoRows {
		if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
			return fmt.Errorf("commit empty source content delete: %w", err)
		}
		committed = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("find source content reference: %w", err)
	}
	if _, err := c.ExecContext(ctx, `DELETE FROM source_content_blob_refs WHERE `+clause, args...); err != nil {
		return fmt.Errorf("delete source content reference: %w", err)
	}
	if err := deleteBlobIfOrphaned(ctx, c, sha); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit source content delete: %w", err)
	}
	committed = true
	return nil
}

func (owner SourceContentOwner) validate() error {
	owners := 0
	for _, value := range []string{owner.LocalSnapshotID, owner.EvidenceID, owner.ChangeSnapshotID} {
		if value != "" {
			owners++
		}
	}
	if owners != 1 {
		return fmt.Errorf("source content owner requires exactly one owner ID")
	}
	if owner.PathKey == "" {
		return fmt.Errorf("source content owner path key is required")
	}
	switch owner.Purpose {
	case "before", "after", "patch", "manifest":
		return nil
	default:
		return fmt.Errorf("unknown source content purpose %q", owner.Purpose)
	}
}

func (owner SourceContentOwner) refPredicate() (string, []any) {
	switch {
	case owner.LocalSnapshotID != "":
		return `local_snapshot_id = ? AND path_key = ? AND purpose = ?`, []any{owner.LocalSnapshotID, owner.PathKey, owner.Purpose}
	case owner.EvidenceID != "":
		return `evidence_id = ? AND path_key = ? AND purpose = ?`, []any{owner.EvidenceID, owner.PathKey, owner.Purpose}
	default:
		return `change_snapshot_id = ? AND path_key = ? AND purpose = ?`, []any{owner.ChangeSnapshotID, owner.PathKey, owner.Purpose}
	}
}

func (quota SourceContentQuota) withDefaults() SourceContentQuota {
	if quota.MaxFileBytes <= 0 {
		quota.MaxFileBytes = defaultSourceContentFileBytes
	}
	if quota.MaxSessionBytes <= 0 {
		quota.MaxSessionBytes = defaultSourceContentSessionBytes
	}
	if quota.MaxChangeRequestBytes <= 0 {
		quota.MaxChangeRequestBytes = defaultSourceContentChangeRequestBytes
	}
	if quota.MaxGlobalBytes <= 0 {
		quota.MaxGlobalBytes = defaultSourceContentGlobalBytes
	}
	return quota
}

func checkSourceContentQuota(ctx context.Context, c *sql.Conn, owner SourceContentOwner, quota SourceContentQuota) error {
	var globalBytes int64
	if err := c.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(stored_bytes), 0) FROM source_content_blobs`,
	).Scan(&globalBytes); err != nil {
		return fmt.Errorf("measure global source content: %w", err)
	}
	if globalBytes > quota.MaxGlobalBytes {
		return fmt.Errorf("%w: global stored bytes %d exceed %d", ErrSourceContentQuotaExceeded, globalBytes, quota.MaxGlobalBytes)
	}

	var query string
	var ownerID string
	var limit int64
	switch {
	case owner.LocalSnapshotID != "":
		query = `
			WITH requested_session AS (
				SELECT binding.agent_type, binding.session_id
				FROM session_git_snapshots snapshot
				JOIN session_git_bindings binding ON binding.binding_id = snapshot.binding_id
				WHERE snapshot.snapshot_id = ?
			), scope_blobs AS (
				SELECT r.blob_sha
				FROM source_content_blob_refs r
				JOIN session_git_snapshots snapshot ON snapshot.snapshot_id = r.local_snapshot_id
				JOIN session_git_bindings binding ON binding.binding_id = snapshot.binding_id
				JOIN requested_session requested
				  ON requested.agent_type = binding.agent_type AND requested.session_id = binding.session_id
				UNION
				SELECT r.blob_sha
				FROM source_content_blob_refs r
				JOIN session_git_evidence evidence ON evidence.evidence_id = r.evidence_id
				JOIN session_git_bindings binding ON binding.binding_id = evidence.binding_id
				JOIN requested_session requested
				  ON requested.agent_type = binding.agent_type AND requested.session_id = binding.session_id
			)
			SELECT COALESCE(SUM(b.raw_bytes), 0)
			FROM source_content_blobs b
			WHERE b.sha256 IN (SELECT blob_sha FROM scope_blobs)`
		ownerID = owner.LocalSnapshotID
		limit = quota.MaxSessionBytes
	case owner.EvidenceID != "":
		query = `
			WITH requested_session AS (
				SELECT binding.agent_type, binding.session_id
				FROM session_git_evidence evidence
				JOIN session_git_bindings binding ON binding.binding_id = evidence.binding_id
				WHERE evidence.evidence_id = ?
			), scope_blobs AS (
				SELECT r.blob_sha
				FROM source_content_blob_refs r
				JOIN session_git_snapshots snapshot ON snapshot.snapshot_id = r.local_snapshot_id
				JOIN session_git_bindings binding ON binding.binding_id = snapshot.binding_id
				JOIN requested_session requested
				  ON requested.agent_type = binding.agent_type AND requested.session_id = binding.session_id
				UNION
				SELECT r.blob_sha
				FROM source_content_blob_refs r
				JOIN session_git_evidence evidence ON evidence.evidence_id = r.evidence_id
				JOIN session_git_bindings binding ON binding.binding_id = evidence.binding_id
				JOIN requested_session requested
				  ON requested.agent_type = binding.agent_type AND requested.session_id = binding.session_id
			)
			SELECT COALESCE(SUM(b.raw_bytes), 0)
			FROM source_content_blobs b
			WHERE b.sha256 IN (SELECT blob_sha FROM scope_blobs)`
		ownerID = owner.EvidenceID
		limit = quota.MaxSessionBytes
	default:
		query = `
			SELECT COALESCE(SUM(b.raw_bytes), 0)
			FROM source_content_blobs b
			WHERE b.sha256 IN (
				SELECT DISTINCT r.blob_sha
				FROM source_content_blob_refs r
				JOIN change_request_snapshots s ON s.snapshot_id = r.change_snapshot_id
				JOIN change_request_snapshots requested ON requested.snapshot_id = ?
				WHERE s.change_id = requested.change_id
			)`
		ownerID = owner.ChangeSnapshotID
		limit = quota.MaxChangeRequestBytes
	}
	var scopedBytes int64
	if err := c.QueryRowContext(ctx, query, ownerID).Scan(&scopedBytes); err != nil {
		return fmt.Errorf("measure scoped source content: %w", err)
	}
	if scopedBytes > limit {
		return fmt.Errorf("%w: scoped raw bytes %d exceed %d", ErrSourceContentQuotaExceeded, scopedBytes, limit)
	}
	return nil
}

func deleteBlobIfOrphaned(ctx context.Context, c *sql.Conn, sha string) error {
	if _, err := c.ExecContext(ctx, `
		DELETE FROM source_content_blobs
		WHERE sha256 = ?
		  AND NOT EXISTS (SELECT 1 FROM source_content_blob_refs WHERE blob_sha = ?)`,
		sha, sha,
	); err != nil {
		return fmt.Errorf("garbage collect source content blob: %w", err)
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
