package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bbsteel/session-insight/internal/model"
)

var (
	ErrChangeRequestNotFound  = errors.New("change request not found")
	ErrSourceContentNotFound  = errors.New("source content not found")
	ErrSourceContentReadLimit = errors.New("source content exceeds read limit")
)

// ChangeRequest returns one cached current or fixed historical Change Request
// without network access. Empty contentVersion selects the current cache head.
func (db *DB) ChangeRequest(changeKey string, contentVersion model.ContentVersionKey) (ChangeRequestRecord, error) {
	identity, err := db.readChangeRequestIdentity(changeKey)
	if err != nil {
		return ChangeRequestRecord{}, err
	}
	record := ChangeRequestRecord{
		ChangeKey: changeKey, Identity: identity, Aliases: []string{},
		CacheAssessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestNotFound),
	}
	aliasRows, err := db.conn.Query(`
		SELECT DISTINCT alias_value FROM change_request_aliases
		WHERE change_id = ? AND alias_kind = 'url'
		ORDER BY alias_value`, changeKey)
	if err != nil {
		return ChangeRequestRecord{}, fmt.Errorf("query Change Request URL aliases: %w", err)
	}
	for aliasRows.Next() {
		var alias string
		if err := aliasRows.Scan(&alias); err != nil {
			aliasRows.Close()
			return ChangeRequestRecord{}, err
		}
		record.Aliases = append(record.Aliases, alias)
	}
	if err := aliasRows.Close(); err != nil {
		return ChangeRequestRecord{}, err
	}
	if identity.Provider == model.ChangeProviderGeneric {
		record.CacheState = "offline"
		record.CacheAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeProviderUnsupported)
		return record, nil
	}

	var snapshotID string
	var cacheState, cacheReason string
	if contentVersion == "" {
		err = db.conn.QueryRow(`
			SELECT head.snapshot_id, head.state, head.reason_code
			FROM change_request_cache_heads head WHERE head.change_id = ?`, changeKey,
		).Scan(&snapshotID, &cacheState, &cacheReason)
	} else {
		err = db.conn.QueryRow(`
			SELECT snapshot_id, cache_state FROM change_request_snapshots
			WHERE change_id = ? AND content_version_key = ?`, changeKey, contentVersion,
		).Scan(&snapshotID, &cacheState)
	}
	if err == sql.ErrNoRows {
		return ChangeRequestRecord{}, ErrChangeRequestNotFound
	}
	if err != nil {
		return ChangeRequestRecord{}, fmt.Errorf("resolve Change Request snapshot: %w", err)
	}
	snapshot, err := db.readChangeRequestSnapshot(changeKey, identity, snapshotID)
	if err != nil {
		return ChangeRequestRecord{}, err
	}
	record.Snapshot = snapshot
	record.CacheState = cacheState
	record.CacheAssessment = changeRequestCacheAssessment(cacheState, cacheReason)
	return record, nil
}

func (db *DB) readChangeRequestIdentity(changeKey string) (model.ChangeRequestIdentity, error) {
	var identity model.ChangeRequestIdentity
	var hostID, targetRepositoryID, repositoryHostID, immutableID, slug sql.NullString
	if err := db.conn.QueryRow(`
		SELECT identity.provider, identity.host_id, identity.target_repository_id,
		       identity.provider_object_id, identity.generic_opaque_id,
		       repository.host_id, repository.provider_immutable_id, repository.slug
		FROM change_request_identities identity
		LEFT JOIN hosted_repositories repository
		  ON repository.repository_id = identity.target_repository_id
		WHERE identity.change_id = ?`, changeKey,
	).Scan(
		&identity.Provider, &hostID, &targetRepositoryID,
		&identity.ProviderObjectID, &identity.GenericOpaqueID,
		&repositoryHostID, &immutableID, &slug,
	); err != nil {
		if err == sql.ErrNoRows {
			return identity, ErrChangeRequestNotFound
		}
		return identity, fmt.Errorf("read Change Request identity: %w", err)
	}
	if hostID.Valid {
		identity.HostID = hostID.String
	}
	if targetRepositoryID.Valid {
		if !repositoryHostID.Valid || !immutableID.Valid || !slug.Valid {
			return identity, fmt.Errorf("stored Change Request target repository is incomplete")
		}
		identity.TargetRepository = &model.HostedRepositoryIdentity{
			HostID: repositoryHostID.String, ImmutableID: immutableID.String, Slug: slug.String,
		}
	}
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() {
		return identity, fmt.Errorf("validate stored Change Request identity: %+v", validation.Issues)
	}
	return identity, nil
}

