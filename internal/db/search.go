package db

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	searchSnippetRadius = 60
	maxQueryBytes       = 4096
)

// TurnSearchResult 是一条搜索命中记录。
type TurnSearchResult struct {
	AgentType string `json:"agent_type"`
	SessionID string `json:"session_id"`
	Match     string `json:"match"` // 纯文本 snippet，无 HTML 标签
}

// SearchTurns 执行全文搜索。
//
// 规则：
//   - q >= 3 rune 时使用 FTS5 trigram MATCH（trigram tokenizer 需要至少 3 字符）
//   - q < 3 rune 时回退到 LIKE '%q%'（满足 1-2 字符中文搜索如 "折叠"）
//   - 每个 (agent_type, session_id) 只返回最佳一条
//   - role='meta' 行参与搜索但不作为 snippet 展示
//   - limit 由调用方限制（建议 30）
func (db *DB) SearchTurns(q string, limit int) ([]TurnSearchResult, error) {
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	short := utf8.RuneCountInString(q) < 3

	var rows *sql.Rows
	var err error

	if short {
		rows, err = db.searchLike(q, limit)
	} else {
		rows, err = db.searchFTS(q, limit)
	}
	if err != nil {
		return nil, err
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

// searchLike performs a LIKE-based search for short queries (< 3 runes).
func (db *DB) searchLike(q string, limit int) (*sql.Rows, error) {
	pattern := "%" + q + "%"
	query := `
		WITH content_hits AS (
		    SELECT agent_type, session_id, content, 1.0 AS fts_rank,
		           ROW_NUMBER() OVER (
		               PARTITION BY agent_type, session_id
		               ORDER BY rowid ASC
		           ) AS rn
		    FROM turn_texts
		    WHERE role != 'meta' AND content LIKE ?
		),
		meta_only AS (
		    SELECT DISTINCT agent_type, session_id, content, 1.0 AS fts_rank
		    FROM turn_texts
		    WHERE role = 'meta' AND content LIKE ?
		      AND NOT EXISTS (
		          SELECT 1 FROM content_hits c
		          WHERE c.agent_type = turn_texts.agent_type AND c.session_id = turn_texts.session_id
		      )
		),
		combined AS (
		    SELECT agent_type, session_id, content, fts_rank
		    FROM content_hits WHERE rn = 1
		    UNION ALL
		    SELECT agent_type, session_id, content, fts_rank
		    FROM meta_only
		)
		SELECT agent_type, session_id, content, fts_rank
		FROM combined
		ORDER BY session_id ASC
		LIMIT ?`
	return db.conn.Query(query, pattern, pattern, limit)
}

// searchFTS performs FTS5 trigram search for queries >= 3 runes.
func (db *DB) searchFTS(q string, limit int) (*sql.Rows, error) {
	ftsQuery := prepareFTSQuery(q)
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
		    FROM content_hits WHERE rn = 1
		    UNION ALL
		    SELECT agent_type, session_id, content, fts_rank
		    FROM meta_only
		)
		SELECT agent_type, session_id, content, fts_rank
		FROM combined
		ORDER BY fts_rank ASC, session_id ASC
		LIMIT ?`
	return db.conn.Query(query, ftsQuery, limit)
}

// prepareFTSQuery 将用户原始输入包裹为 FTS5 短语查询，防止特殊字符被解析为 FTS 语法。
// trigram tokenizer 下短语查询等价于子串匹配。
// 过滤 NUL 和控制字符，超长查询截断至 maxQueryBytes。
func prepareFTSQuery(q string) string {
	// 过滤 NUL 和其他可能导致问题的字符
	q = strings.Map(func(r rune) rune {
		if r == 0 || r == '\x1a' {
			return -1 // drop
		}
		return r
	}, q)

	// 截断超长查询，防止 CPU/内存暴增；边界对齐至合法 UTF-8
	if len(q) > maxQueryBytes {
		q = q[:maxQueryBytes]
		// 回退到合法 UTF-8 边界（不在多字节字符中间截断）
		for len(q) > 0 && !utf8.ValidString(q) {
			q = q[:len(q)-1]
		}
	}

	// 转义内部的双引号（FTS5 短语内用 "" 表示字面量 "）
	escaped := strings.ReplaceAll(q, `"`, `""`)
	return `"` + escaped + `"`
}
