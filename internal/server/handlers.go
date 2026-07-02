package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"session-insight/internal/db"
	"session-insight/internal/model"
	"session-insight/internal/render"
	"strings"
)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	agentFilter := r.URL.Query().Get("agent")

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
			sessions = append(sessions, SessionSummary{
				ID:           s.ID,
				AgentType:    s.AgentType,
				Name:         s.Name,
				Repository:   s.Repository,
				Branch:       s.Branch,
				Project:      s.Project,
				PreviewText:  s.PreviewText,
				TurnCount:    s.TurnCount,
				MessageCount: s.MessageCount,
				IsLive:       model.IsSessionLive(s.UpdatedAt),
				CreatedAt:    s.CreatedAt.Format("2006-01-02T15:04:05Z"),
				UpdatedAt:    s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			})
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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(detail)
		return
	}

	http.Error(w, "session not found", http.StatusNotFound)
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

type positionsResponse struct {
	SessionID string              `json:"session_id"`
	AgentType string              `json:"agent_type"`
	Revision  int64               `json:"revision"`
	Cols      int                 `json:"cols"`
	TotalLines int                `json:"total_lines"`
	Positions []positionEntryJSON `json:"positions"`
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

	revision := model.SessionRevision(*sess)

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

		type TurnToken struct {
			TurnIndex  int   `json:"turn_index"`
			Tokens     int64 `json:"tokens"`
			Duration   int64 `json:"duration_ms"`
			ToolCount  int   `json:"tool_count"`
			ErrorCount int   `json:"error_count"`
		}

		var totalPrompt, totalCompletion, totalCache int64
		var maxCumulative int64
		var cumul int64
		var totalTools, totalErrors int
		var timeline []TurnToken
		toolFreq := make(map[string]int)
		toolSuccess := make(map[string]int)
		toolTotal := make(map[string]int)
		skillFreq := make(map[string]int)

		modelName := detail.ModelName
		for _, t := range detail.Turns {
			tok := t.TokenUsage.PromptTokens + t.TokenUsage.CompletionTokens
			totalPrompt += t.TokenUsage.PromptTokens
			totalCompletion += t.TokenUsage.CompletionTokens
			totalCache += t.TokenUsage.CacheReadTokens
			totalTools += t.ToolCallCount
			totalErrors += t.ErrorCount
			cumul += tok
			if cumul > maxCumulative {
				maxCumulative = cumul
			}

			timeline = append(timeline, TurnToken{
				TurnIndex:  t.TurnIndex,
				Tokens:     tok,
				Duration:   t.DurationMs,
				ToolCount:  t.ToolCallCount,
				ErrorCount: t.ErrorCount,
			})

			for _, name := range t.ToolNames {
				toolFreq[name]++
			for _, td := range t.ToolDetails {
				toolTotal[td.Name]++
				if td.ExitCode == 0 {
					toolSuccess[td.Name]++
				}
			}
			for _, name := range t.Skills {
				skillFreq[name]++
			}
			}
		}

		pressurePct := 0.0
		ctxWindow := int64(estimateContext(modelName))
		if ctxWindow > 0 && maxCumulative > 0 {
			pressurePct = float64(maxCumulative) / float64(ctxWindow) * 100
		}
		cacheRate := 0.0
		if totalPrompt+totalCache > 0 {
			cacheRate = float64(totalCache) / float64(totalPrompt+totalCache) * 100
		}

		anomalyCount := detail.AnomalySummary.TotalAnomalies
		healthScore := 100
		healthScore -= detail.AnomalySummary.ToolFailures * 5
		healthScore -= detail.AnomalySummary.DurationSpikes * 5
		if detail.AnomalySummary.MissingShutdown {
			healthScore -= 20
		}
		if healthScore < 0 {
			healthScore = 0
		}
		healthGrade := "A"
		switch {
		case healthScore > 90: healthGrade = "A"
		case healthScore > 75: healthGrade = "B"
		case healthScore > 60: healthGrade = "C"
		case healthScore > 40: healthGrade = "D"
		default: healthGrade = "F"
		}
		totalTokens := totalPrompt + totalCompletion
		tokenEfficiency := 0.0
		if totalTokens > 0 && len(detail.Turns) > 0 {
			tokenEfficiency = float64(totalTokens) / float64(len(detail.Turns))
		}

		resp := map[string]any{
			"total_tokens":      totalTokens,
			"prompt_tokens":     totalPrompt,
			"completion_tokens": totalCompletion,
			"cache_read_tokens": totalCache,
			"cache_hit_rate":    cacheRate,
			"total_tools":       totalTools,
			"total_errors":      totalErrors,
			"anomaly_count":     anomalyCount,
			"health_score":      healthScore,
			"health_grade":      healthGrade,
			"turn_count":        len(detail.Turns),
			"token_efficiency":  tokenEfficiency,
			"timeline":          timeline,
			"tool_freq":         toolFreq,
			"tool_success":       toolSuccess,
			"tool_total":         toolTotal,
			"skill_freq":        skillFreq,
			"todo_count":        len(detail.Todos),
			"todos":             detail.Todos,
			"todo_done":          countDone(detail.Todos),
			"context_window":    estimateContext(modelName),
			"context_peak":      maxCumulative,
			"pressure_pct":      pressurePct,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}
	http.Error(w, "session not found", http.StatusNotFound)
}

func estimateContext(model string) int {
	switch {
	case contains(model, "gpt-5"): return 256000
	case contains(model, "gpt-4"): return 128000
	case contains(model, "claude"): return 200000
	case contains(model, "gemini"): return 1000000
	case contains(model, "deepseek"): return 131072
	default: return 128000
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSub(s, substr)
}

func searchSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub { return true }
	}
	return false
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	type AgentInfo struct {
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
		SessionCount int   `json:"session_count"`
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
	if ms < 1000 { return fmt.Sprintf("%dms", ms) }
	if ms < 60000 { return fmt.Sprintf("%.1fs", float64(ms)/1000) }
	return fmt.Sprintf("%dm%ds", ms/60000, (ms%60000)/1000)
}
