package indexer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

const IndexInterval = 3 * time.Minute

type Indexer struct {
	db      *db.DB
	readers []reader.BaseSessionReader
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Indexer {
	return &Indexer{db: database, readers: readers}
}

// RunOnce 执行一次完整的增量索引。
// 返回聚合错误：第一个错误，或 nil（全部成功）。
func (ix *Indexer) RunOnce(ctx context.Context) error {
	return ix.indexOnce(ctx)
}

// RunBackground 在后台循环运行，每 IndexInterval 增量更新一次。
func (ix *Indexer) RunBackground(ctx context.Context) {
	ticker := time.NewTicker(IndexInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := ix.indexOnce(ctx); err != nil {
				log.Printf("[indexer] background cycle error: %v", err)
			}
		}
	}
}

func (ix *Indexer) indexOnce(ctx context.Context) error {
	var errs []string
	for _, r := range ix.readers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := ix.indexReader(ctx, r); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.AgentType(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("index once errors:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

func (ix *Indexer) indexReader(ctx context.Context, r reader.BaseSessionReader) error {
	sessions, err := r.ListSessions()
	if err != nil {
		log.Printf("[indexer] %s: ListSessions error: %v", r.AgentType(), err)
		return err
	}

	knownIDs := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		knownIDs = append(knownIDs, sess.ID)
		if err := ix.indexSession(r, sess); err != nil {
			log.Printf("[indexer] %s/%s: index error: %v", r.AgentType(), sess.ID, err)
		}
	}

	// 清理该 agent 下已消失的会话（删除不在 knownIDs 中的旧数据）
	// 注意：GetSession 失败的会话仍在 knownIDs 中，保留其旧索引不删除。
	if err := ix.db.DeleteOrphansByAgent(r.AgentType(), knownIDs); err != nil {
		log.Printf("[indexer] %s: orphan cleanup error: %v", r.AgentType(), err)
		// 孤儿清理失败不阻止其他 reader
	}
	return nil
}

func (ix *Indexer) indexSession(r reader.BaseSessionReader, sess model.Session) error {
	agentType := r.AgentType()
	revision := sess.UpdatedAt.UnixNano()

	storedRev, exists, err := ix.db.GetWatermark(agentType, sess.ID)
	if err != nil {
		return fmt.Errorf("get watermark: %w", err)
	}
	if exists && storedRev == revision {
		return nil // 未变化，跳过
	}

	detail, err := r.GetSession(sess.ID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if detail == nil {
		return fmt.Errorf("get session %s: reader returned nil detail", sess.ID)
	}

	turns := buildTurnTexts(sess, detail)
	if err := ix.db.UpsertTurns(agentType, sess.ID, turns, revision); err != nil {
		return fmt.Errorf("upsert turns: %w", err)
	}
	// Also persist session metadata so search enrichment is a pure SQL query.
	return ix.db.UpsertSessionMeta(
		agentType, sess.ID, sess.CWD, sess.Repository, sess.Branch,
		sess.Project, sess.Name, sess.ModelName,
		sess.TurnCount, sess.MessageCount,
		sess.CreatedAt, sess.UpdatedAt,
	)
}

// buildTurnTexts 从 SessionDetail 构造待索引行列表：
//   - role='meta'：会话名称 + repository，供名称搜索（turn_index=-1）
//   - role='user'：每个 Turn 的 UserMessage
func buildTurnTexts(sess model.Session, detail *model.SessionDetail) []db.TurnText {
	var texts []db.TurnText

	// meta 行：会话名称 + repo + session ID（合并便于统一 FTS）
	meta := sess.Name
	if sess.Repository != "" {
		meta += " " + sess.Repository
	}
	meta += " " + sess.ID
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