func (db *DB) readChangeRequestSnapshot(changeKey string, identity model.ChangeRequestIdentity, snapshotID string) (*model.ChangeRequestSnapshot, error) {
	var snapshot model.ChangeRequestSnapshot
	var sourceRepositoryID, sourceHostID, sourceImmutableID, sourceSlug sql.NullString
	var completenessJSON, fetchedAt string
	var draft int
	if err := db.conn.QueryRow(`
		SELECT snapshot.snapshot_id, snapshot.content_version_key, snapshot.native_version,
		       snapshot.metadata_revision, snapshot.base_ref_sha, snapshot.diff_base_sha,
		       snapshot.head_sha, snapshot.file_manifest_digest, snapshot.kind,
		       snapshot.display_number, snapshot.lifecycle_state, snapshot.draft,
		       snapshot.title, snapshot.web_url, snapshot.source_repository_id,
		       source.host_id, source.provider_immutable_id, source.slug,
		       snapshot.source_ref, snapshot.target_ref, snapshot.merge_commit_sha,
		       snapshot.squash_commit_sha, snapshot.completeness_json,
		       snapshot.etag, snapshot.fetched_at
		FROM change_request_snapshots snapshot
		LEFT JOIN hosted_repositories source
		  ON source.repository_id = snapshot.source_repository_id
		WHERE snapshot.change_id = ? AND snapshot.snapshot_id = ?`, changeKey, snapshotID,
	).Scan(
		&snapshot.SnapshotID, &snapshot.Content.Key, &snapshot.Content.NativeVersion,
		&snapshot.MetadataRevision, &snapshot.Content.BaseRefSHA, &snapshot.Content.DiffBaseSHA,
		&snapshot.Content.HeadSHA, &snapshot.Content.FileManifestDigest, &snapshot.Kind,
		&snapshot.DisplayNumber, &snapshot.LifecycleState, &draft, &snapshot.Title,
		&snapshot.WebURL, &sourceRepositoryID, &sourceHostID, &sourceImmutableID, &sourceSlug,
		&snapshot.SourceRef, &snapshot.TargetRef, &snapshot.MergeCommitSHA,
		&snapshot.SquashCommitSHA, &completenessJSON, &snapshot.ETag, &fetchedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrChangeRequestNotFound
		}
		return nil, fmt.Errorf("read Change Request snapshot: %w", err)
	}
	snapshot.Identity = identity
	snapshot.Draft = draft != 0
	if sourceRepositoryID.Valid {
		if !sourceHostID.Valid || !sourceImmutableID.Valid || !sourceSlug.Valid {
			return nil, fmt.Errorf("stored Change Request source repository is incomplete")
		}
		snapshot.SourceRepository = &model.HostedRepositoryIdentity{
			HostID: sourceHostID.String, ImmutableID: sourceImmutableID.String, Slug: sourceSlug.String,
		}
	}
	if err := json.Unmarshal([]byte(completenessJSON), &snapshot.Completeness); err != nil {
		return nil, fmt.Errorf("decode Change Request completeness: %w", err)
	}
	var err error
	if snapshot.FetchedAt, err = parseStoredTime(fetchedAt); err != nil {
		return nil, err
	}
	if snapshot.Files, err = db.readChangeRequestFiles(snapshotID); err != nil {
		return nil, err
	}
	if snapshot.Commits, err = db.readChangeRequestCommits(snapshotID); err != nil {
		return nil, err
	}
	if validation := model.ValidateChangeRequestSnapshot(&snapshot); !validation.OK() {
		return nil, fmt.Errorf("validate stored Change Request snapshot: %+v", validation.Issues)
	}
	return &snapshot, nil
}

