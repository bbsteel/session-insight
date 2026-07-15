package server

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/analytics"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/llm"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/render"
)

// handleListSessions serves the sidebar list straight from the SQLite index
// (populated by the background indexer) — no per-request disk scan. Freshness
// lags at most one index round; the indexer bumps the list revision and pings
// SSE after each round that changed data, so clients refetch right after new
// data lands.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	agentFilter := r.URL.Query().Get("agent")

	// 内容修订没变的重拉直接 304（浏览器 HTTP 缓存自动带 If-None-Match）。
	etag := fmt.Sprintf(`"sessions-%d-%d"`, s.startNano, s.listRev.Load())
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Vary", "Accept-Encoding")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	list, err := s.DB.ListSessionSummaries(agentFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	bookmarkNotes, err := s.bookmarkNotes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	overrides := s.titleOverrides()
	sessions := make([]SessionSummary, 0, len(list))
	for _, sess := range list {
		// AI generations drive local agent CLIs from a scratch temp dir;
		// those CLIs log their own sessions, which are noise in this list.
		if llm.IsScratchCWD(sess.CWD) {
			continue
		}
		// Collaborative Codex children inherit the parent's conversation and
		// therefore look like same-title duplicates. They remain indexed for
		// global search and future parent/child navigation, but are not roots.
		if sess.IsSubagent {
			continue
		}
		key := db.BookmarkKey(sess.AgentType, sess.ID)
		bookmarkNote, bookmarked := bookmarkNotes[key]
		sess.Bookmarked = bookmarked
		if t, ok := overrides[key]; ok {
			sess.Name = t
		}
		summary := sessionToSummary(sess)
		if bookmarked {
			summary.BookmarkNote = bookmarkNote
		}
		sessions = append(sessions, summary)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Total-Count", fmt.Sprintf("%d", len(sessions)))
	writeJSONMaybeGzip(w, r, sessions)
}

// writeJSONMaybeGzip 编码 JSON；客户端支持时 gzip 压缩（几百 KB 的列表
// payload 压到几十 KB）。
func writeJSONMaybeGzip(w http.ResponseWriter, r *http.Request, v any) {
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		json.NewEncoder(gz).Encode(v) //nolint:errcheck // 网络写失败无从补救
		return
	}
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	for _, reader := range s.Readers {
		detail, err := reader.GetSession(id)
		if err != nil {
			continue
		}
		if detail == nil {
			continue
		}

		// Liveness is a serve-time presence heuristic; compute it here so all
		// agents share one definition (see model.IsSessionLive).
		detail.IsLive = model.IsSessionLive(detail.UpdatedAt)
		if s.DB != nil {
			bookmarked, err := s.DB.IsBookmarked(detail.AgentType, detail.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			detail.Bookmarked = bookmarked
			if t, _ := s.DB.TitleOverride(detail.AgentType, detail.ID); t != "" {
				detail.Name = t
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(detail)
		return
	}

	http.Error(w, "session not found", http.StatusNotFound)
}

// titleOverrides returns the display-title override map keyed by
// db.BookmarkKey, or nil when unavailable — callers treat nil as "no
// overrides" so a DB hiccup degrades to original names, never an error.
func (s *Server) titleOverrides() map[string]string {
	if s.DB == nil {
		return nil
	}
	m, err := s.DB.TitleOverrides()
	if err != nil {
		return nil
	}
	return m
}

func sessionToSummary(s model.Session) SessionSummary {
	return SessionSummary{
		ID:                  s.ID,
		AgentType:           s.AgentType,
		Name:                s.Name,
		ModelName:           s.ModelName,
		ModelProvider:       s.ModelProvider,
		Repository:          s.Repository,
		Branch:              s.Branch,
		Project:             s.Project,
		CWD:                 s.CWD,
		ResumeID:            s.ResumeID,
		TurnCount:           s.TurnCount,
		HistoricalTurnCount: s.HistoricalTurnCount,
		RolledBackTurnCount: s.RolledBackTurnCount,
		MessageCount:        s.MessageCount,
		IsLive:              model.IsSessionLive(s.UpdatedAt),
		Bookmarked:          s.Bookmarked,
		CreatedAt:           model.FormatTime(s.CreatedAt),
		UpdatedAt:           model.FormatTime(s.UpdatedAt),
	}
}

// handleLiveRevision returns a cheap stat-level change marker for live-tail
// polling. 404 when no reader owns the session or its reader lacks the
// LiveRevisionProvider capability — the frontend then simply skips live tail.
func (s *Server) handleLiveRevision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, rd := range s.Readers {
		provider, ok := rd.(reader.LiveRevisionProvider)
		if !ok {
			continue
		}
		rev, err := provider.LiveRevision(id)
		if err != nil {
			continue
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"revision": rev})
		return
	}
	http.Error(w, "live revision unavailable", http.StatusNotFound)
}

