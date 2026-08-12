package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
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
	Snapshot      model.ChangeRequestSnapshot
	SyncStartedAt time.Time
	// UpdateCacheHead is true only when Snapshot came from resolving the
	// provider's current Change Request version. Historical version fetches
	// remain queryable but must not replace or invalidate the current head.
	UpdateCacheHead bool
	Aliases         []ChangeRequestAliasWrite
	Contents        []ChangeRequestContentWrite
	Quota           SourceContentQuota
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
	if err := validateExactGenericReference(reference); err != nil {
		return "", err
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

func validateExactGenericReference(reference model.ChangeRequestReference) error {
	parsed, err := url.Parse(reference.NormalizedURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		parsed.Path == "" || parsed.EscapedPath() == "/" || strings.ContainsRune(parsed.Path, '\x00') {
		return fmt.Errorf("generic Change Request requires an exact path-contained HTTPS URL")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("generic Change Request URL contains an unsafe path segment")
		}
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return fmt.Errorf("generic Change Request URL has no hostname")
	}
	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("generic Change Request URL has an invalid port")
		}
	}
	if port == "443" {
		port = ""
	}
	if port == "" {
		parsed.Host = hostname
		if strings.Contains(hostname, ":") {
			parsed.Host = "[" + hostname + "]"
		}
	} else {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	parsed.Scheme = "https"
	normalized := parsed.String()
	displayOrigin := (&url.URL{Scheme: "https", Host: parsed.Host}).String()
	if normalized != reference.NormalizedURL || displayOrigin != reference.DisplayOrigin {
		return fmt.Errorf("generic Change Request URL is not exactly normalized")
	}
	return nil
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
	var previousHead sql.NullString
	if err := c.QueryRowContext(ctx,
		`SELECT snapshot_id FROM change_request_cache_heads WHERE change_id = ?`, changeKey,
	).Scan(&previousHead); err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("read previous Change Request cache head: %w", err)
	}
	var previousHeadSync sql.NullInt64
	if err := c.QueryRowContext(ctx,
		`SELECT sync_started_unix_nano FROM change_request_sync_heads WHERE change_id = ?`, changeKey,
	).Scan(&previousHeadSync); err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("read previous Change Request sync order: %w", err)
	}
	incomingSync := write.SyncStartedAt.UnixNano()
	promoteCacheHead := write.UpdateCacheHead &&
		(!previousHeadSync.Valid || incomingSync > previousHeadSync.Int64)

	var snapshotExists bool
	var contentDeleted bool
	var snapshotCount int
	if err := c.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM change_request_snapshots WHERE snapshot_id = ?`, snapshot.SnapshotID,
	).Scan(&snapshotCount); err != nil {
		return "", fmt.Errorf("inspect Change Request snapshot: %w", err)
	}
	snapshotExists = snapshotCount == 1
	snapshotFresh := !snapshotExists
	if snapshotExists {
		var cacheState string
		if err := c.QueryRowContext(ctx,
			`SELECT cache_state FROM change_request_snapshots WHERE snapshot_id = ?`, snapshot.SnapshotID,
		).Scan(&cacheState); err != nil {
			return "", fmt.Errorf("read Change Request cache state: %w", err)
		}
		contentDeleted = cacheState == "content_deleted"
		if err := verifyStoredChangeRequestHeader(ctx, c, changeKey, sourceRepositoryID, snapshot); err != nil {
			return "", err
		}
		var previousSnapshotSync sql.NullInt64
		if err := c.QueryRowContext(ctx,
			`SELECT sync_started_unix_nano FROM change_request_snapshot_syncs WHERE snapshot_id = ?`, snapshot.SnapshotID,
		).Scan(&previousSnapshotSync); err != nil && err != sql.ErrNoRows {
			return "", fmt.Errorf("read Change Request snapshot sync order: %w", err)
		}
		snapshotFresh = !previousSnapshotSync.Valid || incomingSync > previousSnapshotSync.Int64
		if err := verifyStoredChangeRequestPayload(ctx, c, write, contentDeleted); err != nil {
			return "", err
		}
	}
	// Fixed-version content is immutable, so any validated provider response
	// may restore purged bytes. Snapshot metadata still obeys its own request-
	// start order and cannot be rolled back by a late response.
	restoreDeletedContent := contentDeleted
	if err := verifyChangeRequestSnapshotOrigin(ctx, c, snapshot); err != nil {
		return "", err
	}

	completenessJSON, err := json.Marshal(snapshot.Completeness)
	if err != nil {
		return "", fmt.Errorf("marshal Change Request completeness: %w", err)
	}
	desiredCacheState := "stale"
	if promoteCacheHead || (previousHead.Valid && previousHead.String == snapshot.SnapshotID) {
		desiredCacheState = "current"
	}
	if !snapshotExists {
		if _, err := c.ExecContext(ctx, `
			INSERT INTO change_request_snapshots(
			snapshot_id, change_id, content_version_key, native_version,
			metadata_revision, base_ref_sha, diff_base_sha, head_sha,
			file_manifest_digest, kind, display_number, lifecycle_state, draft,
			title, web_url, source_repository_id, source_ref, target_ref,
			merge_commit_sha, squash_commit_sha, completeness_json, etag,
			fetched_at, cache_state
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshot.SnapshotID, changeKey, snapshot.Content.Key,
			snapshot.Content.NativeVersion, snapshot.MetadataRevision,
			snapshot.Content.BaseRefSHA, snapshot.Content.DiffBaseSHA,
			snapshot.Content.HeadSHA, snapshot.Content.FileManifestDigest,
			snapshot.Kind, snapshot.DisplayNumber, snapshot.LifecycleState,
			boolInt(snapshot.Draft), snapshot.Title, snapshot.WebURL,
			sourceRepositoryID, snapshot.SourceRef, snapshot.TargetRef,
			snapshot.MergeCommitSHA, snapshot.SquashCommitSHA,
			string(completenessJSON), snapshot.ETag, model.FormatTime(snapshot.FetchedAt), desiredCacheState,
		); err != nil {
			return "", fmt.Errorf("insert Change Request snapshot: %w", err)
		}
	} else if snapshotFresh {
		if _, err := c.ExecContext(ctx, `
			UPDATE change_request_snapshots SET
				metadata_revision = ?, display_number = ?, lifecycle_state = ?,
				draft = ?, title = ?, web_url = ?, source_ref = ?, target_ref = ?,
				merge_commit_sha = ?, squash_commit_sha = ?, etag = ?, fetched_at = ?,
				cache_state = ?
			WHERE snapshot_id = ?`,
			snapshot.MetadataRevision, snapshot.DisplayNumber, snapshot.LifecycleState,
			boolInt(snapshot.Draft), snapshot.Title, snapshot.WebURL,
			snapshot.SourceRef, snapshot.TargetRef, snapshot.MergeCommitSHA,
			snapshot.SquashCommitSHA, snapshot.ETag, model.FormatTime(snapshot.FetchedAt),
			desiredCacheState, snapshot.SnapshotID,
		); err != nil {
			return "", fmt.Errorf("update Change Request snapshot metadata: %w", err)
		}
	}
	if snapshotFresh {
		if _, err := c.ExecContext(ctx, `
			INSERT INTO change_request_snapshot_syncs(snapshot_id, sync_started_unix_nano)
			VALUES (?, ?)
			ON CONFLICT(snapshot_id) DO UPDATE SET
				sync_started_unix_nano = excluded.sync_started_unix_nano
			WHERE excluded.sync_started_unix_nano > change_request_snapshot_syncs.sync_started_unix_nano`,
			snapshot.SnapshotID, incomingSync,
		); err != nil {
			return "", fmt.Errorf("update Change Request snapshot sync order: %w", err)
		}
	}

	if !snapshotExists {
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

	}
	if !snapshotExists || restoreDeletedContent {
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
	}
	if restoreDeletedContent && !snapshotFresh {
		if _, err := c.ExecContext(ctx, `
			UPDATE change_request_snapshots SET cache_state = ? WHERE snapshot_id = ?`,
			desiredCacheState, snapshot.SnapshotID,
		); err != nil {
			return "", fmt.Errorf("restore Change Request cache state: %w", err)
		}
	}
	if restoreDeletedContent && previousHead.Valid && previousHead.String == snapshot.SnapshotID {
		if _, err := c.ExecContext(ctx, `
			UPDATE change_request_cache_heads
			SET state = 'current', reason_code = ''
			WHERE change_id = ? AND snapshot_id = ?`, changeKey, snapshot.SnapshotID,
		); err != nil {
			return "", fmt.Errorf("restore Change Request cache head state: %w", err)
		}
	}

	if !snapshotExists || snapshotFresh {
		if _, err := c.ExecContext(ctx, `DELETE FROM change_request_aliases WHERE change_id = ? AND snapshot_id = ?`, changeKey, snapshot.SnapshotID); err != nil {
			return "", fmt.Errorf("delete old Change Request aliases: %w", err)
		}
		aliases := append(defaultChangeRequestAliases(snapshot), write.Aliases...)
		for _, alias := range aliases {
			if alias.Kind == ChangeAliasURL {
				if err := verifyChangeRequestURLOrigin(ctx, c, snapshot.Identity.HostID, alias.Value); err != nil {
					return "", err
				}
			}
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
	}

	var syncRows int64
	if write.UpdateCacheHead {
		syncResult, err := c.ExecContext(ctx, `
		INSERT INTO change_request_sync_heads(change_id, snapshot_id, sync_started_unix_nano)
		VALUES (?, ?, ?)
		ON CONFLICT(change_id) DO UPDATE SET
			snapshot_id = excluded.snapshot_id,
			sync_started_unix_nano = excluded.sync_started_unix_nano
		WHERE excluded.sync_started_unix_nano > change_request_sync_heads.sync_started_unix_nano`,
			changeKey, snapshot.SnapshotID, incomingSync,
		)
		if err != nil {
			return "", fmt.Errorf("update Change Request sync order: %w", err)
		}
		syncRows, err = syncResult.RowsAffected()
		if err != nil {
			return "", fmt.Errorf("inspect Change Request sync-order update: %w", err)
		}
	}
	if syncRows > 0 {
		if _, err := c.ExecContext(ctx, `
			INSERT INTO change_request_cache_heads(change_id, snapshot_id, last_sync_at, state, reason_code)
			VALUES (?, ?, ?, 'current', '')
			ON CONFLICT(change_id) DO UPDATE SET
				snapshot_id = excluded.snapshot_id,
				last_sync_at = excluded.last_sync_at,
				state = 'current',
				reason_code = ''`,
			changeKey, snapshot.SnapshotID, model.FormatTime(snapshot.FetchedAt),
		); err != nil {
			return "", fmt.Errorf("update Change Request cache head: %w", err)
		}
		if _, err := c.ExecContext(ctx, `
			UPDATE change_request_snapshots
			SET cache_state = CASE
				WHEN snapshot_id = ? THEN 'current'
				WHEN cache_state = 'content_deleted' THEN cache_state
				ELSE 'stale'
			END
			WHERE change_id = ?`, snapshot.SnapshotID, changeKey,
		); err != nil {
			return "", fmt.Errorf("mark Change Request cache versions: %w", err)
		}
	}
	if syncRows > 0 && previousHead.Valid && previousHead.String != snapshot.SnapshotID {
		pending := model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonChangeRequestPendingReconfirmation)
		pendingStored, err := marshalAssessment(pending)
		if err != nil {
			return "", err
		}
		if _, err := c.ExecContext(ctx, `
			UPDATE session_change_requests
			SET state = ?, reason_code = ?, reasons_json = ?
			WHERE change_id = ? AND relationship = 'exclusive'
			  AND snapshot_id <> ?`,
			pendingStored.state, pendingStored.reasonCode, pendingStored.reasonsJSON,
			changeKey, snapshot.SnapshotID,
		); err != nil {
			return "", fmt.Errorf("mark old exclusive Change Request links pending reconfirmation: %w", err)
		}
		if _, err := c.ExecContext(ctx, `
			UPDATE session_git_evidence
			SET revision = revision + 1,
			    state = ?, reason_code = ?, reasons_json = ?,
			    stale = 1, authority = 'none', selected_change_snapshot_id = NULL,
			    authority_selection_json = '{}'
			WHERE authority = 'hosted_change' AND EXISTS (
				SELECT 1 FROM session_change_requests links
				WHERE links.binding_id = session_git_evidence.binding_id
				  AND links.change_id = ? AND links.relationship = 'exclusive'
				  AND links.snapshot_id <> ?
				  AND links.snapshot_id = session_git_evidence.selected_change_snapshot_id
				  AND links.link_id = json_extract(session_git_evidence.authority_selection_json, '$.link_id')
			)`,
			pendingStored.state, pendingStored.reasonCode, pendingStored.reasonsJSON,
			changeKey, snapshot.SnapshotID,
		); err != nil {
			return "", fmt.Errorf("demote stale hosted Change Request authority: %w", err)
		}
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return "", fmt.Errorf("commit Change Request snapshot write: %w", err)
	}
	committed = true
	return changeKey, nil
}

