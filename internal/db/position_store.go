package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type PositionEntry struct {
	PositionKey string
	Kind        string
	TurnIndex   int
	LineStart   int
	LineEnd     *int
	Label       string
	Severity    string
	Payload     map[string]any
}

type PositionCache struct {
	AgentType  string
	SessionID  string
	Revision   int64
	Cols       int
	TotalLines int
	Positions  []PositionEntry
}

// SavePositionCache writes the header and all position entries in one transaction.
// Replaces any existing cache for the same (agentType, sessionID, revision, cols).
func (db *DB) SavePositionCache(agentType, sessionID string, revision int64, cols, totalLines int, positions []PositionEntry) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Upsert header (delete cascades to positions via FK).
	if _, err := tx.Exec(
		`DELETE FROM session_position_caches
		 WHERE agent_type = ? AND session_id = ? AND revision = ? AND cols = ?`,
		agentType, sessionID, revision, cols,
	); err != nil {
		return fmt.Errorf("delete old cache: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO session_position_caches(agent_type, session_id, revision, cols, total_lines)
		 VALUES (?, ?, ?, ?, ?)`,
		agentType, sessionID, revision, cols, totalLines,
	); err != nil {
		return fmt.Errorf("insert cache header: %w", err)
	}

	for _, p := range positions {
		payloadJSON := "{}"
		if len(p.Payload) > 0 {
			b, err := json.Marshal(p.Payload)
			if err == nil {
				payloadJSON = string(b)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO session_positions
			 (agent_type, session_id, revision, cols, position_key, kind, turn_index,
			  line_start, line_end, label, severity, payload_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			agentType, sessionID, revision, cols,
			p.PositionKey, p.Kind, p.TurnIndex,
			p.LineStart, p.LineEnd, p.Label, p.Severity, payloadJSON,
		); err != nil {
			return fmt.Errorf("insert position %s: %w", p.PositionKey, err)
		}
	}

	return tx.Commit()
}

// GetPositionCache returns the cached positions for the given key, or nil if not found.
func (db *DB) GetPositionCache(agentType, sessionID string, revision int64, cols int) (*PositionCache, error) {
	var totalLines int
	err := db.conn.QueryRow(
		`SELECT total_lines FROM session_position_caches
		 WHERE agent_type = ? AND session_id = ? AND revision = ? AND cols = ?`,
		agentType, sessionID, revision, cols,
	).Scan(&totalLines)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query cache header: %w", err)
	}

	rows, err := db.conn.Query(
		`SELECT position_key, kind, turn_index, line_start, line_end,
		        label, severity, payload_json
		 FROM session_positions
		 WHERE agent_type = ? AND session_id = ? AND revision = ? AND cols = ?
		 ORDER BY line_start`,
		agentType, sessionID, revision, cols,
	)
	if err != nil {
		return nil, fmt.Errorf("query positions: %w", err)
	}
	defer rows.Close()

	var positions []PositionEntry
	for rows.Next() {
		var p PositionEntry
		var payloadJSON string
		if err := rows.Scan(
			&p.PositionKey, &p.Kind, &p.TurnIndex, &p.LineStart, &p.LineEnd,
			&p.Label, &p.Severity, &payloadJSON,
		); err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		if payloadJSON != "" && payloadJSON != "{}" {
			_ = json.Unmarshal([]byte(payloadJSON), &p.Payload)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &PositionCache{
		AgentType:  agentType,
		SessionID:  sessionID,
		Revision:   revision,
		Cols:       cols,
		TotalLines: totalLines,
		Positions:  positions,
	}, nil
}
