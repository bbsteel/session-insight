package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestV37FreshSchema(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var version int
	if err := database.Conn().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	complete, err := inspectV34Schema(t.Context(), database.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		for _, object := range v34SchemaObjects {
			exists, objectErr := schemaObjectExists(t.Context(), database.Conn(), object)
			if objectErr != nil || !exists {
				t.Logf("v34 object %s %s exists=%v err=%v", object.kind, object.name, exists, objectErr)
			}
		}
		t.Fatal("fresh v34 physical schema is incomplete")
	}
	complete, err = inspectV35Schema(t.Context(), database.Conn())
	if err != nil || !complete {
		t.Fatalf("fresh v35 physical schema complete=%v err=%v", complete, err)
	}
	complete, err = inspectV36Schema(t.Context(), database.Conn())
	if err != nil || !complete {
		t.Fatalf("fresh v36 physical schema complete=%v err=%v", complete, err)
	}
	complete, err = inspectV37Schema(t.Context(), database.Conn())
	if err != nil || !complete {
		t.Fatalf("fresh v37 physical schema complete=%v err=%v", complete, err)
	}
	assertNoForeignKeyViolations(t, database.Conn())
}

func TestV37UpgradesAndRepairsMissingObjects(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Conn().Exec(`
		DROP TABLE session_change_request_creation_evidence;
		DROP TABLE session_change_request_creation_indexes;
		DELETE FROM schema_migrations WHERE version = 37`); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV37(database.Conn()); err != nil {
		t.Fatal(err)
	}
	complete, err := inspectV37Schema(t.Context(), database.Conn())
	if err != nil || !complete {
		t.Fatalf("upgraded v37 schema complete=%v err=%v", complete, err)
	}
	if _, err := database.Conn().Exec(`
		DROP INDEX idx_change_request_creation_url;
		DELETE FROM schema_migrations WHERE version = 37`); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV37(database.Conn()); err != nil {
		t.Fatal(err)
	}
	complete, err = inspectV37Schema(t.Context(), database.Conn())
	if err != nil || !complete {
		t.Fatalf("repaired v37 index complete=%v err=%v", complete, err)
	}
}

func TestV37RejectsIncompatiblePhysicalSchema(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Conn().Exec(`
		DROP TABLE session_change_request_creation_evidence;
		DROP TABLE session_change_request_creation_indexes;
		DELETE FROM schema_migrations WHERE version = 37;
		CREATE TABLE session_change_request_creation_indexes (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	err = migrateGitAssociationV37(database.Conn())
	if err == nil || !strings.Contains(err.Error(), "incompatible table session_change_request_creation_indexes") {
		t.Fatalf("incompatible v37 schema error = %v", err)
	}
}

func TestV37MigrationRollback(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Conn().Exec(`
		DROP TABLE session_change_request_creation_evidence;
		DROP TABLE session_change_request_creation_indexes;
		DELETE FROM schema_migrations WHERE version = 37;
		CREATE TRIGGER reject_v37 BEFORE INSERT ON schema_migrations
		WHEN NEW.version = 37 BEGIN
			SELECT RAISE(ABORT, 'reject v37 for rollback test');
		END`); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV37(database.Conn()); err == nil {
		t.Fatal("expected v37 migration failure")
	}
	var count int
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'session_change_request_creation_indexes'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("v37 DDL survived a failed version insert")
	}
}

func TestV37ConcurrentMigrationUsesSQLiteLock(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		DROP TABLE session_change_request_creation_evidence;
		DROP TABLE session_change_request_creation_indexes;
		DELETE FROM schema_migrations WHERE version = 37`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
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
			errs[i] = migrateGitAssociationV37(conn)
		}(i, conn)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent v37 migration %d: %v", i, err)
		}
	}
	complete, err := inspectV37Schema(t.Context(), connections[0])
	if err != nil || !complete {
		t.Fatalf("concurrent v37 schema complete=%v err=%v", complete, err)
	}
}

