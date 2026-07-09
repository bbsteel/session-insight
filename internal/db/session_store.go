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

func (db *DB) UpsertSessionMeta(agentType, id, cwd, repository, branch, project, name, modelName string, turnCount, messageCount int, createdAt, updatedAt time.Time) error {
	_, err := db.conn.Exec(
		`INSERT INTO sessions(agent_type, id, cwd, repository, branch, project, name, model_name, turn_count, message_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_type, id) DO UPDATE SET
		     cwd = excluded.cwd,
		     repository = excluded.repository,
		     branch = excluded.branch,
		     project = excluded.project,
		     name = excluded.name,
		     model_name = excluded.model_name,
		     turn_count = excluded.turn_count,
		     message_count = excluded.message_count,
		     created_at = excluded.created_at,
		     updated_at = excluded.updated_at`,
		agentType, id, cwd, repository, branch, project, name, modelName,
		turnCount, messageCount,
		model.FormatTime(createdAt),
		model.FormatTime(updatedAt),
	)
	return err
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
