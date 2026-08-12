package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

var ErrHostedAuthorityRequiresReconfirmation = errors.New("hosted Change Request authority requires reconfirmation")

// ReplaceSessionGitEvidence validates and atomically replaces one repository
// entry's derived file, commit, and source-link rows. Snapshot and hosted
// change records are independent aggregates and must already exist when they
// are referenced.
func (db *DB) ReplaceSessionGitEvidence(evidence model.SessionGitEvidence) error {
	validation := model.ValidateSessionGitEvidence(&evidence)
	if !validation.OK() {
		encoded, _ := json.Marshal(validation.Issues)
		return fmt.Errorf("validate session Git evidence: %s", encoded)
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin session Git evidence replacement: %w", err)
	}
	defer tx.Rollback()

	bindingID := CanonicalSessionRepositoryBindingID(
		evidence.RootAgentType, evidence.RootSessionID, evidence.RepositoryEntryKey,
	)
	var persistedBindingID string
	err = tx.QueryRow(`
		SELECT binding_id FROM session_git_bindings
		WHERE agent_type = ? AND session_id = ? AND repository_entry_key = ?`,
		evidence.RootAgentType, evidence.RootSessionID, evidence.RepositoryEntryKey,
	).Scan(&persistedBindingID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("resolve existing Session repository binding: %w", err)
	}
	if err == nil {
		// Preserve pre-v35 internal IDs. Rekeying this row would require moving
		// its entire FK graph and is unnecessary because the API key remains
		// scoped by the root tuple.
		bindingID = persistedBindingID
	}
	var existingAgentType, existingSessionID string
	err = tx.QueryRow(`
		SELECT agent_type, session_id FROM session_git_bindings WHERE binding_id = ?`,
		bindingID,
	).Scan(&existingAgentType, &existingSessionID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("inspect session Git binding owner: %w", err)
	}
	if err == nil && (existingAgentType != evidence.RootAgentType || existingSessionID != evidence.RootSessionID) {
		return fmt.Errorf("session Git binding %q already belongs to %s/%s", bindingID, existingAgentType, existingSessionID)
	}
	repositoryAssessment, err := marshalAssessment(evidence.Repository.Assessment)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO session_git_bindings(
			binding_id, agent_type, session_id, repository_entry_key,
			worktree_root, common_root_id, worktree_id, branch, head_sha,
			state, reason_code, reasons_json, observed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(binding_id) DO UPDATE SET
			agent_type = excluded.agent_type,
			session_id = excluded.session_id,
			repository_entry_key = excluded.repository_entry_key,
			worktree_root = excluded.worktree_root,
			common_root_id = excluded.common_root_id,
			worktree_id = excluded.worktree_id,
			branch = excluded.branch,
			head_sha = excluded.head_sha,
			state = excluded.state,
			reason_code = excluded.reason_code,
			reasons_json = excluded.reasons_json,
			observed_at = excluded.observed_at`,
		bindingID, evidence.RootAgentType, evidence.RootSessionID, evidence.RepositoryEntryKey,
		evidence.Repository.WorktreeRoot, evidence.Repository.CommonRootID, evidence.Repository.WorktreeID,
		evidence.Repository.Branch, evidence.Repository.HeadSHA,
		repositoryAssessment.state, repositoryAssessment.reasonCode, repositoryAssessment.reasonsJSON,
		model.FormatTime(evidence.GeneratedAt),
	); err != nil {
		return fmt.Errorf("upsert session Git binding: %w", err)
	}

	if evidence.Origin != nil {
		originJSON, err := json.Marshal(evidence.Origin)
		if err != nil {
			return fmt.Errorf("marshal session Git origin: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO session_git_origins(binding_id, source_revision, origin_json, captured_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(binding_id) DO UPDATE SET
				source_revision = excluded.source_revision,
				origin_json = excluded.origin_json,
				captured_at = excluded.captured_at`,
			bindingID, originSourceRevision(*evidence.Origin),
			string(originJSON), model.FormatTime(evidence.GeneratedAt),
		); err != nil {
			return fmt.Errorf("upsert session Git origin: %w", err)
		}
	} else if _, err := tx.Exec(`DELETE FROM session_git_origins WHERE binding_id = ?`, bindingID); err != nil {
		return fmt.Errorf("delete stale session Git origin: %w", err)
	}

	assessment, err := marshalAssessment(evidence.Assessment)
	if err != nil {
		return err
	}
	authorityJSON := "{}"
	if evidence.AuthoritySelection != nil {
		encoded, err := json.Marshal(evidence.AuthoritySelection)
		if err != nil {
			return fmt.Errorf("marshal Git authority selection: %w", err)
		}
		authorityJSON = string(encoded)
	}
	var baselineID, finalID any
	if evidence.Baseline != nil {
		baselineID = evidence.Baseline.SnapshotID
	}
	if evidence.Final != nil {
		finalID = evidence.Final.SnapshotID
	}
	var selectedChangeSnapshotID any
	for _, link := range evidence.ChangeRequests {
		changeKey, err := CanonicalChangeRequestKey(link.Change)
		if err != nil {
			return err
		}
		var storedSnapshotID sql.NullString
		var storedRelationship, storedMethod, storedState string
		var storedReason, storedReasonsJSON, storedConfirmationSource, storedConfirmationRevision string
		var storedCacheState string
		var storedCollaborationRevision int64
		if err := tx.QueryRow(`
			SELECT links.snapshot_id, links.relationship, links.method,
			       links.state, links.reason_code, links.reasons_json,
			       links.confirmation_source, links.confirmation_revision,
			       links.collaboration_revision, COALESCE(snapshot.cache_state,'')
			FROM session_change_requests links
			LEFT JOIN change_request_snapshots snapshot ON snapshot.snapshot_id = links.snapshot_id
			WHERE links.link_id = ? AND links.root_agent_type = ? AND links.root_session_id = ?
			  AND COALESCE(links.binding_id,'') = ? AND links.change_id = ?
			  AND links.content_version_key = ?`,
			link.LinkID, evidence.RootAgentType, evidence.RootSessionID,
			bindingID, changeKey, link.ContentVersionKey,
		).Scan(
			&storedSnapshotID, &storedRelationship, &storedMethod,
			&storedState, &storedReason, &storedReasonsJSON,
			&storedConfirmationSource, &storedConfirmationRevision,
			&storedCollaborationRevision, &storedCacheState,
		); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("session change request link %q is not persisted for this repository", link.LinkID)
			}
			return fmt.Errorf("verify Session Change Request link %q: %w", link.LinkID, err)
		}
		linkAssessment, err := marshalAssessment(link.Assessment)
		if err != nil {
			return err
		}
		if storedRelationship != string(link.Relationship) || storedMethod != string(link.Method) ||
			storedState != linkAssessment.state || storedReason != linkAssessment.reasonCode ||
			storedReasonsJSON != linkAssessment.reasonsJSON ||
			storedConfirmationSource != string(link.ConfirmationSource) ||
			storedConfirmationRevision != link.ConfirmationRevision ||
			storedCollaborationRevision != link.CollaborationRevision {
			return fmt.Errorf("session change request link %q changed during evidence derivation", link.LinkID)
		}
		if evidence.AuthoritySelection != nil && evidence.AuthoritySelection.LinkID == link.LinkID {
			if !storedSnapshotID.Valid {
				return fmt.Errorf("%w: selected link %q has no fixed snapshot", ErrHostedAuthorityRequiresReconfirmation, link.LinkID)
			}
			var currentCollaborationRevision int64
			if err := tx.QueryRow(`
				SELECT revision FROM collaboration_roots
				WHERE root_agent_type = ? AND root_session_id = ?`,
				evidence.RootAgentType, evidence.RootSessionID,
			).Scan(&currentCollaborationRevision); err != nil {
				return fmt.Errorf("%w: verify selected link %q collaboration revision: %v", ErrHostedAuthorityRequiresReconfirmation, link.LinkID, err)
			}
			expectedConfirmation, err := CanonicalChangeRequestConfirmationRevision(link)
			if err != nil || storedConfirmationRevision != expectedConfirmation ||
				storedCollaborationRevision != currentCollaborationRevision || storedCacheState != "current" {
				return fmt.Errorf("%w: selected link %q", ErrHostedAuthorityRequiresReconfirmation, link.LinkID)
			}
			selectedChangeSnapshotID = storedSnapshotID.String
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO session_git_evidence(
			evidence_id, binding_id, revision, state, reason_code, reasons_json,
			provisional, stale, authority, baseline_snapshot_id, final_snapshot_id,
			selected_change_snapshot_id, authority_selection_json, generated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(evidence_id) DO UPDATE SET
			binding_id = excluded.binding_id,
			revision = excluded.revision,
			state = excluded.state,
			reason_code = excluded.reason_code,
			reasons_json = excluded.reasons_json,
			provisional = excluded.provisional,
			stale = excluded.stale,
			authority = excluded.authority,
			baseline_snapshot_id = excluded.baseline_snapshot_id,
			final_snapshot_id = excluded.final_snapshot_id,
			selected_change_snapshot_id = excluded.selected_change_snapshot_id,
			authority_selection_json = excluded.authority_selection_json,
			generated_at = excluded.generated_at`,
		bindingID, bindingID, evidence.Revision, assessment.state, assessment.reasonCode, assessment.reasonsJSON,
		boolInt(evidence.Provisional), boolInt(evidence.Stale), evidence.Authority,
		baselineID, finalID, selectedChangeSnapshotID, authorityJSON, model.FormatTime(evidence.GeneratedAt),
	); err != nil {
		return fmt.Errorf("upsert session Git evidence: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM session_git_evidence_links WHERE evidence_id = ?`, bindingID); err != nil {
		return fmt.Errorf("delete old Git evidence links: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM session_git_files WHERE evidence_id = ?`, bindingID); err != nil {
		return fmt.Errorf("delete old Git evidence files: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM session_git_candidate_commits WHERE evidence_id = ?`, bindingID); err != nil {
		return fmt.Errorf("delete old Git evidence commits: %w", err)
	}

	for _, file := range evidence.Files {
		statusAssessment, err := marshalAssessment(file.StatusAssessment)
		if err != nil {
			return err
		}
		patchAssessment, err := marshalAssessment(file.PatchAssessment)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO session_git_files(
				evidence_id, file_key, ordinal, layer, display_path, old_display_path,
				path_bytes_b64, old_path_bytes_b64, path_encoding, status, old_mode, new_mode,
				binary, submodule, additions, deletions,
				status_state, status_reason_code, status_reasons_json,
				patch_state, patch_reason_code, patch_reasons_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bindingID, file.Key, file.Ordinal, file.Layer, file.DisplayPath, file.OldDisplayPath,
			file.PathBytesB64, file.OldPathBytesB64, file.PathEncoding, file.Status,
			file.OldMode, file.NewMode, boolInt(file.Binary), boolInt(file.Submodule),
			file.Additions, file.Deletions,
			statusAssessment.state, statusAssessment.reasonCode, statusAssessment.reasonsJSON,
			patchAssessment.state, patchAssessment.reasonCode, patchAssessment.reasonsJSON,
		); err != nil {
			return fmt.Errorf("insert Git evidence file %q: %w", file.Key, err)
		}
		for ordinal, link := range file.Evidence {
			if err := insertEvidenceLink(tx, bindingID, file.Key, "", ordinal, link); err != nil {
				return fmt.Errorf("insert Git file evidence link %q: %w", file.Key, err)
			}
		}
	}

	for _, commit := range evidence.CandidateCommits {
		commitAssessment, err := marshalAssessment(commit.Assessment)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO session_git_candidate_commits(
				evidence_id, sha, ordinal, subject, author_name, authored_at, committed_at,
				relation, state, reason_code, reasons_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bindingID, commit.SHA, commit.Ordinal, commit.Subject, commit.AuthorName,
			formatGitOptionalTime(commit.AuthoredAt), formatGitOptionalTime(commit.CommittedAt),
			commit.Relation, commitAssessment.state, commitAssessment.reasonCode, commitAssessment.reasonsJSON,
		); err != nil {
			return fmt.Errorf("insert Git candidate commit %q: %w", commit.SHA, err)
		}
		for ordinal, link := range commit.Evidence {
			if err := insertEvidenceLink(tx, bindingID, "", commit.SHA, ordinal, link); err != nil {
				return fmt.Errorf("insert Git commit evidence link %q: %w", commit.SHA, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session Git evidence replacement: %w", err)
	}
	return nil
}

// CanonicalSessionRepositoryBindingID returns the storage identity for one
// opaque repository entry within one Session root. Repository entry keys are
// intentionally scoped to their Session envelope and therefore cannot be
// used directly as database primary keys across all Sessions.
func CanonicalSessionRepositoryBindingID(rootAgentType, rootSessionID, repositoryEntryKey string) string {
	return opaqueAssociationKey("binding", rootAgentType, rootSessionID, repositoryEntryKey)
}

type storedAssessment struct {
	state       string
	reasonCode  string
	reasonsJSON string
}

func marshalAssessment(assessment model.GitEvidenceAssessment) (storedAssessment, error) {
	reasons, err := json.Marshal(assessment.Reasons)
	if err != nil {
		return storedAssessment{}, fmt.Errorf("marshal Git evidence reasons: %w", err)
	}
	return storedAssessment{
		state: string(assessment.State), reasonCode: string(assessment.ReasonCode), reasonsJSON: string(reasons),
	}, nil
}

func insertEvidenceLink(tx *sql.Tx, evidenceID, fileKey, commitSHA string, ordinal int, link model.GitEvidenceLink) error {
	assessment, err := marshalAssessment(link.Assessment)
	if err != nil {
		return err
	}
	var fileValue, commitValue any
	if fileKey != "" {
		fileValue = fileKey
	} else {
		commitValue = commitSHA
	}
	_, err = tx.Exec(`
		INSERT INTO session_git_evidence_links(
			evidence_id, file_key, commit_sha, ordinal,
			root_agent_type, root_session_id, source_agent_type, source_session_id,
			backing_agent_type, backing_session_id, invocation_id, source_revision,
			positions_revision, event_id, tool_call_id, turn_index, recorded_at,
			state, reason_code, reasons_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evidenceID, fileValue, commitValue, ordinal,
		link.RootAgentType, link.RootSessionID, link.SourceAgentType, link.SourceSessionID,
		link.BackingAgentType, link.BackingSessionID, link.InvocationID, link.SourceRevision,
		link.PositionsRevision, link.EventID, link.ToolCallID, link.TurnIndex,
		formatGitOptionalTime(link.RecordedAt), assessment.state, assessment.reasonCode, assessment.reasonsJSON,
	)
	return err
}

func originSourceRevision(origin model.SessionGitOrigin) string {
	for _, revision := range []string{
		origin.RepositoryURL.SourceRevision, origin.WorktreePath.SourceRevision,
		origin.Branch.SourceRevision, origin.HeadSHA.SourceRevision, origin.DirtyState.SourceRevision,
	} {
		if revision != "" {
			return revision
		}
	}
	return ""
}

func formatGitOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return model.FormatTime(*value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
