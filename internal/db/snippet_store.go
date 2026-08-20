package db

import (
	"database/sql"
	"fmt"
)

// Snippet is an immutable local copy of a useful transcript excerpt. Source
// metadata is intentionally denormalized: a saved snippet remains readable
// after its original session is no longer available.
type Snippet struct {
	ID          int64  `json:"id"`
	Content     string `json:"content"`
	AgentType   string `json:"agent_type"`
	SessionID   string `json:"session_id"`
	SessionName string `json:"session_name"`
	SourceKind  string `json:"source_kind"`
	TurnIndex   *int   `json:"turn_index,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func (db *DB) AddSnippet(snippet Snippet) (Snippet, error) {
	result, err := db.conn.Exec(
		`INSERT INTO snippets(content, agent_type, session_id, session_name, source_kind, turn_index)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		snippet.Content, snippet.AgentType, snippet.SessionID, snippet.SessionName,
		snippet.SourceKind, snippet.TurnIndex,
	)
	if err != nil {
		return Snippet{}, fmt.Errorf("add snippet: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Snippet{}, fmt.Errorf("read added snippet id: %w", err)
	}
	created, err := db.GetSnippet(id)
	if err != nil {
		return Snippet{}, err
	}
	return created, nil
}

func (db *DB) GetSnippet(id int64) (Snippet, error) {
	var snippet Snippet
	var turnIndex sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT id, content, agent_type, session_id, session_name, source_kind, turn_index, created_at
		 FROM snippets WHERE id = ?`, id,
	).Scan(
		&snippet.ID, &snippet.Content, &snippet.AgentType, &snippet.SessionID,
		&snippet.SessionName, &snippet.SourceKind, &turnIndex, &snippet.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return Snippet{}, err
		}
		return Snippet{}, fmt.Errorf("get snippet: %w", err)
	}
	if turnIndex.Valid {
		value := int(turnIndex.Int64)
		snippet.TurnIndex = &value
	}
	return snippet, nil
}

func (db *DB) ListSnippets() ([]Snippet, error) {
	rows, err := db.conn.Query(
		`SELECT id, content, agent_type, session_id, session_name, source_kind, turn_index, created_at
		 FROM snippets ORDER BY created_at DESC, id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list snippets: %w", err)
	}
	defer rows.Close()

	snippets := []Snippet{}
	for rows.Next() {
		var snippet Snippet
		var turnIndex sql.NullInt64
		if err := rows.Scan(
			&snippet.ID, &snippet.Content, &snippet.AgentType, &snippet.SessionID,
			&snippet.SessionName, &snippet.SourceKind, &turnIndex, &snippet.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan snippet: %w", err)
		}
		if turnIndex.Valid {
			value := int(turnIndex.Int64)
			snippet.TurnIndex = &value
		}
		snippets = append(snippets, snippet)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snippets: %w", err)
	}
	return snippets, nil
}

func (db *DB) DeleteSnippet(id int64) (bool, error) {
	result, err := db.conn.Exec(`DELETE FROM snippets WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete snippet: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read deleted snippet count: %w", err)
	}
	return deleted > 0, nil
}
