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

	"github.com/bbsteel/session-insight/internal/changeevidence"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
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
	git     *gitEvidenceRuntime

	gitAttempted sync.Map // agent_type\x00session_id -> successful bounded attempt

	requestMu      sync.Mutex
	fullRequested  bool
	agentRequested map[string]struct{}

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
	gitRuntime, _ := newGitEvidenceRuntime()
	return &Indexer{
		db:             database,
		readers:        readers,
		kick:           make(chan struct{}, 1),
		git:            gitRuntime,
		agentRequested: make(map[string]struct{}),
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
	ix.requestMu.Lock()
	ix.fullRequested = true
	ix.requestMu.Unlock()
	ix.notify()
}

// KickAgent requests an incremental pass for one agent only. File watchers
// use this path so an active transcript does not force every unrelated agent
// store to be re-listed. The periodic full pass remains the reconciliation
// path for missed creates, deletes, and renames.
func (ix *Indexer) KickAgent(agentType string) {
	if agentType == "" {
		ix.Kick()
		return
	}
	ix.requestMu.Lock()
	ix.agentRequested[agentType] = struct{}{}
	ix.requestMu.Unlock()
	ix.notify()
}

func (ix *Indexer) notify() {
	select {
	case ix.kick <- struct{}{}:
	default:
	}
}

func (ix *Indexer) takeRequests() (full bool, agents map[string]struct{}) {
	ix.requestMu.Lock()
	defer ix.requestMu.Unlock()
	full = ix.fullRequested
	ix.fullRequested = false
	agents = ix.agentRequested
	ix.agentRequested = make(map[string]struct{})
	return full, agents
}

// RunOnce 执行一次完整的增量索引。
// 返回聚合错误：第一个错误，或 nil（全部成功）。
func (ix *Indexer) RunOnce(ctx context.Context) error {
	return ix.indexOnce(ctx, nil)
}

// RunBackground 在后台循环运行，每 IndexInterval 增量更新一次。
func (ix *Indexer) RunBackground(ctx context.Context) {
	ticker := time.NewTicker(IndexInterval)
	defer ticker.Stop()
	for {
		var agents map[string]struct{}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A periodic full cycle reconciles missed watcher events and orphans.
		case <-ix.kick:
			full, requested := ix.takeRequests()
			if !full {
				agents = requested
			}
		}
		if err := ix.indexOnce(ctx, agents); err != nil {
			log.Printf("[indexer] background cycle error: %v", err)
		}
	}
}

