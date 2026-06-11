package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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
				CreatedAt:    s.CreatedAt.Format("2006-01-02T15:04:05Z"),
				UpdatedAt:    s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			})
		}
	}

	// Sort by updated_at descending
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