func verifyStoredChangeRequestHeader(
	ctx context.Context,
	c *sql.Conn,
	changeKey string,
	sourceRepositoryID any,
	snapshot model.ChangeRequestSnapshot,
) error {
	var storedChangeKey, contentVersion, nativeVersion string
	var baseRefSHA, diffBaseSHA, headSHA, manifestDigest, kind string
	var storedSourceRepository sql.NullString
	if err := c.QueryRowContext(ctx, `
		SELECT change_id, content_version_key, native_version, base_ref_sha,
		       diff_base_sha, head_sha, file_manifest_digest, kind,
		       source_repository_id
		FROM change_request_snapshots WHERE snapshot_id = ?`, snapshot.SnapshotID,
	).Scan(
		&storedChangeKey, &contentVersion, &nativeVersion, &baseRefSHA,
		&diffBaseSHA, &headSHA, &manifestDigest, &kind, &storedSourceRepository,
	); err != nil {
		return fmt.Errorf("read fixed Change Request snapshot header: %w", err)
	}
	if storedChangeKey != changeKey ||
		contentVersion != string(snapshot.Content.Key) ||
		nativeVersion != snapshot.Content.NativeVersion ||
		baseRefSHA != snapshot.Content.BaseRefSHA ||
		diffBaseSHA != snapshot.Content.DiffBaseSHA ||
		headSHA != snapshot.Content.HeadSHA ||
		manifestDigest != snapshot.Content.FileManifestDigest ||
		kind != string(snapshot.Kind) ||
		!nullableStringMatchesAny(storedSourceRepository, sourceRepositoryID) {
		return fmt.Errorf("fixed Change Request snapshot header differs from stored content identity")
	}
	return nil
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
		return fmt.Errorf("change request key %q belongs to a different identity", changeKey)
	}
	return nil
}