func (ix *Indexer) indexOnce(ctx context.Context, agentFilter map[string]struct{}) error {
	cycleStarted := time.Now()
	// Pre-count sessions so the UI can show a stable percentage.
	type agentSessions struct {
		reader            reader.BaseSessionReader
		sessions          []model.Session
		inventoryComplete bool
		listErr           error
	}
	planned := make([]agentSessions, 0, len(ix.readers))
	total := 0
	for _, r := range ix.readers {
		if len(agentFilter) > 0 {
			if _, ok := agentFilter[r.AgentType()]; !ok {
				continue
			}
		}
		if ctx.Err() != nil {
			ix.setProgress(Progress{State: "idle", Message: "cancelled"})
			return ctx.Err()
		}
		listStarted := time.Now()
		var sessions []model.Session
		inventoryComplete := false
		var err error
		if dl, ok := r.(reader.DetailedSessionLister); ok {
			sessions, inventoryComplete, err = dl.ListSessionsDetailed()
		} else {
			// Without a completeness signal, refuse omission tombstones.
			sessions, err = r.ListSessions()
			inventoryComplete = false
		}
		listElapsed := time.Since(listStarted)
		if err != nil {
			log.Printf("[indexer] %s: ListSessions failed after %s: %v", r.AgentType(), listElapsed.Round(time.Millisecond), err)
			planned = append(planned, agentSessions{reader: r, listErr: err})
			continue
		}
		log.Printf("[indexer] %s: listed %d session(s) in %s (inventory_complete=%v)",
			r.AgentType(), len(sessions), listElapsed.Round(time.Millisecond), inventoryComplete)
		planned = append(planned, agentSessions{reader: r, sessions: sessions, inventoryComplete: inventoryComplete})
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
		n, err := ix.indexReaderSessions(ctx, item.reader, item.sessions, item.inventoryComplete, func() {
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
	log.Printf("[indexer] cycle complete: agents=%d sessions=%d changed=%d elapsed=%s", len(planned), total, changed, time.Since(cycleStarted).Round(time.Millisecond))
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
	inventoryComplete bool,
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
		did, err := ix.indexSession(ctx, r, sess)
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

	// Omission tombstones only when discovery reported a complete inventory.
	// Incomplete lists (skipped unreadable entries) must not mark live sessions missing.
	tombstoned := 0
	if inventoryComplete {
		var err error
		tombstoned, err = ix.tombstoneMissingSessions(r.AgentType(), knownIDs)
		if err != nil {
			log.Printf("[indexer] %s: source-missing tombstone error: %v", r.AgentType(), err)
		}
	} else {
		log.Printf("[indexer] %s: skip omission tombstones (incomplete inventory)", r.AgentType())
	}
	if len(sessionErrs) > 0 {
		return changed + tombstoned, fmt.Errorf("session errors: %s", strings.Join(sessionErrs, "; "))
	}
	return changed + tombstoned, nil
}

// tombstoneMissingSessions marks indexed sessions not in knownIDs as source_missing.
func (ix *Indexer) tombstoneMissingSessions(agentType string, knownIDs []string) (int, error) {
	existing, err := ix.db.SessionIDsByAgent(agentType)
	if err != nil {
		return 0, err
	}
	known := make(map[string]struct{}, len(knownIDs))
	for _, id := range knownIDs {
		known[id] = struct{}{}
	}
	var missing []string
	for _, id := range existing {
		if _, ok := known[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return 0, nil
	}
	return ix.db.MarkSessionsSourceMissing(agentType, missing, time.Now().UTC())
}

// indexSession 返回是否发生了实际写入（watermark 未变时跳过并返回 false）。
func (ix *Indexer) indexSession(ctx context.Context, r reader.BaseSessionReader, sess model.Session) (bool, error) {
	started := time.Now()
	agentType := r.AgentType()
	revision := sess.UpdatedAt.UnixNano()

	storedRev, exists, err := ix.db.GetWatermark(agentType, sess.ID)
	if err != nil {
		return false, fmt.Errorf("get watermark: %w", err)
	}
	var (
		detail                *model.SessionDetail
		renderEvents          []model.RenderEvent
		authoritativeEnvelope *model.IndexSnapshotEnvelope
		authoritativeReader   reader.AuthoritativeIndexSnapshotReader
		detailElapsed         time.Duration
		renderElapsed         time.Duration
	)
	authoritativeReader, _ = r.(reader.AuthoritativeIndexSnapshotReader)
	collabCurrentKnown := false
	collabCurrent := false
	if exists && storedRev == revision {
		// The turn index is current, but a missing/older collaboration
		// revision (for example right after the v28 backfill or a manual
		// cleanup) must still fall through to a full pass — otherwise the
		// collaboration index could stay permanently "unchanged".
		var err error
		collabCurrent, err = ix.collaborationCurrent(r, agentType, sess, revision)
		if err != nil {
			return false, err
		}
		collabCurrentKnown = true
		storedSourceRevision, creationCurrent, _, readErr := ix.db.SessionChangeRequestCreationIndexState(agentType, sess.ID)
		if readErr != nil {
			return false, readErr
		}
		gitCurrent := creationCurrent
		if authoritativeReader != nil {
			gitEvidenceCurrent := false
			if ix.git != nil {
				gitEvidenceCurrent, readErr = ix.db.HasSessionGitEvidence(agentType, sess.ID)
				if readErr != nil {
					return false, readErr
				}
			}
			if creationCurrent {
				snapshotStarted := time.Now()
				authoritativeEnvelope, readErr = authoritativeReader.ReadIndexSnapshotEnvelope(ctx, sess)
				detailElapsed = time.Since(snapshotStarted)
				if readErr != nil {
					return ix.handleReadFailure(r, agentType, sess, readErr)
				}
				if validation := model.ValidateIndexSnapshotEnvelope(authoritativeEnvelope); !validation.OK() {
					return false, fmt.Errorf("validate authoritative index snapshot: %+v", validation.Issues)
				}
				creationCurrent = storedSourceRevision == authoritativeEnvelope.SourceRevision
			}
			gitCurrent = creationCurrent
			if ix.git != nil {
				if !gitEvidenceCurrent {
					_, gitEvidenceCurrent = ix.gitAttempted.Load(agentType + "\x00" + sess.ID)
				}
				gitCurrent = gitCurrent && gitEvidenceCurrent
			}
		}
		if collabCurrent && gitCurrent {
			// Turn content is unchanged, but list-derived metadata may still
			// need a backfill when adapter logic improves without touching
			// session files (project basename normalization, Codex resume_id).
			return ix.db.RefreshSessionListMetadata(agentType, sess)
		}
	}

	if authoritativeReader != nil {
		if authoritativeEnvelope == nil {
			snapshotStarted := time.Now()
			envelope, readErr := authoritativeReader.ReadIndexSnapshotEnvelope(ctx, sess)
			detailElapsed = time.Since(snapshotStarted)
			if readErr != nil {
				return ix.handleReadFailure(r, agentType, sess, readErr)
			}
			if validation := model.ValidateIndexSnapshotEnvelope(envelope); !validation.OK() {
				return false, fmt.Errorf("validate authoritative index snapshot: %+v", validation.Issues)
			}
			authoritativeEnvelope = envelope
		}
		renderElapsed = 0
		detail = authoritativeEnvelope.Detail
		renderEvents = authoritativeEnvelope.RenderEvents
	} else if snapshotReader, ok := r.(reader.IndexSnapshotReader); ok {
		snapshotStarted := time.Now()
		var err error
		detail, renderEvents, err = snapshotReader.ReadIndexSnapshot(ctx, sess)
		detailElapsed = time.Since(snapshotStarted)
		renderElapsed = 0
		if err != nil {
			return ix.handleReadFailure(r, agentType, sess, err)
		}
	} else {
		detailStarted := time.Now()
		var err error
		detail, err = r.GetSession(sess.ID)
		detailElapsed = time.Since(detailStarted)
		if err != nil {
			return ix.handleReadFailure(r, agentType, sess, err)
		}
		renderStarted := time.Now()
		renderEvents, err = r.GetRenderEvents(sess.ID)
		renderElapsed = time.Since(renderStarted)
		if err != nil {
			return false, fmt.Errorf("get render events: %w", err)
		}
	}
	if detail == nil {
		return false, fmt.Errorf("get session %s: reader returned nil detail", sess.ID)
	}

	persisted := sess
	persisted.AgentType = agentType
	applyDetailMetadata(&persisted, detail.Session)

	// Collaboration runs before the shared snapshot commit: a collaboration
	// failure aborts the session (watermark not advanced) so the next cycle
	// retries. The previous complete graph is preserved either way.
	collabStarted := time.Now()
	if err := ix.indexCollaboration(ctx, r, persisted, revision, collabCurrentKnown, collabCurrent); err != nil {
		return false, err
	}
	collabElapsed := time.Since(collabStarted)

	turns := buildTurnTexts(persisted, detail, renderEvents)
	writeStarted := time.Now()
	// Atomic metadata + turns + provenance so list/detail never mix revisions.
	if err := ix.db.ReplaceSessionSnapshot(db.SessionSnapshotWrite{
		AgentType:           agentType,
		Session:             persisted,
		TurnCount:           detail.TurnCount,
		HistoricalTurnCount: detail.HistoricalTurnCount,
		RolledBackTurnCount: detail.RolledBackTurnCount,
		MessageCount:        persisted.MessageCount,
		Turns:               turns,
		Provenance:          detail.Provenance,
		Revision:            revision,
	}); err != nil {
		return false, fmt.Errorf("replace session snapshot: %w", err)
	}
	sourceRevision := creationEvidenceSourceRevision(authoritativeEnvelope, agentType, sess.ID, revision)
	creationEvidence := changeevidence.ExtractCreationEvidence(renderEvents, sourceRevision)
	if err := ix.db.ReplaceSessionChangeRequestCreationEvidence(
		agentType, sess.ID, sourceRevision, creationEvidence,
	); err != nil {
		_ = ix.db.ClearSessionWatermark(agentType, sess.ID)
		return false, fmt.Errorf("index Change Request creation evidence: %w", err)
	}
	if authoritativeEnvelope != nil && ix.git != nil {
		if err := ix.indexGitEvidence(ctx, authoritativeReader, persisted, authoritativeEnvelope); err != nil {
			_ = ix.db.ClearSessionWatermark(agentType, sess.ID)
			return false, fmt.Errorf("index Git evidence: %w", err)
		}
		ix.gitAttempted.Store(agentType+"\x00"+sess.ID, struct{}{})
	}
	// Structured read failures with metadata-only / unsupported provenance may
	// still surface via detail.Provenance without body; handled above.
	log.Printf("[indexer] %s/%s: indexed %d row(s) in %s (detail=%s render=%s collaboration=%s write=%s)",
		agentType, sess.ID, len(turns), time.Since(started).Round(time.Millisecond), detailElapsed.Round(time.Millisecond),
		renderElapsed.Round(time.Millisecond), collabElapsed.Round(time.Millisecond), time.Since(writeStarted).Round(time.Millisecond))
	return true, nil
}

func creationEvidenceSourceRevision(envelope *model.IndexSnapshotEnvelope, agentType, sessionID string, revision int64) string {
	if envelope != nil && envelope.SourceRevision != "" {
		return envelope.SourceRevision
	}
	return fmt.Sprintf("index:%s:%s:%d", agentType, sessionID, revision)
}

// handleReadFailure maps typed SessionReadError into persisted provenance
// without inventing complete, and without bulk-missing behavior.
func (ix *Indexer) handleReadFailure(r reader.BaseSessionReader, agentType string, sess model.Session, err error) (bool, error) {
	sre, ok := reader.AsSessionReadError(err)
	if !ok {
		return false, fmt.Errorf("get session: %w", err)
	}
	now := time.Now().UTC()
	adapterRev := 0
	if def, ok := reader.AgentDefinition(agentType); ok && def.AdapterRevision > 0 {
		adapterRev = def.AdapterRevision
	} else if def, ok := reader.AgentDefinition(r.AgentType()); ok && def.AdapterRevision > 0 {
		adapterRev = def.AdapterRevision
	}

	state := model.RecordMetadataOnly
	reason := sre.ReasonCode
	switch sre.Kind {
	case reader.ReadSourceMissing:
		// Ensure session meta exists before tombstone (MarkSessionsSourceMissing
		// only updates rows already in sessions). Seed adapter rev + sources so
		// first-time missing (no prior provenance row) does not invent rev=1.
		if err := ix.db.UpsertSessionMetaWithHistoryLineageAndProvider(
			agentType, sess.ID, sess.CWD, sess.Repository, sess.Branch,
			sess.Project, sess.Name, sess.ModelName, sess.ModelProvider, sess.ResumeID,
			sess.ParentSessionID, sess.AgentPath, sess.IsSubagent,
			sess.TurnCount, sess.HistoricalTurnCount, sess.RolledBackTurnCount, sess.MessageCount,
			sess.CreatedAt, sess.UpdatedAt,
		); err != nil {
			return false, err
		}
		facts := provenance.Build(provenance.Input{
			StateOverride:     model.RecordSourceMissing,
			ReasonCode:        reason,
			CapturedAt:        now,
			AdapterRevision:   adapterRev,
			Sources:           sre.Sources,
			Warnings:          sre.Warnings,
			HasReplayableBody: false,
			MissingSince:      &now,
		})
		if _, markErr := ix.db.MarkSessionSourceMissingWithFacts(agentType, sess.ID, now, facts); markErr != nil {
			return false, markErr
		}
		return true, nil
	case reader.ReadFormatUnsupported:
		state = model.RecordParserUnsupported
		if reason == "" {
			reason = model.WarnUnsupportedSchema
		}
	case reader.ReadMetadataOnly:
		state = model.RecordMetadataOnly
		if reason == "" {
			reason = "no_body"
		}
	case reader.ReadSourceUnreadable, reader.ReadParseFailed:
		state = model.RecordMetadataOnly
		if reason == "" {
			reason = string(sre.Kind)
		}
	}
	// Use shared Build so warnings are aggregated and warning_summary is filled.
	// HasReplayableBody is false: these paths could not produce a body.
	prov := provenance.Build(provenance.Input{
		StateOverride:     state,
		ReasonCode:        reason,
		CapturedAt:        now,
		AdapterRevision:   adapterRev,
		Sources:           sre.Sources,
		Warnings:          sre.Warnings,
		HasReplayableBody: false,
	})
	if err := ix.db.UpsertSessionMetaWithHistoryLineageAndProvider(
		agentType, sess.ID, sess.CWD, sess.Repository, sess.Branch,
		sess.Project, sess.Name, sess.ModelName, sess.ModelProvider, sess.ResumeID,
		sess.ParentSessionID, sess.AgentPath, sess.IsSubagent,
		sess.TurnCount, sess.HistoricalTurnCount, sess.RolledBackTurnCount, sess.MessageCount,
		sess.CreatedAt, sess.UpdatedAt,
	); err != nil {
		return false, err
	}
	if err := ix.db.UpsertProvenance(agentType, sess.ID, prov, sess.UpdatedAt.UnixNano()); err != nil {
		return false, err
	}
	// Non-replayable failures must not leave prior FTS hits unmarked.
	if err := ix.db.ClearSessionSearchIndex(agentType, sess.ID); err != nil {
		return false, err
	}
	if err := ix.db.ClearSessionWatermark(agentType, sess.ID); err != nil {
		return false, err
	}
	return true, nil
}

// collaborationCurrent reports whether the stored collaboration graph already
// matches the session revision. Readers without the optional
// reader.CollaborationReader interface and non-root (backing child) sessions
// are always "current": no graph is fabricated or indexed for them.
func (ix *Indexer) collaborationCurrent(r reader.BaseSessionReader, agentType string, sess model.Session, revision int64) (bool, error) {
	if _, ok := r.(reader.CollaborationReader); !ok || sess.IsSubagent {
		return true, nil
	}
	collabRev, graphStatus, exists, err := ix.db.CollaborationIndexState(agentType, sess.ID)
	if err != nil {
		return false, fmt.Errorf("collaboration revision: %w", err)
	}
	return exists && collabRev == revision && graphStatus == db.CollaborationGraphOK, nil
}

// indexCollaboration parses and persists the normalized collaboration graph
// for one root Session revision. It returns nil without any write when the
// reader lacks the optional interface, the session is a backing child (never
// a second collaboration root), or the stored revision is already current
// (explicit unchanged-revision skip). Any parse, validation, or persistence
// failure preserves the previous complete graph, marks it stale (contract
// stale_graph_retained semantics), and surfaces an error so the shared turn
// watermark is not advanced.
func (ix *Indexer) indexCollaboration(ctx context.Context, r reader.BaseSessionReader, sess model.Session, revision int64, currentKnown, current bool) error {
	cr, ok := r.(reader.CollaborationReader)
	if !ok || sess.IsSubagent {
		return nil
	}
	agentType := r.AgentType()

	if currentKnown && current {
		return nil
	}
	if !currentKnown {
		current, err := ix.collaborationCurrent(r, agentType, sess, revision)
		if err != nil {
			return err
		}
		if current {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	graph, err := cr.ReadCollaboration(ctx, sess)
	if err != nil {
		ix.markCollaborationStale(agentType, sess.ID, err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read collaboration: %w", err)
	}
	if graph.RootAgentType != agentType || graph.RootSessionID != sess.ID {
		err := fmt.Errorf("collaboration graph root %s/%s does not match requested session %s/%s",
			graph.RootAgentType, graph.RootSessionID, agentType, sess.ID)
		ix.markCollaborationStale(agentType, sess.ID, err)
		return err
	}
	// Collaboration and turn indexing share the session revision; the store
	// advances it only when the replacement transaction commits.
	graph.Revision = revision
	if err := ix.db.ReplaceCollaborationGraph(graph); err != nil {
		ix.markCollaborationStale(agentType, sess.ID, err)
		return fmt.Errorf("replace collaboration graph: %w", err)
	}
	return nil
}

// markCollaborationStale flags the retained graph as the last complete
// revision. Best-effort: a marking failure is logged but never masks the
// original indexing error.
func (ix *Indexer) markCollaborationStale(agentType, sessionID string, cause error) {
	if err := ix.db.MarkCollaborationStale(agentType, sessionID, cause.Error()); err != nil {
		log.Printf("[indexer] %s/%s: mark collaboration stale: %v", agentType, sessionID, err)
	}
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

var urlPattern = regexp.MustCompile("https?://[^\\s<>\"`\\[\\]()]+")

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
			texts = appendAssistantTexts(texts, t.TurnIndex, "", s)
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
				texts = appendAssistantTexts(texts, idx, "[已回滚] ", s)
			}
		}
	}

	return texts
}

func appendAssistantTexts(texts []db.TurnText, turnIndex int, prefix, content string) []db.TurnText {
	texts = append(texts, db.TurnText{
		TurnIndex: turnIndex,
		Role:      "assistant",
		Content:   prefix + truncateRunes(content, maxAssistantRunes),
	})
	if links := extractURLs(content); links != "" {
		texts = append(texts, db.TurnText{
			TurnIndex: turnIndex,
			Role:      "link",
			Content:   links,
		})
	}
	return texts
}

// extractURLs preserves URLs that may occur after a capped transcript field.
// URLs are compact, high-signal search keys, so indexing them separately keeps
// the general transcript cap while retaining direct-link recall.
func extractURLs(s string) string {
	matches := urlPattern.FindAllString(s, -1)
	if len(matches) == 0 {
		return ""
	}
	for i, match := range matches {
		matches[i] = strings.TrimRight(match, ".,;:!?")
	}
	urls := uniqueNonEmpty(matches)
	if len(urls) > maxURLsPerTurn {
		urls = urls[:maxURLsPerTurn]
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
