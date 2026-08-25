package db

import (
	"context"
	"database/sql"
	"fmt"
)

// v43 widens the change-host provider CHECK constraints with 'openapi' and
// introduces change_host_profiles, the immutable, revisioned storage for
// verified OpenAPI adapter configurations. SQLite cannot alter a CHECK in
// place, so change_hosts and change_request_identities are rebuilt under one
// pinned transaction with foreign-key enforcement temporarily off; a
// foreign_key_check runs before commit.

var v43ChangeHostsDDL = `CREATE TABLE change_hosts (
		host_id TEXT PRIMARY KEY,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','gitea','forgejo','bitbucket_cloud','bitbucket_data_center','azure_devops','gerrit','openapi','generic')),
		scheme TEXT NOT NULL DEFAULT 'https' CHECK (scheme IN ('https','http')),
		hostname TEXT NOT NULL,
		port INTEGER NOT NULL DEFAULT 443 CHECK (port BETWEEN 1 AND 65535),
		display_origin TEXT NOT NULL,
		endpoint_origins_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(endpoint_origins_json)),
		credential_reference TEXT NOT NULL DEFAULT '',
		allow_http INTEGER NOT NULL DEFAULT 0 CHECK (allow_http IN (0,1)),
		allow_private_network INTEGER NOT NULL DEFAULT 0 CHECK (allow_private_network IN (0,1)),
		lifecycle TEXT NOT NULL CHECK (lifecycle IN ('preview','approved','revoked')),
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		approved_at TEXT,
		revoked_at TEXT,
		last_checked_at TEXT,
		UNIQUE (scheme, hostname, port),
		UNIQUE (host_id, provider),
		CHECK (hostname <> '' AND display_origin <> ''),
		CHECK (json_type(endpoint_origins_json) = 'array'),
		CHECK (lifecycle = 'preview' OR json_array_length(endpoint_origins_json) > 0),
		CHECK (scheme = 'https' OR lifecycle = 'preview' OR allow_http = 1),
		CHECK (provider <> 'generic' OR lifecycle = 'preview'),
		CHECK ((lifecycle = 'preview' AND approved_at IS NULL AND revoked_at IS NULL) OR (lifecycle = 'approved' AND approved_at IS NOT NULL AND revoked_at IS NULL) OR (lifecycle = 'revoked' AND approved_at IS NOT NULL AND revoked_at IS NOT NULL))
	)`

var v43ChangeRequestIdentitiesDDL = `CREATE TABLE change_request_identities (
		change_id TEXT PRIMARY KEY,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','gitea','forgejo','bitbucket_cloud','bitbucket_data_center','azure_devops','gerrit','openapi','generic')),
		host_id TEXT,
		target_repository_id TEXT,
		provider_object_id TEXT NOT NULL DEFAULT '',
		generic_opaque_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE (change_id, host_id),
		FOREIGN KEY (host_id) REFERENCES change_hosts(host_id) ON DELETE RESTRICT,
		FOREIGN KEY (host_id, provider) REFERENCES change_hosts(host_id, provider) ON DELETE RESTRICT,
		FOREIGN KEY (target_repository_id, host_id) REFERENCES hosted_repositories(repository_id, host_id) ON DELETE RESTRICT,
		CHECK ((provider = 'generic' AND generic_opaque_id <> '' AND host_id IS NULL AND target_repository_id IS NULL AND provider_object_id = '') OR (provider <> 'generic' AND generic_opaque_id = '' AND host_id IS NOT NULL AND target_repository_id IS NOT NULL AND provider_object_id <> ''))
	)`

