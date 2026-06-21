# 全文搜索本地索引实现方案（终版）

> 经 Codex 独立评审后修订。可直接交 DeepSeek 执行。

## 决策基线

| 决策点 | 结论 |
|---|---|
| 搜索引擎 | SQLite FTS5，external content 模式 |
| Tokenizer | `trigram`（子串语义，中文友好，不分词） |
| Watermark revision 来源 | `session.UpdatedAt.UnixNano()`（所有 Reader 的 `ListSessions()` 均已设置） |
| Session 名称/repo 搜索 | 以 `role='meta'` 行插入 `turn_texts`，统一进 FTS，不单独维护 sessions 写入路径 |
| Snippet 生成 | Go 端截取纯文本，不用 `snippet()`（避免 `<b>` 字面量出现在前端） |
| 搜索 SQL 结构 | 两层 CTE + `ROW_NUMBER()`，每个 (agent_type, session_id) 取最佳一条 |
| Query 注入防护 | 用双引号包裹整个 query，转义内部 `"`；< 2 rune 时直接返回空 |
| 首次索引策略 | 启动时同步完成首次 `indexOnce()`（10s 超时），之后 goroutine 每 3 分钟增量 |
| Indexer package | `internal/indexer/`（独立于 DB 层） |
| 事务边界 | UpsertTurns + SetWatermark 合并为一个事务 |
| 组合身份键 | `(agent_type, session_id)` |
| 孤儿清理 | 每轮 list 成功后删除该 agent 不再存在的 session |
| Schema 版本 | `schema_migrations` 表 + 版本号常量 |
| FTS5 build tag | 全部构建/测试入口加 `-tags sqlite_fts5` |
| DB 连接 | 增加 `_busy_timeout=5000` |

---

## 涉及文件清单

| 文件 | 类型 | 说明 |
|---|---|---|
| `internal/db/db.go` | 修改 | migrate 追加新表、触发器；连接串加 busy_timeout |
| `internal/db/search.go` | 新建 | DB 层搜索方法 + snippet 工具 |
| `internal/db/index_store.go` | 新建 | UpsertTurns / Watermark / DeleteOrphansByAgent |
| `internal/indexer/indexer.go` | 新建 | 后台索引器（独立 package） |
| `internal/server/handlers.go` | 修改 | handleSearch 改为走 DB |
| `main.go` | 修改 | 首次同步索引 + 后台 goroutine |
| `start.sh` | 修改 | `go build` 加 `-tags sqlite_fts5` |
| `scripts/start.sh` | 修改 | 同上 |

---

## 一、`internal/db/db.go`

### 1.1 连接串

```go
// 原
conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")

// 改为
conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
```

### 1.2 Schema 版本常量

在文件顶部加：

```go
const currentSchemaVersion = 2
```

### 1.3 migrate() 末尾追加

在现有 `CREATE TABLE sessions` 之后的 `query` 字符串末尾追加（保持一个 `Exec` 调用即可）：

```sql
-- Schema version tracking
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
    role       TEXT    NOT NULL,   -- 'user' | 'assistant' | 'meta'
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
```

然后在 `migrate()` 末尾写入版本号：

```go
_, err = conn.Exec(
    `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`,
    currentSchemaVersion,
)
return err
```

### 1.4 新增 RebuildFTS 方法（供手动修复）

```go
// RebuildFTS 重建 FTS5 内容索引，用于 tokenizer/schema 变更后强制同步。
func (db *DB) RebuildFTS() error {
    _, err := db.conn.Exec(`INSERT INTO turn_texts_fts(turn_texts_fts) VALUES ('rebuild')`)
    return err
}
```

---

## 二、`internal/db/index_store.go`（新建）

