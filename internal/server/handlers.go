package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"session-insight/internal/model"
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
				TurnCount:    s.TurnCount,
				MessageCount: s.MessageCount,
				IsLive:       s.IsLive,
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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(detail)
		return
	}

	http.Error(w, "session not found", http.StatusNotFound)
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

	qlower := strings.ToLower(q)
	type result struct {
		SessionID string `json:"session_id"`
		Match     string `json:"match"`
	}

	var results []result
	for _, reader := range s.Readers {
		sessions, err := reader.ListSessions()
		if err != nil {
			continue
		}
		for _, sess := range sessions {
			if len(results) >= 20 {
				break
			}
			// Check name and repo
			if strings.Contains(strings.ToLower(sess.Name), qlower) ||
				strings.Contains(strings.ToLower(sess.Repository), qlower) {
				results = append(results, result{SessionID: sess.ID, Match: sess.Name})
				continue
			}
			// Check first user message
			detail, err := reader.GetSession(sess.ID)
			if err != nil || detail == nil {
				continue
			}
			for _, t := range detail.Turns {
				if strings.Contains(strings.ToLower(t.UserMessage), qlower) {
					match := t.UserMessage
					if len(match) > 100 {
						idx := strings.Index(strings.ToLower(match), qlower)
						start := idx - 30
						if start < 0 { start = 0 }
						end := idx + len(q) + 40
						if end > len(match) { end = len(match) }
						match = "..." + match[start:end] + "..."
					}
					results = append(results, result{SessionID: sess.ID, Match: match})
					break
				}
			}
		}
	}

	if results == nil {
		results = []result{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
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
