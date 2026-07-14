package opencode

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// setupRealSchemaDB mirrors the current opencode schema where it matters
// for deletion: parent_id on session (no FK) and ON DELETE CASCADE foreign
// keys from message/part/todo, exactly as observed in a live opencode.db.
func setupRealSchemaDB(t *testing.T) (*OpenCodeReader, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")

	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE session (
			id text PRIMARY KEY, parent_id text,
			directory text NOT NULL DEFAULT '', title text NOT NULL DEFAULT '',
			time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0,
			time_archived integer, model text, agent text)`,
		`CREATE TABLE message (
			id text PRIMARY KEY, session_id text NOT NULL,
			time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0,
			data text NOT NULL,
			FOREIGN KEY (session_id) REFERENCES session(id) ON DELETE CASCADE)`,
		`CREATE TABLE part (
			id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL,
			time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0,
			data text NOT NULL,
			FOREIGN KEY (message_id) REFERENCES message(id) ON DELETE CASCADE)`,
		`CREATE TABLE todo (
			session_id text NOT NULL, content text NOT NULL DEFAULT '',
			status text NOT NULL DEFAULT '', priority text NOT NULL DEFAULT '',
			position integer NOT NULL DEFAULT 0,
			time_created integer NOT NULL DEFAULT 0, time_updated integer NOT NULL DEFAULT 0,
			PRIMARY KEY(session_id, position),
			FOREIGN KEY (session_id) REFERENCES session(id) ON DELETE CASCADE)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}

	reader, err := New(dbPath)
	if err != nil {
		t.Fatalf("New reader: %v", err)
	}
	t.Cleanup(func() { reader.db.Close() })

	return reader, db, dbPath
}

func seedFullSession(t *testing.T, db *sql.DB, id, parentID string) {
	t.Helper()
	now := time.Now().UnixMilli()
	var parent any
	if parentID != "" {
		parent = parentID
	}
	mustExec(t, db, `INSERT INTO session (id, parent_id, directory, title, time_created, time_updated) VALUES (?, ?, '/p', 'T', ?, ?)`,
		id, parent, now, now)
	mustExec(t, db, `INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
		"msg-"+id, id, now, `{"role":"user"}`)
	mustExec(t, db, `INSERT INTO part (id, message_id, session_id, time_created, data) VALUES (?, ?, ?, ?, ?)`,
		"part-"+id, "msg-"+id, id, now, `{"type":"text","text":"secret"}`)
	mustExec(t, db, `INSERT INTO todo (session_id, content, position, time_created) VALUES (?, 'todo', 0, ?)`,
		id, now)
}

func countRows(t *testing.T, db *sql.DB, table, column, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+column+` = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestDeleteSessionCascadesAndKeepsOthers(t *testing.T) {
	r, db, _ := setupRealSchemaDB(t)
	seedFullSession(t, db, "ses_del", "")
	seedFullSession(t, db, "ses_keep", "")

	if err := r.DeleteSession("ses_del"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	for _, check := range []struct{ table, column string }{
		{"session", "id"}, {"message", "session_id"}, {"part", "session_id"}, {"todo", "session_id"},
	} {
		if n := countRows(t, db, check.table, check.column, "ses_del"); n != 0 {
			t.Errorf("%s: %d rows of deleted session survive", check.table, n)
		}
		if n := countRows(t, db, check.table, check.column, "ses_keep"); n != 1 {
			t.Errorf("%s: other session's rows damaged (%d)", check.table, n)
		}
	}
}

func TestDeleteSessionRemovesSubagentChildren(t *testing.T) {
	r, db, _ := setupRealSchemaDB(t)
	seedFullSession(t, db, "ses_parent", "")
	seedFullSession(t, db, "ses_child", "ses_parent")
	seedFullSession(t, db, "ses_grandchild", "ses_child")
	seedFullSession(t, db, "ses_other", "")

	if err := r.DeleteSession("ses_parent"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	for _, id := range []string{"ses_parent", "ses_child", "ses_grandchild"} {
		if n := countRows(t, db, "session", "id", id); n != 0 {
			t.Errorf("session %s should be gone", id)
		}
		if n := countRows(t, db, "message", "session_id", id); n != 0 {
			t.Errorf("messages of %s should be gone", id)
		}
	}
	if n := countRows(t, db, "session", "id", "ses_other"); n != 1 {
		t.Error("unrelated session damaged")
	}
}

func TestDeleteSessionUnknownID(t *testing.T) {
	r, _, _ := setupRealSchemaDB(t)
	if err := r.DeleteSession("ses_nope"); err == nil {
		t.Fatal("DeleteSession for unknown id should fail")
	}
}

func TestDeleteSessionPreSubagentSchema(t *testing.T) {
	// The legacy schema (no parent_id) must still delete cleanly.
	reader, db, cleanup := setupTestDB(t)
	defer cleanup()
	seedSession(t, db, "ses_old", "/p", "T", "m")

	if err := reader.DeleteSession("ses_old"); err != nil {
		t.Fatalf("DeleteSession on legacy schema: %v", err)
	}
	if n := countRows(t, db, "session", "id", "ses_old"); n != 0 {
		t.Error("legacy session should be gone")
	}
}
