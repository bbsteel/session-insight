package server

import (
	"net/http"

	"session-insight/internal/db"
	"session-insight/internal/reader"
)

type Server struct {
	DB      *db.DB
	Readers []reader.BaseSessionReader
	Mux     *http.ServeMux
}

type SessionSummary struct {
	ID           string `json:"id"`
	AgentType    string `json:"agent_type"`
	Name         string `json:"name"`
	ModelName    string `json:"model_name"`
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	Project      string `json:"project"`
	CWD          string `json:"cwd"`
	ResumeID     string `json:"resume_id,omitempty"`
	PreviewText  string `json:"preview_text"`
	TurnCount    int    `json:"turn_count"`
	MessageCount int    `json:"message_count"`
	IsLive       bool   `json:"is_live"`
	Bookmarked   bool   `json:"bookmarked"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Server {
	s := &Server{
		DB:      database,
		Readers: readers,
		Mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.Mux.HandleFunc("GET /api/bookmarks", s.handleListBookmarks)
	s.Mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.Mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	s.Mux.HandleFunc("PUT /api/sessions/{id}/bookmark", s.handleAddBookmark)
	s.Mux.HandleFunc("DELETE /api/sessions/{id}/bookmark", s.handleRemoveBookmark)
	s.Mux.HandleFunc("GET /api/sessions/{id}/analytics", s.handleSessionAnalytics)
	s.Mux.HandleFunc("GET /api/agents", s.handleListAgents)
	s.Mux.HandleFunc("GET /api/search", s.handleSearch)
	s.Mux.HandleFunc("GET /api/sessions/{id}/export", s.handleExportSession)
	s.Mux.HandleFunc("GET /api/sessions/{id}/render", s.handleRenderSession)
	s.Mux.HandleFunc("GET /api/sessions/{id}/edits", s.handleSessionEdits)
	s.Mux.HandleFunc("GET /api/sessions/{id}/positions", s.handleSessionPositions)
}