func validateChangeRequestSnapshotWrite(write ChangeRequestSnapshotWrite) error {
	if write.SyncStartedAt.IsZero() || write.SyncStartedAt.UnixNano() <= 0 || write.SyncStartedAt.After(write.Snapshot.FetchedAt) {
		return fmt.Errorf("change request sync requires a request start no later than its fetch completion")
	}
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
			return fmt.Errorf("change request source repository belongs to another host")
		}
	}
	fileKeys := make(map[string]bool, len(write.Snapshot.Files))
	for _, file := range write.Snapshot.Files {
		if len(file.Evidence) != 0 {
			return fmt.Errorf("hosted Change Request files cannot carry Session evidence anchors")
		}
		fileKeys[file.Key] = true
	}
	for _, commit := range write.Snapshot.Commits {
		if len(commit.Evidence) != 0 {
			return fmt.Errorf("hosted Change Request commits cannot carry Session evidence anchors")
		}
	}
	seenContent := make(map[string]bool, len(write.Contents))
	for _, content := range write.Contents {
		key := content.FileKey + "\x00" + content.Purpose
		if !fileKeys[content.FileKey] || seenContent[key] {
			return fmt.Errorf("change request content has unknown or duplicate file key %q", content.FileKey)
		}
		seenContent[key] = true
		switch content.Purpose {
		case "before", "after", "patch":
		default:
			return fmt.Errorf("change request content %q has invalid purpose", content.FileKey)
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
		if err := validateChangeRequestAlias(alias, write.Snapshot); err != nil {
			return err
		}
	}
	return nil
}

