package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

type ChangeRequestSessionMatch struct {
	ChangeKey          string                          `json:"change_key"`
	LinkID             string                          `json:"link_id,omitempty"`
	RootAgentType      string                          `json:"root_agent_type"`
	RootSessionID      string                          `json:"root_session_id"`
	RepositoryEntryKey string                          `json:"repository_entry_key,omitempty"`
	ContentVersionKey  model.ContentVersionKey         `json:"content_version_key,omitempty"`
	Relationship       model.ChangeRequestRelationship `json:"relationship,omitempty"`
	Match              string                          `json:"match"`
	Assessment         model.GitEvidenceAssessment     `json:"assessment"`
}

// StoreSessionHostedRepositoryBinding records a provider-resolved mapping
// from one Session repository entry to one immutable hosted repository. SHA
// and branch reverse lookup must pass through this mapping; object IDs alone
// are never matched across unrelated repositories.
func (db *DB) StoreSessionHostedRepositoryBinding(
	rootAgentType, rootSessionID, repositoryEntryKey string,
	repository model.HostedRepositoryIdentity,
) error {
	if err := validateHostedRepositoryForStore(repository); err != nil {
		return err
	}
	repositoryID, err := CanonicalHostedRepositoryKey(repository)
	if err != nil {
		return err
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin Session hosted repository binding: %w", err)
	}
	defer tx.Rollback()
	var bindingID string
	if err := tx.QueryRow(`
		SELECT binding_id FROM session_git_bindings
		WHERE agent_type = ? AND session_id = ? AND repository_entry_key = ?`,
		rootAgentType, rootSessionID, repositoryEntryKey,
	).Scan(&bindingID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("Session repository entry does not exist")
		}
		return fmt.Errorf("resolve Session repository entry: %w", err)
	}
	var lifecycle string
	if err := tx.QueryRow(`
		SELECT lifecycle FROM change_hosts WHERE host_id = ?`, repository.HostID,
	).Scan(&lifecycle); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("Change Request host does not exist")
		}
		return fmt.Errorf("resolve Change Request host: %w", err)
	}
	if lifecycle != "approved" {
		return fmt.Errorf("Change Request host is not approved")
	}
	if _, err := tx.Exec(`
		INSERT INTO hosted_repositories(repository_id, host_id, provider_immutable_id, slug)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(repository_id) DO UPDATE SET slug = excluded.slug`,
		repositoryID, repository.HostID, repository.ImmutableID, repository.Slug,
	); err != nil {
		return fmt.Errorf("upsert hosted repository: %w", err)
	}
	var hostID, immutableID string
	if err := tx.QueryRow(`
		SELECT host_id, provider_immutable_id FROM hosted_repositories
		WHERE repository_id = ?`, repositoryID,
	).Scan(&hostID, &immutableID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("hosted repository does not exist")
		}
		return fmt.Errorf("resolve hosted repository: %w", err)
	}
	if hostID != repository.HostID || immutableID != repository.ImmutableID {
		return fmt.Errorf("hosted repository identity changed")
	}
	if _, err := tx.Exec(`
		INSERT INTO session_hosted_repository_bindings(binding_id, host_id, repository_id, resolved_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(binding_id) DO UPDATE SET
			host_id = excluded.host_id,
			repository_id = excluded.repository_id,
			resolved_at = excluded.resolved_at`,
		bindingID, repository.HostID, repositoryID, model.FormatTime(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("store Session hosted repository binding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Session hosted repository binding: %w", err)
	}
	return nil
}

