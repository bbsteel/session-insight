package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// ChangeRequestRecord is the cache-only read projection used by API handlers.
// Generic manual identities have no provider snapshot; their exact sanitized
// URL remains available through Aliases.
type ChangeRequestRecord struct {
	ChangeKey       string                       `json:"change_key"`
	Identity        model.ChangeRequestIdentity  `json:"identity"`
	Snapshot        *model.ChangeRequestSnapshot `json:"snapshot,omitempty"`
	CacheState      string                       `json:"cache_state"`
	CacheAssessment model.GitEvidenceAssessment  `json:"cache_assessment"`
	Aliases         []string                     `json:"aliases"`
}

// HasSessionGitEvidence is the cheap backfill gate used when the normal turn
// watermark is already current. It does not reconstruct manifests or blobs.
func (db *DB) HasSessionGitEvidence(rootAgentType, rootSessionID string) (bool, error) {
	var found int
	err := db.conn.QueryRow(`
		SELECT 1
		FROM session_git_bindings binding
		JOIN session_git_evidence evidence ON evidence.binding_id = binding.binding_id
		WHERE binding.agent_type = ? AND binding.session_id = ?
		LIMIT 1`, rootAgentType, rootSessionID,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Session Git evidence: %w", err)
	}
	return true, nil
}

// SessionGitEvidenceEnvelope reconstructs every repository entry for one
// attribution root without consulting the filesystem or a hosted provider.
func (db *DB) SessionGitEvidenceEnvelope(rootAgentType, rootSessionID string) (model.SessionGitEvidenceEnvelope, bool, error) {
	rows, err := db.conn.Query(`
		SELECT binding.binding_id, binding.repository_entry_key,
		       binding.worktree_root, binding.common_root_id, binding.worktree_id,
		       binding.branch, binding.head_sha,
		       binding.state, binding.reason_code, binding.reasons_json,
		       evidence.revision, evidence.state, evidence.reason_code, evidence.reasons_json,
		       evidence.provisional, evidence.stale, evidence.authority,
		       evidence.baseline_snapshot_id, evidence.final_snapshot_id,
		       evidence.authority_selection_json, evidence.generated_at,
		       origin.origin_json
		FROM session_git_bindings binding
		JOIN session_git_evidence evidence ON evidence.binding_id = binding.binding_id
		LEFT JOIN session_git_origins origin ON origin.binding_id = binding.binding_id
		WHERE binding.agent_type = ? AND binding.session_id = ?
		ORDER BY binding.repository_entry_key`, rootAgentType, rootSessionID)
	if err != nil {
		return model.SessionGitEvidenceEnvelope{}, false, fmt.Errorf("query Session Git evidence: %w", err)
	}
	defer rows.Close()

	envelope := model.SessionGitEvidenceEnvelope{
		RootAgentType: rootAgentType, RootSessionID: rootSessionID,
		Repositories: []model.SessionGitEvidence{},
	}
	for rows.Next() {
		var bindingID string
		var repositoryState, repositoryReason, repositoryReasons string
		var evidenceState, evidenceReason, evidenceReasons string
		var provisional, stale int
		var baselineID, finalID, originJSON sql.NullString
		var authorityJSON, generatedAt string
		var evidence model.SessionGitEvidence
		if err := rows.Scan(
			&bindingID, &evidence.RepositoryEntryKey,
			&evidence.Repository.WorktreeRoot, &evidence.Repository.CommonRootID,
			&evidence.Repository.WorktreeID, &evidence.Repository.Branch,
			&evidence.Repository.HeadSHA, &repositoryState, &repositoryReason,
			&repositoryReasons, &evidence.Revision, &evidenceState,
			&evidenceReason, &evidenceReasons, &provisional, &stale,
			&evidence.Authority, &baselineID, &finalID, &authorityJSON,
			&generatedAt, &originJSON,
		); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, fmt.Errorf("scan Session Git evidence: %w", err)
		}
		evidence.RootAgentType = rootAgentType
		evidence.RootSessionID = rootSessionID
		evidence.Repository.RepositoryEntryKey = evidence.RepositoryEntryKey
		evidence.Provisional = provisional != 0
		evidence.Stale = stale != 0
		evidence.Files = []model.GitFileChange{}
		evidence.CandidateCommits = []model.GitCandidateCommit{}
		evidence.ChangeRequests = []model.SessionChangeRequestLink{}
		if evidence.Repository.Assessment, err = decodeStoredAssessment(repositoryState, repositoryReason, repositoryReasons); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, err
		}
		if evidence.Assessment, err = decodeStoredAssessment(evidenceState, evidenceReason, evidenceReasons); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, err
		}
		if evidence.GeneratedAt, err = parseStoredTime(generatedAt); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, err
		}
		if originJSON.Valid {
			var origin model.SessionGitOrigin
			if err := json.Unmarshal([]byte(originJSON.String), &origin); err != nil {
				return model.SessionGitEvidenceEnvelope{}, false, fmt.Errorf("decode Session Git origin: %w", err)
			}
			evidence.Origin = &origin
		}
		if baselineID.Valid {
			if evidence.Baseline, err = db.readGitSnapshotSummary(bindingID, baselineID.String); err != nil {
				return model.SessionGitEvidenceEnvelope{}, false, err
			}
		}
		if finalID.Valid {
			if evidence.Final, err = db.readGitSnapshotSummary(bindingID, finalID.String); err != nil {
				return model.SessionGitEvidenceEnvelope{}, false, err
			}
		}
		if strings.TrimSpace(authorityJSON) != "{}" {
			var selection model.ChangeRequestAuthoritySelection
			if err := json.Unmarshal([]byte(authorityJSON), &selection); err != nil {
				return model.SessionGitEvidenceEnvelope{}, false, fmt.Errorf("decode Change Request authority selection: %w", err)
			}
			evidence.AuthoritySelection = &selection
		}
		if evidence.Files, err = db.readEvidenceFiles(bindingID); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, err
		}
		if evidence.CandidateCommits, err = db.readEvidenceCommits(bindingID); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, err
		}
		if evidence.ChangeRequests, err = db.readSessionChangeRequestLinks(rootAgentType, rootSessionID, bindingID); err != nil {
			return model.SessionGitEvidenceEnvelope{}, false, err
		}
		if validation := model.ValidateSessionGitEvidence(&evidence); !validation.OK() {
			return model.SessionGitEvidenceEnvelope{}, false, fmt.Errorf("validate stored Session Git evidence: %+v", validation.Issues)
		}
		envelope.Repositories = append(envelope.Repositories, evidence)
	}
	if err := rows.Err(); err != nil {
		return model.SessionGitEvidenceEnvelope{}, false, err
	}
	if len(envelope.Repositories) == 0 {
		return envelope, false, nil
	}
	aggregateEvidenceEnvelope(&envelope)
	if validation := model.ValidateSessionGitEvidenceEnvelope(&envelope); !validation.OK() {
		return model.SessionGitEvidenceEnvelope{}, false, fmt.Errorf("validate stored Session Git evidence envelope: %+v", validation.Issues)
	}
	return envelope, true, nil
}

