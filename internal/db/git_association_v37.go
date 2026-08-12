package db

import (
	"context"
	"database/sql"
	"fmt"
)

var v37SchemaObjects = []v34SchemaObject{
	{kind: "table", name: "session_change_request_creation_indexes", ddl: `CREATE TABLE session_change_request_creation_indexes (
		agent_type TEXT NOT NULL,
		session_id TEXT NOT NULL,
		source_revision TEXT NOT NULL,
		indexed_at TEXT NOT NULL,
		PRIMARY KEY (agent_type, session_id),
		FOREIGN KEY (agent_type, session_id) REFERENCES sessions(agent_type, id) ON DELETE CASCADE,
		CHECK (source_revision <> '')
	)`},
	{kind: "table", name: "session_change_request_creation_evidence", ddl: `CREATE TABLE session_change_request_creation_evidence (
		evidence_id TEXT PRIMARY KEY,
		agent_type TEXT NOT NULL,
		session_id TEXT NOT NULL,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab')),
		display_origin TEXT NOT NULL,
		target_repository_slug TEXT NOT NULL,
		display_number TEXT NOT NULL,
		normalized_url TEXT NOT NULL,
		command_kind TEXT NOT NULL CHECK (command_kind IN ('github_cli_pr_create','gitlab_cli_mr_create')),
		tool_name TEXT NOT NULL,
		event_id TEXT NOT NULL,
		tool_call_id TEXT NOT NULL DEFAULT '',
		turn_index INTEGER NOT NULL,
		invocation_id TEXT NOT NULL DEFAULT '',
		recorded_at TEXT NOT NULL,
		source_revision TEXT NOT NULL,
		UNIQUE (agent_type, session_id, event_id, normalized_url),
		FOREIGN KEY (agent_type, session_id) REFERENCES session_change_request_creation_indexes(agent_type, session_id) ON DELETE CASCADE,
		CHECK (display_origin <> '' AND target_repository_slug <> '' AND display_number <> '' AND normalized_url <> '' AND tool_name <> '' AND event_id <> '' AND source_revision <> '')
	)`},
	{kind: "index", name: "idx_change_request_creation_url", ddl: `CREATE INDEX idx_change_request_creation_url ON session_change_request_creation_evidence(normalized_url, recorded_at DESC)`},
}

func migrateGitAssociationV37(conn *sql.DB) error {
	ctx := context.Background()
	complete, err := inspectV37Schema(ctx, conn)
	if err != nil {
		return err
	}
	versioned, err := schemaVersionExists(ctx, conn, 37)
	if err != nil {
		return fmt.Errorf("v37 inspect version: %w", err)
	}
	if complete && versioned {
		return nil
	}
	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v37 pin connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v37 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	for _, object := range v37SchemaObjects {
		exists, err := schemaObjectExists(ctx, c, object)
		if err != nil {
			return fmt.Errorf("v37 inspect %s: %w", object.name, err)
		}
		if !exists {
			if _, err := c.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("v37 create %s: %w", object.name, err)
			}
		}
	}
	if complete, err = inspectV37Schema(ctx, c); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("v37 physical schema remained incomplete after migration")
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (37)`); err != nil {
		return fmt.Errorf("v37 record version: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v37 commit: %w", err)
	}
	committed = true
	return nil
}

func inspectV37Schema(ctx context.Context, q schemaQueryer) (bool, error) {
	for _, object := range v37SchemaObjects {
		exists, err := schemaObjectExists(ctx, q, object)
		if err != nil {
			return false, fmt.Errorf("v37 inspect %s: %w", object.name, err)
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}