```go
package db

import (
    "database/sql"
    "fmt"
    "strings"
    "unicode/utf8"
)

// TurnText 是一条待索引的内容行。
type TurnText struct {
    TurnIndex int    // -1 表示 meta 行
    Role      string // 'user' | 'assistant' | 'meta'
    Content   string
}

// UpsertTurns 在一个事务内完成：
//   1. 删除旧 turn_texts（触发 FTS5 delete 触发器）
//   2. 批量插入新 turn_texts（触发 FTS5 insert 触发器）
//   3. 更新 index_watermarks
//
// revision 传入 session.UpdatedAt.UnixNano()。
func (db *DB) UpsertTurns(agentType, sessionID string, turns []TurnText, revision int64) error {
    if len(turns) == 0 {
        return nil
    }
    tx, err := db.conn.Begin()
    if err != nil {
        return fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback()

    // 1. 删除旧数据（触发器维护 FTS 同步）
    if _, err := tx.Exec(
        `DELETE FROM turn_texts WHERE agent_type = ? AND session_id = ?`,
        agentType, sessionID,
    ); err != nil {
        return fmt.Errorf("delete old turns: %w", err)
    }

    // 2. 批量插入（每批 100 条）
    const batchSize = 100
    for i := 0; i < len(turns); i += batchSize {
        end := i + batchSize
        if end > len(turns) {
            end = len(turns)
        }
        batch := turns[i:end]

        placeholders := make([]string, len(batch))
        args := make([]any, 0, len(batch)*5)
        for j, t := range batch {
            placeholders[j] = "(?, ?, ?, ?, ?)"
            args = append(args, agentType, sessionID, t.TurnIndex, t.Role, t.Content)
        }
        q := `INSERT OR REPLACE INTO turn_texts(agent_type, session_id, turn_index, role, content)
              VALUES ` + strings.Join(placeholders, ",")
        if _, err := tx.Exec(q, args...); err != nil {
            return fmt.Errorf("insert turns batch: %w", err)
        }
    }

    // 3. 更新 watermark
    if _, err := tx.Exec(
        `INSERT INTO index_watermarks(agent_type, session_id, revision, indexed_at)
         VALUES (?, ?, ?, datetime('now'))
         ON CONFLICT(agent_type, session_id) DO UPDATE SET
             revision   = excluded.revision,
             indexed_at = excluded.indexed_at`,
        agentType, sessionID, revision,
    ); err != nil {
        return fmt.Errorf("set watermark: %w", err)
    }

    return tx.Commit()
}

// GetWatermark 返回 (revision, exists, error)。
func (db *DB) GetWatermark(agentType, sessionID string) (int64, bool, error) {
    var rev int64
    err := db.conn.QueryRow(
        `SELECT revision FROM index_watermarks WHERE agent_type = ? AND session_id = ?`,
        agentType, sessionID,
    ).Scan(&rev)
    if err == sql.ErrNoRows {
        return 0, false, nil
    }
    if err != nil {
        return 0, false, err
    }
    return rev, true, nil
}

// DeleteOrphansByAgent 删除该 agent 已知 session 集合之外的 watermark 和 turn_texts。
// 仅在该 agent 的 ListSessions() 完整成功后调用。
func (db *DB) DeleteOrphansByAgent(agentType string, knownSessionIDs []string) error {
    if len(knownSessionIDs) == 0 {
        // 安全保护：如果列表为空（可能是 reader 出错），不删除任何数据
        return nil
    }

    placeholders := make([]string, len(knownSessionIDs))
    args := make([]any, 0, len(knownSessionIDs)+1)
    args = append(args, agentType)
    for i, id := range knownSessionIDs {
        placeholders[i] = "?"
        args = append(args, id)
    }
    inClause := strings.Join(placeholders, ",")

    tx, err := db.conn.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    if _, err := tx.Exec(
        `DELETE FROM turn_texts WHERE agent_type = ? AND session_id NOT IN (`+inClause+`)`,
        args...,
    ); err != nil {
        return fmt.Errorf("delete orphan turns: %w", err)
    }
    if _, err := tx.Exec(
        `DELETE FROM index_watermarks WHERE agent_type = ? AND session_id NOT IN (`+inClause+`)`,
        args...,
    ); err != nil {
        return fmt.Errorf("delete orphan watermarks: %w", err)
    }

    return tx.Commit()
}

// snippetAround 在 content 中找到 query 的第一次出现（大小写无关），
// 返回其前后各 60 字节的窗口，截断至 rune 边界。
// 找不到时返回开头 120 字节。
func snippetAround(content, query string, radius int) string {
    lower := strings.ToLower(content)
    lowerQ := strings.ToLower(query)
    idx := strings.Index(lower, lowerQ)
    if idx < 0 {
        idx = 0
    }
    lo := idx - radius
    if lo < 0 {
        lo = 0
    }
    hi := idx + len(query) + radius
    if hi > len(content) {
        hi = len(content)
    }
    // snap to rune boundaries
    for lo > 0 && !utf8.RuneStart(content[lo]) {
        lo--
    }
    for hi < len(content) && !utf8.RuneStart(content[hi]) {
        hi++
    }
    result := content[lo:hi]
    if lo > 0 {
        result = "…" + result
    }
    if hi < len(content) {
        result = result + "…"
    }
    return result
}
```

