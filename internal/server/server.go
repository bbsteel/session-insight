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
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	TurnCount    int    `json:"turn_count"`
	MessageCount int    `json:"message_count"`
	IsLive       bool   `json:"is_live"`
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
	s.Mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.Mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	s.Mux.HandleFunc("GET /api/sessions/{id}/analytics", s.handleSessionAnalytics)
	s.Mux.HandleFunc("GET /api/agents", s.handleListAgents)
		s.Mux.HandleFunc("GET /api/search", s.handleSearch)
}
