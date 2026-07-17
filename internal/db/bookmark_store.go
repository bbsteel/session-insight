package db

import (
	"database/sql"
	"fmt"
)

type Bookmark struct {
	AgentType string
	SessionID string
	Note      string
	CreatedAt string
}

// BookmarkedSession carries the full session summary stored at bookmark time
// so listing bookmarks is a pure SQL query with no per-session disk reads.
type BookmarkedSession struct {
	AgentType         string
	SessionID         string
	Note              string
	Name              string
	ModelName         string
	ModelProvider     string
	Repository        string
	Project           string
	CWD               string
	PreviewText       string
	TurnCount         int
	MessageCount      int
	Branch            string
	SessionUpdatedAt  string
	BookmarkCreatedAt string
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

func (db *DB) UpdateBookmarkMeta(s BookmarkedSession) error {
	const q = `UPDATE bookmarked_sessions SET
		name = ?, model_name = ?, model_provider = ?, repository = ?, project = ?, cwd = ?,
		preview_text = ?, turn_count = ?, message_count = ?, branch = ?,
		session_updated_at = ?
		WHERE agent_type = ? AND session_id = ?`
	_, err := db.conn.Exec(q,
		s.Name, s.ModelName, s.ModelProvider, s.Repository, s.Project, s.CWD,
		s.PreviewText, s.TurnCount, s.MessageCount, s.Branch,
		s.SessionUpdatedAt,
		s.AgentType, s.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update bookmark meta: %w", err)
	}
	return nil
}

func (db *DB) UpdateBookmarkNote(agentType, sessionID, note string) error {
	_, err := db.conn.Exec(
		`UPDATE bookmarked_sessions SET note = ? WHERE agent_type = ? AND session_id = ?`,
		note, agentType, sessionID,
	)
	if err != nil {
		return fmt.Errorf("update bookmark note: %w", err)
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
	_, bookmarked, err := db.GetBookmark(agentType, sessionID)
	return bookmarked, err
}

// GetBookmark returns the note and whether the session is bookmarked.
// Note is empty when not bookmarked or when no note was saved.
func (db *DB) GetBookmark(agentType, sessionID string) (note string, bookmarked bool, err error) {
	err = db.conn.QueryRow(
		`SELECT COALESCE(note, '') FROM bookmarked_sessions WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	).Scan(&note)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get bookmark: %w", err)
	}
	return note, true, nil
}

func (db *DB) ListBookmarks() ([]Bookmark, error) {
	rows, err := db.conn.Query(
		`SELECT agent_type, session_id, note, created_at
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
		if err := rows.Scan(&b.AgentType, &b.SessionID, &b.Note, &b.CreatedAt); err != nil {
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

// ListBookmarkedSessions returns full session summaries directly from the
// bookmarks table — no per-session disk reads needed. Missing metadata
// (pre-v7 bookmarks) will have zero/default values for the new columns.
func (db *DB) ListBookmarkedSessions() ([]BookmarkedSession, error) {
	rows, err := db.conn.Query(
		`SELECT agent_type, session_id, note, name, model_name, model_provider, repository, project, cwd,
		        preview_text, turn_count, message_count, branch, session_updated_at,
		        created_at
		 FROM bookmarked_sessions
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list bookmarked sessions: %w", err)
	}
	defer rows.Close()

	var sessions []BookmarkedSession
	for rows.Next() {
		var s BookmarkedSession
		if err := rows.Scan(
			&s.AgentType, &s.SessionID, &s.Note, &s.Name, &s.ModelName, &s.ModelProvider, &s.Repository,
			&s.Project, &s.CWD, &s.PreviewText, &s.TurnCount, &s.MessageCount,
			&s.Branch, &s.SessionUpdatedAt, &s.BookmarkCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bookmarked session: %w", err)
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bookmarked sessions: %w", err)
	}
	if sessions == nil {
		sessions = []BookmarkedSession{}
	}
	return sessions, nil
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

func (db *DB) BookmarkNotes() (map[string]string, error) {
	bookmarks, err := db.ListBookmarks()
	if err != nil {
		return nil, err
	}
	notes := make(map[string]string, len(bookmarks))
	for _, b := range bookmarks {
		notes[BookmarkKey(b.AgentType, b.SessionID)] = b.Note
	}
	return notes, nil
}

func BookmarkKey(agentType, sessionID string) string {
	return agentType + "\x00" + sessionID
}
