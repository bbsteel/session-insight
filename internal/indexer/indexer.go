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
	kick    chan struct{}

	// OnChanged（可选）在一轮索引产生实际变更（会话新增/更新/删除）后调用。
	// SSE 通知挂在这里而不是文件监听回调上：等数据落库后再让侧栏重拉，
	// 既不会读到旧数据，也不会跟正在跑的索引轮抢 CPU。
	OnChanged func()
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Indexer {
	return &Indexer{db: database, readers: readers, kick: make(chan struct{}, 1)}
}

// Kick 请求 RunBackground 尽快跑一轮增量索引（文件监听器在会话文件变化时
// 调用，让新会话秒级可搜，而不是等下一个 3 分钟周期）。非阻塞：索引正在
// 跑时多次 Kick 合并为一次补跑。
func (ix *Indexer) Kick() {
	select {
	case ix.kick <- struct{}{}:
	default:
	}
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
		case <-ix.kick:
		}
		if err := ix.indexOnce(ctx); err != nil {
			log.Printf("[indexer] background cycle error: %v", err)
		}
	}
}

func (ix *Indexer) indexOnce(ctx context.Context) error {
	var errs []string
	changed := 0
	for _, r := range ix.readers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := ix.indexReader(ctx, r)
		changed += n
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.AgentType(), err))
		}
	}
	if changed > 0 && ix.OnChanged != nil {
		ix.OnChanged()
	}
	if len(errs) > 0 {
		return fmt.Errorf("index once errors:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

// indexReader 返回本轮该 reader 实际变更的会话数（新增/更新/删除）。
func (ix *Indexer) indexReader(ctx context.Context, r reader.BaseSessionReader) (int, error) {
	sessions, err := r.ListSessions()
	if err != nil {
		log.Printf("[indexer] %s: ListSessions error: %v", r.AgentType(), err)
		return 0, err
	}

	changed := 0
	knownIDs := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if ctx.Err() != nil {
			return changed, ctx.Err()
		}
		knownIDs = append(knownIDs, sess.ID)
		did, err := ix.indexSession(r, sess)
		if err != nil {
			log.Printf("[indexer] %s/%s: index error: %v", r.AgentType(), sess.ID, err)
		}
		if did {
			changed++
		}
	}

	// 清理该 agent 下已消失的会话（删除不在 knownIDs 中的旧数据）
	// 注意：GetSession 失败的会话仍在 knownIDs 中，保留其旧索引不删除。
	removed, err := ix.db.DeleteOrphansByAgent(r.AgentType(), knownIDs)
	if err != nil {
		log.Printf("[indexer] %s: orphan cleanup error: %v", r.AgentType(), err)
		// 孤儿清理失败不阻止其他 reader
	}
	return changed + removed, nil
}

// indexSession 返回是否发生了实际写入（watermark 未变时跳过并返回 false）。
func (ix *Indexer) indexSession(r reader.BaseSessionReader, sess model.Session) (bool, error) {
	agentType := r.AgentType()
	revision := sess.UpdatedAt.UnixNano()

	storedRev, exists, err := ix.db.GetWatermark(agentType, sess.ID)
	if err != nil {
		return false, fmt.Errorf("get watermark: %w", err)
	}
	if exists && storedRev == revision {
		// Turn content is unchanged, but lightweight metadata may need a
		// migration backfill (notably Codex resume_id).
		return ix.db.UpdateSessionResumeID(agentType, sess.ID, sess.ResumeID)
	}

	detail, err := r.GetSession(sess.ID)
	if err != nil {
		return false, fmt.Errorf("get session: %w", err)
	}
	if detail == nil {
		return false, fmt.Errorf("get session %s: reader returned nil detail", sess.ID)
	}

	// Persist metadata before UpsertTurns commits the watermark. If metadata
	// fails after a watermark write, the next cycle would otherwise treat the
	// session as unchanged and permanently skip the resume_id backfill.
	if err := ix.db.UpsertSessionMetaWithHistoryAndLineage(
		agentType, sess.ID, sess.CWD, sess.Repository, sess.Branch,
		sess.Project, sess.Name, sess.ModelName, sess.ResumeID,
		sess.ParentSessionID, sess.AgentPath, sess.IsSubagent,
		detail.TurnCount, detail.HistoricalTurnCount, detail.RolledBackTurnCount, sess.MessageCount,
		sess.CreatedAt, sess.UpdatedAt,
	); err != nil {
		return false, err
	}

	turns := buildTurnTexts(sess, detail)
	if err := ix.db.UpsertTurns(agentType, sess.ID, turns, revision); err != nil {
		return false, fmt.Errorf("upsert turns: %w", err)
	}
	return true, nil
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
	for _, group := range detail.RollbackGroups {
		for _, t := range group.Turns {
			if t.UserMessage == "" {
				continue
			}
			texts = append(texts, db.TurnText{
				TurnIndex: -(t.OriginalTurnIndex + 1),
				Role:      "user",
				Content:   "[已回滚] " + t.UserMessage,
			})
		}
	}

	return texts
}
