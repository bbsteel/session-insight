package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestV34FreshSchema(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var version int
	if err := database.Conn().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 34 {
		t.Fatalf("schema version = %d, want 34", version)
	}
	complete, err := inspectV34Schema(t.Context(), database.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("fresh v34 physical schema is incomplete")
	}
	assertNoForeignKeyViolations(t, database.Conn())
}

func TestV34UpgradeFromV33PreservesWatermarks(t *testing.T) {
	dir := makeRawV33Database(t)
	raw, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE index_watermarks (
			agent_type TEXT NOT NULL, session_id TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 0, indexed_at TEXT NOT NULL,
			PRIMARY KEY (agent_type, session_id)
		);
		INSERT INTO index_watermarks VALUES ('codex','historical',42,'2026-08-11T00:00:00Z')`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var revision int64
	if err := database.Conn().QueryRow(`
		SELECT revision FROM index_watermarks
		WHERE agent_type = 'codex' AND session_id = 'historical'`,
	).Scan(&revision); err != nil {
		t.Fatalf("v34 must not clear historical watermarks: %v", err)
	}
	if revision != 42 {
		t.Fatalf("watermark revision = %d, want 42", revision)
	}
}

func TestV34SelfHealsMissingPhysicalIndex(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`DROP INDEX idx_source_content_blob_refs_blob`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var name string
	if err := reopened.Conn().QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_source_content_blob_refs_blob'`,
	).Scan(&name); err != nil {
		t.Fatalf("missing v34 index was not healed: %v", err)
	}
}

func TestV34SelfHealsMissingPhysicalTable(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`DROP TABLE session_git_evidence_links`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var name string
	if err := reopened.Conn().QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name = 'session_git_evidence_links'`,
	).Scan(&name); err != nil {
		t.Fatalf("missing v34 table was not healed: %v", err)
	}
}

func TestV34RejectsIncompatiblePhysicalSchema(t *testing.T) {
	dir := makeRawV33Database(t)
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE session_git_origins (agent_type TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	err = migrateGitAssociationV34(conn)
	if err == nil || !strings.Contains(err.Error(), "incompatible table session_git_origins") {
		t.Fatalf("incompatible schema error = %v", err)
	}
}

func TestV34MigrationRollback(t *testing.T) {
	dir := makeRawV33Database(t)
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`
		CREATE TRIGGER reject_v34 BEFORE INSERT ON schema_migrations
		WHEN NEW.version = 34 BEGIN
			SELECT RAISE(ABORT, 'reject v34 for rollback test');
		END`); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV34(conn); err == nil {
		t.Fatal("expected v34 migration failure")
	}
	var count int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'session_git_origins'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("v34 DDL survived a failed version insert")
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 34`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed v34 migration recorded its version")
	}
}

func TestV34ConcurrentMigrationUsesSQLiteLock(t *testing.T) {
	dir := makeRawV33Database(t)
	dsn := filepath.Join(dir, "index.db") + "?_foreign_keys=on&_busy_timeout=5000"
	connections := make([]*sql.DB, 2)
	for i := range connections {
		conn, err := sql.Open("sqlite3", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		connections[i] = conn
	}

	start := make(chan struct{})
	errs := make([]error, len(connections))
	var wg sync.WaitGroup
	for i, conn := range connections {
		wg.Add(1)
		go func(i int, conn *sql.DB) {
			defer wg.Done()
			<-start
			errs[i] = migrateGitAssociationV34(conn)
		}(i, conn)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration %d: %v", i, err)
		}
	}
	complete, err := inspectV34Schema(t.Context(), connections[0])
	if err != nil || !complete {
		t.Fatalf("concurrent schema complete=%v err=%v", complete, err)
	}
}

func TestV34ConstraintsAndIndexes(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "constraints")
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_bindings(
			binding_id, agent_type, session_id, repository_entry_key, worktree_root,
			common_root_id, worktree_id, state, observed_at
		) VALUES ('bad','codex','constraints','bad','/repo','common','worktree','certain','2026-08-11T00:00:00Z')`); err == nil {
		t.Fatal("binding accepted an unknown certainty state")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO source_content_blobs(sha256,content,raw_bytes,stored_bytes)
		VALUES (?, X'61', 1, 1)`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO source_content_blob_refs(blob_sha,path_key,purpose)
		VALUES (?, 'path', 'after')`, strings.Repeat("a", 64)); err == nil {
		t.Fatal("blob ref accepted no owner")
	}
	for _, index := range []string{
		"idx_session_git_snapshots_baseline",
		"idx_change_request_alias_lookup",
		"idx_source_content_blob_refs_local",
	} {
		var got string
		if err := database.Conn().QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&got); err != nil {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
	assertNoForeignKeyViolations(t, database.Conn())
}

func makeRawV33Database(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO schema_migrations(version) VALUES (33);
		CREATE TABLE sessions (
			agent_type TEXT NOT NULL DEFAULT 'copilot', id TEXT NOT NULL,
			cwd TEXT NOT NULL DEFAULT '', repository TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', model_name TEXT NOT NULL DEFAULT '',
			model_provider TEXT NOT NULL DEFAULT '', resume_id TEXT NOT NULL DEFAULT '',
			parent_session_id TEXT NOT NULL DEFAULT '', agent_path TEXT NOT NULL DEFAULT '', is_subagent INTEGER NOT NULL DEFAULT 0,
			turn_count INTEGER NOT NULL DEFAULT 0, historical_turn_count INTEGER NOT NULL DEFAULT 0,
			rolled_back_turn_count INTEGER NOT NULL DEFAULT 0, message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (agent_type, id)
		);
		CREATE TABLE session_provenance (
			agent_type TEXT NOT NULL, session_id TEXT NOT NULL, state TEXT NOT NULL,
			reason_code TEXT NOT NULL DEFAULT '', captured_at TEXT NOT NULL,
			source_updated_at TEXT, adapter_revision INTEGER NOT NULL,
			sources_json TEXT NOT NULL DEFAULT '[]', warnings_json TEXT NOT NULL DEFAULT '[]',
			warning_summary_json TEXT NOT NULL DEFAULT '{}', last_successful_at TEXT,
			missing_since TEXT, revision INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (agent_type, session_id)
		)`); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertNoForeignKeyViolations(t *testing.T, conn *sql.DB) {
	t.Helper()
	rows, err := conn.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check reported a violation")
	}
}
