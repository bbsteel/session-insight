package opencode

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// SessionRunning reports whether the session's last turn looks in-flight
// (assistant message without time.completed, bounded by LiveWindow over the
// store's mtime — both already applied at render-event emission). OpenCode
// offers nothing better: busy state lives in a per-process in-memory map
// (its own source keeps "persist status durably" as an open TODO), every
// instance holds the same database fd, and the TUI listens on no port — so
// no PID can be attributed to a session, and deletion of a live session can
// only be refused, never resolved by force-stop.
func (r *OpenCodeReader) SessionRunning(id string) (bool, error) {
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return false, err
	}
	return shared.HasTrailingInProgress(events), nil
}

// DeleteSession permanently removes an opencode session — and its subagent
// child sessions (parent_id, which carries no FK and would otherwise be
// orphaned invisibly) — from the shared SQLite store. Deleting the session
// rows cascades through the schema's ON DELETE CASCADE foreign keys to
// message (and part via message), todo, session_share, session_input and
// the other per-session tables, which is where the sensitive content
// lives. The write happens on a short-lived read-write connection so the
// reader's long-lived handle stays read-only; a running opencode instance
// holding the database is ridden out via the busy timeout.
func (r *OpenCodeReader) DeleteSession(id string) error {
	var exists int
	err := r.db.QueryRow(`SELECT 1 FROM session WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("opencode session not found: %s", id)
	}
	if err != nil {
		return err
	}

	w, err := sql.Open("sqlite3", "file:"+r.dbPath+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return err
	}
	defer w.Close()
	w.SetMaxOpenConns(1)

	if hasParent, err := columnExists(w, "session", "parent_id"); err != nil {
		return err
	} else if !hasParent {
		// Pre-subagent schema: no descendants to chase.
		_, err := w.Exec(`DELETE FROM session WHERE id = ?`, id)
		return err
	}

	_, err = w.Exec(`
		WITH RECURSIVE tree(id) AS (
			VALUES(?)
			UNION
			SELECT s.id FROM session s JOIN tree ON s.parent_id = tree.id
		)
		DELETE FROM session WHERE id IN (SELECT id FROM tree)`, id)
	return err
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
