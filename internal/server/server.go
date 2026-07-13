package server

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/reader"
)

type Server struct {
	DB      *db.DB
	Readers []reader.BaseSessionReader
	Mux     *http.ServeMux
	events  *eventHub

	// listRev 是会话列表的修订号，作为 /api/sessions 的 ETag：索引轮落库、
	// 书签/标题变更都会 bump，内容没变的重拉直接 304。startNano 隔离进程
	// 重启，避免新进程撞上浏览器缓存的旧 ETag。
	listRev   atomic.Int64
	startNano int64
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
	TurnCount    int    `json:"turn_count"`
	MessageCount int    `json:"message_count"`
	IsLive       bool   `json:"is_live"`
	Bookmarked   bool   `json:"bookmarked"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Server {
	s := &Server{
		DB:        database,
		Readers:   readers,
		Mux:       http.NewServeMux(),
		events:    newEventHub(),
		startNano: time.Now().UnixNano(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.Mux.HandleFunc("GET /api/bookmarks", s.handleListBookmarks)
	s.Mux.HandleFunc("GET /api/events", s.handleEvents)
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
	s.Mux.HandleFunc("GET /api/sessions/{id}/tool-outputs", s.handleSessionToolOutputs)
	s.Mux.HandleFunc("GET /api/sessions/{id}/positions", s.handleSessionPositions)
	s.Mux.HandleFunc("GET /api/sessions/{id}/live-revision", s.handleLiveRevision)
	s.Mux.HandleFunc("GET /api/resolve-file", s.handleResolveFile)
	s.Mux.HandleFunc("GET /api/fs/list", s.handleFsList)
	s.Mux.HandleFunc("GET /api/fs/read", s.handleFsRead)
	s.Mux.HandleFunc("POST /api/open-file", s.handleOpenFile)
	s.Mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.Mux.HandleFunc("PUT /api/settings", s.handlePutSettings)

	s.Mux.HandleFunc("GET /api/llm/providers", s.handleListLLMProviders)
	s.Mux.HandleFunc("POST /api/llm/providers", s.handleAddLLMProvider)
	s.Mux.HandleFunc("PUT /api/llm/providers/{id}", s.handleUpdateLLMProvider)
	s.Mux.HandleFunc("DELETE /api/llm/providers/{id}", s.handleDeleteLLMProvider)
	s.Mux.HandleFunc("POST /api/llm/providers/default", s.handleSetDefaultLLMProvider)
	s.Mux.HandleFunc("POST /api/llm/providers/test", s.handleTestLLMProvider)
	s.Mux.HandleFunc("POST /api/sessions/{id}/ai/{kind}", s.handleAIGenerate)
	s.Mux.HandleFunc("GET /api/sessions/{id}/ai/{kind}/latest", s.handleAILatest)
	s.Mux.HandleFunc("GET /api/ai/generations", s.handleListAIGenerations)
	s.Mux.HandleFunc("DELETE /api/ai/generations/{id}", s.handleDeleteAIGeneration)
	s.Mux.HandleFunc("PUT /api/sessions/{id}/title", s.handleSetTitle)
	s.Mux.HandleFunc("DELETE /api/sessions/{id}/title", s.handleRemoveTitle)
}
