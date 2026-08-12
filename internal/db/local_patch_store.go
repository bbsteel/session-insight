package db

import (
	"context"
	"fmt"
	"sort"
)

// ReplaceSessionGitPatchContent atomically replaces every retained local
// patch for one server-issued repository entry. Evidence rows must already
// exist in a non-exact publication state; callers publish exact assessments
// only after this transaction succeeds.
func (db *DB) ReplaceSessionGitPatchContent(
	rootAgentType, rootSessionID, repositoryEntryKey string,
	patches map[string][]byte,
	quota SourceContentQuota,
) error {
	if rootAgentType == "" || rootSessionID == "" || repositoryEntryKey == "" || patches == nil {
		return fmt.Errorf("local Git patch replacement requires a root, repository entry, and explicit patch map")
	}
	quota = quota.withDefaults()
	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin local Git patch connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin local Git patch replacement: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	var evidenceID string
	if err := c.QueryRowContext(ctx, `
		SELECT evidence.evidence_id
		FROM session_git_bindings binding
		JOIN session_git_evidence evidence ON evidence.binding_id = binding.binding_id
		WHERE binding.agent_type = ? AND binding.session_id = ?
		  AND binding.repository_entry_key = ?`,
		rootAgentType, rootSessionID, repositoryEntryKey,
	).Scan(&evidenceID); err != nil {
		return fmt.Errorf("resolve local Git patch evidence: %w", err)
	}

	keys := make([]string, 0, len(patches))
	for key := range patches {
		if key == "" {
			return fmt.Errorf("local Git patch file key is required")
		}
		var exists int
		if err := c.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM session_git_files
			WHERE evidence_id = ? AND file_key = ?`, evidenceID, key,
		).Scan(&exists); err != nil {
			return fmt.Errorf("verify local Git patch file %q: %w", key, err)
		}
		if exists != 1 {
			return fmt.Errorf("local Git patch file %q is not published", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if _, err := c.ExecContext(ctx, `
		DELETE FROM source_content_blob_refs
		WHERE evidence_id = ? AND purpose = 'patch'`, evidenceID,
	); err != nil {
		return fmt.Errorf("clear previous local Git patch references: %w", err)
	}
	if _, err := c.ExecContext(ctx, `
		DELETE FROM source_content_blobs
		WHERE NOT EXISTS (
			SELECT 1 FROM source_content_blob_refs refs
			WHERE refs.blob_sha = source_content_blobs.sha256
		)`); err != nil {
		return fmt.Errorf("garbage collect previous local Git patches: %w", err)
	}

	var quotaOwner *SourceContentOwner
	for _, key := range keys {
		owner := SourceContentOwner{EvidenceID: evidenceID, PathKey: key, Purpose: "patch"}
		if _, err := putSourceContentReference(ctx, c, owner, patches[key], quota); err != nil {
			return fmt.Errorf("retain local Git patch %q: %w", key, err)
		}
		if quotaOwner == nil {
			copy := owner
			quotaOwner = &copy
		}
	}
	if quotaOwner != nil {
		if err := checkSourceContentQuota(ctx, c, *quotaOwner, quota); err != nil {
			return err
		}
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit local Git patch replacement: %w", err)
	}
	committed = true
	return nil
}
