package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

type ChangeRequestAliasKind string

const (
	ChangeAliasURL            ChangeRequestAliasKind = "url"
	ChangeAliasBranch         ChangeRequestAliasKind = "branch"
	ChangeAliasHeadSHA        ChangeRequestAliasKind = "head_sha"
	ChangeAliasDisplayNumber  ChangeRequestAliasKind = "display_number"
	ChangeAliasProviderNative ChangeRequestAliasKind = "provider_native"
)

type ChangeRequestAliasWrite struct {
	Kind       ChangeRequestAliasKind
	Value      string
	Repository *model.HostedRepositoryIdentity
	ExpiresAt  *time.Time
}

type ChangeRequestContentWrite struct {
	FileKey string
	Purpose string
	Content []byte
}

type ChangeRequestSnapshotWrite struct {
	Snapshot model.ChangeRequestSnapshot
	Aliases  []ChangeRequestAliasWrite
	Contents []ChangeRequestContentWrite
	Quota    SourceContentQuota
}

// CanonicalHostedRepositoryKey derives the opaque local key used by foreign
// keys and API routes. It is based only on provider-immutable identity, never
// on a mutable slug.
func CanonicalHostedRepositoryKey(repository model.HostedRepositoryIdentity) (string, error) {
	if !validLocalSnapshotID(repository.HostID) || !validLocalSnapshotID(repository.ImmutableID) {
		return "", fmt.Errorf("canonical hosted repository identity is incomplete")
	}
	return opaqueAssociationKey("repo", repository.HostID, repository.ImmutableID), nil
}

// CanonicalChangeRequestKey derives a stable opaque local key without
// exposing provider object IDs as database primary keys.
func CanonicalChangeRequestKey(identity model.ChangeRequestIdentity) (string, error) {
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() {
		return "", fmt.Errorf("validate Change Request identity: %+v", validation.Issues)
	}
	if identity.Provider == model.ChangeProviderGeneric {
		return opaqueAssociationKey("change", string(identity.Provider), identity.GenericOpaqueID), nil
	}
	repositoryID, err := CanonicalHostedRepositoryKey(*identity.TargetRepository)
	if err != nil {
		return "", err
	}
	return opaqueAssociationKey("change", string(identity.Provider), identity.HostID, repositoryID, identity.ProviderObjectID), nil
}

func opaqueAssociationKey(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write([]byte(part))
	}
	return prefix + "-" + hex.EncodeToString(hash.Sum(nil))
}

