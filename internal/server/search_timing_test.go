//go:build sqlite_fts5

package server

import (
	"net/http/httptest"
	"testing"
	"time"

	"session-insight/internal/db"
	"session-insight/internal/reader"
)

func TestRealSearchPath_Timing(t *testing.T) {
	database, err := db.Open("/home/user/.session-insight")
	if err != nil {
		t.Skipf("production db not available: %v", err)
	}
	defer database.Close()

	discovered := reader.Discover()

	queries := []string{"折叠", "性能优化"}
	for _, q := range queries {
		t.Run("q="+q, func(t *testing.T) {
			// Phase 1: SearchTurns only
			start := time.Now()
			results, err := database.SearchTurns(q, 30)
			searchElapsed := time.Since(start)
			if err != nil {
				t.Fatalf("SearchTurns: %v", err)
			}

			// Phase 2: ListSessions metadata enrichment
			type sessionMeta struct {
				project   string
				name      string
				updatedAt time.Time
			}
			start = time.Now()
			metas := make(map[string]sessionMeta)
			if len(results) > 0 {
				for _, rd := range discovered {
					list, err := rd.ListSessions()
					if err != nil {
						continue
					}
					for _, sess := range list {
						metas[rd.AgentType()+"\x00"+sess.ID] = sessionMeta{
							project:   sess.Project,
							name:      sess.Name,
							updatedAt: sess.UpdatedAt,
						}
					}
				}
			}
			metaElapsed := time.Since(start)

			// Phase 3: Build response
			start = time.Now()
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
				key := r.AgentType + "\x00" + r.SessionID
				meta, ok := metas[key]
				if !ok {
					continue
				}
				out = append(out, result{
					SessionID: r.SessionID,
					AgentType: r.AgentType,
					Project:   meta.project,
					Name:      meta.name,
					UpdatedAt: meta.updatedAt.Format("2006-01-02T15:04:05Z"),
					Match:     r.Match,
				})
			}
			joinElapsed := time.Since(start)

			t.Logf("SearchTurns: %v | ListSessions meta: %v | join: %v | total: %v | results: %d",
				searchElapsed, metaElapsed, joinElapsed, searchElapsed+metaElapsed+joinElapsed, len(out))
		})
	}

	// Full HTTP handler path
	t.Run("http", func(t *testing.T) {
		srv := New(database, discovered)
		for _, q := range queries {
			req := httptest.NewRequest("GET", "/api/search?q="+q, nil)
			w := httptest.NewRecorder()
			start := time.Now()
			srv.Mux.ServeHTTP(w, req)
			elapsed := time.Since(start)
			t.Logf("HTTP q=%q: %v, status=%d, body_len=%d", q, elapsed, w.Code, w.Body.Len())
		}
	})
}
