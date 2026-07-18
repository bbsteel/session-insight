package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

const currentSchemaVersion = 25

type DB struct {
	conn *sql.DB
}

// dbOpenLocks serializes Open calls for the same database path. Multiple
// goroutines opening the same SQLite file during migrations (DDL) can race and
// produce "database is locked"; the mutex lets one caller finish migrations and
// the others proceed against an already-initialized schema.
var dbOpenLocks sync.Map // map[string]*sync.Mutex

func openMutex(dbPath string) *sync.Mutex {
	mu, _ := dbOpenLocks.LoadOrStore(dbPath, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "index.db")

	mu := openMutex(dbPath)
	mu.Lock()
	defer mu.Unlock()

	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}

	log.Printf("SQLite database opened at %s", dbPath)
	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Conn() *sql.DB {
	return db.conn
}

// RebuildFTS 重建 FTS5 内容索引，用于 tokenizer/schema 变更后强制同步。
func (db *DB) RebuildFTS() error {
	_, err := db.conn.Exec(`INSERT INTO turn_texts_fts(turn_texts_fts) VALUES ('rebuild')`)
	return err
}

func migrate(conn *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		agent_type TEXT NOT NULL DEFAULT 'copilot',
		cwd TEXT NOT NULL DEFAULT '',
		repository TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			model_provider TEXT NOT NULL DEFAULT '',
			turn_count INTEGER NOT NULL DEFAULT 0,
		message_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_type);
	CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC);

	-- Schema version tracking（简单应用记录，非完整迁移框架）
	-- 当前行为：记录最近一次 schema 版本；不判断、不拒绝、不事务化回滚。
	CREATE TABLE IF NOT EXISTS schema_migrations (
	    version    INTEGER PRIMARY KEY,
	    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	-- Turn 内容表（含 role='meta' 用于会话名称搜索）
	CREATE TABLE IF NOT EXISTS turn_texts (
	    id         INTEGER PRIMARY KEY AUTOINCREMENT,
	    agent_type TEXT    NOT NULL,
	    session_id TEXT    NOT NULL,
	    turn_index INTEGER NOT NULL,
	    role       TEXT    NOT NULL,   -- 'meta' | 'user' | 'assistant' | 'skill' | 'tool' | 'error'
	    content    TEXT    NOT NULL,
	    UNIQUE(agent_type, session_id, turn_index, role)
	);
	CREATE INDEX IF NOT EXISTS idx_turn_texts_agent_session
	    ON turn_texts(agent_type, session_id);

	-- FTS5 虚拟表（trigram tokenizer，与 LIKE 子串语义一致，中文友好）
	CREATE VIRTUAL TABLE IF NOT EXISTS turn_texts_fts
	    USING fts5(
	        content,
	        content="turn_texts",
	        content_rowid="id",
	        tokenize="trigram"
	    );

	-- 同步触发器
	CREATE TRIGGER IF NOT EXISTS turn_texts_ai
	    AFTER INSERT ON turn_texts BEGIN
	        INSERT INTO turn_texts_fts(rowid, content)
	            VALUES (new.id, new.content);
	    END;

	CREATE TRIGGER IF NOT EXISTS turn_texts_au
	    AFTER UPDATE ON turn_texts BEGIN
	        INSERT INTO turn_texts_fts(turn_texts_fts, rowid, content)
	            VALUES ('delete', old.id, old.content);
	        INSERT INTO turn_texts_fts(rowid, content)
	            VALUES (new.id, new.content);
	    END;

	CREATE TRIGGER IF NOT EXISTS turn_texts_ad
	    AFTER DELETE ON turn_texts BEGIN
	        INSERT INTO turn_texts_fts(turn_texts_fts, rowid, content)
	            VALUES ('delete', old.id, old.content);
	    END;

	-- Watermark：记录已索引会话的 revision（UpdatedAt.UnixNano）
	CREATE TABLE IF NOT EXISTS index_watermarks (
	    agent_type  TEXT    NOT NULL,
	    session_id  TEXT    NOT NULL,
	    revision    INTEGER NOT NULL DEFAULT 0,
	    indexed_at  TEXT    NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id)
	);

	-- MiniMap 位点缓存 header（每个 agent_type+session+revision+cols 一条）
	CREATE TABLE IF NOT EXISTS session_position_caches (
	    agent_type   TEXT    NOT NULL,
	    session_id   TEXT    NOT NULL,
	    revision     INTEGER NOT NULL,
	    cols         INTEGER NOT NULL,
	    total_lines  INTEGER NOT NULL,
	    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id, revision, cols)
	);

	-- MiniMap 关键位点（通过 FK 级联依赖 header）
	CREATE TABLE IF NOT EXISTS session_positions (
	    agent_type   TEXT    NOT NULL,
	    session_id   TEXT    NOT NULL,
	    revision     INTEGER NOT NULL,
	    cols         INTEGER NOT NULL,
	    position_key TEXT    NOT NULL,
	    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit', 'fold', 'trunc', 'tool')),
	    turn_index   INTEGER NOT NULL,
	    line_start   INTEGER NOT NULL,
	    line_end     INTEGER,
	    label        TEXT    NOT NULL DEFAULT '',
	    severity     TEXT    NOT NULL DEFAULT '',
	    payload_json TEXT    NOT NULL DEFAULT '{}',
	    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
	    FOREIGN KEY (agent_type, session_id, revision, cols)
	        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
	        ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
	    ON session_positions(agent_type, session_id, revision, cols, line_start);

	CREATE TABLE IF NOT EXISTS bookmarked_sessions (
	    agent_type TEXT NOT NULL,
		    session_id TEXT NOT NULL,
		    note TEXT NOT NULL DEFAULT '',
		    model_provider TEXT NOT NULL DEFAULT '',
		    created_at TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id)
	);

	CREATE TABLE IF NOT EXISTS app_settings (
	    key TEXT PRIMARY KEY,
	    value TEXT NOT NULL
	);

	-- LLM provider 配置（api 型 = OpenAI 兼容 HTTP；acp 型 = 本地 CLI 走 ACP 协议）
	-- model_id 全局唯一索引在 v23 迁移里创建（需先对旧数据去重）
	CREATE TABLE IF NOT EXISTS llm_providers (
	    id          INTEGER PRIMARY KEY AUTOINCREMENT,
	    name        TEXT NOT NULL,
	    kind        TEXT NOT NULL CHECK (kind IN ('api', 'acp')),
	    base_url    TEXT NOT NULL DEFAULT '',
	    api_key     TEXT NOT NULL DEFAULT '',
	    headers     TEXT NOT NULL DEFAULT '',
	    agent       TEXT NOT NULL DEFAULT '',
	    model_id    TEXT NOT NULL,
	    model_label TEXT NOT NULL DEFAULT '',
	    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
	);

	-- AI 生成历史（summary / title / handoff / insight 共用一张表）
	CREATE TABLE IF NOT EXISTS ai_generations (
	    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	    kind               TEXT NOT NULL CHECK (kind IN ('summary', 'title', 'handoff', 'insight')),
	    agent_type         TEXT NOT NULL,
	    session_id         TEXT NOT NULL,
	    provider_name      TEXT NOT NULL DEFAULT '',
	    model_id           TEXT NOT NULL DEFAULT '',
	    content            TEXT NOT NULL,
	    metadata           TEXT NOT NULL DEFAULT '',
	    source_revision    INTEGER NOT NULL DEFAULT 0,
	    prompt_version     TEXT NOT NULL DEFAULT '',
	    source_fingerprint TEXT NOT NULL DEFAULT '',
	    created_at         TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_ai_generations_session
	    ON ai_generations(agent_type, session_id, kind);

	-- LLM 生成的标题覆盖：只影响本应用显示，不碰 agent 原始日志文件
	CREATE TABLE IF NOT EXISTS session_title_overrides (
	    agent_type TEXT NOT NULL,
	    session_id TEXT NOT NULL,
	    title      TEXT NOT NULL,
	    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (agent_type, session_id)
	);
	`
	_, err := conn.Exec(query)
	if err != nil {
		return err
	}

	// Version 4: 'edit' kind added to position constraint.
	// Drop position cache tables so they're recreated with the new schema
	// on next positions request (they're pure caches, safe to discard).
	var maxVersion int
	conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion)
	if maxVersion < 4 {
		conn.Exec(`DROP TABLE IF EXISTS session_positions`)
		conn.Exec(`DROP TABLE IF EXISTS session_position_caches`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_position_caches (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    total_lines  INTEGER NOT NULL,
		    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, session_id, revision, cols)
		)`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_positions (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    position_key TEXT    NOT NULL,
		    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit')),
		    turn_index   INTEGER NOT NULL,
		    line_start   INTEGER NOT NULL,
		    line_end     INTEGER,
		    label        TEXT    NOT NULL DEFAULT '',
		    severity     TEXT    NOT NULL DEFAULT '',
		    payload_json TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
		    FOREIGN KEY (agent_type, session_id, revision, cols)
		        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
		        ON DELETE CASCADE
		)`)
		conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
		    ON session_positions(agent_type, session_id, revision, cols, line_start)`)
	}

	// Version 6: 'fold' / 'trunc' kinds added to the position constraint
	// (collapsible tool groups + truncated-output expansion). Same pattern:
	// the tables are pure caches, drop and let the next request rebuild them
	// with the widened CHECK.
	if maxVersion < 6 {
		conn.Exec(`DROP TABLE IF EXISTS session_positions`)
		conn.Exec(`DROP TABLE IF EXISTS session_position_caches`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_position_caches (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    total_lines  INTEGER NOT NULL,
		    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, session_id, revision, cols)
		)`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_positions (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    position_key TEXT    NOT NULL,
		    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit', 'fold', 'trunc')),
		    turn_index   INTEGER NOT NULL,
		    line_start   INTEGER NOT NULL,
		    line_end     INTEGER,
		    label        TEXT    NOT NULL DEFAULT '',
		    severity     TEXT    NOT NULL DEFAULT '',
		    payload_json TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
		    FOREIGN KEY (agent_type, session_id, revision, cols)
		        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
		        ON DELETE CASCADE
		)`)
		conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
		    ON session_positions(agent_type, session_id, revision, cols, line_start)`)
	}

	// Version 9: 'tool' kind added to the position constraint (tool-call
	// panel entries). Same pattern: pure caches, drop and rebuild on the
	// next positions request with the widened CHECK. Runs after the v6
	// block so any older DB (including fresh ones the v4/v6 blocks just
	// rebuilt with the narrower CHECK) ends up with the current schema.
	if maxVersion < 9 {
		conn.Exec(`DROP TABLE IF EXISTS session_positions`)
		conn.Exec(`DROP TABLE IF EXISTS session_position_caches`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_position_caches (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    total_lines  INTEGER NOT NULL,
		    generated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, session_id, revision, cols)
		)`)
		conn.Exec(`
		CREATE TABLE IF NOT EXISTS session_positions (
		    agent_type   TEXT    NOT NULL,
		    session_id   TEXT    NOT NULL,
		    revision     INTEGER NOT NULL,
		    cols         INTEGER NOT NULL,
		    position_key TEXT    NOT NULL,
		    kind         TEXT    NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error', 'edit', 'fold', 'trunc', 'tool')),
		    turn_index   INTEGER NOT NULL,
		    line_start   INTEGER NOT NULL,
		    line_end     INTEGER,
		    label        TEXT    NOT NULL DEFAULT '',
		    severity     TEXT    NOT NULL DEFAULT '',
		    payload_json TEXT    NOT NULL DEFAULT '{}',
		    PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
		    FOREIGN KEY (agent_type, session_id, revision, cols)
		        REFERENCES session_position_caches(agent_type, session_id, revision, cols)
		        ON DELETE CASCADE
		)`)
		conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_positions_lookup
		    ON session_positions(agent_type, session_id, revision, cols, line_start)`)
	}

	// Version 7: expand bookmarked_sessions with session metadata so
	// listing bookmarks is a pure SQL query (no per-session disk reads).
	if maxVersion < 7 {
		for _, col := range []string{
			`name TEXT NOT NULL DEFAULT ''`,
			`model_name TEXT NOT NULL DEFAULT ''`,
			`model_provider TEXT NOT NULL DEFAULT ''`,
			`repository TEXT NOT NULL DEFAULT ''`,
			`project TEXT NOT NULL DEFAULT ''`,
			`cwd TEXT NOT NULL DEFAULT ''`,
			`preview_text TEXT NOT NULL DEFAULT ''`,
			`turn_count INTEGER NOT NULL DEFAULT 0`,
			`message_count INTEGER NOT NULL DEFAULT 0`,
			`branch TEXT NOT NULL DEFAULT ''`,
			`session_updated_at TEXT NOT NULL DEFAULT ''`,
		} {
			conn.Exec(`ALTER TABLE bookmarked_sessions ADD COLUMN ` + col)
		}
	}

	// Version 14: structured metadata for AI generations (handoff difficulty
	// assessment + recommended executor list, JSON text). The CREATE TABLE
	// above already includes the column for fresh DBs. Gate on the actual
	// column, not schema_migrations: the DB is shared with other running
	// instances (main + worktree validation), and an ALTER can lose the lock
	// race on one startup while the version row still gets written — checking
	// the real schema makes the migration self-healing on the next start.
	hasMetadata := false
	if rows, err := conn.Query(`PRAGMA table_info(ai_generations)`); err == nil {
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == "metadata" {
				hasMetadata = true
			}
		}
		rows.Close()
	}
	if !hasMetadata {
		if _, err := conn.Exec(`ALTER TABLE ai_generations ADD COLUMN metadata TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add ai_generations.metadata column: %w", err)
		}
	}

	// Version 18: bookmark notes record why a session was saved.
	hasBookmarkNote := false
	if rows, err := conn.Query(`PRAGMA table_info(bookmarked_sessions)`); err == nil {
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == "note" {
				hasBookmarkNote = true
			}
		}
		rows.Close()
	}
	if !hasBookmarkNote {
		if _, err := conn.Exec(`ALTER TABLE bookmarked_sessions ADD COLUMN note TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("add bookmarked_sessions.note column: %w", err)
		}
	}

	// Version 8: rebuild sessions table with correct composite key and project
	// column. The old table was never populated (no code path wrote to it), so
	// DROP + CREATE is safe. Also clear index watermarks so the indexer
	// repopulates the sessions table on next startup.
	if maxVersion < 8 {
		conn.Exec(`DROP TABLE IF EXISTS sessions`)
		conn.Exec(`
		CREATE TABLE sessions (
		    agent_type TEXT NOT NULL DEFAULT 'copilot',
		    id TEXT NOT NULL,
		    cwd TEXT NOT NULL DEFAULT '',
		    repository TEXT NOT NULL DEFAULT '',
		    branch TEXT NOT NULL DEFAULT '',
			    project TEXT NOT NULL DEFAULT '',
			    name TEXT NOT NULL DEFAULT '',
			    model_name TEXT NOT NULL DEFAULT '',
			    model_provider TEXT NOT NULL DEFAULT '',
			    turn_count INTEGER NOT NULL DEFAULT 0,
		    message_count INTEGER NOT NULL DEFAULT 0,
		    created_at TEXT NOT NULL DEFAULT (datetime('now')),
		    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		    PRIMARY KEY (agent_type, id)
		)`)
		conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_type)`)
		conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC)`)
		// Clear watermarks so the indexer backfills session metadata.
		conn.Exec(`DELETE FROM index_watermarks`)
	}

	// Version 10: sessions gains resume_id so /api/sessions can be served
	// entirely from SQLite. Clear codex watermarks to backfill the column
	// (codex is the only reader that populates ResumeID today).
	if maxVersion < 10 {
		conn.Exec(`ALTER TABLE sessions ADD COLUMN resume_id TEXT NOT NULL DEFAULT ''`)
		conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC)`)
		conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`)
	}

	// Version 15: distinguish the active resumable turn count from turns that
	// remain in an append-only transcript after an explicit rollback.
	if maxVersion < 15 {
		for _, col := range []string{
			`historical_turn_count INTEGER NOT NULL DEFAULT 0`,
			`rolled_back_turn_count INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := conn.Exec(`ALTER TABLE sessions ADD COLUMN ` + col); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("add sessions rollback count: %w", err)
			}
		}
		conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`)
	}

	// Version 16: preserve Codex collaborative-agent lineage. Child rollout
	// files remain indexed/searchable but no longer masquerade as duplicate
	// root sessions in the sidebar.
	if maxVersion < 16 {
		for _, col := range []string{
			`parent_session_id TEXT NOT NULL DEFAULT ''`,
			`agent_path TEXT NOT NULL DEFAULT ''`,
			`is_subagent INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := conn.Exec(`ALTER TABLE sessions ADD COLUMN ` + col); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("add sessions lineage column: %w", err)
			}
		}
		conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`)
	}

	// Version 17: Deep Insight generations. Add the 'insight' kind and three
	// freshness columns (source_revision, prompt_version, source_fingerprint).
	// SQLite cannot ALTER a CHECK constraint, so the kind widening needs a
	// table rebuild. This is gated on the real schema, not the version row: the
	// index.db is shared across running instances, so a concurrent Open must be
	// able to detect a rebuild another process already finished and skip it.
	if err := migrateAIGenerationsV17(conn); err != nil {
		return err
	}

	if err := migrateModelProviderV19(conn); err != nil {
		return err
	}

	// Version 20: model_provider is now the recorded runtime/provider, not a
	// value inferred from the model developer/name. Re-scan existing sessions so
	// providers exposed by source logs (for example OpenCode Go serving glm-5.1)
	// are backfilled into the sidebar filter data.
	if maxVersion < 20 {
		if _, err := conn.Exec(`DELETE FROM index_watermarks`); err != nil {
			return fmt.Errorf("v20 clear watermarks: %w", err)
		}
	}

	// Version 21: Codex stores the concrete selected model in turn_context.model.
	// Older indexing only derived broad family names such as "GPT-5" from
	// session_meta.base_instructions, so re-scan Codex sessions to backfill the
	// specific model IDs.
	if maxVersion < 21 {
		if _, err := conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`); err != nil {
			return fmt.Errorf("v21 clear codex watermarks: %w", err)
		}
	}

	// Version 22: the indexer now persists richer metadata parsed from
	// GetSession, not only shallow ListSessions metadata. Re-scan Codex once more
	// so turn_context.model backfills even for sessions whose first turn_context
	// appears beyond the list scan window.
	if maxVersion < 22 {
		if _, err := conn.Exec(`DELETE FROM index_watermarks WHERE agent_type = 'codex'`); err != nil {
			return fmt.Errorf("v22 clear codex watermarks: %w", err)
		}
	}

	// Version 23: each configured model_id may appear only once across providers
	// so the picker and default selection stay unambiguous. Pre-existing
	// duplicates keep the lowest id and rewrite later rows to model_id~<id>
	// so the unique index can be applied without dropping user configs.
	if maxVersion < 23 {
		if err := dedupeLLMProviderModelIDs(conn); err != nil {
			return fmt.Errorf("v23 dedupe llm model_id: %w", err)
		}
		if _, err := conn.Exec(
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_providers_model_id ON llm_providers(model_id)`,
		); err != nil {
			return fmt.Errorf("v23 unique llm model_id index: %w", err)
		}
	}

	// Version 24: custom HTTP headers for OpenAI-compatible API model sources
	// (gateways, enterprise proxies, OpenRouter Referer/X-Title, etc.).
	// Gate on the real column so concurrent worktree startups remain safe.
	hasHeaders, err := tableHasColumn(context.Background(), conn, "llm_providers", "headers")
	if err != nil {
		// Fresh installs create the table with the column; missing table is fine.
		if !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("v24 inspect llm_providers.headers: %w", err)
		}
	} else if !hasHeaders {
		if _, err := conn.Exec(`ALTER TABLE llm_providers ADD COLUMN headers TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("v24 add llm_providers.headers: %w", err)
		}
	}

	// Version 25: expand FTS content (assistant / skill / tool summary / error).
	// Clear watermarks so every session is re-indexed under the new shape.
	// Leave turn_texts in place; UpsertTurns replaces rows per session as the
	// indexer catches up (avoid a long exclusive wipe on startup).
	if maxVersion < 25 {
		if _, err := conn.Exec(`DELETE FROM index_watermarks`); err != nil {
			return fmt.Errorf("v25 clear index_watermarks: %w", err)
		}
	}

	_, err = conn.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`,
		currentSchemaVersion,
	)
	return err
}

// hasInsightColumn reports whether the ai_generations rows from a
// PRAGMA table_info result include source_fingerprint. Gating on a real column
// (not the version row) makes the migration self-healing and concurrency-safe:
// a version number can be written even when an ALTER lost a lock race, but the
// physical column cannot lie.
func hasInsightColumn(rows *sql.Rows) bool {
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == "source_fingerprint" {
			return true
		}
	}
	return false
}

func tableHasColumn(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// dedupeLLMProviderModelIDs rewrites every non-first row that shares a model_id
// so a UNIQUE index can be created. The lowest id for each model_id is kept.
func dedupeLLMProviderModelIDs(conn *sql.DB) error {
	rows, err := conn.Query(
		`SELECT id, model_id FROM llm_providers ORDER BY id`)
	if err != nil {
		// Table may not exist on very old DBs that never created providers.
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	defer rows.Close()

	type row struct {
		id      int64
		modelID string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.modelID); err != nil {
			return err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	seen := map[string]int64{} // model_id -> first id
	for _, r := range all {
		if first, ok := seen[r.modelID]; ok {
			// Disambiguate: keep original token visible, append ~id.
			newID := fmt.Sprintf("%s~%d", r.modelID, r.id)
			// Extremely unlikely collision with another rewritten id; loop-safe.
			for {
				if _, clash := seen[newID]; !clash {
					break
				}
				newID = fmt.Sprintf("%s~%d", newID, r.id)
			}
			if _, err := conn.Exec(
				`UPDATE llm_providers SET model_id = ? WHERE id = ?`, newID, r.id,
			); err != nil {
				return err
			}
			seen[newID] = r.id
			_ = first
		} else {
			seen[r.modelID] = r.id
		}
	}
	return nil
}

func migrateModelProviderV19(conn *sql.DB) (retErr error) {
	ctx := context.Background()
	sessionsOK, _ := tableHasColumn(ctx, conn, "sessions", "model_provider")
	bookmarksOK, _ := tableHasColumn(ctx, conn, "bookmarked_sessions", "model_provider")
	if sessionsOK && bookmarksOK {
		return nil
	}

	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v19 pin connection: %w", err)
	}
	defer c.Close()

	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v19 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	for _, table := range []string{"sessions", "bookmarked_sessions"} {
		hasColumn, err := tableHasColumn(ctx, c, table, "model_provider")
		if err != nil {
			return fmt.Errorf("v19 check %s.model_provider: %w", table, err)
		}
		if hasColumn {
			continue
		}
		if _, err := c.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN model_provider TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("v19 add %s.model_provider: %w", table, err)
		}
		if table == "sessions" {
			if _, err := c.ExecContext(ctx, `DELETE FROM index_watermarks`); err != nil {
				return fmt.Errorf("v19 clear watermarks: %w", err)
			}
		}
	}

	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v19 commit: %w", err)
	}
	committed = true
	return nil
}

// migrateAIGenerationsV17 rebuilds ai_generations to add the 'insight' kind and
// the freshness columns, preserving every existing row (id, content, metadata,
// provider/model, timestamps). The rebuild runs inside one BEGIN IMMEDIATE
// transaction on a single pinned connection: the immediate write lock (bounded
// by the DSN busy_timeout) serializes concurrent instances instead of
// dead-locking two deferred readers, a mid-way failure rolls the whole thing
// back to the working old table, and re-checking the real schema under the lock
// lets a process that lost the race no-op instead of rebuilding twice.
func migrateAIGenerationsV17(conn *sql.DB) (retErr error) {
	// Fast path: fresh DB or already migrated — no lock needed.
	if rows, err := conn.Query(`PRAGMA table_info(ai_generations)`); err == nil {
		if hasInsightColumn(rows) {
			return nil
		}
	}

	ctx := context.Background()
	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v17 pin connection: %w", err)
	}
	defer c.Close()

	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v17 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// Re-check under the write lock: another instance may have rebuilt the
	// table between the fast-path check and acquiring the lock.
	rows, err := c.QueryContext(ctx, `PRAGMA table_info(ai_generations)`)
	if err != nil {
		return fmt.Errorf("v17 recheck schema: %w", err)
	}
	if hasInsightColumn(rows) {
		if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
			return fmt.Errorf("v17 commit noop: %w", err)
		}
		committed = true
		return nil
	}

	var before int
	if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_generations`).Scan(&before); err != nil {
		return fmt.Errorf("v17 count old rows: %w", err)
	}

	stmts := []string{
		`CREATE TABLE ai_generations_new (
		    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
		    kind               TEXT NOT NULL CHECK (kind IN ('summary', 'title', 'handoff', 'insight')),
		    agent_type         TEXT NOT NULL,
		    session_id         TEXT NOT NULL,
		    provider_name      TEXT NOT NULL DEFAULT '',
		    model_id           TEXT NOT NULL DEFAULT '',
		    content            TEXT NOT NULL,
		    metadata           TEXT NOT NULL DEFAULT '',
		    source_revision    INTEGER NOT NULL DEFAULT 0,
		    prompt_version     TEXT NOT NULL DEFAULT '',
		    source_fingerprint TEXT NOT NULL DEFAULT '',
		    created_at         TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		// Explicit column list preserves ids and existing default semantics; the
		// three new columns take their table defaults for old rows.
		`INSERT INTO ai_generations_new
		    (id, kind, agent_type, session_id, provider_name, model_id, content, metadata, created_at)
		 SELECT id, kind, agent_type, session_id, provider_name, model_id, content, metadata, created_at
		 FROM ai_generations`,
		`DROP TABLE ai_generations`,
		`ALTER TABLE ai_generations_new RENAME TO ai_generations`,
		`CREATE INDEX IF NOT EXISTS idx_ai_generations_session
		    ON ai_generations(agent_type, session_id, kind)`,
	}
	for _, s := range stmts {
		if _, err := c.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("v17 rebuild step failed (rolled back): %w", err)
		}
	}

	var after int
	if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_generations`).Scan(&after); err != nil {
		return fmt.Errorf("v17 count new rows: %w", err)
	}
	if after != before {
		return fmt.Errorf("v17 row count mismatch: before=%d after=%d (rolled back)", before, after)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v17 commit: %w", err)
	}
	committed = true
	return nil
}