// StoreGenericChangeRequest persists a sanitized, local-only manual identity
// and URL alias. It never creates a host approval or a fabricated snapshot.
func (db *DB) StoreGenericChangeRequest(reference model.ChangeRequestReference, identity model.ChangeRequestIdentity) (string, error) {
	if validation := model.ValidateChangeRequestReference(reference); !validation.OK() || reference.Provider != model.ChangeProviderGeneric {
		return "", fmt.Errorf("validate generic Change Request reference: %+v", validation.Issues)
	}
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() || identity.Provider != model.ChangeProviderGeneric {
		return "", fmt.Errorf("validate generic Change Request identity: %+v", validation.Issues)
	}
	digest := sha256.Sum256([]byte(reference.NormalizedURL))
	if identity.GenericOpaqueID != "generic-"+hex.EncodeToString(digest[:]) {
		return "", fmt.Errorf("generic Change Request identity does not match its sanitized URL")
	}
	changeKey, err := CanonicalChangeRequestKey(identity)
	if err != nil {
		return "", err
	}

	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("pin generic Change Request connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return "", fmt.Errorf("begin generic Change Request write: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if err := ensureChangeRequestIdentity(ctx, c, changeKey, identity); err != nil {
		return "", err
	}
	if _, err := c.ExecContext(ctx, `
		INSERT INTO change_request_aliases(alias_kind, host_id, repository_id, alias_value, change_id, snapshot_id)
		VALUES ('url', NULL, NULL, ?, ?, NULL)
		ON CONFLICT DO NOTHING`, reference.NormalizedURL, changeKey,
	); err != nil {
		return "", fmt.Errorf("store generic Change Request URL alias: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return "", fmt.Errorf("commit generic Change Request write: %w", err)
	}
	committed = true
	return changeKey, nil
}

// StoreChangeRequestSnapshot atomically publishes one fixed provider content
// version, its mutable metadata revision, files, commits, reverse aliases, and
// retained content. Network work must finish before calling this method.
func (db *DB) StoreChangeRequestSnapshot(write ChangeRequestSnapshotWrite) (string, error) {
	if err := validateChangeRequestSnapshotWrite(write); err != nil {
		return "", err
	}
	snapshot := write.Snapshot
	changeKey, err := CanonicalChangeRequestKey(snapshot.Identity)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("pin Change Request snapshot connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return "", fmt.Errorf("begin Change Request snapshot write: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	if err := ensureHostedRepository(ctx, c, *snapshot.Identity.TargetRepository); err != nil {
		return "", err
	}
	var sourceRepositoryID any
	if snapshot.SourceRepository != nil {
		if err := ensureHostedRepository(ctx, c, *snapshot.SourceRepository); err != nil {
			return "", err
		}
		key, err := CanonicalHostedRepositoryKey(*snapshot.SourceRepository)
		if err != nil {
			return "", err
		}
		sourceRepositoryID = key
	}
	if err := ensureChangeRequestIdentity(ctx, c, changeKey, snapshot.Identity); err != nil {
		return "", err
	}

	completenessJSON, err := json.Marshal(snapshot.Completeness)
	if err != nil {
		return "", fmt.Errorf("marshal Change Request completeness: %w", err)
	}
	if _, err := c.ExecContext(ctx, `
		INSERT INTO change_request_snapshots(
			snapshot_id, change_id, content_version_key, native_version,
			metadata_revision, base_ref_sha, diff_base_sha, head_sha,
			file_manifest_digest, kind, display_number, lifecycle_state, draft,
			title, web_url, source_repository_id, source_ref, target_ref,
			merge_commit_sha, squash_commit_sha, completeness_json, etag,
			fetched_at, cache_state
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'current')
		ON CONFLICT(snapshot_id) DO UPDATE SET
			change_id = excluded.change_id,
			content_version_key = excluded.content_version_key,
			native_version = excluded.native_version,
			base_ref_sha = excluded.base_ref_sha,
			diff_base_sha = excluded.diff_base_sha,
			head_sha = excluded.head_sha,
			file_manifest_digest = excluded.file_manifest_digest,
			kind = excluded.kind,
			source_repository_id = excluded.source_repository_id,
			metadata_revision = excluded.metadata_revision,
			display_number = excluded.display_number,
			lifecycle_state = excluded.lifecycle_state,
			draft = excluded.draft,
			title = excluded.title,
			web_url = excluded.web_url,
			source_ref = excluded.source_ref,
			target_ref = excluded.target_ref,
			merge_commit_sha = excluded.merge_commit_sha,
			squash_commit_sha = excluded.squash_commit_sha,
			completeness_json = excluded.completeness_json,
			etag = excluded.etag,
			fetched_at = excluded.fetched_at,
			cache_state = 'current'`,
		snapshot.SnapshotID, changeKey, snapshot.Content.Key,
		snapshot.Content.NativeVersion, snapshot.MetadataRevision,
		snapshot.Content.BaseRefSHA, snapshot.Content.DiffBaseSHA,
		snapshot.Content.HeadSHA, snapshot.Content.FileManifestDigest,
		snapshot.Kind, snapshot.DisplayNumber, snapshot.LifecycleState,
		boolInt(snapshot.Draft), snapshot.Title, snapshot.WebURL,
		sourceRepositoryID, snapshot.SourceRef, snapshot.TargetRef,
		snapshot.MergeCommitSHA, snapshot.SquashCommitSHA,
		string(completenessJSON), snapshot.ETag, model.FormatTime(snapshot.FetchedAt),
	); err != nil {
		return "", fmt.Errorf("upsert Change Request snapshot: %w", err)
	}

	if _, err := c.ExecContext(ctx, `DELETE FROM source_content_blob_refs WHERE change_snapshot_id = ?`, snapshot.SnapshotID); err != nil {
		return "", fmt.Errorf("delete old Change Request content references: %w", err)
	}
	if _, err := c.ExecContext(ctx, `DELETE FROM change_request_files WHERE snapshot_id = ?`, snapshot.SnapshotID); err != nil {
		return "", fmt.Errorf("delete old Change Request files: %w", err)
	}
	if _, err := c.ExecContext(ctx, `DELETE FROM change_request_commits WHERE snapshot_id = ?`, snapshot.SnapshotID); err != nil {
		return "", fmt.Errorf("delete old Change Request commits: %w", err)
	}

	for _, file := range snapshot.Files {
		status, err := marshalAssessment(file.StatusAssessment)
		if err != nil {
			return "", err
		}
		patch, err := marshalAssessment(file.PatchAssessment)
		if err != nil {
			return "", err
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO change_request_files(
				snapshot_id, file_key, ordinal, layer, display_path, old_display_path,
				path_bytes_b64, old_path_bytes_b64, path_encoding, status,
				old_mode, new_mode, binary, submodule, additions, deletions,
				status_state, status_reason_code, status_reasons_json,
				patch_state, patch_reason_code, patch_reasons_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshot.SnapshotID, file.Key, file.Ordinal, file.Layer,
			file.DisplayPath, file.OldDisplayPath, file.PathBytesB64,
			file.OldPathBytesB64, file.PathEncoding, file.Status,
			file.OldMode, file.NewMode, boolInt(file.Binary), boolInt(file.Submodule),
			file.Additions, file.Deletions, status.state, status.reasonCode,
			status.reasonsJSON, patch.state, patch.reasonCode, patch.reasonsJSON,
		); err != nil {
			return "", fmt.Errorf("insert Change Request file %q: %w", file.Key, err)
		}
	}
	for _, commit := range snapshot.Commits {
		assessment, err := marshalAssessment(commit.Assessment)
		if err != nil {
			return "", err
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO change_request_commits(
				snapshot_id, sha, ordinal, subject, author_name, authored_at,
				committed_at, relation, state, reason_code, reasons_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshot.SnapshotID, commit.SHA, commit.Ordinal, commit.Subject,
			commit.AuthorName, formatGitOptionalTime(commit.AuthoredAt),
			formatGitOptionalTime(commit.CommittedAt), commit.Relation,
			assessment.state, assessment.reasonCode, assessment.reasonsJSON,
		); err != nil {
			return "", fmt.Errorf("insert Change Request commit %q: %w", commit.SHA, err)
		}
	}

	quota := write.Quota.withDefaults()
	var quotaOwner *SourceContentOwner
	for _, content := range write.Contents {
		owner := SourceContentOwner{
			ChangeSnapshotID: snapshot.SnapshotID,
			PathKey:          content.FileKey,
			Purpose:          content.Purpose,
		}
		if _, err := putSourceContentReference(ctx, c, owner, content.Content, quota); err != nil {
			return "", fmt.Errorf("retain Change Request content %q: %w", content.FileKey, err)
		}
		if quotaOwner == nil {
			ownerCopy := owner
			quotaOwner = &ownerCopy
		}
	}
	if quotaOwner != nil {
		if err := checkSourceContentQuota(ctx, c, *quotaOwner, quota); err != nil {
			return "", err
		}
	}

	if _, err := c.ExecContext(ctx, `DELETE FROM change_request_aliases WHERE change_id = ? AND snapshot_id = ?`, changeKey, snapshot.SnapshotID); err != nil {
		return "", fmt.Errorf("delete old Change Request aliases: %w", err)
	}
	aliases := append(defaultChangeRequestAliases(snapshot), write.Aliases...)
	for _, alias := range aliases {
		var repositoryID any
		if alias.Repository != nil {
			key, err := CanonicalHostedRepositoryKey(*alias.Repository)
			if err != nil {
				return "", err
			}
			repositoryID = key
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO change_request_aliases(
				alias_kind, host_id, repository_id, alias_value,
				change_id, snapshot_id, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT DO NOTHING`, alias.Kind, snapshot.Identity.HostID,
			repositoryID, alias.Value, changeKey, snapshot.SnapshotID,
			formatGitOptionalTime(alias.ExpiresAt),
		); err != nil {
			return "", fmt.Errorf("insert Change Request alias: %w", err)
		}
	}

	if _, err := c.ExecContext(ctx, `
		INSERT INTO change_request_cache_heads(change_id, snapshot_id, last_sync_at, state, reason_code)
		VALUES (?, ?, ?, 'current', '')
		ON CONFLICT(change_id) DO UPDATE SET
			snapshot_id = excluded.snapshot_id,
			last_sync_at = excluded.last_sync_at,
			state = 'current',
			reason_code = ''`, changeKey, snapshot.SnapshotID, model.FormatTime(snapshot.FetchedAt),
	); err != nil {
		return "", fmt.Errorf("update Change Request cache head: %w", err)
	}
	if _, err := c.ExecContext(ctx, `
		DELETE FROM source_content_blobs
		WHERE NOT EXISTS (
			SELECT 1 FROM source_content_blob_refs refs
			WHERE refs.blob_sha = source_content_blobs.sha256
		)`); err != nil {
		return "", fmt.Errorf("garbage collect replaced Change Request content: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return "", fmt.Errorf("commit Change Request snapshot write: %w", err)
	}
	committed = true
	return changeKey, nil
}

func ensureHostedRepository(ctx context.Context, c *sql.Conn, repository model.HostedRepositoryIdentity) error {
	if err := validateHostedRepositoryForStore(repository); err != nil {
		return err
	}
	repositoryID, err := CanonicalHostedRepositoryKey(repository)
	if err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `
		INSERT INTO hosted_repositories(repository_id, host_id, provider_immutable_id, slug)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(repository_id) DO UPDATE SET slug = excluded.slug`,
		repositoryID, repository.HostID, repository.ImmutableID, repository.Slug,
	); err != nil {
		return fmt.Errorf("upsert hosted repository: %w", err)
	}
	return nil
}

func ensureChangeRequestIdentity(ctx context.Context, c *sql.Conn, changeKey string, identity model.ChangeRequestIdentity) error {
	var targetRepositoryID any
	if identity.TargetRepository != nil {
		key, err := CanonicalHostedRepositoryKey(*identity.TargetRepository)
		if err != nil {
			return err
		}
		targetRepositoryID = key
	}
	if _, err := c.ExecContext(ctx, `
		INSERT INTO change_request_identities(
			change_id, provider, host_id, target_repository_id,
			provider_object_id, generic_opaque_id
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(change_id) DO NOTHING`, changeKey, identity.Provider,
		nullString(identity.HostID), targetRepositoryID,
		identity.ProviderObjectID, identity.GenericOpaqueID,
	); err != nil {
		return fmt.Errorf("insert Change Request identity: %w", err)
	}
	var provider, hostID, repositoryID, objectID, genericID string
	if err := c.QueryRowContext(ctx, `
		SELECT provider, COALESCE(host_id,''), COALESCE(target_repository_id,''),
		       provider_object_id, generic_opaque_id
		FROM change_request_identities WHERE change_id = ?`, changeKey,
	).Scan(&provider, &hostID, &repositoryID, &objectID, &genericID); err != nil {
		return fmt.Errorf("verify Change Request identity: %w", err)
	}
	expectedRepositoryID, _ := targetRepositoryID.(string)
	if provider != string(identity.Provider) || hostID != identity.HostID ||
		repositoryID != expectedRepositoryID || objectID != identity.ProviderObjectID ||
		genericID != identity.GenericOpaqueID {
		return fmt.Errorf("Change Request key %q belongs to a different identity", changeKey)
	}
	return nil
}

func validateChangeRequestSnapshotWrite(write ChangeRequestSnapshotWrite) error {
	if validation := model.ValidateChangeRequestSnapshot(&write.Snapshot); !validation.OK() {
		return fmt.Errorf("validate Change Request snapshot: %+v", validation.Issues)
	}
	if write.Snapshot.Identity.Provider == model.ChangeProviderGeneric {
		return fmt.Errorf("generic Change Requests do not have provider snapshots")
	}
	if err := validateHostedRepositoryForStore(*write.Snapshot.Identity.TargetRepository); err != nil {
		return err
	}
	if write.Snapshot.SourceRepository != nil {
		if err := validateHostedRepositoryForStore(*write.Snapshot.SourceRepository); err != nil {
			return err
		}
		if write.Snapshot.SourceRepository.HostID != write.Snapshot.Identity.HostID {
			return fmt.Errorf("Change Request source repository belongs to another host")
		}
	}
	fileKeys := make(map[string]bool, len(write.Snapshot.Files))
	for _, file := range write.Snapshot.Files {
		fileKeys[file.Key] = true
	}
	seenContent := make(map[string]bool, len(write.Contents))
	for _, content := range write.Contents {
		key := content.FileKey + "\x00" + content.Purpose
		if !fileKeys[content.FileKey] || seenContent[key] {
			return fmt.Errorf("Change Request content has unknown or duplicate file key %q", content.FileKey)
		}
		seenContent[key] = true
		switch content.Purpose {
		case "before", "after", "patch":
		default:
			return fmt.Errorf("Change Request content %q has invalid purpose", content.FileKey)
		}
	}
	for _, file := range write.Snapshot.Files {
		patchKey := file.Key + "\x00patch"
		if write.Snapshot.Completeness.Patches.State == model.GitEvidenceExact && file.PatchAssessment.State != model.GitEvidenceExact {
			return fmt.Errorf("complete Change Request patches contain non-exact file %q", file.Key)
		}
		if file.PatchAssessment.State == model.GitEvidenceExact && !seenContent[patchKey] {
			return fmt.Errorf("exact Change Request patch %q has no retained content", file.Key)
		}
		if seenContent[patchKey] && file.PatchAssessment.State != model.GitEvidenceExact {
			return fmt.Errorf("non-exact Change Request patch %q cannot retain authoritative content", file.Key)
		}
	}
	for _, alias := range append(defaultChangeRequestAliases(write.Snapshot), write.Aliases...) {
		if err := validateChangeRequestAlias(alias, *write.Snapshot.Identity.TargetRepository); err != nil {
			return err
		}
	}
	return nil
}

func validateChangeRequestAlias(alias ChangeRequestAliasWrite, targetRepository model.HostedRepositoryIdentity) error {
	switch alias.Kind {
	case ChangeAliasURL, ChangeAliasBranch, ChangeAliasHeadSHA, ChangeAliasDisplayNumber, ChangeAliasProviderNative:
	default:
		return fmt.Errorf("unknown Change Request alias kind %q", alias.Kind)
	}
	if alias.Value == "" || strings.TrimSpace(alias.Value) != alias.Value || len(alias.Value) > 4096 || strings.ContainsRune(alias.Value, '\x00') {
		return fmt.Errorf("invalid Change Request alias value")
	}
	if alias.Kind == ChangeAliasURL {
		parsed, err := url.Parse(alias.Value)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("unsafe Change Request URL alias")
		}
	}
	if alias.Kind == ChangeAliasHeadSHA && !isLowerHex(alias.Value, 40, 64) {
		return fmt.Errorf("invalid Change Request SHA alias")
	}
	if alias.Repository == nil || alias.Repository.HostID != targetRepository.HostID || alias.Repository.ImmutableID != targetRepository.ImmutableID {
		return fmt.Errorf("Change Request alias repository must match the target repository")
	}
	return nil
}

func validateHostedRepositoryForStore(repository model.HostedRepositoryIdentity) error {
	if _, err := CanonicalHostedRepositoryKey(repository); err != nil {
		return err
	}
	if repository.Slug == "" || strings.TrimSpace(repository.Slug) != repository.Slug ||
		len(repository.Slug) > 4096 || strings.ContainsRune(repository.Slug, '\x00') {
		return fmt.Errorf("hosted repository has invalid slug")
	}
	return nil
}

func defaultChangeRequestAliases(snapshot model.ChangeRequestSnapshot) []ChangeRequestAliasWrite {
	target := snapshot.Identity.TargetRepository
	aliases := []ChangeRequestAliasWrite{
		{Kind: ChangeAliasURL, Value: snapshot.WebURL, Repository: target},
		{Kind: ChangeAliasDisplayNumber, Value: snapshot.DisplayNumber, Repository: target},
		{Kind: ChangeAliasProviderNative, Value: snapshot.Identity.ProviderObjectID, Repository: target},
	}
	if snapshot.Content.HeadSHA != "" {
		aliases = append(aliases, ChangeRequestAliasWrite{Kind: ChangeAliasHeadSHA, Value: snapshot.Content.HeadSHA, Repository: target})
	}
	if snapshot.SourceRef != "" {
		aliases = append(aliases, ChangeRequestAliasWrite{Kind: ChangeAliasBranch, Value: snapshot.SourceRef, Repository: target})
	}
	for _, sha := range []string{snapshot.MergeCommitSHA, snapshot.SquashCommitSHA} {
		if sha != "" {
			aliases = append(aliases, ChangeRequestAliasWrite{Kind: ChangeAliasHeadSHA, Value: sha, Repository: target})
		}
	}
	return aliases
}