var v43SchemaObjects = []v34SchemaObject{
	{kind: "table", name: "change_host_profiles", ddl: `CREATE TABLE change_host_profiles (
		profile_id TEXT PRIMARY KEY,
		host_id TEXT NOT NULL,
		profile_revision INTEGER NOT NULL CHECK (profile_revision >= 1),
		schema_version INTEGER NOT NULL CHECK (schema_version = 1),
		display_name TEXT NOT NULL,
		lifecycle TEXT NOT NULL CHECK (lifecycle IN ('draft','verified','active','degraded','invalid','revoked')),
		profile_json TEXT NOT NULL CHECK (json_valid(profile_json)),
		inference_report_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(inference_report_json)),
		spec_digest TEXT NOT NULL,
		spec_version TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		verified_at TEXT,
		activated_at TEXT,
		last_success_at TEXT,
		last_failure_at TEXT,
		last_failure_code TEXT NOT NULL DEFAULT '',
		UNIQUE (host_id, profile_revision),
		FOREIGN KEY (host_id) REFERENCES change_hosts(host_id) ON DELETE RESTRICT,
		CHECK (profile_id <> '' AND host_id <> '' AND display_name <> ''),
		CHECK (length(spec_digest) = 71 AND substr(spec_digest, 1, 7) = 'sha256:' AND substr(spec_digest, 8) NOT GLOB '*[^0-9a-f]*'),
		CHECK (lifecycle <> 'verified' OR verified_at IS NOT NULL),
		CHECK (lifecycle <> 'active' OR (verified_at IS NOT NULL AND activated_at IS NOT NULL)),
		CHECK (lifecycle <> 'degraded' OR last_failure_code <> '')
	)`},
	{kind: "index", name: "idx_change_host_profiles_active", ddl: `CREATE UNIQUE INDEX idx_change_host_profiles_active ON change_host_profiles(host_id) WHERE lifecycle = 'active'`},
	{kind: "index", name: "idx_change_host_profiles_host", ddl: `CREATE INDEX idx_change_host_profiles_host ON change_host_profiles(host_id, profile_revision DESC)`},
	{kind: "trigger", name: "trg_change_host_profiles_identity_immutable", ddl: `CREATE TRIGGER trg_change_host_profiles_identity_immutable
		BEFORE UPDATE ON change_host_profiles
		WHEN NEW.profile_id IS NOT OLD.profile_id OR NEW.host_id IS NOT OLD.host_id OR
			NEW.profile_revision IS NOT OLD.profile_revision OR NEW.schema_version IS NOT OLD.schema_version
		BEGIN
			SELECT RAISE(ABORT, 'change host profile identity is immutable');
		END`},
	{kind: "trigger", name: "trg_change_host_profiles_active_immutable", ddl: `CREATE TRIGGER trg_change_host_profiles_active_immutable
		BEFORE UPDATE ON change_host_profiles
		WHEN OLD.lifecycle = 'active' AND (
			NEW.profile_json IS NOT OLD.profile_json OR NEW.spec_digest IS NOT OLD.spec_digest
		)
		BEGIN
			SELECT RAISE(ABORT, 'active change host profile mapping is immutable');
		END`},
	{kind: "trigger", name: "trg_change_host_profiles_delete_guard", ddl: `CREATE TRIGGER trg_change_host_profiles_delete_guard
		BEFORE DELETE ON change_host_profiles
		WHEN EXISTS (
			SELECT 1 FROM change_request_snapshots snapshot
			WHERE snapshot.profile_id = OLD.profile_id
		)
		BEGIN
			SELECT RAISE(ABORT, 'change host profile is referenced by historical snapshots');
		END`},
}

// v43ChangeRequestSnapshotsDDL is the v34 snapshot table after the two
// profile provenance columns were appended by ALTER TABLE. v34's physical
// audit accepts this as the compatible successor shape.
var v43ChangeRequestSnapshotsDDL = `CREATE TABLE change_request_snapshots (
		snapshot_id TEXT PRIMARY KEY,
		change_id TEXT NOT NULL,
		content_version_key TEXT NOT NULL,
		native_version TEXT NOT NULL DEFAULT '',
		metadata_revision TEXT NOT NULL,
		base_ref_sha TEXT NOT NULL DEFAULT '',
		diff_base_sha TEXT NOT NULL DEFAULT '',
		head_sha TEXT NOT NULL DEFAULT '',
		file_manifest_digest TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL CHECK (kind IN ('pull_request','merge_request','change','code_review')),
		display_number TEXT NOT NULL,
		lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('open','merged','closed','abandoned','unknown')),
		draft INTEGER NOT NULL DEFAULT 0 CHECK (draft IN (0,1)),
		title TEXT NOT NULL DEFAULT '',
		web_url TEXT NOT NULL,
		source_repository_id TEXT,
		source_ref TEXT NOT NULL DEFAULT '',
		target_ref TEXT NOT NULL DEFAULT '',
		merge_commit_sha TEXT NOT NULL DEFAULT '',
		squash_commit_sha TEXT NOT NULL DEFAULT '',
		completeness_json TEXT NOT NULL CHECK (json_valid(completeness_json)),
		etag TEXT NOT NULL DEFAULT '',
		fetched_at TEXT NOT NULL,
		cache_state TEXT NOT NULL DEFAULT 'current' CHECK (cache_state IN ('current','stale','content_deleted')),
		profile_id TEXT NOT NULL DEFAULT '',
		profile_revision INTEGER NOT NULL DEFAULT 0,
		UNIQUE (change_id, content_version_key),
		UNIQUE (change_id, snapshot_id),
		UNIQUE (change_id, snapshot_id, content_version_key),
		FOREIGN KEY (change_id) REFERENCES change_request_identities(change_id) ON DELETE CASCADE,
		FOREIGN KEY (source_repository_id) REFERENCES hosted_repositories(repository_id) ON DELETE RESTRICT
	)`