func (s *Server) bookmarkNotes() (map[string]string, error) {
	if s.DB == nil {
		return map[string]string{}, nil
	}
	return s.DB.BookmarkNotes()
}

func (s *Server) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SessionSummary{})
		return
	}
	sessions, err := s.DB.ListBookmarkedSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	overrides := s.titleOverrides()
	summaries := make([]SessionSummary, 0, len(sessions))
	for _, bm := range sessions {
		if t, ok := overrides[db.BookmarkKey(bm.AgentType, bm.SessionID)]; ok {
			bm.Name = t
		}
		ss := SessionSummary{
			ID:            bm.SessionID,
			AgentType:     bm.AgentType,
			BookmarkNote:  bm.Note,
			Name:          bm.Name,
			ModelName:     bm.ModelName,
			ModelProvider: bm.ModelProvider,
			Repository:    bm.Repository,
			Project:       bm.Project,
			CWD:           bm.CWD,
			TurnCount:     bm.TurnCount,
			MessageCount:  bm.MessageCount,
			Branch:        bm.Branch,
			IsLive:        false,
			Bookmarked:    true,
			CreatedAt:     bm.BookmarkCreatedAt,
			UpdatedAt:     bm.SessionUpdatedAt,
		}
		// Legacy bookmark without metadata: hydrate from reader once and
		// backfill so subsequent requests are pure SQL.
		if bm.Name == "" {
			for _, rd := range s.Readers {
				if rd.AgentType() != bm.AgentType {
					continue
				}
				if detail, e := rd.GetSession(bm.SessionID); e == nil && detail != nil {
					ss = sessionToSummary(detail.Session)
					ss.Bookmarked = true
					ss.BookmarkNote = bm.Note
					s.DB.UpdateBookmarkMeta(db.BookmarkedSession{
						AgentType:        bm.AgentType,
						SessionID:        bm.SessionID,
						Name:             ss.Name,
						ModelName:        ss.ModelName,
						ModelProvider:    ss.ModelProvider,
						Repository:       ss.Repository,
						Project:          ss.Project,
						CWD:              ss.CWD,
						PreviewText:      detail.Session.PreviewText,
						TurnCount:        ss.TurnCount,
						MessageCount:     ss.MessageCount,
						Branch:           ss.Branch,
						SessionUpdatedAt: ss.UpdatedAt,
					})
				}
				break
			}
		}
		summaries = append(summaries, ss)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (s *Server) handleAddBookmark(w http.ResponseWriter, r *http.Request) {
	s.handleBookmarkWrite(w, r, true)
}

func (s *Server) handleRemoveBookmark(w http.ResponseWriter, r *http.Request) {
	s.handleBookmarkWrite(w, r, false)
}

func (s *Server) handleBookmarkWrite(w http.ResponseWriter, r *http.Request, add bool) {
	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	id := r.PathValue("id")
	agentType := r.URL.Query().Get("agent")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	if agentType == "" {
		http.Error(w, "missing agent", http.StatusBadRequest)
		return
	}

	var err error
	if add {
		err = s.DB.AddBookmark(agentType, id)
		// Store session metadata so listing bookmarks is a pure SQL query.
		if err == nil {
			for _, rd := range s.Readers {
				if rd.AgentType() != agentType {
					continue
				}
				if detail, e := rd.GetSession(id); e == nil && detail != nil {
					ss := sessionToSummary(detail.Session)
					s.DB.UpdateBookmarkMeta(db.BookmarkedSession{
						AgentType:        agentType,
						SessionID:        id,
						Name:             ss.Name,
						ModelName:        ss.ModelName,
						ModelProvider:    ss.ModelProvider,
						Repository:       ss.Repository,
						Project:          ss.Project,
						CWD:              ss.CWD,
						PreviewText:      detail.Session.PreviewText,
						TurnCount:        ss.TurnCount,
						MessageCount:     ss.MessageCount,
						Branch:           ss.Branch,
						SessionUpdatedAt: ss.UpdatedAt,
					})
				}
				break
			}
		}
	} else {
		err = s.DB.RemoveBookmark(agentType, id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.bumpListRev()
	w.WriteHeader(http.StatusNoContent)
}

type bookmarkNoteRequest struct {
	Note string `json:"note"`
}

func (s *Server) handleUpdateBookmarkNote(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	id := r.PathValue("id")
	agentType := r.URL.Query().Get("agent")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	if agentType == "" {
		http.Error(w, "missing agent", http.StatusBadRequest)
		return
	}
	bookmarked, err := s.DB.IsBookmarked(agentType, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !bookmarked {
		http.Error(w, "bookmark not found", http.StatusNotFound)
		return
	}
	var req bookmarkNoteRequest
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	note := strings.TrimSpace(req.Note)
	if len([]rune(note)) > 2000 {
		http.Error(w, "note is too long", http.StatusBadRequest)
		return
	}
	if err := s.DB.UpdateBookmarkNote(agentType, id, note); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.bumpListRev()
	w.WriteHeader(http.StatusNoContent)
}

// handleRenderSession returns the Phase 2 ANSI terminal text for a session,
// for xterm.js to write() directly once the frontend TerminalPanel exists.
//
// Goes through the shared reader.BaseSessionReader.RenderANSI interface
// method rather than a concrete-type assertion — Codex/Copilot return a
// clear "not yet implemented" error from their stub implementations, so
// this loop just tries each reader in turn the same way handleGetSession
// already does, with no special-casing for which agent type is involved.
func (s *Server) handleRenderSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	opts := render.ParseTimestampKinds(r.URL.Query().Get("ts"))

	var lastErr error
	for _, rd := range s.Readers {
		events, err := rd.GetRenderEvents(id)
		if err != nil {
			lastErr = err
			continue
		}
		ansi := render.FormatEventsOpts(events, cols, opts)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(ansi))
		return
	}

	msg := "session not found or rendering not supported for its agent type"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	http.Error(w, msg, http.StatusNotFound)
}

func (s *Server) handleSessionEdits(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	for _, rd := range s.Readers {
		events, err := rd.GetRenderEvents(id)
		if err != nil {
			continue
		}
		var edits []model.EditCall
		for _, evt := range events {
			if evt.Type != "ToolInvocation" {
				continue
			}
			edits = append(edits, model.ExtractEditCalls(evt)...)
		}
		if edits == nil {
			edits = []model.EditCall{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(edits)
		return
	}
	http.Error(w, "session not found", http.StatusNotFound)
}

// handleSessionToolOutputs returns the full content of every tool output
// segment the terminal render truncates, in document order. The frontend's
// "点击展开" affordance indexes into this array via the "trunc" positions'
// payload.output_index (both sides enumerate segments identically).
func (s *Server) handleSessionToolOutputs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	for _, rd := range s.Readers {
		events, err := rd.GetRenderEvents(id)
		if err != nil {
			continue
		}
		outputs := render.CollectTruncatedOutputs(events)
		if outputs == nil {
			outputs = []render.TruncatedOutput{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(outputs)
		return
	}
	http.Error(w, "session not found", http.StatusNotFound)
}

type positionsResponse struct {
	SessionID  string              `json:"session_id"`
	AgentType  string              `json:"agent_type"`
	Revision   int64               `json:"revision"`
	Cols       int                 `json:"cols"`
	TotalLines int                 `json:"total_lines"`
	Positions  []positionEntryJSON `json:"positions"`
}

type positionEntryJSON struct {
	PositionKey string         `json:"position_key"`
	Kind        string         `json:"kind"`
	TurnIndex   int            `json:"turn_index"`
	LineStart   int            `json:"line_start"`
	LineEnd     *int           `json:"line_end,omitempty"`
	Label       string         `json:"label"`
	Severity    string         `json:"severity,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
}

func positionCacheToResponse(c *db.PositionCache) positionsResponse {
	entries := make([]positionEntryJSON, 0, len(c.Positions))
	for _, p := range c.Positions {
		entries = append(entries, positionEntryJSON{
			PositionKey: p.PositionKey,
			Kind:        p.Kind,
			TurnIndex:   p.TurnIndex,
			LineStart:   p.LineStart,
			LineEnd:     p.LineEnd,
			Label:       p.Label,
			Severity:    p.Severity,
			Payload:     p.Payload,
		})
	}
	return positionsResponse{
		SessionID:  c.SessionID,
		AgentType:  c.AgentType,
		Revision:   c.Revision,
		Cols:       c.Cols,
		TotalLines: c.TotalLines,
		Positions:  entries,
	}
}

// positionsRevision derives the position-cache key from session content,
// renderer layout version, and render options. Hashed rather than additive:
// SessionRevision is a UnixNano timestamp, so adding a small option mask
// could collide with a genuinely newer revision of the same session.
func positionsRevision(sess model.Session, opts render.Options) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%d|%d", model.SessionRevision(sess), render.FormatVersion, opts.Mask())
	return int64(h.Sum64() &^ (1 << 63))
}

func (s *Server) handleSessionPositions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	if cols <= 0 {
		cols = render.TermWidth
	}

	// Find session and its reader.
	var sess *model.Session
	var foundReader interface {
		GetRenderEvents(id string) ([]model.RenderEvent, error)
	}
	for _, rd := range s.Readers {
		detail, err := rd.GetSession(id)
		if err != nil || detail == nil {
			continue
		}
		s := detail.Session
		sess = &s
		foundReader = rd
		break
	}
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	opts := render.ParseTimestampKinds(r.URL.Query().Get("ts"))

	// Renderer layout changes shift line numbers, so the cache key must
	// change with FormatVersion and render options as well as with session
	// content.
	revision := positionsRevision(*sess, opts)

	// Cache hit: return immediately.
	if cached, err := s.DB.GetPositionCache(sess.AgentType, id, revision, cols); err == nil && cached != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(positionCacheToResponse(cached))
		return
	}

	// Cache miss: generate, timeout after 1500ms.
	type buildResult struct {
		cache *db.PositionCache
		err   error
	}
	ch := make(chan buildResult, 1)

	// Capture for goroutine (avoids closing over loop variable).
	agentType := sess.AgentType
	dbRef := s.DB

	go func() {
		events, err := foundReader.GetRenderEvents(id)
		if err != nil {
			ch <- buildResult{err: err}
			return
		}
		_, rpositions := render.FormatEventsWithPositionsOpts(events, cols, opts)

		totalLines := 0
		entries := make([]db.PositionEntry, 0, len(rpositions))
		for _, rp := range rpositions {
			entries = append(entries, db.PositionEntry{
				PositionKey: rp.PositionKey,
				Kind:        rp.Kind,
				TurnIndex:   rp.TurnIndex,
				LineStart:   rp.LineStart,
				LineEnd:     rp.LineEnd,
				Label:       rp.Label,
				Severity:    rp.Severity,
				Payload:     rp.Payload,
			})
			if rp.LineStart > totalLines {
				totalLines = rp.LineStart
			}
		}
		totalLines++ // convert max line_start to total count

		if err := dbRef.SavePositionCache(agentType, id, revision, cols, totalLines, entries); err != nil {
			ch <- buildResult{err: err}
			return
		}
		cached := &db.PositionCache{
			AgentType:  agentType,
			SessionID:  id,
			Revision:   revision,
			Cols:       cols,
			TotalLines: totalLines,
			Positions:  entries,
		}
		ch <- buildResult{cache: cached}
	}()

	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()

	select {
	case res := <-ch:
		if res.err != nil {
			http.Error(w, res.err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(positionCacheToResponse(res.cache))
	case <-timer.C:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "building"})
	}
}

func (s *Server) handleSessionAnalytics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, reader := range s.Readers {
		detail, err := reader.GetSession(id)
		if err != nil || detail == nil {
			continue
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(analytics.Compute(detail))
		return
	}
	http.Error(w, "session not found", http.StatusNotFound)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	type AgentInfo struct {
		Type         string `json:"type"`
		DisplayName  string `json:"display_name"`
		SessionCount int    `json:"session_count"`
		CanDelete    bool   `json:"can_delete"`
	}

	var agents []AgentInfo
	for _, rd := range s.Readers {
		sessions, _ := rd.ListSessions()
		count := 0
		if sessions != nil {
			for _, sess := range sessions {
				if !sess.IsSubagent {
					count++
				}
			}
		}
		_, canDelete := rd.(reader.SessionDeleter)
		agents = append(agents, AgentInfo{
			Type:         rd.AgentType(),
			DisplayName:  rd.DisplayName(),
			SessionCount: count,
			CanDelete:    canDelete,
		})
	}

	if agents == nil {
		agents = []AgentInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

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

	// Resolve session metadata from the sessions table (populated by the
	// indexer). Falls back to ListSessions for any session not yet indexed.
	metas := make(map[string]db.SessionMeta)
	if s.DB != nil && len(results) > 0 {
		keys := make([]struct{ AgentType, SessionID string }, 0, len(results))
		seen := make(map[string]bool, len(results))
		for _, r := range results {
			k := r.AgentType + "\x00" + r.SessionID
			if seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, struct{ AgentType, SessionID string }{r.AgentType, r.SessionID})
		}
		metas, err := s.DB.GetSessionMetas(keys)
		if err != nil {
			log.Printf("search: GetSessionMetas: %v", err)
		}
		if metas == nil {
			metas = make(map[string]db.SessionMeta)
		}

		// Fallback: any keys not found in DB, load from readers.
		missing := false
		for _, k := range keys {
			if _, ok := metas[k.AgentType+"\x00"+k.SessionID]; !ok {
				missing = true
				break
			}
		}
		if missing {
			for _, reader := range s.Readers {
				list, err := reader.ListSessions()
				if err != nil {
					continue
				}
				for _, sess := range list {
					key := sess.AgentType + "\x00" + sess.ID
					if _, ok := metas[key]; ok {
						continue
					}
					if seen[key] {
						metas[key] = db.SessionMeta{
							Project:   sess.Project,
							Name:      sess.Name,
							UpdatedAt: sess.UpdatedAt,
						}
					}
				}
			}
		}
	}

	type result struct {
		SessionID string `json:"session_id"`
		AgentType string `json:"agent_type"`
		Project   string `json:"project"`
		Name      string `json:"name"`
		UpdatedAt string `json:"updated_at"`
		Match     string `json:"match"`
	}
	out := make([]result, 0, len(results))
	for _, r := range results {
		meta := metas[r.AgentType+"\x00"+r.SessionID]
		updatedAt := ""
		if !meta.UpdatedAt.IsZero() {
			updatedAt = model.FormatTime(meta.UpdatedAt)
		}
		out = append(out, result{
			SessionID: r.SessionID,
			AgentType: r.AgentType,
			Project:   meta.Project,
			Name:      meta.Name,
			UpdatedAt: updatedAt,
			Match:     r.Match,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func countDone(todos []model.Todo) int {
	n := 0
	for _, t := range todos {
		if t.Status == "done" {
			n++
		}
	}
	return n
}

func (s *Server) handleExportSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, reader := range s.Readers {
		detail, err := reader.GetSession(id)
		if err != nil || detail == nil {
			continue
		}

		var md strings.Builder
		md.WriteString(fmt.Sprintf("# %s\n\n", detail.Name))
		if detail.Repository != "" {
			md.WriteString(fmt.Sprintf("**Repo:** %s", detail.Repository))
			if detail.Branch != "" {
				md.WriteString(fmt.Sprintf(" @%s", detail.Branch))
			}
			md.WriteString("\n")
		}
		md.WriteString(fmt.Sprintf("**Model:** %s | **Turns:** %d | **Todos:** %d/%d done\n\n",
			detail.ModelName, len(detail.Turns),
			countDone(detail.Todos), len(detail.Todos)))
		md.WriteString("---\n\n")

		for _, t := range detail.Turns {
			md.WriteString(fmt.Sprintf("## Turn %d\n\n", t.TurnIndex))
			if t.UserMessage != "" {
				md.WriteString(fmt.Sprintf("**User:** %s\n\n", t.UserMessage))
			}
			if t.AssistantMessage != "" {
				md.WriteString(fmt.Sprintf("**Assistant:** %s\n\n", t.AssistantMessage))
			}
			if len(t.ToolNames) > 0 {
				md.WriteString(fmt.Sprintf("*Tools: %s*\n\n", strings.Join(t.ToolNames, ", ")))
			}
			md.WriteString(fmt.Sprintf("*%d tokens | %s*\n\n---\n\n",
				t.TokenUsage.PromptTokens+t.TokenUsage.CompletionTokens,
				formatDur(t.DurationMs)))
		}

		filename := fmt.Sprintf("session-%s.md", id[:8])
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Write([]byte(md.String()))
		return
	}
	http.Error(w, "session not found", http.StatusNotFound)
}

func formatDur(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm%ds", ms/60000, (ms%60000)/1000)
}
