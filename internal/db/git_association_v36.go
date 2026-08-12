package db

import (
	"context"
	"database/sql"
	"fmt"
)

var v36SnapshotFilesObject = v34SchemaObject{
	kind: "table",
	name: "session_git_snapshot_files",
	ddl: `CREATE TABLE session_git_snapshot_files (
		snapshot_id TEXT NOT NULL,
		path_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		raw_path BLOB NOT NULL,
		display_path TEXT NOT NULL,
		path_encoding TEXT NOT NULL CHECK (path_encoding IN ('utf8','bytes_b64')),
		layer TEXT NOT NULL CHECK (layer IN ('tree','index','worktree','hosted_change')),
		file_type TEXT NOT NULL DEFAULT 'file' CHECK (file_type IN ('file','symlink','submodule','binary','special','missing')),
		mode TEXT NOT NULL DEFAULT '',
		git_oid TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL DEFAULT '',
		content_bytes INTEGER NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
		content_state TEXT NOT NULL CHECK (content_state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (snapshot_id, path_key),
		UNIQUE (snapshot_id, ordinal),
		FOREIGN KEY (snapshot_id) REFERENCES session_git_snapshots(snapshot_id) ON DELETE CASCADE,
		CHECK (length(path_key) = 64 AND path_key NOT GLOB '*[^0-9a-f]*'),
		CHECK (git_oid = '' OR ((length(git_oid) = 40 OR length(git_oid) = 64) AND git_oid NOT GLOB '*[^0-9a-f]*')),
		CHECK (content_hash = '' OR (length(content_hash) = 64 AND content_hash NOT GLOB '*[^0-9a-f]*'))
	)`,
}

var v36SnapshotFilesHashIndex = v34SchemaObject{
	kind: "index",
	name: "idx_session_git_snapshot_files_hash",
	ddl:  `CREATE INDEX idx_session_git_snapshot_files_hash ON session_git_snapshot_files(content_hash)`,
}

const v36SnapshotFilesTemporaryDDL = `CREATE TABLE session_git_snapshot_files_v36 (
		snapshot_id TEXT NOT NULL,
		path_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		raw_path BLOB NOT NULL,
		display_path TEXT NOT NULL,
		path_encoding TEXT NOT NULL CHECK (path_encoding IN ('utf8','bytes_b64')),
		layer TEXT NOT NULL CHECK (layer IN ('tree','index','worktree','hosted_change')),
		file_type TEXT NOT NULL DEFAULT 'file' CHECK (file_type IN ('file','symlink','submodule','binary','special','missing')),
		mode TEXT NOT NULL DEFAULT '',
		git_oid TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL DEFAULT '',
		content_bytes INTEGER NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
		content_state TEXT NOT NULL CHECK (content_state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (snapshot_id, path_key),
		UNIQUE (snapshot_id, ordinal),
		FOREIGN KEY (snapshot_id) REFERENCES session_git_snapshots(snapshot_id) ON DELETE CASCADE,
		CHECK (length(path_key) = 64 AND path_key NOT GLOB '*[^0-9a-f]*'),
		CHECK (git_oid = '' OR ((length(git_oid) = 40 OR length(git_oid) = 64) AND git_oid NOT GLOB '*[^0-9a-f]*')),
		CHECK (content_hash = '' OR (length(content_hash) = 64 AND content_hash NOT GLOB '*[^0-9a-f]*'))
	)`

// migrateGitAssociationV36 widens only the local snapshot file-type check.
// SQLite cannot alter a CHECK constraint in place, so the table is rebuilt
// under one pinned BEGIN IMMEDIATE transaction and its complete payload is
// copied before the old table is dropped.
func migrateGitAssociationV36(conn *sql.DB) error {
	ctx := context.Background()
	complete, err := inspectV36Schema(ctx, conn)
	if err != nil {
		return err
	}
	versioned, err := schemaVersionExists(ctx, conn, 36)
	if err != nil {
		return fmt.Errorf("v36 inspect version: %w", err)
	}
	if complete && versioned {
		return nil
	}

	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v36 pin connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v36 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	complete, err = inspectV36Schema(ctx, c)
	if err != nil {
		return err
	}
	if !complete {
		old, ok := v34ObjectByName("table", v36SnapshotFilesObject.name)
		if !ok {
			return fmt.Errorf("v36 prior snapshot file table contract is unavailable")
		}
		var actual string
		if err := c.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, old.name,
		).Scan(&actual); err != nil {
			return fmt.Errorf("v36 inspect prior snapshot file table: %w", err)
		}
		if compactDDL(actual) != compactDDL(old.ddl) {
			return fmt.Errorf("v36 incompatible snapshot file table")
		}
		if _, err := c.ExecContext(ctx, v36SnapshotFilesTemporaryDDL); err != nil {
			return fmt.Errorf("v36 create replacement snapshot file table: %w", err)
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO session_git_snapshot_files_v36(
				snapshot_id, path_key, ordinal, raw_path, display_path,
				path_encoding, layer, file_type, mode, git_oid, content_hash,
				content_bytes, content_state, reason_code
			)
			SELECT snapshot_id, path_key, ordinal, raw_path, display_path,
			       path_encoding, layer, file_type, mode, git_oid, content_hash,
			       content_bytes, content_state, reason_code
			FROM session_git_snapshot_files`); err != nil {
			return fmt.Errorf("v36 copy snapshot file rows: %w", err)
		}
		if _, err := c.ExecContext(ctx, `DROP TABLE session_git_snapshot_files`); err != nil {
			return fmt.Errorf("v36 drop prior snapshot file table: %w", err)
		}
		if _, err := c.ExecContext(ctx, `ALTER TABLE session_git_snapshot_files_v36 RENAME TO session_git_snapshot_files`); err != nil {
			return fmt.Errorf("v36 rename replacement snapshot file table: %w", err)
		}
	}
	indexExists, err := schemaObjectExists(ctx, c, v36SnapshotFilesHashIndex)
	if err != nil {
		return fmt.Errorf("v36 inspect snapshot file hash index: %w", err)
	}
	if !indexExists {
		if _, err := c.ExecContext(ctx, v36SnapshotFilesHashIndex.ddl); err != nil {
			return fmt.Errorf("v36 create snapshot file hash index: %w", err)
		}
	}
	if complete, err = inspectV36Schema(ctx, c); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("v36 physical schema remained incomplete after migration")
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (36)`); err != nil {
		return fmt.Errorf("v36 record version: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v36 commit: %w", err)
	}
	committed = true
	return nil
}

func inspectV36Schema(ctx context.Context, q schemaQueryer) (bool, error) {
	var actual string
	err := q.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, v36SnapshotFilesObject.name,
	).Scan(&actual)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("v36 inspect snapshot file table: %w", err)
	}
	if compactDDL(actual) == compactDDL(v36SnapshotFilesObject.ddl) {
		return schemaObjectExists(ctx, q, v36SnapshotFilesHashIndex)
	}
	old, ok := v34ObjectByName("table", v36SnapshotFilesObject.name)
	if ok && compactDDL(actual) == compactDDL(old.ddl) {
		return false, nil
	}
	return false, fmt.Errorf("v36 incompatible snapshot file table")
}