---

## 三、`internal/db/search.go`（新建）

```go
package db

import (
    "fmt"
    "strings"
    "unicode/utf8"
)

const searchSnippetRadius = 60

// TurnSearchResult 是一条搜索命中记录。
type TurnSearchResult struct {
    AgentType string `json:"agent_type"`
    SessionID string `json:"session_id"`
    Match     string `json:"match"` // 纯文本 snippet，无 HTML 标签
}

// SearchTurns 执行 FTS5 trigram 全文搜索。
//
// 规则：
//   - q < 2 rune 时返回空（trigram 无效且结果太噪）
//   - 用双引号包裹 q，转义内部双引号，防止 FTS 语法注入
//   - 每个 (agent_type, session_id) 只返回最佳一条（ROW_NUMBER 取 rank ASC 第一）
//   - role='meta' 行参与 FTS 但不作为 snippet 展示
//   - limit 由调用方限制（建议 30）
func (db *DB) SearchTurns(q string, limit int) ([]TurnSearchResult, error) {
    if utf8.RuneCountInString(q) < 2 {
        return nil, nil
    }
    if limit <= 0 || limit > 100 {
        limit = 30
    }

    ftsQuery := prepareFTSQuery(q)

    // 两层 CTE：
    //   all_hits  — FTS 全部命中行（含 meta）
    //   best_hits — 每个 (agent_type, session_id) 取 role != 'meta' 的最佳行；
    //               若该 session 只在 meta 行命中，取 meta 行
    query := `
        WITH all_hits AS (
            SELECT tt.agent_type,
                   tt.session_id,
                   tt.role,
                   tt.content,
                   rank AS fts_rank
            FROM turn_texts_fts
            JOIN turn_texts tt ON turn_texts_fts.rowid = tt.id
            WHERE turn_texts_fts MATCH ?
        ),
        content_hits AS (
            SELECT agent_type, session_id, content, fts_rank,
                   ROW_NUMBER() OVER (
                       PARTITION BY agent_type, session_id
                       ORDER BY fts_rank ASC
                   ) AS rn
            FROM all_hits
            WHERE role != 'meta'
        ),
        meta_only AS (
            SELECT DISTINCT a.agent_type, a.session_id, a.content, a.fts_rank
            FROM all_hits a
            WHERE a.role = 'meta'
              AND NOT EXISTS (
                  SELECT 1 FROM content_hits c
                  WHERE c.agent_type = a.agent_type AND c.session_id = a.session_id
              )
        ),
        combined AS (
            SELECT agent_type, session_id, content, fts_rank
            FROM content_hits
            WHERE rn = 1
            UNION ALL
            SELECT agent_type, session_id, content, fts_rank
            FROM meta_only
        )
        SELECT agent_type, session_id, content, fts_rank
        FROM combined
        ORDER BY fts_rank ASC, session_id ASC
        LIMIT ?`

    rows, err := db.conn.Query(query, ftsQuery, limit)
    if err != nil {
        return nil, fmt.Errorf("search: %w", err)
    }
    defer rows.Close()

    var results []TurnSearchResult
    for rows.Next() {
        var agentType, sessionID, content string
        var rank float64
        if err := rows.Scan(&agentType, &sessionID, &content, &rank); err != nil {
            return nil, fmt.Errorf("scan: %w", err)
        }
        results = append(results, TurnSearchResult{
            AgentType: agentType,
            SessionID: sessionID,
            Match:     snippetAround(content, q, searchSnippetRadius),
        })
    }
    if err := rows.Err(); err != nil {
        return nil, err
    }
    return results, nil
}

// prepareFTSQuery 将用户原始输入包裹为 FTS5 短语查询，防止特殊字符被解析为 FTS 语法。
// trigram tokenizer 下短语查询等价于子串匹配。
func prepareFTSQuery(q string) string {
    // 转义内部的双引号（FTS5 短语内用 "" 表示字面量 "）
    escaped := strings.ReplaceAll(q, `"`, `""`)
    return `"` + escaped + `"`
}
```

---

## 四、`internal/indexer/indexer.go`（新建）

```go
package indexer