func validateChangeRequestAlias(alias ChangeRequestAliasWrite, snapshot model.ChangeRequestSnapshot) error {
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
	if alias.Repository == nil || alias.Repository.HostID != snapshot.Identity.HostID {
		return fmt.Errorf("change request alias repository must use the Change Request host")
	}
	target := snapshot.Identity.TargetRepository
	isTarget := alias.Repository.ImmutableID == target.ImmutableID
	isSource := snapshot.SourceRepository != nil && alias.Repository.ImmutableID == snapshot.SourceRepository.ImmutableID
	if !isTarget && !isSource {
		return fmt.Errorf("change request alias repository must match its target or fixed source repository")
	}
	if (alias.Kind == ChangeAliasURL || alias.Kind == ChangeAliasDisplayNumber || alias.Kind == ChangeAliasProviderNative) && !isTarget {
		return fmt.Errorf("change request identity aliases must use the target repository")
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
	source := snapshot.SourceRepository
	if source == nil {
		source = target
	}
	aliases := []ChangeRequestAliasWrite{
		{Kind: ChangeAliasURL, Value: snapshot.WebURL, Repository: target},
		{Kind: ChangeAliasDisplayNumber, Value: snapshot.DisplayNumber, Repository: target},
		{Kind: ChangeAliasProviderNative, Value: snapshot.Identity.ProviderObjectID, Repository: target},
	}
	if snapshot.Content.HeadSHA != "" {
		aliases = append(aliases, ChangeRequestAliasWrite{Kind: ChangeAliasHeadSHA, Value: snapshot.Content.HeadSHA, Repository: source})
	}
	if snapshot.SourceRef != "" {
		aliases = append(aliases, ChangeRequestAliasWrite{Kind: ChangeAliasBranch, Value: snapshot.SourceRef, Repository: source})
	}
	for _, sha := range []string{snapshot.MergeCommitSHA, snapshot.SquashCommitSHA} {
		if sha != "" {
			aliases = append(aliases, ChangeRequestAliasWrite{Kind: ChangeAliasHeadSHA, Value: sha, Repository: target})
		}
	}
	return aliases
}

type changeRequestPayload struct {
	Completeness model.ChangeRequestCompleteness `json:"completeness"`
	Files        []changeRequestPayloadFile      `json:"files"`
	Commits      []changeRequestPayloadCommit    `json:"commits"`
	Contents     []changeRequestPayloadContent   `json:"contents"`
}

type changeRequestPayloadFile struct {
	Ordinal          int                         `json:"ordinal"`
	Key              string                      `json:"key"`
	Layer            model.GitFileLayer          `json:"layer"`
	DisplayPath      string                      `json:"display_path"`
	OldDisplayPath   string                      `json:"old_display_path"`
	PathBytesB64     string                      `json:"path_bytes_b64"`
	OldPathBytesB64  string                      `json:"old_path_bytes_b64"`
	PathEncoding     model.GitPathEncoding       `json:"path_encoding"`
	Status           model.GitFileStatus         `json:"status"`
	OldMode          string                      `json:"old_mode"`
	NewMode          string                      `json:"new_mode"`
	Binary           bool                        `json:"binary"`
	Submodule        bool                        `json:"submodule"`
	Additions        *int                        `json:"additions"`
	Deletions        *int                        `json:"deletions"`
	StatusAssessment model.GitEvidenceAssessment `json:"status_assessment"`
	PatchAssessment  model.GitEvidenceAssessment `json:"patch_assessment"`
}

type changeRequestPayloadCommit struct {
	Ordinal     int                              `json:"ordinal"`
	SHA         string                           `json:"sha"`
	Subject     string                           `json:"subject"`
	AuthorName  string                           `json:"author_name"`
	AuthoredAt  string                           `json:"authored_at"`
	CommittedAt string                           `json:"committed_at"`
	Relation    model.GitCandidateCommitRelation `json:"relation"`
	Assessment  model.GitEvidenceAssessment      `json:"assessment"`
}

type changeRequestPayloadContent struct {
	FileKey string `json:"file_key"`
	Purpose string `json:"purpose"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

func verifyStoredChangeRequestPayload(ctx context.Context, c *sql.Conn, write ChangeRequestSnapshotWrite, allowContentRestore bool) error {
	expected := changeRequestPayloadFromWrite(write)
	actual, err := readStoredChangeRequestPayload(ctx, c, write.Snapshot.SnapshotID)
	if err != nil {
		return err
	}
	if allowContentRestore {
		expectedContent := make(map[string]changeRequestPayloadContent, len(expected.Contents))
		for _, content := range expected.Contents {
			expectedContent[content.FileKey+"\x00"+content.Purpose] = content
		}
		for _, content := range actual.Contents {
			if wanted, ok := expectedContent[content.FileKey+"\x00"+content.Purpose]; !ok || wanted != content {
				return fmt.Errorf("retained Change Request content differs from the fixed snapshot payload")
			}
		}
		expected.Contents = []changeRequestPayloadContent{}
		actual.Contents = []changeRequestPayloadContent{}
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return fmt.Errorf("marshal expected Change Request payload: %w", err)
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		return fmt.Errorf("marshal stored Change Request payload: %w", err)
	}
	if !bytes.Equal(expectedJSON, actualJSON) {
		return fmt.Errorf("fixed Change Request snapshot payload differs from stored content")
	}
	return nil
}

func changeRequestPayloadFromWrite(write ChangeRequestSnapshotWrite) changeRequestPayload {
	payload := changeRequestPayload{
		Completeness: write.Snapshot.Completeness,
		Files:        make([]changeRequestPayloadFile, 0, len(write.Snapshot.Files)),
		Commits:      make([]changeRequestPayloadCommit, 0, len(write.Snapshot.Commits)),
		Contents:     make([]changeRequestPayloadContent, 0, len(write.Contents)),
	}
	for _, file := range write.Snapshot.Files {
		payload.Files = append(payload.Files, changeRequestPayloadFile{
			Ordinal: file.Ordinal, Key: file.Key, Layer: file.Layer,
			DisplayPath: file.DisplayPath, OldDisplayPath: file.OldDisplayPath,
			PathBytesB64: file.PathBytesB64, OldPathBytesB64: file.OldPathBytesB64,
			PathEncoding: file.PathEncoding, Status: file.Status,
			OldMode: file.OldMode, NewMode: file.NewMode, Binary: file.Binary,
			Submodule: file.Submodule, Additions: file.Additions, Deletions: file.Deletions,
			StatusAssessment: file.StatusAssessment, PatchAssessment: file.PatchAssessment,
		})
	}
	for _, commit := range write.Snapshot.Commits {
		payload.Commits = append(payload.Commits, changeRequestPayloadCommit{
			Ordinal: commit.Ordinal, SHA: commit.SHA, Subject: commit.Subject,
			AuthorName: commit.AuthorName, AuthoredAt: optionalTimeString(commit.AuthoredAt),
			CommittedAt: optionalTimeString(commit.CommittedAt), Relation: commit.Relation,
			Assessment: commit.Assessment,
		})
	}
	for _, content := range write.Contents {
		digest := sha256.Sum256(content.Content)
		payload.Contents = append(payload.Contents, changeRequestPayloadContent{
			FileKey: content.FileKey, Purpose: content.Purpose,
			SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(content.Content)),
		})
	}
	sort.Slice(payload.Contents, func(i, j int) bool {
		if payload.Contents[i].FileKey == payload.Contents[j].FileKey {
			return payload.Contents[i].Purpose < payload.Contents[j].Purpose
		}
		return payload.Contents[i].FileKey < payload.Contents[j].FileKey
	})
	return payload
}

func readStoredChangeRequestPayload(ctx context.Context, c *sql.Conn, snapshotID string) (changeRequestPayload, error) {
	payload := changeRequestPayload{
		Files: []changeRequestPayloadFile{}, Commits: []changeRequestPayloadCommit{},
		Contents: []changeRequestPayloadContent{},
	}
	var completenessJSON string
	if err := c.QueryRowContext(ctx,
		`SELECT completeness_json FROM change_request_snapshots WHERE snapshot_id = ?`, snapshotID,
	).Scan(&completenessJSON); err != nil {
		return payload, fmt.Errorf("read stored Change Request completeness: %w", err)
	}
	if err := json.Unmarshal([]byte(completenessJSON), &payload.Completeness); err != nil {
		return payload, fmt.Errorf("decode stored Change Request completeness: %w", err)
	}
	fileRows, err := c.QueryContext(ctx, `
		SELECT ordinal, file_key, layer, display_path, old_display_path,
		       path_bytes_b64, old_path_bytes_b64, path_encoding, status,
		       old_mode, new_mode, binary, submodule, additions, deletions,
		       status_state, status_reason_code, status_reasons_json,
		       patch_state, patch_reason_code, patch_reasons_json
		FROM change_request_files WHERE snapshot_id = ? ORDER BY ordinal`, snapshotID)
	if err != nil {
		return payload, fmt.Errorf("read stored Change Request files: %w", err)
	}
	for fileRows.Next() {
		var file changeRequestPayloadFile
		var binary, submodule int
		var additions, deletions sql.NullInt64
		var statusState, statusReason, statusReasons string
		var patchState, patchReason, patchReasons string
		if err := fileRows.Scan(
			&file.Ordinal, &file.Key, &file.Layer, &file.DisplayPath, &file.OldDisplayPath,
			&file.PathBytesB64, &file.OldPathBytesB64, &file.PathEncoding, &file.Status,
			&file.OldMode, &file.NewMode, &binary, &submodule, &additions, &deletions,
			&statusState, &statusReason, &statusReasons, &patchState, &patchReason, &patchReasons,
		); err != nil {
			fileRows.Close()
			return payload, fmt.Errorf("scan stored Change Request file: %w", err)
		}
		file.Binary, file.Submodule = binary != 0, submodule != 0
		file.Additions = nullableInt(additions)
		file.Deletions = nullableInt(deletions)
		file.StatusAssessment, err = decodeStoredAssessment(statusState, statusReason, statusReasons)
		if err == nil {
			file.PatchAssessment, err = decodeStoredAssessment(patchState, patchReason, patchReasons)
		}
		if err != nil {
			fileRows.Close()
			return payload, err
		}
		payload.Files = append(payload.Files, file)
	}
	if err := fileRows.Err(); err != nil {
		fileRows.Close()
		return payload, fmt.Errorf("iterate stored Change Request files: %w", err)
	}
	if err := fileRows.Close(); err != nil {
		return payload, err
	}

	commitRows, err := c.QueryContext(ctx, `
		SELECT ordinal, sha, subject, author_name, COALESCE(authored_at,''),
		       COALESCE(committed_at,''), relation, state, reason_code, reasons_json
		FROM change_request_commits WHERE snapshot_id = ? ORDER BY ordinal`, snapshotID)
	if err != nil {
		return payload, fmt.Errorf("read stored Change Request commits: %w", err)
	}
	for commitRows.Next() {
		var commit changeRequestPayloadCommit
		var state, reason, reasons string
		if err := commitRows.Scan(
			&commit.Ordinal, &commit.SHA, &commit.Subject, &commit.AuthorName,
			&commit.AuthoredAt, &commit.CommittedAt, &commit.Relation,
			&state, &reason, &reasons,
		); err != nil {
			commitRows.Close()
			return payload, fmt.Errorf("scan stored Change Request commit: %w", err)
		}
		commit.Assessment, err = decodeStoredAssessment(state, reason, reasons)
		if err != nil {
			commitRows.Close()
			return payload, err
		}
		payload.Commits = append(payload.Commits, commit)
	}
	if err := commitRows.Err(); err != nil {
		commitRows.Close()
		return payload, fmt.Errorf("iterate stored Change Request commits: %w", err)
	}
	if err := commitRows.Close(); err != nil {
		return payload, err
	}

	contentRows, err := c.QueryContext(ctx, `
		SELECT refs.path_key, refs.purpose, blobs.sha256, blobs.raw_bytes
		FROM source_content_blob_refs refs
		JOIN source_content_blobs blobs ON blobs.sha256 = refs.blob_sha
		WHERE refs.change_snapshot_id = ?
		ORDER BY refs.path_key, refs.purpose`, snapshotID)
	if err != nil {
		return payload, fmt.Errorf("read stored Change Request content: %w", err)
	}
	for contentRows.Next() {
		var content changeRequestPayloadContent
		if err := contentRows.Scan(&content.FileKey, &content.Purpose, &content.SHA256, &content.Bytes); err != nil {
			contentRows.Close()
			return payload, fmt.Errorf("scan stored Change Request content: %w", err)
		}
		payload.Contents = append(payload.Contents, content)
	}
	if err := contentRows.Err(); err != nil {
		contentRows.Close()
		return payload, fmt.Errorf("iterate stored Change Request content: %w", err)
	}
	if err := contentRows.Close(); err != nil {
		return payload, err
	}
	return payload, nil
}

func nullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func optionalTimeString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return model.FormatTime(*value)
}

func verifyChangeRequestSnapshotOrigin(ctx context.Context, c *sql.Conn, snapshot model.ChangeRequestSnapshot) error {
	return verifyChangeRequestURLOrigin(ctx, c, snapshot.Identity.HostID, snapshot.WebURL)
}

func verifyChangeRequestURLOrigin(ctx context.Context, c *sql.Conn, hostID, rawURL string) error {
	var lifecycle, endpointsJSON string
	if err := c.QueryRowContext(ctx, `
		SELECT lifecycle, endpoint_origins_json FROM change_hosts WHERE host_id = ?`,
		hostID,
	).Scan(&lifecycle, &endpointsJSON); err != nil {
		return fmt.Errorf("read Change Request host approval: %w", err)
	}
	if lifecycle != "approved" {
		return fmt.Errorf("change request host is not approved")
	}
	var endpoints []string
	if err := json.Unmarshal([]byte(endpointsJSON), &endpoints); err != nil {
		return fmt.Errorf("decode Change Request host endpoints: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse Change Request web URL: %w", err)
	}
	origin := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
	for _, endpoint := range endpoints {
		if endpoint == origin {
			return nil
		}
	}
	return fmt.Errorf("change request web URL is outside the approved host endpoints")
}