func TestV36RebuildPreservesSnapshotFilesAndAcceptsSpecialKinds(t *testing.T) {
	dir := makeRawV33Database(t)
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrateGitAssociationV34(conn); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV35(conn); err != nil {
		t.Fatal(err)
	}
	database := &DB{conn: conn}
	insertTestSession(t, database, "codex", "v36-upgrade")
	seed := testSessionGitEvidence("v36-upgrade", "entry-v36", "seed.go")
	if err := database.ReplaceSessionGitEvidence(seed); err != nil {
		t.Fatal(err)
	}
	bindingID := CanonicalSessionRepositoryBindingID("codex", "v36-upgrade", "entry-v36")
	write := localSnapshotTestWrite(bindingID, "v36-baseline", model.GitSnapshotBaseline, "seed.go", []byte("seed"))
	if err := database.StoreLocalGitSnapshot(write); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV36(conn); err != nil {
		t.Fatal(err)
	}

	var displayPath string
	if err := conn.QueryRow(`SELECT display_path FROM session_git_snapshot_files WHERE snapshot_id='v36-baseline'`).Scan(&displayPath); err != nil {
		t.Fatal(err)
	}
	if displayPath != "seed.go" {
		t.Fatalf("preserved display path = %q", displayPath)
	}
	if _, err := conn.Exec(`
		INSERT INTO session_git_snapshot_files(
			snapshot_id,path_key,ordinal,raw_path,display_path,path_encoding,
			layer,file_type,content_state
		) VALUES ('v36-baseline',?,1,?,'socket','utf8','worktree','special','unavailable')`,
		strings.Repeat("f", 64), []byte("socket"),
	); err != nil {
		t.Fatalf("insert special snapshot file: %v", err)
	}
	complete, err := inspectV34Schema(t.Context(), conn)
	if err != nil || !complete {
		for _, object := range v34SchemaObjects {
			exists, objectErr := schemaObjectExists(t.Context(), conn, object)
			if objectErr != nil || !exists {
				t.Logf("v34 object %s %s exists=%v err=%v", object.kind, object.name, exists, objectErr)
			}
		}
		t.Fatalf("v34 schema rejected v36 successor: complete=%v err=%v", complete, err)
	}
	assertNoForeignKeyViolations(t, conn)
}

func TestV35UpgradesV34AliasScopeWithoutWeakeningV34Inspection(t *testing.T) {
	dir := makeRawV33Database(t)
	conn, err := sql.Open("sqlite3", filepath.Join(dir, "index.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrateGitAssociationV34(conn); err != nil {
		t.Fatal(err)
	}
	oldTrigger, ok := v34ObjectByName("trigger", "trg_change_request_aliases_scope_insert")
	if !ok {
		t.Fatal("v34 alias trigger missing from contract")
	}
	var actual string
	if err := conn.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, oldTrigger.name).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if compactDDL(actual) != compactDDL(oldTrigger.ddl) {
		t.Fatal("v34 setup did not install the old alias trigger")
	}
	if err := migrateGitAssociationV35(conn); err != nil {
		t.Fatal(err)
	}
	complete, err := inspectV35Schema(t.Context(), conn)
	if err != nil || !complete {
		t.Fatalf("v35 schema complete=%v err=%v", complete, err)
	}
	complete, err = inspectV34Schema(t.Context(), conn)
	if err != nil || !complete {
		t.Fatalf("v34 schema rejected its compatible v35 successor: complete=%v err=%v", complete, err)
	}
}

