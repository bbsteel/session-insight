package db

import (
	"context"
	"database/sql"
	"fmt"
)

var v35SchemaObjects = []v34SchemaObject{
	{kind: "table", name: "change_request_sync_heads", ddl: `CREATE TABLE change_request_sync_heads (
		change_id TEXT PRIMARY KEY,
		snapshot_id TEXT NOT NULL,
		sync_started_unix_nano INTEGER NOT NULL CHECK (sync_started_unix_nano > 0),
		FOREIGN KEY (change_id, snapshot_id) REFERENCES change_request_snapshots(change_id, snapshot_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "change_request_snapshot_syncs", ddl: `CREATE TABLE change_request_snapshot_syncs (
		snapshot_id TEXT PRIMARY KEY,
		sync_started_unix_nano INTEGER NOT NULL CHECK (sync_started_unix_nano > 0),
		FOREIGN KEY (snapshot_id) REFERENCES change_request_snapshots(snapshot_id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "session_hosted_repository_bindings", ddl: `CREATE TABLE session_hosted_repository_bindings (
		binding_id TEXT PRIMARY KEY,
		host_id TEXT NOT NULL,
		repository_id TEXT NOT NULL,
		resolved_at TEXT NOT NULL,
		FOREIGN KEY (binding_id) REFERENCES session_git_bindings(binding_id) ON DELETE CASCADE,
		FOREIGN KEY (repository_id, host_id) REFERENCES hosted_repositories(repository_id, host_id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "session_change_request_evidence_links", ddl: `CREATE TABLE session_change_request_evidence_links (
		change_link_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		root_agent_type TEXT NOT NULL,
		root_session_id TEXT NOT NULL,
		source_agent_type TEXT NOT NULL,
		source_session_id TEXT NOT NULL,
		backing_agent_type TEXT NOT NULL DEFAULT '',
		backing_session_id TEXT NOT NULL DEFAULT '',
		invocation_id TEXT NOT NULL DEFAULT '',
		source_revision TEXT NOT NULL,
		positions_revision INTEGER NOT NULL CHECK (positions_revision >= 1),
		event_id TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		turn_index INTEGER CHECK (turn_index IS NULL OR turn_index >= 0),
		recorded_at TEXT,
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		PRIMARY KEY (change_link_id, ordinal),
		FOREIGN KEY (change_link_id) REFERENCES session_change_requests(link_id) ON DELETE CASCADE
	)`},
	{kind: "index", name: "idx_session_git_bindings_head", ddl: `CREATE INDEX idx_session_git_bindings_head ON session_git_bindings(head_sha, agent_type, session_id) WHERE head_sha <> ''`},
	{kind: "index", name: "idx_session_git_candidate_sha", ddl: `CREATE INDEX idx_session_git_candidate_sha ON session_git_candidate_commits(sha, evidence_id)`},
	{kind: "index", name: "idx_change_request_alias_change_kind", ddl: `CREATE INDEX idx_change_request_alias_change_kind ON change_request_aliases(change_id, alias_kind, repository_id, alias_value)`},
	{kind: "index", name: "idx_change_request_alias_value", ddl: `CREATE INDEX idx_change_request_alias_value ON change_request_aliases(alias_kind, alias_value, repository_id, change_id)`},
	{kind: "index", name: "idx_session_hosted_repository", ddl: `CREATE INDEX idx_session_hosted_repository ON session_hosted_repository_bindings(repository_id, binding_id)`},
	{kind: "index", name: "idx_session_change_requests_binding_ordinal_unique", ddl: `CREATE UNIQUE INDEX idx_session_change_requests_binding_ordinal_unique ON session_change_requests(root_agent_type, root_session_id, binding_id, ordinal) WHERE binding_id IS NOT NULL`},
	{kind: "index", name: "idx_session_change_requests_related_ordinal_unique", ddl: `CREATE UNIQUE INDEX idx_session_change_requests_related_ordinal_unique ON session_change_requests(root_agent_type, root_session_id, ordinal) WHERE binding_id IS NULL`},
	{kind: "index", name: "idx_session_change_request_evidence_source", ddl: `CREATE INDEX idx_session_change_request_evidence_source ON session_change_request_evidence_links(source_agent_type, source_session_id, source_revision)`},
}

var v35ReplacementTriggers = []v34SchemaObject{
	{kind: "trigger", name: "trg_change_request_aliases_scope_insert", ddl: `CREATE TRIGGER trg_change_request_aliases_scope_insert
		BEFORE INSERT ON change_request_aliases
		WHEN NOT EXISTS (
			SELECT 1 FROM change_request_identities identity
			WHERE identity.change_id = NEW.change_id AND (
				(identity.provider = 'generic' AND NEW.alias_kind = 'url' AND NEW.host_id IS NULL AND NEW.repository_id IS NULL AND NEW.snapshot_id IS NULL) OR
				(identity.provider <> 'generic' AND NEW.host_id = identity.host_id AND (
					NEW.repository_id = identity.target_repository_id OR EXISTS (
						SELECT 1 FROM change_request_snapshots snapshot
						WHERE NEW.alias_kind IN ('branch','head_sha')
						  AND snapshot.snapshot_id = NEW.snapshot_id
						  AND snapshot.change_id = identity.change_id
						  AND snapshot.source_repository_id = NEW.repository_id
					)
				))
			)
		)
		BEGIN
			SELECT RAISE(ABORT, 'change request alias scope must match its identity');
		END`},
	{kind: "trigger", name: "trg_change_request_aliases_scope_update", ddl: `CREATE TRIGGER trg_change_request_aliases_scope_update
		BEFORE UPDATE OF alias_kind, host_id, repository_id, change_id, snapshot_id ON change_request_aliases
		WHEN NOT EXISTS (
			SELECT 1 FROM change_request_identities identity
			WHERE identity.change_id = NEW.change_id AND (
				(identity.provider = 'generic' AND NEW.alias_kind = 'url' AND NEW.host_id IS NULL AND NEW.repository_id IS NULL AND NEW.snapshot_id IS NULL) OR
				(identity.provider <> 'generic' AND NEW.host_id = identity.host_id AND (
					NEW.repository_id = identity.target_repository_id OR EXISTS (
						SELECT 1 FROM change_request_snapshots snapshot
						WHERE NEW.alias_kind IN ('branch','head_sha')
						  AND snapshot.snapshot_id = NEW.snapshot_id
						  AND snapshot.change_id = identity.change_id
						  AND snapshot.source_repository_id = NEW.repository_id
					)
				))
			)
		)
		BEGIN
			SELECT RAISE(ABORT, 'change request alias scope must match its identity');
		END`},
}

// migrateGitAssociationV35 adds version-fixed link anchors and replaces only
// the two v34 alias-scope triggers. The replacement accepts source-repository
// aliases only when they are pinned to the matching immutable snapshot.
func migrateGitAssociationV35(conn *sql.DB) error {
	ctx := context.Background()
	complete, err := inspectV35Schema(ctx, conn)
	if err != nil {
		return err
	}
	versioned, err := schemaVersionExists(ctx, conn, 35)
	if err != nil {
		return fmt.Errorf("v35 inspect version: %w", err)
	}
	if complete && versioned {
		return nil
	}

	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v35 pin connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v35 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	for _, object := range v35SchemaObjects {
		exists, err := schemaObjectExists(ctx, c, object)
		if err != nil {
			return fmt.Errorf("v35 inspect %s %s: %w", object.kind, object.name, err)
		}
		if !exists {
			if _, err := c.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("v35 create %s %s: %w", object.kind, object.name, err)
			}
		}
	}
	for _, replacement := range v35ReplacementTriggers {
		var actual string
		err := c.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, replacement.name,
		).Scan(&actual)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("v35 inspect trigger %s: %w", replacement.name, err)
		}
		if err == nil && compactDDL(actual) == compactDDL(replacement.ddl) {
			continue
		}
		if err == nil {
			old, ok := v34ObjectByName("trigger", replacement.name)
			if !ok || compactDDL(actual) != compactDDL(old.ddl) {
				return fmt.Errorf("v35 incompatible trigger %s", replacement.name)
			}
			if _, err := c.ExecContext(ctx, `DROP TRIGGER `+replacement.name); err != nil {
				return fmt.Errorf("v35 drop trigger %s: %w", replacement.name, err)
			}
		}
		if _, err := c.ExecContext(ctx, replacement.ddl); err != nil {
			return fmt.Errorf("v35 create trigger %s: %w", replacement.name, err)
		}
	}
	if complete, err = inspectV35Schema(ctx, c); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("v35 physical schema remained incomplete after migration")
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (35)`); err != nil {
		return fmt.Errorf("v35 record version: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v35 commit: %w", err)
	}
	committed = true
	return nil
}

func inspectV35Schema(ctx context.Context, q schemaQueryer) (bool, error) {
	complete := true
	for _, object := range v35SchemaObjects {
		exists, err := schemaObjectExists(ctx, q, object)
		if err != nil {
			return false, fmt.Errorf("v35 inspect %s %s: %w", object.kind, object.name, err)
		}
		if !exists {
			complete = false
		}
	}
	for _, replacement := range v35ReplacementTriggers {
		var actual string
		err := q.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, replacement.name,
		).Scan(&actual)
		if err == sql.ErrNoRows {
			complete = false
			continue
		}
		if err != nil {
			return false, fmt.Errorf("v35 inspect trigger %s: %w", replacement.name, err)
		}
		if compactDDL(actual) == compactDDL(replacement.ddl) {
			continue
		}
		old, ok := v34ObjectByName("trigger", replacement.name)
		if ok && compactDDL(actual) == compactDDL(old.ddl) {
			complete = false
			continue
		}
		return false, fmt.Errorf("v35 incompatible trigger %s", replacement.name)
	}
	return complete, nil
}

func v34ObjectByName(kind, name string) (v34SchemaObject, bool) {
	for _, object := range v34SchemaObjects {
		if object.kind == kind && object.name == name {
			return object, true
		}
	}
	return v34SchemaObject{}, false
}
