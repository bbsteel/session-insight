package indexer

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

const IndexInterval = 3 * time.Minute

// Progress is a snapshot of the current (or last completed) index cycle.
// Percent is 0–100; when State is "idle", Percent is 100 after a successful
// pass and 0 before the first cycle has finished.
type Progress struct {
	State   string `json:"state"` // "idle" | "running"
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
}

type Indexer struct {
	db      *db.DB
	readers []reader.BaseSessionReader
	kick    chan struct{}

	// OnChanged（可选）在一轮索引产生实际变更（会话新增/更新/删除）后调用。
	// SSE 通知挂在这里而不是文件监听回调上：等数据落库后再让侧栏重拉，
	// 既不会读到旧数据，也不会跟正在跑的索引轮抢 CPU。
	OnChanged func()

	// OnProgress（可选）在进度变化时调用（开始/步进/结束），供 UI 轮询或 SSE。
	OnProgress func(Progress)

	progressMu sync.Mutex
	progress   Progress
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Indexer {
	return &Indexer{
		db:      database,
		readers: readers,
		kick:    make(chan struct{}, 1),
		progress: Progress{
			State:   "idle",
			Percent: 0,
			Message: "waiting",
		},
	}
}

// SnapshotProgress returns a copy of the latest progress snapshot.
func (ix *Indexer) SnapshotProgress() Progress {
	ix.progressMu.Lock()
	defer ix.progressMu.Unlock()
	return ix.progress
}

func (ix *Indexer) setProgress(p Progress) {
	if p.Total > 0 {
		p.Percent = (p.Done * 100) / p.Total
		if p.Percent > 100 {
			p.Percent = 100
		}
	} else if p.State == "idle" {
		p.Percent = 100
	}
	ix.progressMu.Lock()
	ix.progress = p
	ix.progressMu.Unlock()
	if ix.OnProgress != nil {
		ix.OnProgress(p)
	}
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
	// Pre-count sessions so the UI can show a stable percentage.
	type agentSessions struct {
		reader   reader.BaseSessionReader
		sessions []model.Session
		listErr  error
	}
	planned := make([]agentSessions, 0, len(ix.readers))
	total := 0
	for _, r := range ix.readers {
		if ctx.Err() != nil {
			ix.setProgress(Progress{State: "idle", Message: "cancelled"})
			return ctx.Err()
		}
		sessions, err := r.ListSessions()
		if err != nil {
			log.Printf("[indexer] %s: ListSessions error: %v", r.AgentType(), err)
			planned = append(planned, agentSessions{reader: r, listErr: err})
			continue
		}
		planned = append(planned, agentSessions{reader: r, sessions: sessions})
		total += len(sessions)
	}

	ix.setProgress(Progress{
		State:   "running",
		Done:    0,
		Total:   total,
		Message: "indexing",
	})

	var errs []string
	changed := 0
	done := 0
	for _, item := range planned {
		if ctx.Err() != nil {
			ix.setProgress(Progress{
				State:   "idle",
				Done:    done,
				Total:   total,
				Message: "cancelled",
			})
			return ctx.Err()
		}
		if item.listErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", item.reader.AgentType(), item.listErr))
			continue
		}
		n, err := ix.indexReaderSessions(ctx, item.reader, item.sessions, func() {
			done++
			ix.setProgress(Progress{
				State:   "running",
				Done:    done,
				Total:   total,
				Message: "indexing",
			})
		})
		changed += n
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", item.reader.AgentType(), err))
		}
	}

	msg := "ready"
	if len(errs) > 0 {
		msg = "completed_with_errors"
	}
	ix.setProgress(Progress{
		State:   "idle",
		Done:    done,
		Total:   total,
		Message: msg,
	})

	if changed > 0 && ix.OnChanged != nil {
		ix.OnChanged()
	}
	if len(errs) > 0 {
		return fmt.Errorf("index once errors:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

// indexReaderSessions indexes a pre-listed session set for one agent.
// onEach is called after each session attempt (success or failure) for progress.
// Per-session errors are aggregated so the cycle ends as completed_with_errors
// instead of ready+nil when some sessions fail (watermark not advanced for those).
func (ix *Indexer) indexReaderSessions(
	ctx context.Context,
	r reader.BaseSessionReader,
	sessions []model.Session,
	onEach func(),
) (int, error) {
	changed := 0
	var sessionErrs []string
	knownIDs := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if ctx.Err() != nil {
			if len(sessionErrs) > 0 {
				return changed, fmt.Errorf("%w; also: %s", ctx.Err(), strings.Join(sessionErrs, "; "))
			}
			return changed, ctx.Err()
		}
		knownIDs = append(knownIDs, sess.ID)
		did, err := ix.indexSession(r, sess)
		if err != nil {
			log.Printf("[indexer] %s/%s: index error: %v", r.AgentType(), sess.ID, err)
			sessionErrs = append(sessionErrs, fmt.Sprintf("%s: %v", sess.ID, err))
		}
		if did {
			changed++
		}
		if onEach != nil {
			onEach()
		}
	}

	// 清理该 agent 下已消失的会话（删除不在 knownIDs 中的旧数据）
	// 注意：GetSession 失败的会话仍在 knownIDs 中，保留其旧索引不删除。
	removed, err := ix.db.DeleteOrphansByAgent(r.AgentType(), knownIDs)
	if err != nil {
		log.Printf("[indexer] %s: orphan cleanup error: %v", r.AgentType(), err)
		// 孤儿清理失败不阻止其他 reader
	}
	if len(sessionErrs) > 0 {
		return changed + removed, fmt.Errorf("session errors: %s", strings.Join(sessionErrs, "; "))
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

	persisted := sess
	applyDetailMetadata(&persisted, detail.Session)

	// Persist metadata before UpsertTurns commits the watermark. If metadata
	// fails after a watermark write, the next cycle would otherwise treat the
	// session as unchanged and permanently skip the resume_id backfill.
	if err := ix.db.UpsertSessionMetaWithHistoryLineageAndProvider(
		agentType, persisted.ID, persisted.CWD, persisted.Repository, persisted.Branch,
		persisted.Project, persisted.Name, persisted.ModelName, persisted.ModelProvider, persisted.ResumeID,
		persisted.ParentSessionID, persisted.AgentPath, persisted.IsSubagent,
		detail.TurnCount, detail.HistoricalTurnCount, detail.RolledBackTurnCount, persisted.MessageCount,
		persisted.CreatedAt, persisted.UpdatedAt,
	); err != nil {
		return false, err
	}

	// Render events carry tool inputs (command/path/query) that TurnVM often
	// omits. Fail the session (do not advance the watermark) so a transient
	// error is retried on the next cycle instead of locking in a partial index.
	renderEvents, err := r.GetRenderEvents(sess.ID)
	if err != nil {
		return false, fmt.Errorf("get render events: %w", err)
	}

	turns := buildTurnTexts(persisted, detail, renderEvents)
	if err := ix.db.UpsertTurns(agentType, sess.ID, turns, revision); err != nil {
		return false, fmt.Errorf("upsert turns: %w", err)
	}
	return true, nil
}

func applyDetailMetadata(base *model.Session, detail model.Session) {
	if detail.CWD != "" {
		base.CWD = detail.CWD
	}
	if detail.Repository != "" {
		base.Repository = detail.Repository
	}
	if detail.Branch != "" {
		base.Branch = detail.Branch
	}
	if detail.Project != "" {
		base.Project = detail.Project
	}
	if detail.Name != "" {
		base.Name = detail.Name
	}
	if detail.ModelName != "" {
		base.ModelName = detail.ModelName
	}
	if detail.ModelProvider != "" {
		base.ModelProvider = detail.ModelProvider
	}
	if detail.ResumeID != "" {
		base.ResumeID = detail.ResumeID
	}
	if detail.ParentSessionID != "" {
		base.ParentSessionID = detail.ParentSessionID
	}
	if detail.AgentPath != "" {
		base.AgentPath = detail.AgentPath
	}
	if detail.IsSubagent {
		base.IsSubagent = true
	}
}

// Index content caps keep FTS volume bounded (cross-session recall, not archive).
const (
	maxAssistantRunes = 8192
	maxToolRunes      = 4096
	maxErrorRunes     = 2048
	maxFieldRunes     = 500
	maxURLsPerTurn    = 32
)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"\x60]+`)

// highSignalToolInputKeys are short, searchable tool argument fields.
// Long blobs (file bodies, patches, stdout) are intentionally omitted.
var highSignalToolInputKeys = []string{
	"command", "cmd", "file_path", "path", "pattern", "query", "url",
	"skill", "glob", "target_file", "target_directory", "args",
}

// buildTurnTexts builds FTS rows from a session detail (and optional render events):
//   - role='meta': name, repo, branch, model, session id (turn_index=-1)
//   - role='user': UserMessage
//   - role='assistant': AssistantMessage (capped)
//   - role='link': URLs extracted before any content cap
//   - role='skill': skill names used in the turn
//   - role='tool': tool names + high-signal input summaries (capped)
//   - role='error': tool/LLM/agent failure text + anomaly labels (capped)
//
// UNIQUE(agent_type, session_id, turn_index, role) allows one row per role per
// turn, so multi-tool/skill/error fragments are joined into a single content.
func buildTurnTexts(sess model.Session, detail *model.SessionDetail, renderEvents []model.RenderEvent) []db.TurnText {
	var texts []db.TurnText

	metaParts := make([]string, 0, 6)
	for _, p := range []string{sess.Name, sess.Repository, sess.Branch, sess.ModelName, sess.ID} {
		if p != "" {
			metaParts = append(metaParts, p)
		}
	}
	if meta := strings.Join(metaParts, " "); meta != "" {
		texts = append(texts, db.TurnText{TurnIndex: -1, Role: "meta", Content: meta})
	}

	toolByTurn := toolSummariesByTurn(detail, renderEvents)

	for _, t := range detail.Turns {
		if t.UserMessage != "" {
			texts = append(texts, db.TurnText{
				TurnIndex: t.TurnIndex,
				Role:      "user",
				Content:   t.UserMessage,
			})
		}
		if s := strings.TrimSpace(t.AssistantMessage); s != "" {
			texts = append(texts, db.TurnText{
				TurnIndex: t.TurnIndex,
				Role:      "assistant",
				Content:   truncateRunes(s, maxAssistantRunes),
			})
			if links := extractURLs(s); links != "" {
				texts = append(texts, db.TurnText{
					TurnIndex: t.TurnIndex,
					Role:      "link",
					Content:   links,
				})
			}
		}
		if skills := uniqueNonEmpty(t.Skills); len(skills) > 0 {
			texts = append(texts, db.TurnText{
				TurnIndex: t.TurnIndex,
				Role:      "skill",
				Content:   strings.Join(skills, " "),
			})
		}
		if tool := strings.TrimSpace(toolByTurn[t.TurnIndex]); tool != "" {
			texts = append(texts, db.TurnText{
				TurnIndex: t.TurnIndex,
				Role:      "tool",
				Content:   truncateRunes(tool, maxToolRunes),
			})
		}
		if errText := turnErrorText(t); errText != "" {
			texts = append(texts, db.TurnText{
				TurnIndex: t.TurnIndex,
				Role:      "error",
				Content:   truncateRunes(errText, maxErrorRunes),
			})
		}
	}

	for _, group := range detail.RollbackGroups {
		for _, t := range group.Turns {
			idx := -(t.OriginalTurnIndex + 1)
			if t.UserMessage != "" {
				texts = append(texts, db.TurnText{
					TurnIndex: idx,
					Role:      "user",
					Content:   "[已回滚] " + t.UserMessage,
				})
			}
			if s := strings.TrimSpace(t.AssistantMessage); s != "" {
				texts = append(texts, db.TurnText{
					TurnIndex: idx,
					Role:      "assistant",
					Content:   "[已回滚] " + truncateRunes(s, maxAssistantRunes),
				})
				if links := extractURLs(s); links != "" {
					texts = append(texts, db.TurnText{
						TurnIndex: idx,
						Role:      "link",
						Content:   links,
					})
				}
			}
		}
	}

	return texts
}

// extractURLs preserves URLs that may occur after a capped transcript field.
// URLs are compact, high-signal search keys, so indexing them separately keeps
// the general transcript cap while retaining direct-link recall.
func extractURLs(s string) string {
	urls := urlPattern.FindAllString(s, maxURLsPerTurn)
	if len(urls) == 0 {
		return ""
	}
	return strings.Join(uniqueNonEmpty(urls), " ")
}

func turnErrorText(t model.TurnVM) string {
	var parts []string
	for _, a := range t.Anomalies {
		if a = strings.TrimSpace(a); a != "" {
			parts = append(parts, a)
		}
	}
	for _, td := range t.ToolDetails {
		var bits []string
		if td.Name != "" {
			bits = append(bits, td.Name)
		}
		if td.ErrorKind != "" {
			bits = append(bits, td.ErrorKind)
		}
		if td.ErrorMessage != "" {
			bits = append(bits, truncateRunes(td.ErrorMessage, maxFieldRunes))
		}
		if td.TimedOut {
			bits = append(bits, "timed_out")
		}
		if td.Rejected {
			bits = append(bits, "rejected")
		}
		if len(bits) > 0 && (td.ErrorKind != "" || td.ErrorMessage != "" || td.TimedOut || td.Rejected || td.ExitCode != 0) {
			// Index non-zero exit even without structured error fields.
			if td.ExitCode != 0 && td.ErrorKind == "" && td.ErrorMessage == "" {
				bits = append(bits, fmt.Sprintf("exit_%d", td.ExitCode))
			}
			parts = append(parts, strings.Join(bits, " "))
		}
	}
	for _, e := range t.Events {
		if e.Data == nil {
			continue
		}
		if isErr, _ := e.Data["is_error"].(bool); isErr {
			if stderr, _ := e.Data["stderr"].(string); strings.TrimSpace(stderr) != "" {
				parts = append(parts, truncateRunes(strings.TrimSpace(stderr), maxFieldRunes))
			} else {
				parts = append(parts, "tool_error")
			}
		}
		if kind, _ := e.Data["error_kind"].(string); strings.TrimSpace(kind) != "" {
			parts = append(parts, strings.TrimSpace(kind))
		}
		if msg, _ := e.Data["error_message"].(string); strings.TrimSpace(msg) != "" {
			parts = append(parts, truncateRunes(strings.TrimSpace(msg), maxFieldRunes))
		}
	}
	return strings.Join(uniqueNonEmpty(parts), " ")
}

// toolSummariesByTurn merges TurnVM tool names/details with render-event inputs.
// Render-event summaries (command/path/query) are appended first so the tool
// content cap prefers high-signal inputs over bare tool-name fallbacks.
func toolSummariesByTurn(detail *model.SessionDetail, renderEvents []model.RenderEvent) map[int]string {
	parts := map[int][]string{}
	add := func(turn int, s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		parts[turn] = append(parts[turn], s)
	}

	for _, ev := range renderEvents {
		// SI readers emit PascalCase "ToolInvocation"; skip results/stdout.
		if ev.Type == "ToolResult" || ev.Type == "tool_result" {
			continue
		}
		isTool := ev.Type == "ToolInvocation" ||
			ev.Type == "tool_use" || ev.Type == "tool_call" ||
			ev.Type == "function_call" || ev.Type == "custom_tool_call" ||
			(ev.ToolName != "" && len(ev.ToolInput) > 0)
		if !isTool {
			continue
		}
		var bits []string
		if ev.ToolName != "" {
			bits = append(bits, ev.ToolName)
		}
		if sum := summarizeToolInput(ev.ToolInput); sum != "" {
			bits = append(bits, sum)
		}
		if len(bits) > 0 {
			add(ev.TurnIndex, strings.Join(bits, " "))
		}
	}

	for _, t := range detail.Turns {
		for _, name := range t.ToolNames {
			add(t.TurnIndex, name)
		}
		for _, td := range t.ToolDetails {
			var bits []string
			if td.Name != "" {
				bits = append(bits, td.Name)
			}
			if td.ToolKind != "" {
				bits = append(bits, td.ToolKind)
			}
			if len(bits) > 0 {
				add(t.TurnIndex, strings.Join(bits, " "))
			}
		}
		// EventVM sometimes carries only the tool name (no full input).
		for _, e := range t.Events {
			if e.Data == nil {
				continue
			}
			switch e.Type {
			case "tool_call", "function_call", "custom_tool_call":
				if name, _ := e.Data["name"].(string); name != "" {
					add(t.TurnIndex, name)
				}
			}
		}
	}

	out := make(map[int]string, len(parts))
	for turn, list := range parts {
		out[turn] = strings.Join(uniqueNonEmpty(list), " ")
	}
	return out
}

func summarizeToolInput(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	var parts []string
	for _, key := range highSignalToolInputKeys {
		v, ok := input[key]
		if !ok || v == nil {
			continue
		}
		s := stringifyToolField(v)
		if s == "" {
			continue
		}
		parts = append(parts, key+":"+s)
	}
	return strings.Join(parts, " ")
}

func stringifyToolField(v any) string {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return ""
		}
		// Skip huge free-form payloads under args/command-like keys.
		return truncateRunes(s, maxFieldRunes)
	case float64:
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

func uniqueNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