// v43RebuiltTables maps each rebuilt table to its widened v43 DDL.
var v43RebuiltTables = []struct {
	name string
	ddl  string
}{
	{name: "change_hosts", ddl: v43ChangeHostsDDL},
	{name: "change_request_identities", ddl: v43ChangeRequestIdentitiesDDL},
}

// v43RebuiltTriggers are the v34 triggers dropped together with the rebuilt
// tables; they are recreated verbatim once the widened tables exist.
var v43RebuiltTriggers = []string{
	"trg_change_hosts_endpoint_insert",
	"trg_change_hosts_endpoint_update",
	"trg_change_hosts_approval_immutable",
	"trg_change_request_identities_immutable",
}

// v43RebuiltIndexes are the v34 indexes on change_request_identities that the
// rebuild drops and must recreate.
var v43RebuiltIndexes = []string{
	"idx_change_request_identity_provider",
	"idx_change_request_identity_generic",
}

func migrateGitAssociationV43(conn *sql.DB) error {
	ctx := context.Background()
	complete, err := inspectV43Schema(ctx, conn)
	if err != nil {
		return err
	}
	versioned, err := schemaVersionExists(ctx, conn, 43)
	if err != nil {
		return fmt.Errorf("v43 inspect version: %w", err)
	}
	if complete && versioned {
		return nil
	}

	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v43 pin connection: %w", err)
	}
	defer c.Close()

	// The rebuild drops tables other tables reference. PRAGMA foreign_keys is
	// a no-op inside a transaction, so enforcement must be relaxed before
	// BEGIN and restored after COMMIT on this pinned connection.
	// legacy_alter_table keeps ALTER TABLE ... RENAME TO from reparsing the
	// triggers that reference the temporarily missing original table name;
	// the migration recreates every affected trigger itself.
	if _, err := c.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("v43 relax foreign keys: %w", err)
	}
	if _, err := c.ExecContext(ctx, `PRAGMA legacy_alter_table=ON`); err != nil {
		return fmt.Errorf("v43 legacy rename mode: %w", err)
	}
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v43 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
		_, _ = c.ExecContext(ctx, `PRAGMA legacy_alter_table=OFF`)
		_, _ = c.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	}()

	complete, err = inspectV43Schema(ctx, c)
	if err != nil {
		return err
	}
	if !complete {
		for _, table := range v43RebuiltTables {
			if err := rebuildProviderTableV43(ctx, c, table.name, table.ddl); err != nil {
				return err
			}
		}
		for _, object := range v43SchemaObjects {
			exists, err := schemaObjectExists(ctx, c, object)
			if err != nil {
				return fmt.Errorf("v43 inspect %s %s: %w", object.kind, object.name, err)
			}
			if exists {
				continue
			}
			if _, err := c.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("v43 create %s %s: %w", object.kind, object.name, err)
			}
		}
		if err := recreateV43DroppedObjects(ctx, c); err != nil {
			return err
		}
		if err := addV43SnapshotProfileColumns(ctx, c); err != nil {
			return err
		}
	}
	// Scope the integrity check to the change-host graph: the tables this
	// migration rebuilds or creates, plus children whose parents all live in
	// that graph. Session-linked tables are excluded because partial upgrade
	// fixtures legitimately carry older sessions shapes.
	for _, table := range []string{
		"change_hosts", "change_request_identities", "change_host_profiles",
		"hosted_repositories", "change_request_snapshots", "change_request_aliases",
	} {
		if err := v43ForeignKeyCheck(ctx, c, table); err != nil {
			return err
		}
	}
	if complete, err = inspectV43Schema(ctx, c); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("v43 physical schema remained incomplete after migration")
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (43)`); err != nil {
		return fmt.Errorf("v43 record version: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v43 commit: %w", err)
	}
	committed = true
	return nil
}

// v43ForeignKeyCheck verifies one table's foreign keys when the table exists;
// upgrade fixtures from older schemas may legitimately lack it.
func v43ForeignKeyCheck(ctx context.Context, conn *sql.Conn, table string) error {
	var present int
	err := conn.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&present)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("v43 inspect table %s: %w", table, err)
	}
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check(`+table+`)`)
	if err != nil {
		return fmt.Errorf("v43 foreign key check %s: %w", table, err)
	}
	violations := 0
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("v43 foreign key check %s: %w", table, err)
	}
	rows.Close()
	if violations > 0 {
		return fmt.Errorf("v43 foreign key check %s reported %d violations", table, violations)
	}
	return nil
}