func TestV35AliasScopeTriggersRejectSourceIdentityBypass(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertChangeHost(t, database, "host-github-public", "github", "github.example")
	write := testChangeRequestSnapshotWrite()
	changeKey, err := database.StoreChangeRequestSnapshot(write)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := CanonicalHostedRepositoryKey(*write.Snapshot.SourceRepository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_aliases(
			alias_kind,host_id,repository_id,alias_value,change_id,snapshot_id
		) VALUES ('url',?,?,?,?,?)`,
		write.Snapshot.Identity.HostID, sourceID, "https://github.example/fork/pull/42",
		changeKey, write.Snapshot.SnapshotID,
	); err == nil {
		t.Fatal("source repository accepted a Change Request identity URL alias")
	}
	if _, err := database.Conn().Exec(`
		UPDATE change_request_aliases SET alias_kind='url'
		WHERE change_id=? AND snapshot_id=? AND alias_kind='branch'`,
		changeKey, write.Snapshot.SnapshotID,
	); err == nil {
		t.Fatal("source branch alias was rewritten into an identity alias")
	}
	if _, err := database.Conn().Exec(`
		UPDATE change_request_aliases SET repository_id=?
		WHERE change_id=? AND snapshot_id=? AND alias_kind='url'`,
		sourceID, changeKey, write.Snapshot.SnapshotID,
	); err == nil {
		t.Fatal("target identity alias was moved to the source repository")
	}
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
		INSERT INTO index_watermarks VALUES ('claude','historical',42,'2026-08-11T00:00:00Z')`); err != nil {
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
	// Non-codex agent: v38 deliberately clears codex watermarks (paginated
	// history reparse), so the preservation check uses an untouched agent.
	if err := database.Conn().QueryRow(`
		SELECT revision FROM index_watermarks
		WHERE agent_type = 'claude' AND session_id = 'historical'`,
	).Scan(&revision); err != nil {
		t.Fatalf("v34 must not clear historical watermarks: %v", err)
	}
	if revision != 42 {
		t.Fatalf("watermark revision = %d, want 42", revision)
	}
}

// TestV38UpgradeClearsCodexWatermarks pins the reparse trigger for the Codex
// paginated-history parser (AdapterRevision 6): pre-v38 codex rows indexed
// empty message fields, so their watermarks must be cleared exactly once.
func TestV38UpgradeClearsCodexWatermarks(t *testing.T) {
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
		INSERT INTO index_watermarks VALUES ('codex','paginated-session',42,'2026-08-11T00:00:00Z')`); err != nil {
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
	var remaining int
	if err := database.Conn().QueryRow(`
		SELECT COUNT(*) FROM index_watermarks
		WHERE agent_type = 'codex' AND session_id = 'paginated-session'`,
	).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("v38 must clear codex watermarks, %d row(s) remain", remaining)
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

func TestV34SelfHealsMissingVersionRowInsideMigration(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Conn().Exec(`DELETE FROM schema_migrations WHERE version = 34`); err != nil {
		t.Fatal(err)
	}
	if err := migrateGitAssociationV34(database.Conn()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 34`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("v34 migration row count = %d, want 1", count)
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

func TestV34HostApprovalConstraints(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if _, err := database.Conn().Exec(`
		INSERT INTO change_hosts(
			host_id, provider, scheme, hostname, port, display_origin,
			endpoint_origins_json, allow_http, allow_private_network,
			lifecycle, state, approved_at
		) VALUES (
			'http-approved','gitlab','http','git.internal',80,'http://git.internal',
			'["http://git.internal"]',1,1,'approved','exact','2026-08-11T00:00:00Z'
		)`); err != nil {
		t.Fatalf("approved HTTP host rejected: %v", err)
	}
	var allowHTTP, allowPrivate int
	if err := database.Conn().QueryRow(`
		SELECT allow_http, allow_private_network FROM change_hosts WHERE host_id='http-approved'`,
	).Scan(&allowHTTP, &allowPrivate); err != nil {
		t.Fatal(err)
	}
	if allowHTTP != 1 || allowPrivate != 1 {
		t.Fatalf("persisted approval flags = (%d,%d), want (1,1)", allowHTTP, allowPrivate)
	}
	if _, err := database.Conn().Exec(`
		UPDATE change_hosts
		SET endpoint_origins_json='["http://git.internal","http://api.git.internal"]'
		WHERE host_id='http-approved'`); err == nil {
		t.Fatal("approved host endpoint authority changed without a new approval resource")
	}
	if _, err := database.Conn().Exec(`
		UPDATE change_hosts
		SET lifecycle='revoked', revoked_at='2026-08-11T00:01:00Z'
		WHERE host_id='http-approved'`); err != nil {
		t.Fatalf("approved host could not be revoked: %v", err)
	}
	if _, err := database.Conn().Exec(`
		UPDATE change_hosts SET lifecycle='approved', revoked_at=NULL
		WHERE host_id='http-approved'`); err == nil {
		t.Fatal("revoked host authority was silently reactivated")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_hosts(
			host_id, provider, scheme, hostname, port, display_origin,
			allow_http, lifecycle, state
		) VALUES ('http-preview','gitlab','http','other.internal',80,'http://other.internal',0,'preview','exact')`); err != nil {
		t.Fatalf("unapproved HTTP preview was not persistable: %v", err)
	}
	if _, err := database.Conn().Exec(`
		UPDATE change_hosts SET
			lifecycle='approved', approved_at='2026-08-11T00:00:00Z',
			endpoint_origins_json='["http://other.internal"]'
		WHERE host_id='http-preview'`); err == nil {
		t.Fatal("HTTP preview became approved without explicit allow_http")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_hosts(
			host_id,provider,hostname,display_origin,endpoint_origins_json,lifecycle,state,approved_at
		) VALUES (
			'generic-approved','generic','code.example','https://code.example',
			'["https://code.example"]','approved','exact','2026-08-11T00:00:00Z'
		)`); err == nil {
		t.Fatal("generic manual reference became a network-approved host")
	}
	for name, endpoints := range map[string]string{
		"object":  `{}`,
		"empty":   `[]`,
		"missing": `["https://api.example"]`,
	} {
		if _, err := database.Conn().Exec(`
			INSERT INTO change_hosts(
				host_id,provider,hostname,display_origin,endpoint_origins_json,lifecycle,state,approved_at
			) VALUES (?,?,?,?,?,'approved','exact','2026-08-11T00:00:00Z')`,
			"bad-endpoints-"+name, "github", name+".example", "https://"+name+".example", endpoints,
		); err == nil {
			t.Fatalf("approved host accepted invalid endpoint set %s", name)
		}
	}
}

func TestV34ChangeRequestRelationsCannotCrossAuthorityBoundaries(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "root-one")
	insertTestSession(t, database, "codex", "root-two")

	if _, err := database.Conn().Exec(`
		INSERT INTO change_hosts(
			host_id,provider,hostname,display_origin,endpoint_origins_json,lifecycle,state,approved_at
		) VALUES
			('host-one','github','one.example','https://one.example','["https://one.example"]','approved','exact','2026-08-11T00:00:00Z'),
			('host-two','gitlab','two.example','https://two.example','["https://two.example"]','approved','exact','2026-08-11T00:00:00Z');
		INSERT INTO hosted_repositories(repository_id,host_id,provider_immutable_id,slug)
		VALUES
			('repo-one','host-one','repo-native-one','acme/widgets'),
			('repo-two','host-two','repo-native-two','acme/widgets-fork');
		INSERT INTO session_git_bindings(
			binding_id,agent_type,session_id,repository_entry_key,worktree_root,
			common_root_id,worktree_id,state,observed_at
		) VALUES
			('binding-one','codex','root-one','entry-one','/repo','common-one','worktree-one','exact','2026-08-11T00:00:00Z'),
			('binding-two','codex','root-two','entry-two','/repo-two','common-two','worktree-two','exact','2026-08-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_identities(
			change_id,provider,host_id,target_repository_id,provider_object_id
		) VALUES ('cross-host','gitlab','host-two','repo-one','mr-cross')`); err == nil {
		t.Fatal("change request identity accepted a repository from another host")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_identities(
			change_id,provider,host_id,target_repository_id,provider_object_id
		) VALUES ('provider-mismatch','github','host-two','repo-two','pr-cross')`); err == nil {
		t.Fatal("change request identity accepted a provider different from its host")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_identities(
			change_id,provider,host_id,target_repository_id,provider_object_id
		) VALUES ('change-one','github','host-one','repo-one','pr-one');
		INSERT INTO change_request_identities(
			change_id,provider,host_id,target_repository_id,provider_object_id
		) VALUES ('change-two','github','host-one','repo-one','pr-two');
		INSERT INTO change_request_snapshots(
			snapshot_id,change_id,content_version_key,metadata_revision,kind,
			display_number,lifecycle_state,web_url,completeness_json,fetched_at
		) VALUES
		(
			'snapshot-one','change-one','content-one','metadata-one','pull_request',
			'1','open','https://one.example/acme/widgets/pull/1','{}','2026-08-11T00:00:00Z'
		),
		(
			'snapshot-two','change-two','content-two','metadata-two','pull_request',
			'2','open','https://one.example/acme/widgets/pull/2','{}','2026-08-11T00:00:00Z'
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_snapshots(
			snapshot_id,change_id,content_version_key,metadata_revision,kind,
			display_number,lifecycle_state,web_url,source_repository_id,completeness_json,fetched_at
		) VALUES (
			'cross-source','change-one','cross-source-content','metadata-two','pull_request',
			'1','open','https://one.example/acme/widgets/pull/1','repo-two','{}','2026-08-11T00:00:00Z'
		)`); err == nil {
		t.Fatal("change request snapshot accepted a source repository from another host")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_aliases(
			alias_kind,host_id,repository_id,alias_value,change_id,snapshot_id
		) VALUES ('url','host-two','repo-two','https://two.example/acme/widgets/merge_requests/1','change-one','snapshot-one')`); err == nil {
		t.Fatal("change request alias accepted a scope from another host/repository")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_aliases(
			alias_kind,host_id,repository_id,alias_value,change_id,snapshot_id
		) VALUES ('url','host-one','repo-one','https://one.example/acme/widgets/pull/1','change-one','snapshot-one')`); err != nil {
		t.Fatalf("valid scoped change request alias rejected: %v", err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO change_request_aliases(
			alias_kind,host_id,repository_id,alias_value,change_id,snapshot_id
		) VALUES ('head_sha','host-one','repo-one','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','change-one','snapshot-two')`); err == nil {
		t.Fatal("change request alias accepted a snapshot from another change")
	}

	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,snapshot_id,content_version_key,
			relationship,method,state,confirmation_source
		) VALUES (
			'cross-root',0,'codex','root-two','codex','root-two',1,'binding-one',
			'change-one','snapshot-one','content-one','contributing','head_sha','exact','none'
		)`); err == nil {
		t.Fatal("change request link accepted a binding owned by another root Session")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,snapshot_id,content_version_key,
			relationship,method,state,confirmation_source,confirmation_revision
		) VALUES (
			'wrong-content',0,'codex','root-one','codex','root-one',1,'binding-one',
			'change-one','snapshot-one','content-two','exclusive','explicit','exact','user','confirm-one'
		)`); err == nil {
		t.Fatal("change request link accepted a snapshot under the wrong content version")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,content_version_key,
			relationship,method,state,confirmation_source
		) VALUES (
			'unconfirmed-exclusive',0,'codex','root-one','codex','root-one',1,'binding-one',
			'change-one','content-one','exclusive','explicit','exact','none'
		)`); err == nil {
		t.Fatal("exclusive link accepted no fixed snapshot or user confirmation")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,content_version_key,
			relationship,method,state,confirmation_source
		) VALUES (
			'unfixed-contributing',0,'codex','root-one','codex','root-one',1,'binding-one',
			'change-one','invented-content','contributing','head_sha','estimated','none'
		)`); err == nil {
		t.Fatal("contributing link accepted an unpinned content version")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,snapshot_id,content_version_key,
			relationship,method,state,confirmation_source,confirmation_revision
		) VALUES (
			'valid-exclusive',0,'codex','root-one','codex','root-one',1,'binding-one',
			'change-one','snapshot-one','content-one','exclusive','explicit','exact','user','confirm-one'
		)`); err != nil {
		t.Fatalf("valid exclusive link rejected: %v", err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,snapshot_id,content_version_key,
			relationship,method,state,confirmation_source,confirmation_revision
		) VALUES (
			'duplicate-binding-exclusive',1,'codex','root-one','codex','root-one',1,'binding-one',
			'change-two','snapshot-two','content-two','exclusive','explicit','exact','user','confirm-two'
		)`); err == nil {
		t.Fatal("one repository binding accepted multiple exclusive change requests")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_change_requests(
			link_id,ordinal,root_agent_type,root_session_id,source_agent_type,source_session_id,
			collaboration_revision,binding_id,change_id,snapshot_id,content_version_key,
			relationship,method,state,confirmation_source,confirmation_revision
		) VALUES (
			'duplicate-change-exclusive',0,'codex','root-two','codex','root-two',1,'binding-two',
			'change-one','snapshot-one','content-one','exclusive','explicit','exact','user','confirm-three'
		)`); err == nil {
		t.Fatal("one fixed change request version became exclusive to multiple roots")
	}
	assertNoForeignKeyViolations(t, database.Conn())
}

