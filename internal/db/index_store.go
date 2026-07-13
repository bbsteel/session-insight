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
// turns 为空时仍然执行删除 + watermark 更新（会话内容清空的情况）。
// revision 传入 session.UpdatedAt.UnixNano()。
func (db *DB) UpsertTurns(agentType, sessionID string, turns []TurnText, revision int64) error {
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

// DeleteOrphansByAgent 删除该 agent 已知 session 集合之外的 turn_texts、FTS 条目和 watermark。
// 仅在该 agent 的 ListSessions() 完整成功后调用。
// knownSessionIDs 为空时（该 agent 无会话），删除该 agent 的全部旧索引数据。
// DeleteOrphansByAgent 清理该 agent 下已从磁盘消失的会话，返回删除的会话数
// （供调用方判断是否需要广播列表变更）。
func (db *DB) DeleteOrphansByAgent(agentType string, knownSessionIDs []string) (int, error) {
	// 查询该 agent 下所有已有的 session ID（同时查 turn_texts 和 index_watermarks，
	// 防止空内容会话的 watermark 永久泄露）
	rows, err := db.conn.Query(
		`SELECT DISTINCT session_id FROM turn_texts WHERE agent_type = ?
		 UNION
		 SELECT DISTINCT session_id FROM index_watermarks WHERE agent_type = ?
		 UNION
		 SELECT DISTINCT id FROM sessions WHERE agent_type = ?`,
		agentType, agentType, agentType,
	)
	if err != nil {
		return 0, fmt.Errorf("query existing sessions: %w", err)
	}
	var existingIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		existingIDs = append(existingIDs, id)
	}
	rows.Close()

	// Go 端计算差集
	knownSet := make(map[string]struct{}, len(knownSessionIDs))
	for _, id := range knownSessionIDs {
		knownSet[id] = struct{}{}
	}
	var orphans []string
	for _, id := range existingIDs {
		if _, ok := knownSet[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	if len(orphans) == 0 {
		return 0, nil
	}

	// 分批删除孤儿（每批最多 100 个 ID，避免参数爆炸）
	const batchSize = 100
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for i := 0; i < len(orphans); i += batchSize {
		end := i + batchSize
		if end > len(orphans) {
			end = len(orphans)
		}
		batch := orphans[i:end]

		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		args = append(args, agentType)
		for j, id := range batch {
			placeholders[j] = "?"
			args = append(args, id)
		}
		inClause := strings.Join(placeholders, ",")

		if _, err := tx.Exec(
			`DELETE FROM turn_texts WHERE agent_type = ? AND session_id IN (`+inClause+`)`,
			args...,
		); err != nil {
			return 0, fmt.Errorf("delete orphan turns batch: %w", err)
		}
		if _, err := tx.Exec(
			`DELETE FROM index_watermarks WHERE agent_type = ? AND session_id IN (`+inClause+`)`,
			args...,
		); err != nil {
			return 0, fmt.Errorf("delete orphan watermarks batch: %w", err)
		}
		if _, err := tx.Exec(
			`DELETE FROM sessions WHERE agent_type = ? AND id IN (`+inClause+`)`,
			args...,
		); err != nil {
			return 0, fmt.Errorf("delete orphan sessions batch: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(orphans), nil
}

// snippetAround 在 content 中找到 query 的第一次出现（大小写无关），
// 返回其前后各 radius 字节的窗口，边界对齐至 rune。
// 找不到时返回开头 120 字节。返回纯文本，无 HTML 标签。
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