// rebuildProviderTableV43 replaces one provider-CHECK table with its widened
// definition, preserving every row. Already-widened tables are left alone.
func rebuildProviderTableV43(ctx context.Context, conn *sql.Conn, name, widenedDDL string) error {
	var actual string
	err := conn.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&actual)
	if err == sql.ErrNoRows {
		return fmt.Errorf("v43 expected table %s to exist", name)
	}
	if err != nil {
		return fmt.Errorf("v43 inspect table %s: %w", name, err)
	}
	if compactDDL(actual) == compactDDL(widenedDDL) {
		return nil
	}
	var prior string
	for _, object := range v34SchemaObjects {
		if object.kind == "table" && object.name == name {
			prior = object.ddl
		}
	}
	if prior == "" || compactDDL(actual) != compactDDL(prior) {
		return fmt.Errorf("v43 incompatible table %s", name)
	}
	temporary := name + "_v43"
	replacement := replaceTableName(widenedDDL, name, temporary)
	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS `+temporary); err != nil {
		return fmt.Errorf("v43 clear stale %s: %w", temporary, err)
	}
	if _, err := conn.ExecContext(ctx, replacement); err != nil {
		return fmt.Errorf("v43 create replacement %s: %w", name, err)
	}
	columns, err := tableColumns(ctx, conn, name)
	if err != nil {
		return fmt.Errorf("v43 list %s columns: %w", name, err)
	}
	columnList := ""
	for i, column := range columns {
		if i > 0 {
			columnList += ", "
		}
		columnList += column
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO `+temporary+`(`+columnList+`) SELECT `+columnList+` FROM `+name); err != nil {
		return fmt.Errorf("v43 copy %s rows: %w", name, err)
	}
	if _, err := conn.ExecContext(ctx, `DROP TABLE `+name); err != nil {
		return fmt.Errorf("v43 drop prior %s: %w", name, err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE `+temporary+` RENAME TO `+name); err != nil {
		return fmt.Errorf("v43 rename replacement %s: %w", name, err)
	}
	return nil
}

// replaceTableName rewrites the table name in a CREATE TABLE statement so the
// widened DDL can create its temporary replacement.
func replaceTableName(ddl, name, replacement string) string {
	const prefix = "CREATE TABLE "
	out := ddl
	if len(out) >= len(prefix)+len(name) && out[:len(prefix)] == prefix && out[len(prefix):len(prefix)+len(name)] == name {
		out = prefix + replacement + out[len(prefix)+len(name):]
	}
	return out
}

func tableColumns(ctx context.Context, conn *sql.Conn, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

// recreateV43DroppedObjects restores the triggers and indexes the table
// rebuilds dropped. Existing objects are detected by name so a partially
// completed earlier attempt is repaired instead of failing.
func recreateV43DroppedObjects(ctx context.Context, conn *sql.Conn) error {
	for _, name := range v43RebuiltTriggers {
		for _, object := range v34SchemaObjects {
			if object.kind != "trigger" || object.name != name {
				continue
			}
			exists, err := schemaObjectExists(ctx, conn, object)
			if err != nil {
				return fmt.Errorf("v43 inspect trigger %s: %w", name, err)
			}
			if exists {
				break
			}
			var present string
			err = conn.QueryRowContext(ctx,
				`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name,
			).Scan(&present)
			if err == nil {
				return fmt.Errorf("v43 incompatible trigger %s", name)
			}
			if err != sql.ErrNoRows {
				return fmt.Errorf("v43 inspect trigger %s: %w", name, err)
			}
			if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("v43 recreate trigger %s: %w", name, err)
			}
		}
	}
	for _, name := range v43RebuiltIndexes {
		for _, object := range v34SchemaObjects {
			if object.kind != "index" || object.name != name {
				continue
			}
			var present string
			err := conn.QueryRowContext(ctx,
				`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
			).Scan(&present)
			if err == sql.ErrNoRows {
				if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
					return fmt.Errorf("v43 recreate index %s: %w", name, err)
				}
				continue
			}
			if err != nil {
				return fmt.Errorf("v43 inspect index %s: %w", name, err)
			}
			if compactDDL(present) != compactDDL(object.ddl) {
				return fmt.Errorf("v43 incompatible index %s", name)
			}
		}
	}
	return nil
}

// addV43SnapshotProfileColumns records which profile revision produced each
// hosted snapshot (design §10 recommendation). The columns are diagnostic
// only and never participate in the user-visible change identity.
func addV43SnapshotProfileColumns(ctx context.Context, conn *sql.Conn) error {
	for _, column := range []struct{ name, ddl string }{
		{"profile_id", `profile_id TEXT NOT NULL DEFAULT ''`},
		{"profile_revision", `profile_revision INTEGER NOT NULL DEFAULT 0`},
	} {
		has, err := tableHasColumn(ctx, conn, "change_request_snapshots", column.name)
		if err != nil {
			return fmt.Errorf("v43 inspect change_request_snapshots.%s: %w", column.name, err)
		}
		if has {
			continue
		}
		if _, err := conn.ExecContext(ctx, `ALTER TABLE change_request_snapshots ADD COLUMN `+column.ddl); err != nil {
			return fmt.Errorf("v43 add change_request_snapshots.%s: %w", column.name, err)
		}
	}
	return nil
}

func inspectV43Schema(ctx context.Context, q schemaQueryer) (bool, error) {
	for _, table := range v43RebuiltTables {
		var actual string
		err := q.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table.name,
		).Scan(&actual)
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("v43 missing table %s", table.name)
		}
		if err != nil {
			return false, fmt.Errorf("v43 inspect table %s: %w", table.name, err)
		}
		if compactDDL(actual) != compactDDL(table.ddl) {
			return false, nil
		}
	}
	for _, object := range v43SchemaObjects {
		exists, err := schemaObjectExists(ctx, q, object)
		if err != nil || !exists {
			return false, err
		}
	}
	rowsQueryer, ok := q.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	})
	if !ok {
		return false, fmt.Errorf("v43 inspect requires row queries")
	}
	snapshotProfileID, err := tableHasColumn(ctx, rowsQueryer, "change_request_snapshots", "profile_id")
	if err != nil || !snapshotProfileID {
		return false, err
	}
	snapshotProfileRevision, err := tableHasColumn(ctx, rowsQueryer, "change_request_snapshots", "profile_revision")
	if err != nil || !snapshotProfileRevision {
		return false, err
	}
	for _, name := range v43RebuiltTriggers {
		var present int
		err := q.QueryRowContext(ctx,
			`SELECT 1 FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name,
		).Scan(&present)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("v43 inspect trigger %s: %w", name, err)
		}
	}
	for _, name := range v43RebuiltIndexes {
		var present int
		err := q.QueryRowContext(ctx,
			`SELECT 1 FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
		).Scan(&present)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("v43 inspect index %s: %w", name, err)
		}
	}
	return true, nil
}

// v43ObjectDDL returns the widened v43 DDL for one of the v34 tables whose
// physical shape v43 replaced (rebuilt provider-CHECK tables) or extended
// (snapshot profile provenance columns), or "" when v43 left it untouched.
func v43ObjectDDL(name string) string {
	for _, table := range v43RebuiltTables {
		if table.name == name {
			return table.ddl
		}
	}
	if name == "change_request_snapshots" {
		return v43ChangeRequestSnapshotsDDL
	}
	return ""
}
