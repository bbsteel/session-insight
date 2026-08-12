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