func (db *DB) readGitSnapshotSummary(bindingID, snapshotID string) (*model.GitSnapshotSummary, error) {
	var summary model.GitSnapshotSummary
	var state, reason, reasons, startedAt, completedAt string
	if err := db.conn.QueryRow(`
		SELECT snapshot_id, kind, head_sha, manifest_digest, source_revision,
		       capture_started_at, capture_completed_at, state, reason_code, reasons_json
		FROM session_git_snapshots WHERE binding_id = ? AND snapshot_id = ?`,
		bindingID, snapshotID,
	).Scan(
		&summary.SnapshotID, &summary.Kind, &summary.HeadSHA, &summary.ManifestDigest,
		&summary.SourceRevision, &startedAt, &completedAt, &state, &reason, &reasons,
	); err != nil {
		return nil, fmt.Errorf("read Session Git snapshot %q: %w", snapshotID, err)
	}
	var err error
	if summary.CaptureStartedAt, err = parseStoredTime(startedAt); err != nil {
		return nil, err
	}
	if summary.CaptureEndedAt, err = parseStoredTime(completedAt); err != nil {
		return nil, err
	}
	if summary.Assessment, err = decodeStoredAssessment(state, reason, reasons); err != nil {
		return nil, err
	}
	return &summary, nil
}