import (
    "context"
    "log"
    "time"

    "session-insight/internal/db"
    "session-insight/internal/reader"
)

const (
    IndexInterval = 3 * time.Minute
    snippetRadius = 60
)

// Indexer 把各 Reader 的会话内容异步写入 SQLite FTS 索引。
type Indexer struct {
    db      *db.DB
    readers []reader.BaseSessionReader
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Indexer {
    return &Indexer{db: database, readers: readers}
}

// RunOnce 执行一次完整的增量索引，供启动时同步调用。
func (ix *Indexer) RunOnce(ctx context.Context) {
    ix.indexOnce(ctx)
}

// RunBackground 在后台循环运行，每 IndexInterval 增量更新一次。
// 调用方应在 goroutine 中启动：go ix.RunBackground(ctx)。
func (ix *Indexer) RunBackground(ctx context.Context) {
    ticker := time.NewTicker(IndexInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            ix.indexOnce(ctx)
        }
    }
}

func (ix *Indexer) indexOnce(ctx context.Context) {
    for _, r := range ix.readers {
        if ctx.Err() != nil {
            return
        }
        ix.indexReader(ctx, r)
    }
}

func (ix *Indexer) indexReader(ctx context.Context, r reader.BaseSessionReader) {
    sessions, err := r.ListSessions()
    if err != nil {
        log.Printf("[indexer] %s: ListSessions error: %v", r.AgentType(), err)
        return // 不清理旧数据，保留已有索引
    }

    knownIDs := make([]string, 0, len(sessions))
    for _, sess := range sessions {
        if ctx.Err() != nil {
            return
        }
        knownIDs = append(knownIDs, sess.ID)
        ix.indexSession(ctx, r, sess)
    }

    // 清理该 agent 下已消失的会话（仅在 ListSessions 成功后执行）
    if err := ix.db.DeleteOrphansByAgent(r.AgentType(), knownIDs); err != nil {
        log.Printf("[indexer] %s: DeleteOrphansByAgent error: %v", r.AgentType(), err)
    }
}

func (ix *Indexer) indexSession(ctx context.Context, r reader.BaseSessionReader, sess interface{ /* model.Session */ }) {
    // 使用具体类型
    // 此处 sess 实际类型为 model.Session，见下方具体签名
}
```

> **注意**：由于 `model.Session` 在 `session-insight/internal/model` 包，上面是伪代码结构。  
> 实际签名需要 import model 包。完整实现见下：

```go
package indexer

import (
    "context"
    "log"
    "time"

    "session-insight/internal/db"
    "session-insight/internal/model"
    "session-insight/internal/reader"
)

const IndexInterval = 3 * time.Minute