func TestV34EvidenceLinksRequirePositivePositionsRevision(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "positions")
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_bindings(
			binding_id,agent_type,session_id,repository_entry_key,worktree_root,
			common_root_id,worktree_id,state,observed_at
		) VALUES ('positions-binding','codex','positions','positions-entry','/repo','common','worktree','exact','2026-08-11T00:00:00Z');
		INSERT INTO session_git_evidence(
			evidence_id,binding_id,revision,state,authority,generated_at
		) VALUES ('positions-binding','positions-binding',1,'exact','none','2026-08-11T00:00:00Z');
		INSERT INTO session_git_files(
			evidence_id,file_key,ordinal,layer,display_path,path_encoding,status,status_state,patch_state
		) VALUES ('positions-binding','file-one',0,'worktree','file.go','utf8','modified','exact','exact')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_evidence_links(
			evidence_id,file_key,ordinal,root_agent_type,root_session_id,
			source_agent_type,source_session_id,source_revision,positions_revision,state
		) VALUES (
			'positions-binding','file-one',0,'codex','positions',
			'codex','positions','source-one',0,'exact'
		)`); err == nil {
		t.Fatal("evidence link accepted positions_revision=0")
	}
}

func TestV34GitEvidenceSnapshotsMatchBindingAndKind(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertTestSession(t, database, "codex", "snapshot-root-one")
	insertTestSession(t, database, "codex", "snapshot-root-two")
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_bindings(
			binding_id,agent_type,session_id,repository_entry_key,worktree_root,
			common_root_id,worktree_id,state,observed_at
		) VALUES
			('snapshot-binding-one','codex','snapshot-root-one','snapshot-entry-one','/repo-one','common-one','worktree-one','exact','2026-08-11T00:00:00Z'),
			('snapshot-binding-two','codex','snapshot-root-two','snapshot-entry-two','/repo-two','common-two','worktree-two','exact','2026-08-11T00:00:00Z');
		INSERT INTO session_git_snapshots(
			snapshot_id,binding_id,kind,source_revision,state,capture_started_at,capture_completed_at
		) VALUES
			('baseline-one','snapshot-binding-one','baseline','source-one','exact','2026-08-11T00:00:00Z','2026-08-11T00:00:01Z'),
			('final-one','snapshot-binding-one','final','source-two','exact','2026-08-11T00:01:00Z','2026-08-11T00:01:01Z'),
			('baseline-two','snapshot-binding-two','baseline','source-one','exact','2026-08-11T00:00:00Z','2026-08-11T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_evidence(
			evidence_id,binding_id,revision,state,authority,baseline_snapshot_id,generated_at
		) VALUES ('cross-binding-evidence','snapshot-binding-one',1,'exact','local_interval','baseline-two','2026-08-11T00:02:00Z')`); err == nil {
		t.Fatal("Git evidence accepted a baseline from another binding")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_evidence(
			evidence_id,binding_id,revision,state,authority,baseline_snapshot_id,generated_at
		) VALUES ('wrong-kind-evidence','snapshot-binding-one',1,'exact','local_interval','final-one','2026-08-11T00:02:00Z')`); err == nil {
		t.Fatal("Git evidence accepted a final snapshot as its baseline")
	}
	if _, err := database.Conn().Exec(`
		INSERT INTO session_git_evidence(
			evidence_id,binding_id,revision,state,authority,baseline_snapshot_id,final_snapshot_id,generated_at
		) VALUES (
			'valid-snapshot-evidence','snapshot-binding-one',1,'exact','local_interval',
			'baseline-one','final-one','2026-08-11T00:02:00Z'
		)`); err != nil {
		t.Fatalf("valid evidence snapshot pair rejected: %v", err)
	}
	if err := database.DeleteSessionData("codex", "snapshot-root-one"); err != nil {
		t.Fatalf("delete Session with evidence snapshots: %v", err)
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