func (db *DB) readChangeRequestFiles(snapshotID string) ([]model.GitFileChange, error) {
	rows, err := db.conn.Query(`
		SELECT file_key, ordinal, layer, display_path, old_display_path,
		       path_bytes_b64, old_path_bytes_b64, path_encoding, status,
		       old_mode, new_mode, binary, submodule, additions, deletions,
		       status_state, status_reason_code, status_reasons_json,
		       patch_state, patch_reason_code, patch_reasons_json
		FROM change_request_files WHERE snapshot_id = ? ORDER BY ordinal`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("query Change Request files: %w", err)
	}
	defer rows.Close()
	files := []model.GitFileChange{}
	for rows.Next() {
		var file model.GitFileChange
		var binary, submodule int
		var additions, deletions sql.NullInt64
		var statusState, statusReason, statusReasons string
		var patchState, patchReason, patchReasons string
		if err := rows.Scan(
			&file.Key, &file.Ordinal, &file.Layer, &file.DisplayPath, &file.OldDisplayPath,
			&file.PathBytesB64, &file.OldPathBytesB64, &file.PathEncoding, &file.Status,
			&file.OldMode, &file.NewMode, &binary, &submodule, &additions, &deletions,
			&statusState, &statusReason, &statusReasons, &patchState, &patchReason, &patchReasons,
		); err != nil {
			return nil, err
		}
		file.Binary = binary != 0
		file.Submodule = submodule != 0
		file.Additions = nullableInt(additions)
		file.Deletions = nullableInt(deletions)
		file.Evidence = []model.GitEvidenceLink{}
		if file.StatusAssessment, err = decodeStoredAssessment(statusState, statusReason, statusReasons); err != nil {
			return nil, err
		}
		if file.PatchAssessment, err = decodeStoredAssessment(patchState, patchReason, patchReasons); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (db *DB) readChangeRequestCommits(snapshotID string) ([]model.GitCandidateCommit, error) {
	rows, err := db.conn.Query(`
		SELECT sha, ordinal, subject, author_name, authored_at, committed_at,
		       relation, state, reason_code, reasons_json
		FROM change_request_commits WHERE snapshot_id = ? ORDER BY ordinal`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("query Change Request commits: %w", err)
	}
	defer rows.Close()
	commits := []model.GitCandidateCommit{}
	for rows.Next() {
		var commit model.GitCandidateCommit
		var authoredAt, committedAt sql.NullString
		var state, reason, reasons string
		if err := rows.Scan(
			&commit.SHA, &commit.Ordinal, &commit.Subject, &commit.AuthorName,
			&authoredAt, &committedAt, &commit.Relation, &state, &reason, &reasons,
		); err != nil {
			return nil, err
		}
		var err error
		if commit.AuthoredAt, err = parseOptionalStoredTime(authoredAt); err != nil {
			return nil, err
		}
		if commit.CommittedAt, err = parseOptionalStoredTime(committedAt); err != nil {
			return nil, err
		}
		if commit.Assessment, err = decodeStoredAssessment(state, reason, reasons); err != nil {
			return nil, err
		}
		commit.Evidence = []model.GitEvidenceLink{}
		commits = append(commits, commit)
	}
	return commits, rows.Err()
}

func changeRequestCacheAssessment(state, reason string) model.GitEvidenceAssessment {
	switch state {
	case "current":
		return model.ExactGitEvidence()
	case "stale":
		code := model.GitEvidenceReasonCode(reason)
		if code == "" {
			code = model.ReasonChangeRequestRevisionChanged
		}
		return model.NonExactGitEvidence(model.GitEvidenceEstimated, code)
	case "content_deleted":
		return model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonChangeRequestPartial)
	default:
		code := model.GitEvidenceReasonCode(reason)
		if code == "" {
			code = model.ReasonChangeRequestPartial
		}
		return model.NonExactGitEvidence(model.GitEvidenceUnavailable, code)
	}
}

// ChangeRequestPatch reads only a retained exact patch selected by opaque
// Change Request, version, and file keys. It never accepts a filesystem path.
func (db *DB) ChangeRequestPatch(changeKey string, contentVersion model.ContentVersionKey, fileKey string, maxBytes int64) ([]byte, error) {
	if changeKey == "" || contentVersion == "" || fileKey == "" || maxBytes <= 0 {
		return nil, ErrSourceContentNotFound
	}
	var content []byte
	var rawBytes int64
	err := db.conn.QueryRow(`
		SELECT blob.content, blob.raw_bytes
		FROM change_request_snapshots snapshot
		JOIN change_request_files file ON file.snapshot_id = snapshot.snapshot_id
		JOIN source_content_blob_refs ref
		  ON ref.change_snapshot_id = snapshot.snapshot_id
		 AND ref.path_key = file.file_key AND ref.purpose = 'patch'
		JOIN source_content_blobs blob ON blob.sha256 = ref.blob_sha
		WHERE snapshot.change_id = ? AND snapshot.content_version_key = ?
		  AND file.file_key = ? AND file.patch_state = 'exact'
		LIMIT 1`, changeKey, contentVersion, fileKey,
	).Scan(&content, &rawBytes)
	if err == sql.ErrNoRows {
		return nil, ErrSourceContentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read Change Request patch: %w", err)
	}
	if rawBytes > maxBytes || int64(len(content)) > maxBytes {
		return nil, ErrSourceContentReadLimit
	}
	return append([]byte(nil), content...), nil
}

// SessionGitEvidencePatch resolves a file patch through one server-issued
// repository entry. Hosted authority reads its fixed selected snapshot;
// local authority reads the derived evidence content reference.
func (db *DB) SessionGitEvidencePatch(rootAgentType, rootSessionID, repositoryEntryKey, fileKey string, maxBytes int64) ([]byte, error) {
	if rootAgentType == "" || rootSessionID == "" || repositoryEntryKey == "" || fileKey == "" || maxBytes <= 0 {
		return nil, ErrSourceContentNotFound
	}
	var evidenceID, authority string
	var selectedSnapshot sql.NullString
	if err := db.conn.QueryRow(`
		SELECT evidence.evidence_id, evidence.authority, evidence.selected_change_snapshot_id
		FROM session_git_bindings binding
		JOIN session_git_evidence evidence ON evidence.binding_id = binding.binding_id
		WHERE binding.agent_type = ? AND binding.session_id = ?
		  AND binding.repository_entry_key = ?`,
		rootAgentType, rootSessionID, repositoryEntryKey,
	).Scan(&evidenceID, &authority, &selectedSnapshot); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSourceContentNotFound
		}
		return nil, fmt.Errorf("resolve Session Git patch authority: %w", err)
	}
	var ownerColumn string
	var ownerID string
	if authority == string(model.GitAuthorityHostedChange) && selectedSnapshot.Valid {
		ownerColumn = "change_snapshot_id"
		ownerID = selectedSnapshot.String
	} else {
		ownerColumn = "evidence_id"
		ownerID = evidenceID
	}
	// ownerColumn is selected only from the closed constants above; values
	// remain bound query parameters.
	query := fmt.Sprintf(`
		SELECT blob.content, blob.raw_bytes
		FROM source_content_blob_refs ref
		JOIN source_content_blobs blob ON blob.sha256 = ref.blob_sha
		LEFT JOIN session_git_files file
		  ON file.evidence_id = ref.evidence_id AND file.file_key = ref.path_key
		WHERE ref.%s = ? AND ref.path_key = ? AND ref.purpose = 'patch'
		  AND (? = 'hosted_change' OR file.patch_state = 'exact')
		LIMIT 1`, ownerColumn)
	var content []byte
	var rawBytes int64
	if err := db.conn.QueryRow(query, ownerID, fileKey, authority).Scan(&content, &rawBytes); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSourceContentNotFound
		}
		return nil, fmt.Errorf("read Session Git patch: %w", err)
	}
	if rawBytes > maxBytes || int64(len(content)) > maxBytes {
		return nil, ErrSourceContentReadLimit
	}
	return append([]byte(nil), content...), nil
}