// CanonicalChangeRequestConfirmationRevision ties one explicit user
// assertion to the current root, repository binding, collaboration revision,
// canonical Change Request, and fixed content version.
func CanonicalChangeRequestConfirmationRevision(link model.SessionChangeRequestLink) (string, error) {
	changeKey, err := CanonicalChangeRequestKey(link.Change)
	if err != nil {
		return "", err
	}
	if link.Relationship != model.ChangeRelationshipExclusive || link.RepositoryEntryKey == "" || link.ContentVersionKey == "" || link.CollaborationRevision < 1 {
		return "", fmt.Errorf("exclusive Change Request confirmation is incomplete")
	}
	return opaqueAssociationKey(
		"confirmation", link.RootAgentType, link.RootSessionID,
		link.RepositoryEntryKey, changeKey, string(link.ContentVersionKey),
		strconv.FormatInt(link.CollaborationRevision, 10),
	), nil
}

// StoreSessionChangeRequestLink atomically appends or updates one server-
// derived link. New ordinals are assigned inside the transaction; an existing
// link keeps its original ordinal. The database rechecks collaboration and
// repository ownership instead of trusting client-supplied root fields.
func (db *DB) StoreSessionChangeRequestLink(link model.SessionChangeRequestLink) (model.SessionChangeRequestLink, error) {
	changeKey, err := CanonicalChangeRequestKey(link.Change)
	if err != nil {
		return model.SessionChangeRequestLink{}, err
	}
	if link.Relationship == model.ChangeRelationshipExclusive {
		expected, err := CanonicalChangeRequestConfirmationRevision(link)
		if err != nil {
			return model.SessionChangeRequestLink{}, err
		}
		if link.ConfirmationSource != model.ChangeConfirmationUser || link.ConfirmationRevision != expected {
			return model.SessionChangeRequestLink{}, fmt.Errorf("exclusive Change Request confirmation revision is invalid")
		}
	}
	if validation := model.ValidateSessionChangeRequestLink(
		link, link.RootAgentType, link.RootSessionID, link.RepositoryEntryKey,
	); !validation.OK() {
		return model.SessionChangeRequestLink{}, fmt.Errorf("validate Session Change Request link: %+v", validation.Issues)
	}

	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return model.SessionChangeRequestLink{}, fmt.Errorf("pin Session Change Request connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return model.SessionChangeRequestLink{}, fmt.Errorf("begin Session Change Request write: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	if err := verifyStoredChangeRequestIdentity(ctx, c, changeKey, link.Change); err != nil {
		return model.SessionChangeRequestLink{}, err
	}
	if err := verifyChangeLinkCollaboration(ctx, c, link); err != nil {
		return model.SessionChangeRequestLink{}, err
	}

	var bindingID any
	if link.RepositoryEntryKey != "" {
		var resolved string
		if err := c.QueryRowContext(ctx, `
			SELECT binding_id FROM session_git_bindings
			WHERE agent_type = ? AND session_id = ? AND repository_entry_key = ?`,
			link.RootAgentType, link.RootSessionID, link.RepositoryEntryKey,
		).Scan(&resolved); err != nil {
			if err == sql.ErrNoRows {
				return model.SessionChangeRequestLink{}, fmt.Errorf("Session repository entry does not exist")
			}
			return model.SessionChangeRequestLink{}, fmt.Errorf("resolve Session repository entry: %w", err)
		}
		bindingID = resolved
	}

	var snapshotID any
	if link.ContentVersionKey != "" {
		var resolved, completenessJSON, cacheState string
		if err := c.QueryRowContext(ctx, `
			SELECT snapshot_id, completeness_json, cache_state
			FROM change_request_snapshots
			WHERE change_id = ? AND content_version_key = ?`,
			changeKey, link.ContentVersionKey,
		).Scan(&resolved, &completenessJSON, &cacheState); err != nil {
			if err == sql.ErrNoRows {
				return model.SessionChangeRequestLink{}, fmt.Errorf("fixed Change Request content version does not exist")
			}
			return model.SessionChangeRequestLink{}, fmt.Errorf("resolve Change Request content version: %w", err)
		}
		if link.Relationship == model.ChangeRelationshipExclusive {
			if cacheState != "current" {
				return model.SessionChangeRequestLink{}, fmt.Errorf("exclusive Change Request content version is not current")
			}
			var completeness model.ChangeRequestCompleteness
			if err := json.Unmarshal([]byte(completenessJSON), &completeness); err != nil {
				return model.SessionChangeRequestLink{}, fmt.Errorf("decode Change Request completeness: %w", err)
			}
			if !completeChangeRequestDelivery(completeness) {
				return model.SessionChangeRequestLink{}, fmt.Errorf("exclusive Change Request content version is incomplete")
			}
			var missingPatches int
			if err := c.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM change_request_files files
				WHERE files.snapshot_id = ? AND NOT EXISTS (
					SELECT 1 FROM source_content_blob_refs refs
					JOIN source_content_blobs blobs ON blobs.sha256 = refs.blob_sha
					WHERE refs.change_snapshot_id = files.snapshot_id
					  AND refs.path_key = files.file_key AND refs.purpose = 'patch'
				)`, resolved,
			).Scan(&missingPatches); err != nil {
				return model.SessionChangeRequestLink{}, fmt.Errorf("verify retained Change Request patches: %w", err)
			}
			if missingPatches != 0 {
				return model.SessionChangeRequestLink{}, fmt.Errorf("exclusive Change Request content is no longer retained")
			}
		}
		snapshotID = resolved
	}

	assessment, err := marshalAssessment(link.Assessment)
	if err != nil {
		return model.SessionChangeRequestLink{}, err
	}
	selectionChanged, err := storedChangeLinkSelectionChanged(
		ctx, c, link, bindingID, snapshotID, changeKey, assessment,
	)
	if err != nil {
		return model.SessionChangeRequestLink{}, err
	}
	if selectionChanged {
		if err := demoteHostedAuthorityForLink(ctx, c, bindingID, link.LinkID); err != nil {
			return model.SessionChangeRequestLink{}, err
		}
	}
	ordinal, err := stableChangeLinkOrdinal(ctx, c, link, bindingID, changeKey)
	if err != nil {
		return model.SessionChangeRequestLink{}, err
	}
	link.Ordinal = ordinal
	if _, err := c.ExecContext(ctx, `
		INSERT INTO session_change_requests(
			link_id, ordinal, root_agent_type, root_session_id, source_agent_type,
			source_session_id, collaboration_revision, invocation_id, binding_id,
			change_id, snapshot_id, content_version_key, relationship, method,
			state, reason_code, reasons_json, confirmation_source, confirmation_revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(link_id) DO UPDATE SET
			ordinal = excluded.ordinal,
			source_agent_type = excluded.source_agent_type,
			source_session_id = excluded.source_session_id,
			collaboration_revision = excluded.collaboration_revision,
			invocation_id = excluded.invocation_id,
			binding_id = excluded.binding_id,
			change_id = excluded.change_id,
			snapshot_id = excluded.snapshot_id,
			content_version_key = excluded.content_version_key,
			relationship = excluded.relationship,
			method = excluded.method,
			state = excluded.state,
			reason_code = excluded.reason_code,
			reasons_json = excluded.reasons_json,
			confirmation_source = excluded.confirmation_source,
			confirmation_revision = excluded.confirmation_revision`,
		link.LinkID, link.Ordinal, link.RootAgentType, link.RootSessionID,
		link.SourceAgentType, link.SourceSessionID, link.CollaborationRevision,
		link.InvocationID, bindingID, changeKey, snapshotID, link.ContentVersionKey,
		link.Relationship, link.Method, assessment.state, assessment.reasonCode,
		assessment.reasonsJSON, link.ConfirmationSource, link.ConfirmationRevision,
	); err != nil {
		return model.SessionChangeRequestLink{}, fmt.Errorf("upsert Session Change Request link: %w", err)
	}
	if _, err := c.ExecContext(ctx, `DELETE FROM session_change_request_evidence_links WHERE change_link_id = ?`, link.LinkID); err != nil {
		return model.SessionChangeRequestLink{}, fmt.Errorf("delete old Change Request evidence anchors: %w", err)
	}
	for ordinal, evidence := range link.Evidence {
		assessment, err := marshalAssessment(evidence.Assessment)
		if err != nil {
			return model.SessionChangeRequestLink{}, err
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO session_change_request_evidence_links(
				change_link_id, ordinal, root_agent_type, root_session_id,
				source_agent_type, source_session_id, backing_agent_type,
				backing_session_id, invocation_id, source_revision,
				positions_revision, event_id, tool_call_id, turn_index,
				recorded_at, state, reason_code, reasons_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			link.LinkID, ordinal, evidence.RootAgentType, evidence.RootSessionID,
			evidence.SourceAgentType, evidence.SourceSessionID,
			evidence.BackingAgentType, evidence.BackingSessionID,
			evidence.InvocationID, evidence.SourceRevision,
			evidence.PositionsRevision, evidence.EventID, evidence.ToolCallID,
			evidence.TurnIndex, formatGitOptionalTime(evidence.RecordedAt),
			assessment.state, assessment.reasonCode, assessment.reasonsJSON,
		); err != nil {
			return model.SessionChangeRequestLink{}, fmt.Errorf("insert Change Request evidence anchor: %w", err)
		}
	}
	if link.Relationship == model.ChangeRelationshipExclusive {
		selection := model.ChangeRequestAuthoritySelection{
			LinkID: link.LinkID, ContentVersionKey: link.ContentVersionKey,
			RootAgentType: link.RootAgentType, RootSessionID: link.RootSessionID,
			RepositoryEntryKey: link.RepositoryEntryKey,
			Coverage:           model.ChangeCoverageCompleteDelivery,
		}
		selectionJSON, err := json.Marshal(selection)
		if err != nil {
			return model.SessionChangeRequestLink{}, fmt.Errorf("marshal Change Request authority selection: %w", err)
		}
		exact, err := marshalAssessment(model.ExactGitEvidence())
		if err != nil {
			return model.SessionChangeRequestLink{}, err
		}
		result, err := c.ExecContext(ctx, `
			UPDATE session_git_evidence
			SET revision = revision + 1,
			    state = ?, reason_code = ?, reasons_json = ?,
			    stale = 0, authority = 'hosted_change',
			    selected_change_snapshot_id = ?, authority_selection_json = ?,
			    generated_at = ?
			WHERE binding_id = ?`,
			exact.state, exact.reasonCode, exact.reasonsJSON,
			snapshotID, string(selectionJSON), model.FormatTime(time.Now().UTC()), bindingID,
		)
		if err != nil {
			return model.SessionChangeRequestLink{}, fmt.Errorf("select hosted Change Request authority: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return model.SessionChangeRequestLink{}, fmt.Errorf("inspect hosted Change Request authority selection: %w", err)
		}
		if rows != 1 {
			return model.SessionChangeRequestLink{}, fmt.Errorf("Session repository evidence does not exist")
		}
	}

	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return model.SessionChangeRequestLink{}, fmt.Errorf("commit Session Change Request write: %w", err)
	}
	committed = true
	return link, nil
}

func verifyStoredChangeRequestIdentity(ctx context.Context, c *sql.Conn, changeKey string, identity model.ChangeRequestIdentity) error {
	var provider, hostID, repositoryID, objectID, genericID string
	if err := c.QueryRowContext(ctx, `
		SELECT provider, COALESCE(host_id,''), COALESCE(target_repository_id,''),
		       provider_object_id, generic_opaque_id
		FROM change_request_identities WHERE change_id = ?`, changeKey,
	).Scan(&provider, &hostID, &repositoryID, &objectID, &genericID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("Change Request identity does not exist")
		}
		return fmt.Errorf("read Change Request identity: %w", err)
	}
	var expectedRepositoryID string
	if identity.TargetRepository != nil {
		expectedRepositoryID, _ = CanonicalHostedRepositoryKey(*identity.TargetRepository)
	}
	if provider != string(identity.Provider) || hostID != identity.HostID ||
		repositoryID != expectedRepositoryID || objectID != identity.ProviderObjectID ||
		genericID != identity.GenericOpaqueID {
		return fmt.Errorf("stored Change Request identity differs from the requested identity")
	}
	return nil
}

func verifyChangeLinkCollaboration(ctx context.Context, c *sql.Conn, link model.SessionChangeRequestLink) error {
	var revision int64
	if err := c.QueryRowContext(ctx, `
		SELECT revision FROM collaboration_roots
		WHERE root_agent_type = ? AND root_session_id = ?`,
		link.RootAgentType, link.RootSessionID,
	).Scan(&revision); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("Session collaboration root does not exist")
		}
		return fmt.Errorf("read Session collaboration revision: %w", err)
	}
	if revision != link.CollaborationRevision {
		return fmt.Errorf("Session collaboration revision changed")
	}
	if link.InvocationID == "" {
		if link.SourceAgentType != link.RootAgentType || link.SourceSessionID != link.RootSessionID {
			return fmt.Errorf("root Change Request link source must match its root")
		}
		return nil
	}
	var sourceAgentType, backingAgentType, backingSessionID string
	if err := c.QueryRowContext(ctx, `
		SELECT agent_type, backing_agent_type, backing_session_id
		FROM collaboration_invocations
		WHERE root_agent_type = ? AND root_session_id = ? AND invocation_id = ?`,
		link.RootAgentType, link.RootSessionID, link.InvocationID,
	).Scan(&sourceAgentType, &backingAgentType, &backingSessionID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("Session collaboration invocation does not exist")
		}
		return fmt.Errorf("read Session collaboration invocation: %w", err)
	}
	if sourceAgentType != link.SourceAgentType ||
		(backingSessionID != "" && (backingAgentType != link.SourceAgentType || backingSessionID != link.SourceSessionID)) {
		return fmt.Errorf("Session collaboration invocation identity changed")
	}
	return nil
}

func stableChangeLinkOrdinal(ctx context.Context, c *sql.Conn, link model.SessionChangeRequestLink, bindingID any, changeKey string) (int, error) {
	var ordinal int
	err := c.QueryRowContext(ctx, `
		SELECT ordinal FROM session_change_requests WHERE link_id = ?`, link.LinkID,
	).Scan(&ordinal)
	if err == nil {
		var rootAgentType, rootSessionID, storedChangeKey string
		var storedBindingID sql.NullString
		if err := c.QueryRowContext(ctx, `
			SELECT root_agent_type, root_session_id, binding_id, change_id
			FROM session_change_requests WHERE link_id = ?`,
			link.LinkID,
		).Scan(&rootAgentType, &rootSessionID, &storedBindingID, &storedChangeKey); err != nil {
			return 0, err
		}
		if rootAgentType != link.RootAgentType || rootSessionID != link.RootSessionID {
			return 0, fmt.Errorf("Change Request link belongs to another Session root")
		}
		if !nullableStringMatchesAny(storedBindingID, bindingID) || storedChangeKey != changeKey {
			return 0, fmt.Errorf("Change Request link repository and identity are immutable")
		}
		return ordinal, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("read Change Request link ordinal: %w", err)
	}
	query := `
		SELECT COALESCE(MAX(ordinal) + 1, 0)
		FROM session_change_requests
		WHERE root_agent_type = ? AND root_session_id = ? AND binding_id IS NULL`
	args := []any{link.RootAgentType, link.RootSessionID}
	if bindingID != nil {
		query = `
			SELECT COALESCE(MAX(ordinal) + 1, 0)
			FROM session_change_requests
			WHERE root_agent_type = ? AND root_session_id = ? AND binding_id = ?`
		args = append(args, bindingID)
	}
	if err := c.QueryRowContext(ctx, query, args...).Scan(&ordinal); err != nil {
		return 0, fmt.Errorf("allocate Change Request link ordinal: %w", err)
	}
	return ordinal, nil
}

func storedChangeLinkSelectionChanged(
	ctx context.Context,
	c *sql.Conn,
	link model.SessionChangeRequestLink,
	bindingID, snapshotID any,
	changeKey string,
	assessment storedAssessment,
) (bool, error) {
	var storedBindingID, storedSnapshotID sql.NullString
	var storedChangeKey, contentVersion, relationship, method string
	var state, reasonCode, reasonsJSON, confirmationSource, confirmationRevision string
	var collaborationRevision int64
	err := c.QueryRowContext(ctx, `
		SELECT binding_id, snapshot_id, change_id, content_version_key,
		       relationship, method, state, reason_code, reasons_json,
		       confirmation_source, confirmation_revision, collaboration_revision
		FROM session_change_requests WHERE link_id = ?`, link.LinkID,
	).Scan(
		&storedBindingID, &storedSnapshotID, &storedChangeKey, &contentVersion,
		&relationship, &method, &state, &reasonCode, &reasonsJSON,
		&confirmationSource, &confirmationRevision, &collaborationRevision,
	)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect existing Change Request link: %w", err)
	}
	return !nullableStringMatchesAny(storedBindingID, bindingID) ||
		!nullableStringMatchesAny(storedSnapshotID, snapshotID) ||
		storedChangeKey != changeKey || contentVersion != string(link.ContentVersionKey) ||
		relationship != string(link.Relationship) || method != string(link.Method) ||
		state != assessment.state || reasonCode != assessment.reasonCode || reasonsJSON != assessment.reasonsJSON ||
		confirmationSource != string(link.ConfirmationSource) || confirmationRevision != link.ConfirmationRevision ||
		collaborationRevision != link.CollaborationRevision, nil
}

func nullableStringMatchesAny(stored sql.NullString, value any) bool {
	if value == nil {
		return !stored.Valid
	}
	text, ok := value.(string)
	return ok && stored.Valid && stored.String == text
}

func demoteHostedAuthorityForLink(ctx context.Context, c *sql.Conn, bindingID any, linkID string) error {
	if bindingID == nil {
		return nil
	}
	pending, err := marshalAssessment(model.NonExactGitEvidence(
		model.GitEvidenceEstimated, model.ReasonChangeRequestPendingReconfirmation,
	))
	if err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `
		UPDATE session_git_evidence
		SET revision = revision + 1,
		    state = ?, reason_code = ?, reasons_json = ?, stale = 1,
		    authority = 'none', selected_change_snapshot_id = NULL,
		    authority_selection_json = '{}'
		WHERE binding_id = ? AND authority = 'hosted_change'
		  AND json_extract(authority_selection_json, '$.link_id') = ?`,
		pending.state, pending.reasonCode, pending.reasonsJSON, bindingID, linkID,
	); err != nil {
		return fmt.Errorf("demote changed hosted Change Request authority: %w", err)
	}
	return nil
}

func completeChangeRequestDelivery(completeness model.ChangeRequestCompleteness) bool {
	for _, assessment := range []model.GitEvidenceAssessment{
		completeness.Metadata, completeness.FileSet, completeness.Patches,
		completeness.Modes, completeness.Commits,
	} {
		if assessment.State != model.GitEvidenceExact {
			return false
		}
	}
	return true
}

// DeleteSessionChangeRequestLink removes only a link owned by the URL-path
// Session root. Cached Change Request snapshots and aliases remain available.
func (db *DB) DeleteSessionChangeRequestLink(rootAgentType, rootSessionID, linkID string) (bool, error) {
	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("pin Change Request link deletion: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return false, fmt.Errorf("begin Change Request link deletion: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	var bindingID sql.NullString
	err = c.QueryRowContext(ctx, `
		SELECT binding_id FROM session_change_requests
		WHERE root_agent_type = ? AND root_session_id = ? AND link_id = ?`,
		rootAgentType, rootSessionID, linkID,
	).Scan(&bindingID)
	if err == sql.ErrNoRows {
		if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
			return false, err
		}
		committed = true
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve Change Request link deletion: %w", err)
	}
	var demotionBinding any
	if bindingID.Valid {
		demotionBinding = bindingID.String
	}
	if err := demoteHostedAuthorityForLink(ctx, c, demotionBinding, linkID); err != nil {
		return false, err
	}
	result, err := c.ExecContext(ctx, `
		DELETE FROM session_change_requests
		WHERE root_agent_type = ? AND root_session_id = ? AND link_id = ?`,
		rootAgentType, rootSessionID, linkID,
	)
	if err != nil {
		return false, fmt.Errorf("delete Session Change Request link: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return false, fmt.Errorf("commit Change Request link deletion: %w", err)
	}
	committed = true
	return rows > 0, nil
}

// ChangeRequestSessions returns confirmed links without filesystem or network
// scanning. Stable relationship/ordinal order makes the reverse path cheap and
// deterministic while offline.
func (db *DB) ChangeRequestSessions(changeKey string) ([]ChangeRequestSessionMatch, error) {
	rows, err := db.conn.Query(`
		SELECT link_id, root_agent_type, root_session_id,
		       COALESCE(binding.repository_entry_key,''), content_version_key,
		       relationship, links.state, links.reason_code, links.reasons_json
		FROM session_change_requests links
		LEFT JOIN session_git_bindings binding ON binding.binding_id = links.binding_id
		WHERE change_id = ?
		ORDER BY relationship = 'exclusive' DESC,
		         relationship = 'contributing' DESC,
		         root_agent_type, root_session_id, ordinal, link_id`, changeKey,
	)
	if err != nil {
		return nil, fmt.Errorf("query Change Request sessions: %w", err)
	}
	defer rows.Close()
	result := make([]ChangeRequestSessionMatch, 0)
	for rows.Next() {
		var item ChangeRequestSessionMatch
		var state, reason, reasonsJSON string
		if err := rows.Scan(
			&item.LinkID, &item.RootAgentType, &item.RootSessionID,
			&item.RepositoryEntryKey, &item.ContentVersionKey,
			&item.Relationship, &state, &reason, &reasonsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan Change Request session: %w", err)
		}
		assessment, err := decodeStoredAssessment(state, reason, reasonsJSON)
		if err != nil {
			return nil, err
		}
		item.ChangeKey = changeKey
		item.Match = "linked"
		item.Assessment = assessment
		result = append(result, item)
	}
	return result, rows.Err()
}

// ChangeRequestCandidateSessions performs indexed, cache-only SHA matching.
// It deliberately does not use branch names without a canonical repository
// binding; identical branch names across repositories are not evidence.
func (db *DB) ChangeRequestCandidateSessions(changeKey string, limit int) ([]ChangeRequestSessionMatch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.conn.Query(`
		WITH change_shas(sha, repository_id, strength) AS (
			SELECT alias_value, repository_id, 0 FROM change_request_aliases
			WHERE change_id = ? AND alias_kind = 'head_sha'
			UNION
			SELECT commits.sha,
			       COALESCE(snapshots.source_repository_id, identity.target_repository_id), 1
			FROM change_request_commits commits
			JOIN change_request_snapshots snapshots ON snapshots.snapshot_id = commits.snapshot_id
			JOIN change_request_identities identity ON identity.change_id = snapshots.change_id
			WHERE snapshots.change_id = ?
		), candidates AS (
			SELECT binding.agent_type, binding.session_id, binding.repository_entry_key,
			       'head_sha' AS match, MIN(change_shas.strength) AS strength
			FROM change_shas
			JOIN session_git_bindings binding ON binding.head_sha = change_shas.sha
			JOIN session_hosted_repository_bindings hosted
			  ON hosted.binding_id = binding.binding_id
			 AND hosted.repository_id = change_shas.repository_id
			WHERE NOT EXISTS (
				SELECT 1 FROM session_change_requests linked
				WHERE linked.change_id = ?
				  AND linked.root_agent_type = binding.agent_type
				  AND linked.root_session_id = binding.session_id
			)
			GROUP BY binding.agent_type, binding.session_id, binding.repository_entry_key
			UNION ALL
			SELECT binding.agent_type, binding.session_id, binding.repository_entry_key,
			       'commit_membership' AS match, 2 AS strength
			FROM change_shas
			JOIN session_git_candidate_commits candidate ON candidate.sha = change_shas.sha
			JOIN session_git_evidence evidence ON evidence.evidence_id = candidate.evidence_id
			JOIN session_git_bindings binding ON binding.binding_id = evidence.binding_id
			JOIN session_hosted_repository_bindings hosted
			  ON hosted.binding_id = binding.binding_id
			 AND hosted.repository_id = change_shas.repository_id
			WHERE NOT EXISTS (
				SELECT 1 FROM session_change_requests linked
				WHERE linked.change_id = ?
				  AND linked.root_agent_type = binding.agent_type
				  AND linked.root_session_id = binding.session_id
			)
		)
		SELECT agent_type, session_id, repository_entry_key, match, MIN(strength)
		FROM candidates
		GROUP BY agent_type, session_id, repository_entry_key, match
		ORDER BY MIN(strength), agent_type, session_id, repository_entry_key
		LIMIT ?`, changeKey, changeKey, changeKey, changeKey, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query Change Request candidate sessions: %w", err)
	}
	defer rows.Close()
	result := make([]ChangeRequestSessionMatch, 0)
	for rows.Next() {
		var item ChangeRequestSessionMatch
		var strength int
		if err := rows.Scan(&item.RootAgentType, &item.RootSessionID, &item.RepositoryEntryKey, &item.Match, &strength); err != nil {
			return nil, fmt.Errorf("scan Change Request candidate session: %w", err)
		}
		item.ChangeKey = changeKey
		item.Assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonChangeLinkAmbiguous)
		result = append(result, item)
	}
	return result, rows.Err()
}

// FindChangeRequestsByURL resolves cached sanitized URL aliases only. The
// caller handles multiple rows as ambiguity; this method never guesses.
func (db *DB) FindChangeRequestsByURL(normalizedURL string) ([]string, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT change_id FROM change_request_aliases
		WHERE alias_kind = 'url' AND alias_value = ?
		  AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY change_id`, normalizedURL, model.FormatTime(time.Now().UTC()),
	)
	if err != nil {
		return nil, fmt.Errorf("resolve Change Request URL alias: %w", err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var changeKey string
		if err := rows.Scan(&changeKey); err != nil {
			return nil, err
		}
		result = append(result, changeKey)
	}
	return result, rows.Err()
}

func decodeStoredAssessment(state, reason, reasonsJSON string) (model.GitEvidenceAssessment, error) {
	assessment := model.GitEvidenceAssessment{
		State: model.GitEvidenceState(state), ReasonCode: model.GitEvidenceReasonCode(reason),
		Reasons: []model.GitEvidenceReasonCode{},
	}
	if err := json.Unmarshal([]byte(reasonsJSON), &assessment.Reasons); err != nil {
		return model.GitEvidenceAssessment{}, fmt.Errorf("decode Git evidence assessment: %w", err)
	}
	if validation := model.ValidateGitEvidenceAssessment(assessment); !validation.OK() {
		return model.GitEvidenceAssessment{}, fmt.Errorf("validate stored Git evidence assessment: %+v", validation.Issues)
	}
	return assessment, nil
}
