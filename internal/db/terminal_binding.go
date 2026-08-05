package db

import (
	"database/sql"
	"time"
)

type TerminalBindingRecord struct {
	AgentType      string
	SessionID      string
	TerminalID     string
	TerminalName   string
	InstanceID     string
	WindowID       string
	TabID          string
	TerminalPID    int
	AgentPID       int
	Confidence     string
	Focusable      bool
	State          string
	LaunchedAt     time.Time
	LastVerifiedAt time.Time
}

func (db *DB) UpsertTerminalBinding(binding TerminalBindingRecord) error {
	_, err := db.conn.Exec(`
		INSERT INTO terminal_bindings(
			agent_type, session_id, terminal_id, terminal_name,
			instance_id, window_id, tab_id, terminal_pid, agent_pid,
			confidence, focusable, state, launched_at, last_verified_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_type, session_id) DO UPDATE SET
			terminal_id = excluded.terminal_id,
			terminal_name = excluded.terminal_name,
			instance_id = excluded.instance_id,
			window_id = excluded.window_id,
			tab_id = excluded.tab_id,
			terminal_pid = excluded.terminal_pid,
			agent_pid = excluded.agent_pid,
			confidence = excluded.confidence,
			focusable = excluded.focusable,
			state = excluded.state,
			launched_at = excluded.launched_at,
			last_verified_at = excluded.last_verified_at`,
		binding.AgentType, binding.SessionID, binding.TerminalID, binding.TerminalName,
		binding.InstanceID, binding.WindowID, binding.TabID, binding.TerminalPID, binding.AgentPID,
		binding.Confidence, binding.Focusable, binding.State,
		binding.LaunchedAt.UTC().Format(time.RFC3339Nano), formatOptionalTime(binding.LastVerifiedAt),
	)
	return err
}

func (db *DB) GetTerminalBinding(agentType, sessionID string) (TerminalBindingRecord, bool, error) {
	var record TerminalBindingRecord
	var focusable int
	var launchedAt, verifiedAt string
	err := db.conn.QueryRow(`
		SELECT agent_type, session_id, terminal_id, terminal_name,
		       instance_id, window_id, tab_id, terminal_pid, agent_pid,
		       confidence, focusable, state, launched_at, last_verified_at
		FROM terminal_bindings WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	).Scan(
		&record.AgentType, &record.SessionID, &record.TerminalID, &record.TerminalName,
		&record.InstanceID, &record.WindowID, &record.TabID, &record.TerminalPID, &record.AgentPID,
		&record.Confidence, &focusable, &record.State, &launchedAt, &verifiedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return TerminalBindingRecord{}, false, nil
		}
		return TerminalBindingRecord{}, false, err
	}
	record.Focusable = focusable != 0
	record.LaunchedAt, _ = time.Parse(time.RFC3339Nano, launchedAt)
	if verifiedAt != "" {
		record.LastVerifiedAt, _ = time.Parse(time.RFC3339Nano, verifiedAt)
	}
	return record, true, nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