func (db *DB) readEvidenceFiles(evidenceID string) ([]model.GitFileChange, error) {
	rows, err := db.conn.Query(`
		SELECT file_key, ordinal, layer, display_path, old_display_path,
		       path_bytes_b64, old_path_bytes_b64, path_encoding, status,
		       old_mode, new_mode, binary, submodule, additions, deletions,
		       status_state, status_reason_code, status_reasons_json,
		       patch_state, patch_reason_code, patch_reasons_json
		FROM session_git_files WHERE evidence_id = ? ORDER BY ordinal`, evidenceID)
	if err != nil {
		return nil, fmt.Errorf("query Session Git files: %w", err)
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
			return nil, fmt.Errorf("scan Session Git file: %w", err)
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
		if file.Evidence, err = db.readEvidenceLinks(evidenceID, file.Key, ""); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (db *DB) readEvidenceCommits(evidenceID string) ([]model.GitCandidateCommit, error) {
	rows, err := db.conn.Query(`
		SELECT sha, ordinal, subject, author_name, authored_at, committed_at,
		       relation, state, reason_code, reasons_json
		FROM session_git_candidate_commits WHERE evidence_id = ? ORDER BY ordinal`, evidenceID)
	if err != nil {
		return nil, fmt.Errorf("query Session Git candidate commits: %w", err)
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
			return nil, fmt.Errorf("scan Session Git candidate commit: %w", err)
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
		if commit.Evidence, err = db.readEvidenceLinks(evidenceID, "", commit.SHA); err != nil {
			return nil, err
		}
		commits = append(commits, commit)
	}
	return commits, rows.Err()
}

func (db *DB) readEvidenceLinks(evidenceID, fileKey, commitSHA string) ([]model.GitEvidenceLink, error) {
	rows, err := db.conn.Query(`
		SELECT root_agent_type, root_session_id, source_agent_type, source_session_id,
		       backing_agent_type, backing_session_id, invocation_id, source_revision,
		       positions_revision, event_id, tool_call_id, turn_index, recorded_at,
		       state, reason_code, reasons_json
		FROM session_git_evidence_links
		WHERE evidence_id = ? AND COALESCE(file_key,'') = ? AND COALESCE(commit_sha,'') = ?
		ORDER BY ordinal`, evidenceID, fileKey, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("query Git evidence anchors: %w", err)
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

type rowScanner interface {
	Scan(...any) error
}

func scanGitEvidenceLink(scanner rowScanner) (model.GitEvidenceLink, error) {
	var link model.GitEvidenceLink
	var turnIndex sql.NullInt64
	var recordedAt sql.NullString
	var state, reason, reasons string
	if err := scanner.Scan(
		&link.RootAgentType, &link.RootSessionID, &link.SourceAgentType, &link.SourceSessionID,
		&link.BackingAgentType, &link.BackingSessionID, &link.InvocationID, &link.SourceRevision,
		&link.PositionsRevision, &link.EventID, &link.ToolCallID, &turnIndex, &recordedAt,
		&state, &reason, &reasons,
	); err != nil {
		return link, fmt.Errorf("scan Git evidence anchor: %w", err)
	}
	if turnIndex.Valid {
		value := int(turnIndex.Int64)
		link.TurnIndex = &value
	}
	var err error
	if link.RecordedAt, err = parseOptionalStoredTime(recordedAt); err != nil {
		return link, err
	}
	if link.Assessment, err = decodeStoredAssessment(state, reason, reasons); err != nil {
		return link, err
	}
	return link, nil
}

func aggregateEvidenceEnvelope(envelope *model.SessionGitEvidenceEnvelope) {
	envelope.Revision = 0
	worst := model.GitEvidenceExact
	reasons := []model.GitEvidenceReasonCode{}
	seenReasons := map[model.GitEvidenceReasonCode]bool{}
	for _, repository := range envelope.Repositories {
		envelope.Revision += repository.Revision
		envelope.Provisional = envelope.Provisional || repository.Provisional
		envelope.Stale = envelope.Stale || repository.Stale
		if repository.GeneratedAt.After(envelope.GeneratedAt) {
			envelope.GeneratedAt = repository.GeneratedAt
		}
		if evidenceStateRank(repository.Assessment.State) > evidenceStateRank(worst) {
			worst = repository.Assessment.State
		}
		for _, reason := range repository.Assessment.Reasons {
			if !seenReasons[reason] {
				seenReasons[reason] = true
				reasons = append(reasons, reason)
			}
		}
	}
	if worst == model.GitEvidenceExact {
		envelope.Assessment = model.ExactGitEvidence()
		return
	}
	if len(reasons) == 0 {
		for _, repository := range envelope.Repositories {
			if repository.Assessment.ReasonCode != "" {
				reasons = append(reasons, repository.Assessment.ReasonCode)
				break
			}
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, model.ReasonBaselineNotCaptured)
	}
	envelope.Assessment = model.NonExactGitEvidence(worst, reasons[0], reasons[1:]...)
}

func evidenceStateRank(state model.GitEvidenceState) int {
	switch state {
	case model.GitEvidenceExact:
		return 0
	case model.GitEvidenceEstimated:
		return 1
	case model.GitEvidenceMissing:
		return 2
	default:
		return 3
	}
}

func parseStoredTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode stored timestamp: %w", err)
	}
	return parsed.UTC(), nil
}

func parseOptionalStoredTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := parseStoredTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func sortUniqueStrings(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
