package db

import (
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

// SearchTurns 执行 FTS5 trigram 全文搜索。
//
// 规则：
//   - q < 3 rune 时返回空（trigram 需要至少 3 字符才有意义）
//   - 用双引号包裹 q，转义内部双引号，防止 FTS 语法注入
//   - 每个 (agent_type, session_id) 只返回最佳一条（ROW_NUMBER 取 rank ASC 第一）
//   - role='meta' 行参与 FTS 但不作为 snippet 展示
//   - limit 由调用方限制（建议 30）
func (db *DB) SearchTurns(q string, limit int) ([]TurnSearchResult, error) {
	if utf8.RuneCountInString(q) < 3 {
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
