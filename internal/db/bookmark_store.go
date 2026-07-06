package db

import (
	"database/sql"
	"fmt"
)

type Bookmark struct {
	AgentType string
	SessionID string
	CreatedAt string
}

func (db *DB) AddBookmark(agentType, sessionID string) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO bookmarked_sessions(agent_type, session_id) VALUES (?, ?)`,
		agentType, sessionID,
	)
	if err != nil {
		return fmt.Errorf("add bookmark: %w", err)
	}
	return nil
}

func (db *DB) RemoveBookmark(agentType, sessionID string) error {
	_, err := db.conn.Exec(
		`DELETE FROM bookmarked_sessions WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	)
	if err != nil {
		return fmt.Errorf("remove bookmark: %w", err)
	}
	return nil
}

func (db *DB) IsBookmarked(agentType, sessionID string) (bool, error) {
	var one int
	err := db.conn.QueryRow(
		`SELECT 1 FROM bookmarked_sessions WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check bookmark: %w", err)
	}
	return true, nil
}

func (db *DB) ListBookmarks() ([]Bookmark, error) {
	rows, err := db.conn.Query(
		`SELECT agent_type, session_id, created_at
		 FROM bookmarked_sessions
		 ORDER BY created_at DESC, agent_type ASC, session_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	defer rows.Close()

	var bookmarks []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.AgentType, &b.SessionID, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan bookmark: %w", err)
		}
		bookmarks = append(bookmarks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bookmarks: %w", err)
	}
	if bookmarks == nil {
		bookmarks = []Bookmark{}
	}
	return bookmarks, nil
}

func (db *DB) BookmarkSet() (map[string]bool, error) {
	bookmarks, err := db.ListBookmarks()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(bookmarks))
	for _, b := range bookmarks {
		set[BookmarkKey(b.AgentType, b.SessionID)] = true
	}
	return set, nil
}

func BookmarkKey(agentType, sessionID string) string {
	return agentType + "\x00" + sessionID
}