// SessionChangeRequestLinks returns every persisted root link, including
// related links that intentionally have no repository binding.
func (db *DB) SessionChangeRequestLinks(rootAgentType, rootSessionID string) ([]model.SessionChangeRequestLink, error) {
	return db.readSessionChangeRequestLinks(rootAgentType, rootSessionID, "")
}

func (db *DB) readSessionChangeRequestLinks(rootAgentType, rootSessionID, bindingID string) ([]model.SessionChangeRequestLink, error) {
	query := `
		SELECT links.link_id, links.ordinal, links.root_agent_type, links.root_session_id,
		       links.source_agent_type, links.source_session_id, links.collaboration_revision,
		       links.invocation_id, COALESCE(binding.repository_entry_key,''),
		       links.content_version_key, links.relationship, links.method,
		       links.state, links.reason_code, links.reasons_json,
		       links.confirmation_source, links.confirmation_revision,
		       identity.provider, identity.host_id, identity.provider_object_id,
		       identity.generic_opaque_id, repository.host_id,
		       repository.provider_immutable_id, repository.slug
		FROM session_change_requests links
		JOIN change_request_identities identity ON identity.change_id = links.change_id
		LEFT JOIN session_git_bindings binding ON binding.binding_id = links.binding_id
		LEFT JOIN hosted_repositories repository ON repository.repository_id = identity.target_repository_id
		WHERE links.root_agent_type = ? AND links.root_session_id = ?`
	args := []any{rootAgentType, rootSessionID}
	if bindingID != "" {
		query += ` AND links.binding_id = ?`
		args = append(args, bindingID)
	}
	query += ` ORDER BY COALESCE(binding.repository_entry_key,''), links.ordinal, links.link_id`
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query Session Change Request links: %w", err)
	}
	defer rows.Close()
	links := []model.SessionChangeRequestLink{}
	for rows.Next() {
		var link model.SessionChangeRequestLink
		var state, reason, reasons string
		var identityHostID, repositoryHostID, immutableID, slug sql.NullString
		if err := rows.Scan(
			&link.LinkID, &link.Ordinal, &link.RootAgentType, &link.RootSessionID,
			&link.SourceAgentType, &link.SourceSessionID, &link.CollaborationRevision,
			&link.InvocationID, &link.RepositoryEntryKey, &link.ContentVersionKey,
			&link.Relationship, &link.Method, &state, &reason, &reasons,
			&link.ConfirmationSource, &link.ConfirmationRevision,
			&link.Change.Provider, &identityHostID, &link.Change.ProviderObjectID,
			&link.Change.GenericOpaqueID, &repositoryHostID, &immutableID, &slug,
		); err != nil {
			return nil, fmt.Errorf("scan Session Change Request link: %w", err)
		}
		if identityHostID.Valid {
			link.Change.HostID = identityHostID.String
		}
		if repositoryHostID.Valid {
			link.Change.TargetRepository = &model.HostedRepositoryIdentity{
				HostID: repositoryHostID.String, ImmutableID: immutableID.String, Slug: slug.String,
			}
		}
		if link.Assessment, err = decodeStoredAssessment(state, reason, reasons); err != nil {
			return nil, err
		}
		if link.Evidence, err = db.readChangeRequestEvidenceLinks(link.LinkID); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (db *DB) readChangeRequestEvidenceLinks(linkID string) ([]model.GitEvidenceLink, error) {
	rows, err := db.conn.Query(`
		SELECT root_agent_type, root_session_id, source_agent_type, source_session_id,
		       backing_agent_type, backing_session_id, invocation_id, source_revision,
		       positions_revision, event_id, tool_call_id, turn_index, recorded_at,
		       state, reason_code, reasons_json
		FROM session_change_request_evidence_links
		WHERE change_link_id = ? ORDER BY ordinal`, linkID)
	if err != nil {
		return nil, fmt.Errorf("query Change Request evidence anchors: %w", err)
	}
	defer rows.Close()
	links := []model.GitEvidenceLink{}
	for rows.Next() {
		link, err := scanGitEvidenceLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}
