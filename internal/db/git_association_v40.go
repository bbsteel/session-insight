package db

import (
	"context"
	"database/sql"
	"fmt"
)

var v40CreationEvidenceObject = v34SchemaObject{
	kind: "table",
	name: "session_change_request_creation_evidence",
	ddl: `CREATE TABLE session_change_request_creation_evidence (
		evidence_id TEXT PRIMARY KEY,
		agent_type TEXT NOT NULL,
		session_id TEXT NOT NULL,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','generic')),
		display_origin TEXT NOT NULL,
		target_repository_slug TEXT NOT NULL,
		display_number TEXT NOT NULL,
		normalized_url TEXT NOT NULL,
		command_kind TEXT NOT NULL CHECK (command_kind IN ('github_cli_pr_create','gitlab_cli_mr_create','change_request_url')),
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
	)`,
}

const v40CreationEvidenceTemporaryDDL = `CREATE TABLE session_change_request_creation_evidence_v40 (
		evidence_id TEXT PRIMARY KEY,
		agent_type TEXT NOT NULL,
		session_id TEXT NOT NULL,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','generic')),
		display_origin TEXT NOT NULL,
		target_repository_slug TEXT NOT NULL,
		display_number TEXT NOT NULL,
		normalized_url TEXT NOT NULL,
		command_kind TEXT NOT NULL CHECK (command_kind IN ('github_cli_pr_create','gitlab_cli_mr_create','change_request_url')),
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
	)`

// migrateGitAssociationV40 widens creation evidence so a recognized PR/MR URL
// can be stored without a GitHub/GitLab CLI command. SQLite cannot alter those
// CHECKs in place, so the table is rebuilt under one pinned transaction.
func migrateGitAssociationV40(conn *sql.DB) error {
	ctx := context.Background()
	complete, err := inspectV40Schema(ctx, conn)
	if err != nil {
		return err
	}
	versioned, err := schemaVersionExists(ctx, conn, 40)
	if err != nil {
		return fmt.Errorf("v40 inspect version: %w", err)
	}
	if complete && versioned {
		return nil
	}

	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v40 pin connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v40 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	complete, err = inspectV40Schema(ctx, c)
	if err != nil {
		return err
	}
	if !complete {
		var actual string
		if err := c.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, v40CreationEvidenceObject.name,
		).Scan(&actual); err != nil {
			return fmt.Errorf("v40 inspect prior creation evidence table: %w", err)
		}
		if compactDDL(actual) != compactDDL(v37CreationEvidenceDDL()) {
			return fmt.Errorf("v40 incompatible creation evidence table")
		}
		if _, err := c.ExecContext(ctx, v40CreationEvidenceTemporaryDDL); err != nil {
			return fmt.Errorf("v40 create replacement creation evidence table: %w", err)
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO session_change_request_creation_evidence_v40(
				evidence_id, agent_type, session_id, provider, display_origin,
				target_repository_slug, display_number, normalized_url, command_kind,
				tool_name, event_id, tool_call_id, turn_index, invocation_id,
				recorded_at, source_revision
			)
			SELECT evidence_id, agent_type, session_id, provider, display_origin,
			       target_repository_slug, display_number, normalized_url, command_kind,
			       tool_name, event_id, tool_call_id, turn_index, invocation_id,
			       recorded_at, source_revision
			FROM session_change_request_creation_evidence`); err != nil {
			return fmt.Errorf("v40 copy creation evidence rows: %w", err)
		}
		if _, err := c.ExecContext(ctx, `DROP TABLE session_change_request_creation_evidence`); err != nil {
			return fmt.Errorf("v40 drop prior creation evidence table: %w", err)
		}
		if _, err := c.ExecContext(ctx, `ALTER TABLE session_change_request_creation_evidence_v40 RENAME TO session_change_request_creation_evidence`); err != nil {
			return fmt.Errorf("v40 rename replacement creation evidence table: %w", err)
		}
	}
	indexExists, err := schemaObjectExists(ctx, c, v37CreationURLIndex())
	if err != nil {
		return fmt.Errorf("v40 inspect creation URL index: %w", err)
	}
	if !indexExists {
		if _, err := c.ExecContext(ctx, v37CreationURLIndex().ddl); err != nil {
			return fmt.Errorf("v40 create creation URL index: %w", err)
		}
	}
	if complete, err = inspectV40Schema(ctx, c); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("v40 physical schema remained incomplete after migration")
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (40)`); err != nil {
		return fmt.Errorf("v40 record version: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v40 commit: %w", err)
	}
	committed = true
	return nil
}

func inspectV40Schema(ctx context.Context, q schemaQueryer) (bool, error) {
	var actual string
	err := q.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, v40CreationEvidenceObject.name,
	).Scan(&actual)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("v40 inspect creation evidence table: %w", err)
	}
	if compactDDL(actual) == compactDDL(v40CreationEvidenceObject.ddl) {
		return schemaObjectExists(ctx, q, v37CreationURLIndex())
	}
	if compactDDL(actual) == compactDDL(v37CreationEvidenceDDL()) {
		return false, nil
	}
	return false, fmt.Errorf("v40 incompatible creation evidence table")
}

func v37CreationEvidenceDDL() string {
	for _, object := range v37SchemaObjects {
		if object.kind == "table" && object.name == "session_change_request_creation_evidence" {
			return object.ddl
		}
	}
	return ""
}

func v37CreationURLIndex() v34SchemaObject {
	for _, object := range v37SchemaObjects {
		if object.kind == "index" && object.name == "idx_change_request_creation_url" {
			return object
		}
	}
	return v34SchemaObject{}
}