type Indexer struct {
    db      *db.DB
    readers []reader.BaseSessionReader
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Indexer {
    return &Indexer{db: database, readers: readers}
}

func (ix *Indexer) RunOnce(ctx context.Context) {
    ix.indexOnce(ctx)
}

func (ix *Indexer) RunBackground(ctx context.Context) {
    ticker := time.NewTicker(IndexInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            ix.indexOnce(ctx)
        }
    }
}

func (ix *Indexer) indexOnce(ctx context.Context) {
    for _, r := range ix.readers {
        if ctx.Err() != nil {
            return
        }
        ix.indexReader(ctx, r)
    }
}

func (ix *Indexer) indexReader(ctx context.Context, r reader.BaseSessionReader) {
    sessions, err := r.ListSessions()
    if err != nil {
        log.Printf("[indexer] %s: ListSessions error: %v", r.AgentType(), err)
        return
    }

    knownIDs := make([]string, 0, len(sessions))
    for _, sess := range sessions {
        if ctx.Err() != nil {
            return
        }
        knownIDs = append(knownIDs, sess.ID)
        ix.indexSession(r, sess)
    }

    if err := ix.db.DeleteOrphansByAgent(r.AgentType(), knownIDs); err != nil {
        log.Printf("[indexer] %s: orphan cleanup error: %v", r.AgentType(), err)
    }
}

func (ix *Indexer) indexSession(r reader.BaseSessionReader, sess model.Session) {
    agentType := r.AgentType()
    revision := sess.UpdatedAt.UnixNano()

    storedRev, exists, err := ix.db.GetWatermark(agentType, sess.ID)
    if err != nil {
        log.Printf("[indexer] %s/%s: GetWatermark error: %v", agentType, sess.ID, err)
        return
    }
    if exists && storedRev == revision {
        return // 未变化，跳过
    }

    detail, err := r.GetSession(sess.ID)
    if err != nil || detail == nil {
        log.Printf("[indexer] %s/%s: GetSession error: %v", agentType, sess.ID, err)
        return // 失败时保留旧索引，不更新 watermark
    }

    turns := buildTurnTexts(sess, detail)
    if len(turns) == 0 {
        return
    }

    if err := ix.db.UpsertTurns(agentType, sess.ID, turns, revision); err != nil {
        log.Printf("[indexer] %s/%s: UpsertTurns error: %v", agentType, sess.ID, err)
    }
}

// buildTurnTexts 从 SessionDetail 构造待索引行列表：
//   - role='meta'：会话名称 + repository，供名称搜索（turn_index=-1）
//   - role='user'：每个 Turn 的 UserMessage
func buildTurnTexts(sess model.Session, detail *model.SessionDetail) []db.TurnText {
    var texts []db.TurnText

    // meta 行：会话名称 + repo（合并便于统一 FTS）
    meta := sess.Name
    if sess.Repository != "" {
        meta += " " + sess.Repository
    }
    if meta != "" {
        texts = append(texts, db.TurnText{
            TurnIndex: -1,
            Role:      "meta",
            Content:   meta,
        })
    }

    // user 消息行
    for _, t := range detail.Turns {
        if t.UserMessage != "" {
            texts = append(texts, db.TurnText{
                TurnIndex: t.TurnIndex,
                Role:      "user",
                Content:   t.UserMessage,
            })
        }
    }

    return texts
}
```

---

## 五、`internal/server/handlers.go`

### 替换 `handleSearch`

删除原有 `handleSearch`（约 55 行），替换为：

```go
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query().Get("q")
    if q == "" {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode([]map[string]string{})
        return
    }

    results, err := s.DB.SearchTurns(q, 30)
    if err != nil {
        http.Error(w, "search error", http.StatusInternalServerError)
        return
    }

    type result struct {
        SessionID string `json:"session_id"`
        Match     string `json:"match"`
    }
    out := make([]result, 0, len(results))
    for _, r := range results {
        out = append(out, result{SessionID: r.SessionID, Match: r.Match})
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(out)
}
```

同时删除已无用的辅助函数 `contains()` 和 `searchSub()`（如果仅用于 handleSearch）。

---

## 六、`main.go`

```go
// 在 srv := server.New(...) 之前插入：

