//go:build sqlite_fts5

package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/collaboration"
)

// oldCollaborationRootsSchema is the collaboration_roots shape written by
// pre-merge collaboration binaries: no list-aggregate count columns. The v28
// CREATE TABLE IF NOT EXISTS cannot add columns to this existing table, so
// every ReplaceCollaborationGraph failed with "no column named child_count"
// until v29 backfilled them.
const oldCollaborationRootsSchema = `
CREATE TABLE collaboration_roots (
    root_agent_type     TEXT    NOT NULL,
    root_session_id     TEXT    NOT NULL,
    revision            INTEGER NOT NULL DEFAULT 0,
    completeness_state  TEXT    NOT NULL DEFAULT 'missing',
    completeness_reason TEXT    NOT NULL DEFAULT '',
    graph_status        TEXT    NOT NULL DEFAULT 'ok' CHECK (graph_status IN ('ok', 'stale')),
    status_detail       TEXT    NOT NULL DEFAULT '',
    issues_json         TEXT    NOT NULL DEFAULT '[]',
    indexed_at          TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (root_agent_type, root_session_id)
);`

// makePreV29CollaborationDB builds a raw index.db whose collaboration_roots
// table predates the count columns, then closes it. schema_migrations is
// pinned at 28 with an extra stray 29 row (observed in the wild: a version row
// can exist without the physical columns, so v29 must gate on the columns,
// not the version). The invocation/delegation tables match the v28 shape.
func makePreV29CollaborationDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmts := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_migrations(version) VALUES (28)`,
		`INSERT INTO schema_migrations(version) VALUES (29)`,
		oldCollaborationRootsSchema,
		`CREATE TABLE collaboration_invocations (
		    root_agent_type         TEXT    NOT NULL,
		    root_session_id         TEXT    NOT NULL,
		    invocation_id           TEXT    NOT NULL,
		    ordinal                 INTEGER NOT NULL DEFAULT 0,
		    is_root                 INTEGER NOT NULL DEFAULT 0,
		    display_name            TEXT    NOT NULL DEFAULT '',
		    agent_type              TEXT    NOT NULL DEFAULT '',
		    role_label              TEXT    NOT NULL DEFAULT '',
		    status                  TEXT    NOT NULL DEFAULT 'unknown',
		    started_at              TEXT,
		    ended_at                TEXT,
		    time_precision_state    TEXT    NOT NULL DEFAULT 'missing',
		    time_precision_reason   TEXT    NOT NULL DEFAULT '',
		    content_precision_state TEXT    NOT NULL DEFAULT 'missing',
		    content_precision_reason TEXT   NOT NULL DEFAULT '',
		    backing_agent_type      TEXT    NOT NULL DEFAULT '',
		    backing_session_id      TEXT    NOT NULL DEFAULT '',
		    source_identity_json    TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (root_agent_type, root_session_id, invocation_id),
		    FOREIGN KEY (root_agent_type, root_session_id)
		        REFERENCES collaboration_roots(root_agent_type, root_session_id)
		        ON DELETE CASCADE
		)`,
		`CREATE TABLE collaboration_delegations (
		    root_agent_type       TEXT    NOT NULL,
		    root_session_id       TEXT    NOT NULL,
		    delegation_id         TEXT    NOT NULL,
		    ordinal               INTEGER NOT NULL DEFAULT 0,
		    parent_invocation_id  TEXT    NOT NULL,
		    child_invocation_id   TEXT    NOT NULL,
		    task_summary          TEXT    NOT NULL DEFAULT '',
		    execution_mode        TEXT    NOT NULL DEFAULT 'unknown',
		    trigger_json          TEXT,
		    result_json           TEXT,
		    evidence_json         TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (root_agent_type, root_session_id, delegation_id),
		    FOREIGN KEY (root_agent_type, root_session_id)
		        REFERENCES collaboration_roots(root_agent_type, root_session_id)
		        ON DELETE CASCADE
		)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("seed pre-v29 schema: %v", err)
		}
	}
	return dir
}

// TestV29BackfillsCollaborationCountColumns proves a database whose
// collaboration_roots predates the count columns — and whose schema_migrations
// already claims version 29 — is healed on open and accepts graph writes.
func TestV29BackfillsCollaborationCountColumns(t *testing.T) {
	dir := makePreV29CollaborationDB(t)
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("open/migrate pre-v29 db: %v", err)
	}
	defer database.Close()

	for _, col := range []string{"child_count", "active_count", "problem_count"} {
		has, err := tableHasColumn(t.Context(), database.Conn(), "collaboration_roots", col)
		if err != nil {
			t.Fatalf("inspect %s: %v", col, err)
		}
		if !has {
			t.Errorf("column collaboration_roots.%s missing after v29 migration", col)
		}
	}

	graph := collabTestGraph("grok", "s1", 7,
		collabTestChild{nativeID: "c1", status: collaboration.StatusCompleted},
		collabTestChild{nativeID: "c2", status: collaboration.StatusRunning},
		collabTestChild{nativeID: "c3", status: collaboration.StatusFailed},
	)
	if err := database.ReplaceCollaborationGraph(graph); err != nil {
		t.Fatalf("replace after v29 backfill: %v", err)
	}
	summaries, err := database.CollaborationSummaries("grok")
	if err != nil {
		t.Fatalf("summaries after v29 backfill: %v", err)
	}
	sum, ok := summaries[CollaborationKey("grok", "s1")]
	if !ok {
		t.Fatalf("summary missing for s1: %+v", summaries)
	}
	if sum.ChildCount != 3 || sum.ActiveCount != 1 || sum.ProblemCount != 1 {
		t.Errorf("unexpected aggregate after backfill: %+v", sum)
	}

	// Idempotent: a second open leaves the healed schema untouched.
	database.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen healed db: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.GetCollaboration("grok", "s1"); err != nil {
		t.Fatalf("read healed graph: %v", err)
	}
}
