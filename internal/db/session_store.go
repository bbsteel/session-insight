package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// SessionMeta is the minimal metadata needed for search result enrichment.
type SessionMeta struct {
	Project   string
	Name      string
	UpdatedAt time.Time
}

func (db *DB) UpsertSessionMeta(agentType, id, cwd, repository, branch, project, name, modelName, resumeID string, turnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, "", resumeID,
		turnCount, turnCount, 0, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistory(agentType, id, cwd, repository, branch, project, name, modelName, resumeID string, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, "", resumeID,
		turnCount, historicalTurnCount, rolledBackTurnCount, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistoryAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID string, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryLineageAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID,
		"", "", false, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistoryAndLineage(agentType, id, cwd, repository, branch, project, name, modelName, resumeID, parentSessionID, agentPath string, isSubagent bool, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	return db.UpsertSessionMetaWithHistoryLineageAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, "", resumeID,
		parentSessionID, agentPath, isSubagent, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount, createdAt, updatedAt)
}

func (db *DB) UpsertSessionMetaWithHistoryLineageAndProvider(agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID, parentSessionID, agentPath string, isSubagent bool, turnCount, historicalTurnCount, rolledBackTurnCount, messageCount int, createdAt, updatedAt time.Time) error {
	_, err := db.conn.Exec(
		`INSERT INTO sessions(agent_type, id, cwd, repository, branch, project, name, model_name, model_provider, resume_id, parent_session_id, agent_path, is_subagent, turn_count, historical_turn_count, rolled_back_turn_count, message_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_type, id) DO UPDATE SET
		     cwd = excluded.cwd,
		     repository = excluded.repository,
		     branch = excluded.branch,
		     project = excluded.project,
		     name = excluded.name,
		     model_name = excluded.model_name,
		     model_provider = excluded.model_provider,
		     resume_id = excluded.resume_id,
		     parent_session_id = excluded.parent_session_id,
		     agent_path = excluded.agent_path,
		     is_subagent = excluded.is_subagent,
		     turn_count = excluded.turn_count,
		     historical_turn_count = excluded.historical_turn_count,
		     rolled_back_turn_count = excluded.rolled_back_turn_count,
		     message_count = excluded.message_count,
		     created_at = excluded.created_at,
		     updated_at = excluded.updated_at`,
		agentType, id, cwd, repository, branch, project, name, modelName, modelProvider, resumeID, parentSessionID, agentPath, isSubagent,
		turnCount, historicalTurnCount, rolledBackTurnCount, messageCount,
		model.FormatTime(createdAt),
		model.FormatTime(updatedAt),
	)
	return err
}

// UpdateSessionResumeID synchronizes a reader-provided native resume ID
// without rebuilding the session's turn index. It repairs historical empty
// IDs and Codex subagent rows that previously stored their parent thread ID.
func (db *DB) UpdateSessionResumeID(agentType, sessionID, resumeID string) (bool, error) {
	if resumeID == "" {
		return false, nil
	}
	result, err := db.conn.Exec(
		`UPDATE sessions SET resume_id = ?
		 WHERE agent_type = ? AND id = ? AND resume_id <> ?`,
		resumeID, agentType, sessionID, resumeID,
	)
	if err != nil {
		return false, fmt.Errorf("update session resume id: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("resume id rows affected: %w", err)
	}
	return rows > 0, nil
}

// ListSessionSummaries returns every indexed session (optionally filtered by
// agent type) ordered by updated_at descending — the sidebar list is served
// straight from this query instead of re-scanning session files on disk.
func (db *DB) ListSessionSummaries(agentType string) ([]model.Session, error) {
	query := `SELECT agent_type, id, cwd, repository, branch, project, name, model_name, model_provider, resume_id, parent_session_id, agent_path, is_subagent,
		                 turn_count, historical_turn_count, rolled_back_turn_count, message_count, created_at, updated_at
	          FROM sessions`
	var args []any
	if agentType != "" {
		query += ` WHERE agent_type = ?`
		args = append(args, agentType)
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list session summaries: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		var s model.Session
		var isSubagent int
		var createdStr, updatedStr string
		if err := rows.Scan(&s.AgentType, &s.ID, &s.CWD, &s.Repository, &s.Branch, &s.Project,
			&s.Name, &s.ModelName, &s.ModelProvider, &s.ResumeID, &s.ParentSessionID, &s.AgentPath, &isSubagent,
			&s.TurnCount, &s.HistoricalTurnCount, &s.RolledBackTurnCount, &s.MessageCount,
			&createdStr, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan session summary: %w", err)
		}
		s.IsSubagent = isSubagent != 0
		s.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// DeleteSessionData removes every trace of a session from the index DB in one
// transaction: search index, watermark, session row, position caches,
// bookmark, AI generations and title override. Called after the reader has
// deleted the session's source files, so a stale row can't resurrect it.
func (db *DB) DeleteSessionData(agentType, sessionID string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	defer tx.Rollback()

	stmts := []string{
		`DELETE FROM turn_texts WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM index_watermarks WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM sessions WHERE agent_type = ? AND id = ?`,
		`DELETE FROM session_positions WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM session_position_caches WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM bookmarked_sessions WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM ai_generations WHERE agent_type = ? AND session_id = ?`,
		`DELETE FROM session_title_overrides WHERE agent_type = ? AND session_id = ?`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt, agentType, sessionID); err != nil {
			return fmt.Errorf("delete session data: %w", err)
		}
	}
	return tx.Commit()
}

func (db *DB) GetSessionMetas(keys []struct{ AgentType, SessionID string }) (map[string]SessionMeta, error) {
	if len(keys) == 0 {
		return map[string]SessionMeta{}, nil
	}

	placeholders := make([]string, len(keys))
	args := make([]any, 0, len(keys)*2)
	for i, k := range keys {
		placeholders[i] = "(?, ?)"
		args = append(args, k.AgentType, k.SessionID)
	}

	query := fmt.Sprintf(
		`SELECT agent_type, id, project, name, updated_at FROM sessions WHERE (agent_type, id) IN (%s)`,
		strings.Join(placeholders, ", "),
	)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get session metas: %w", err)
	}
	defer rows.Close()

	result := make(map[string]SessionMeta, len(keys))
	for rows.Next() {
		var agentType, sessionID, project, name, updatedStr string
		if err := rows.Scan(&agentType, &sessionID, &project, &name, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan session meta: %w", err)
		}
		updatedAt, _ := time.Parse(time.RFC3339, updatedStr)
		result[agentType+"\x00"+sessionID] = SessionMeta{
			Project:   project,
			Name:      name,
			UpdatedAt: updatedAt,
		}
	}
	return result, rows.Err()
}