import (
    // 新增
    "context"
    "time"
    "session-insight/internal/indexer"
)

// ...

idx := indexer.New(database, readers)

// 首次索引同步完成（10s 超时），保证服务启动时已有基础索引
initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
idx.RunOnce(initCtx)
cancel()

// 后台增量更新
go idx.RunBackground(context.Background())

srv := server.New(database, readers)
```

---

## 七、构建脚本

### `start.sh`（第 38 行附近）

```bash
# 原
go build -o "$BIN_PATH" .

# 改为
go build -tags sqlite_fts5 -o "$BIN_PATH" .
```

### `scripts/start.sh`（第 18 行附近）

```bash
# 原
go build -o "$BIN_PATH" .

# 改为
go build -tags sqlite_fts5 -o "$BIN_PATH" .
```

---

## 八、测试要求

### 必须通过的测试（构建命令：`go test -tags sqlite_fts5 ./...`）

#### Schema / FTS5

```
TestMigrate_FTS5Available         — CREATE VIRTUAL TABLE 不报错
TestMigrate_Idempotent            — 重复迁移无 error
TestTurnTexts_InsertDeleteSync    — 插入后 FTS 可查；删除后 FTS 不可查
TestTurnTexts_RebuildConsistency  — RebuildFTS() 后行数与 turn_texts 一致
```

#### Search 语义

```
TestSearch_Chinese2Chars          — 两个中文字符命中含该词的 session
TestSearch_EnglishCaseInsensitive — "Hello" 命中 "hello world"
TestSearch_FilePath               — "/foo/bar.go" 命中含该路径的 turn
TestSearch_SpecialChars           — 含 "、-, OR, NEAR, *, (, ), %, _ 的 query 不 panic、不返回 SQL error
TestSearch_ShortQuery             — 单个 rune 返回空，不报错
TestSearch_OneResultPerSession    — 同一 session 多条命中只返回一条
TestSearch_MetaFallback           — 仅在 session 名称中命中时也返回该 session
TestSearch_SnippetNoHTML          — Match 字段不含 <b> 字面量
TestSearch_EmptyQuery             — 空串返回空数组
TestSearch_LimitRespected         — 超过 100 条结果时上限 30
```

#### Indexer

```
TestIndexer_FirstRun              — 全新 DB 建索引后可搜到内容
TestIndexer_UnchangedSkip         — 第二次运行同一 session 不调用 GetSession
TestIndexer_RevisionChange        — UpdatedAt 更新后重新索引
TestIndexer_OrphanCleanup         — session 消失后 turn_texts 被删除
TestIndexer_ReaderFailurePreserve — ListSessions 失败时旧索引保留
TestIndexer_GetSessionFailure     — GetSession 失败时 watermark 不推进
TestIndexer_ContextCancel         — ctx.Done() 后 indexOnce 停止
TestIndexer_TransactionAtomicity  — UpsertTurns 中途失败后 watermark 不变
```

---

## 九、回滚方案

1. 停服务，备份 `~/.session-insight/index.db`
2. 删除顺序：触发器 → FTS 虚拟表 → 内容表 → watermark 表  
   （不删 `sessions` 表，保留元数据）
3. 恢复旧二进制（无 FTS5 tag），重启即可；`sessions` 表不受影响
4. 强制重建索引：启动后删除 `index_watermarks` 所有行，重启触发全量重建

---

## 十、验证标准

```
go test -tags sqlite_fts5 ./internal/db/... ./internal/indexer/...  # 全绿
./start.sh build && ./start.sh start
# 打开 http://localhost:8080
# 搜索 10+ 个会话的关键词：Network tab /api/search 响应 < 200ms
# 重启后搜索同一关键词：日志中出现 "[indexer] ... skip" 且结果一致
# 搜索两个中文字符：有命中结果
# 搜索 "/home" 等路径：有命中
# 搜索包含双引号的字符串（如 "fix"）：不报 500
```

<!-- impl: done by DeepSeek, 2026-06-20 -->
