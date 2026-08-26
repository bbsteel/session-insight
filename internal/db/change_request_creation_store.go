package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// ReplaceSessionChangeRequestCreationEvidence atomically replaces the facts
// derived from one authoritative transcript revision, including an empty set.
func (db *DB) ReplaceSessionChangeRequestCreationEvidence(
	agentType, sessionID, sourceRevision string,
	evidence []model.ChangeRequestCreationEvidence,
) error {
	if agentType == "" || sessionID == "" || sourceRevision == "" {
		return fmt.Errorf("invalid Change Request creation evidence index")
	}
	for _, item := range evidence {
		if item.EvidenceID == "" || item.SourceRevision != sourceRevision || item.RecordedAt.IsZero() ||
			item.Assessment.State != model.GitEvidenceExact || item.Assessment.ReasonCode != "" ||
			!model.ValidateChangeRequestReference(item.Reference).OK() {
			return fmt.Errorf("invalid Change Request creation evidence %q", item.EvidenceID)
		}
	}
	ctx := context.Background()
	conn, err := db.conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO session_change_request_creation_indexes(agent_type, session_id, source_revision, indexed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_type, session_id) DO UPDATE SET
			source_revision=excluded.source_revision, indexed_at=excluded.indexed_at`,
		agentType, sessionID, sourceRevision, model.FormatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("store Change Request creation index: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		DELETE FROM session_change_request_creation_evidence WHERE agent_type=? AND session_id=?`,
		agentType, sessionID); err != nil {
		return fmt.Errorf("clear Change Request creation evidence: %w", err)
	}
	for _, item := range evidence {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO session_change_request_creation_evidence(
				evidence_id, agent_type, session_id, provider, display_origin,
				target_repository_slug, display_number, normalized_url, command_kind,
				tool_name, event_id, tool_call_id, turn_index, invocation_id,
				recorded_at, source_revision
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.EvidenceID, agentType, sessionID, item.Reference.Provider,
			item.Reference.DisplayOrigin, item.Reference.TargetRepositorySlug,
			item.Reference.DisplayNumber, item.Reference.NormalizedURL, item.CommandKind,
			item.ToolName, item.EventID, item.ToolCallID, item.TurnIndex, item.InvocationID,
			model.FormatTime(item.RecordedAt), item.SourceRevision); err != nil {
			return fmt.Errorf("store Change Request creation evidence: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}

func (db *DB) HasSessionChangeRequestCreationIndex(agentType, sessionID string) (bool, error) {
	_, exists, _, err := db.SessionChangeRequestCreationIndexState(agentType, sessionID)
	return exists, err
}

// SessionChangeRequestCreationEvidence returns one session's recorded creation
// and mention facts in chronological order. References already covered by an
// explicit user link are excluded so derived panel entries never duplicate a
// deliberate binding.
func (db *DB) SessionChangeRequestCreationEvidence(agentType, sessionID string) ([]model.ChangeRequestCreationEvidence, error) {
	rows, err := db.conn.Query(`
		SELECT evidence.evidence_id, evidence.provider, evidence.display_origin,
		       evidence.target_repository_slug, evidence.display_number,
		       evidence.normalized_url, evidence.command_kind, evidence.tool_name,
		       evidence.event_id, evidence.tool_call_id, evidence.turn_index,
		       evidence.invocation_id, evidence.recorded_at, evidence.source_revision
		FROM session_change_request_creation_evidence evidence
		WHERE evidence.agent_type=? AND evidence.session_id=?
		  AND NOT EXISTS (
		    SELECT 1 FROM change_request_aliases alias
		    JOIN session_change_requests linked ON linked.change_id = alias.change_id
		    WHERE alias.alias_kind='url'
		      AND alias.alias_value=evidence.normalized_url
		      AND linked.root_agent_type=evidence.agent_type
		      AND linked.root_session_id=evidence.session_id
		  )
		ORDER BY evidence.recorded_at, evidence.evidence_id`, agentType, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query Session Change Request creation evidence: %w", err)
	}
	defer rows.Close()
	result := make([]model.ChangeRequestCreationEvidence, 0)
	for rows.Next() {
		var item model.ChangeRequestCreationEvidence
		var provider, recordedAt string
		if err := rows.Scan(
			&item.EvidenceID, &provider, &item.Reference.DisplayOrigin,
			&item.Reference.TargetRepositorySlug, &item.Reference.DisplayNumber,
			&item.Reference.NormalizedURL, &item.CommandKind, &item.ToolName,
			&item.EventID, &item.ToolCallID, &item.TurnIndex, &item.InvocationID,
			&recordedAt, &item.SourceRevision,
		); err != nil {
			return nil, fmt.Errorf("scan Session Change Request creation evidence: %w", err)
		}
		item.Reference.Provider = model.ChangeProviderKind(provider)
		item.RecordedAt, err = parseStoredTime(recordedAt)
		if err != nil {
			return nil, fmt.Errorf("parse Change Request creation time: %w", err)
		}
		item.Assessment = model.ExactGitEvidence()
		result = append(result, item)
	}
	return result, rows.Err()
}

func (db *DB) SessionChangeRequestCreationIndexState(agentType, sessionID string) (string, bool, bool, error) {
	var sourceRevision string
	var exists, hasEvidence int
	err := db.conn.QueryRow(`
		SELECT creation_index.source_revision, 1,
		       EXISTS(SELECT 1 FROM session_change_request_creation_evidence evidence
		              WHERE evidence.agent_type=creation_index.agent_type
		                AND evidence.session_id=creation_index.session_id)
		FROM session_change_request_creation_indexes creation_index
		WHERE creation_index.agent_type=? AND creation_index.session_id=?`, agentType, sessionID).
		Scan(&sourceRevision, &exists, &hasEvidence)
	if err == sql.ErrNoRows {
		return "", false, false, nil
	}
	return sourceRevision, err == nil, hasEvidence != 0, err
}

func (db *DB) ChangeRequestCreationSessions(normalizedURL string, limit int) ([]model.ChangeRequestCreationSessionMatch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.conn.Query(`
		SELECT agent_type, session_id, evidence_id, provider, display_origin,
		       target_repository_slug, display_number, normalized_url, command_kind,
		       tool_name, event_id, tool_call_id, turn_index, invocation_id,
		       recorded_at, source_revision
		FROM session_change_request_creation_evidence
		WHERE normalized_url=?
		ORDER BY recorded_at DESC, agent_type, session_id
		LIMIT ?`, normalizedURL, limit)
	if err != nil {
		return nil, fmt.Errorf("query Change Request creation sessions: %w", err)
	}
	defer rows.Close()
	result := make([]model.ChangeRequestCreationSessionMatch, 0)
	for rows.Next() {
		var match model.ChangeRequestCreationSessionMatch
		var provider, recordedAt string
		if err := rows.Scan(
			&match.RootAgentType, &match.RootSessionID, &match.Evidence.EvidenceID,
			&provider, &match.Evidence.Reference.DisplayOrigin,
			&match.Evidence.Reference.TargetRepositorySlug, &match.Evidence.Reference.DisplayNumber,
			&match.Evidence.Reference.NormalizedURL, &match.Evidence.CommandKind,
			&match.Evidence.ToolName, &match.Evidence.EventID, &match.Evidence.ToolCallID,
			&match.Evidence.TurnIndex, &match.Evidence.InvocationID, &recordedAt,
			&match.Evidence.SourceRevision,
		); err != nil {
			return nil, err
		}
		match.Evidence.Reference.Provider = model.ChangeProviderKind(provider)
		match.Evidence.RecordedAt, err = parseStoredTime(recordedAt)
		if err != nil {
			return nil, fmt.Errorf("parse Change Request creation time: %w", err)
		}
		match.Evidence.Assessment = model.ExactGitEvidence()
		result = append(result, match)
	}
	return result, rows.Err()
}
