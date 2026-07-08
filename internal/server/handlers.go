package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"session-insight/internal/analytics"
	"session-insight/internal/db"
	"session-insight/internal/reader"
	"session-insight/internal/model"
	"session-insight/internal/render"
)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	agentFilter := r.URL.Query().Get("agent")
	bookmarkSet, err := s.bookmarkSet()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var sessions []SessionSummary
	for _, reader := range s.Readers {
		if agentFilter != "" && reader.AgentType() != agentFilter {
			continue
		}

		list, err := reader.ListSessions()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, s := range list {
			s.Bookmarked = bookmarkSet[db.BookmarkKey(s.AgentType, s.ID)]
			sessions = append(sessions, sessionToSummary(s))
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})

	if sessions == nil {
		sessions = []SessionSummary{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Total-Count", fmt.Sprintf("%d", len(sessions)))
	json.NewEncoder(w).Encode(sessions)
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
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(detail)
		return
	}

	http.Error(w, "session not found", http.StatusNotFound)
}

func sessionToSummary(s model.Session) SessionSummary {
	return SessionSummary{
		ID:           s.ID,
		AgentType:    s.AgentType,
		Name:         s.Name,
		ModelName:    s.ModelName,
		Repository:   s.Repository,
		Branch:       s.Branch,
		Project:      s.Project,
		CWD:          s.CWD,
		ResumeID:     s.ResumeID,
		PreviewText:  s.PreviewText,
		TurnCount:    s.TurnCount,
		MessageCount: s.MessageCount,
		IsLive:       model.IsSessionLive(s.UpdatedAt),
		Bookmarked:   s.Bookmarked,
		CreatedAt:    s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
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

func (s *Server) bookmarkSet() (map[string]bool, error) {
	if s.DB == nil {
		return map[string]bool{}, nil
	}
	return s.DB.BookmarkSet()
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

	summaries := make([]SessionSummary, 0, len(sessions))
	for _, bm := range sessions {
		ss := SessionSummary{
			ID:           bm.SessionID,
			AgentType:    bm.AgentType,
			Name:         bm.Name,
			ModelName:    bm.ModelName,
			Repository:   bm.Repository,
			Project:      bm.Project,
			CWD:          bm.CWD,
			PreviewText:  bm.PreviewText,
			TurnCount:    bm.TurnCount,
			MessageCount: bm.MessageCount,
			Branch:       bm.Branch,
			IsLive:       false,
			Bookmarked:   true,
			CreatedAt:    bm.BookmarkCreatedAt,
			UpdatedAt:    bm.SessionUpdatedAt,
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
					s.DB.UpdateBookmarkMeta(db.BookmarkedSession{
						AgentType:        bm.AgentType,
						SessionID:        bm.SessionID,
						Name:             ss.Name,
						ModelName:        ss.ModelName,
						Repository:       ss.Repository,
						Project:          ss.Project,
						CWD:              ss.CWD,
						PreviewText:      ss.PreviewText,
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
						Repository:       ss.Repository,
						Project:          ss.Project,
						CWD:              ss.CWD,
						PreviewText:      ss.PreviewText,
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

	var lastErr error
	for _, rd := range s.Readers {
		ansi, err := rd.RenderANSI(id, cols)
		if err != nil {
			lastErr = err
			continue
		}
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

	// Renderer layout changes shift line numbers, so the cache key must
	// change with FormatVersion as well as with session content.
	revision := model.SessionRevision(*sess) + render.FormatVersion

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
		_, rpositions := render.FormatEventsWithPositions(events, cols)

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
	}

	var agents []AgentInfo
	for _, reader := range s.Readers {
		sessions, _ := reader.ListSessions()
		count := 0
		if sessions != nil {
			count = len(sessions)
		}
		agents = append(agents, AgentInfo{
			Type:         reader.AgentType(),
			DisplayName:  reader.DisplayName(),
			SessionCount: count,
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

	// 反查会话元数据：命中结果只带 (agent_type, session_id)，
	// project / 会话名 / 更新时间来自各 reader 的会话列表
	type sessionMeta struct {
		project   string
		name      string
		updatedAt time.Time
	}
	metas := make(map[string]sessionMeta)
	if len(results) > 0 {
		for _, reader := range s.Readers {
			list, err := reader.ListSessions()
			if err != nil {
				continue
			}
			for _, sess := range list {
				metas[sess.AgentType+"\x00"+sess.ID] = sessionMeta{
					project:   sess.Project,
					name:      sess.Name,
					updatedAt: sess.UpdatedAt,
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
		if !meta.updatedAt.IsZero() {
			updatedAt = meta.updatedAt.Format("2006-01-02T15:04:05Z")
		}
		out = append(out, result{
			SessionID: r.SessionID,
			AgentType: r.AgentType,
			Project:   meta.project,
			Name:      meta.name,
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
