package server

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/llm"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/quota"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/terminal"
)

// IndexStatusProvider exposes FTS indexer progress for the search UI.
type IndexStatusProvider interface {
	SnapshotProgress() IndexProgress
}

// IndexProgress mirrors indexer.Progress without importing that package
// into every handler file (avoids cycles). Values match indexer.Progress JSON.
type IndexProgress struct {
	State   string `json:"state"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
}

type Server struct {
	DB      *db.DB
	Readers []reader.BaseSessionReader
	Mux     *http.ServeMux
	events  *eventHub

	// newGenerationClient is overridden by handler tests that need to verify
	// typed model failures without starting a real provider process.
	newGenerationClient func(llm.Config) (llm.Client, error)

	// Version/Commit 由 main 从 -ldflags 注入值转交；release 构建只有 Version，
	// Commit 为空表示非开发构建，GET /api/version 据此决定是否回传 commit。
	Version string
	Commit  string

	// indexStatus is optional; when set, GET /api/index/status reports progress.
	indexStatus IndexStatusProvider

	// listRev 是会话列表的修订号，作为 /api/sessions 的 ETag：索引轮落库、
	// 书签/标题变更都会 bump，内容没变的重拉直接 304。startNano 隔离进程
	// 重启，避免新进程撞上浏览器缓存的旧 ETag。
	listRev   atomic.Int64
	startNano int64

	// replay caches parsed render events and rendered ANSI for the session
	// open path (render/edits/tool-outputs/positions), keyed by agent type +
	// session id and validated by the stat-only LiveRevision.
	replay *replayCache

	terminalLauncher terminal.Launcher
	resumeMu         sync.Mutex
	resumeInFlight   map[string]bool
	changeRegistry   *changehost.Registry
	hostPolicy       *changehost.HostPolicy
	hostMu           sync.Mutex
	approvedHosts    map[string]*changehost.ApprovedHost

	// importRoot is the directory imported session bundles live under
	// (<SI_DATA_DIR>/imports). Empty means the import endpoints are disabled.
	importRoot string
	// kickIndex requests a re-index of one agent type after import/delete.
	// Nil in tests that never wire the indexer.
	kickIndex func(agentType string)

	// codingQuotaManager owns credentialed upstream quota requests. It is kept
	// separate from the session database so quota failures never affect replay.
	codingQuotaManager *quota.Manager
}

// SetIndexStatus wires the indexer progress provider (call before Serve).
func (s *Server) SetIndexStatus(p IndexStatusProvider) {
	s.indexStatus = p
}

// SetCodingQuotaManager replaces the default quota registry for tests or
// embedders that provide their own provider catalog.
func (s *Server) SetCodingQuotaManager(manager *quota.Manager) {
	s.codingQuotaManager = manager
}

type SessionSummary struct {
	ID                  string `json:"id"`
	AgentType           string `json:"agent_type"`
	Name                string `json:"name"`
	ModelName           string `json:"model_name"`
	ModelProvider       string `json:"model_provider,omitempty"`
	Repository          string `json:"repository"`
	Branch              string `json:"branch"`
	Project             string `json:"project"`
	CWD                 string `json:"cwd"`
	ResumeID            string `json:"resume_id,omitempty"`
	TurnCount           int    `json:"turn_count"`
	HistoricalTurnCount int    `json:"historical_turn_count,omitempty"`
	RolledBackTurnCount int    `json:"rolled_back_turn_count,omitempty"`
	MessageCount        int    `json:"message_count"`
	IsLive              bool   `json:"is_live"`
	Bookmarked          bool   `json:"bookmarked"`
	BookmarkNote        string `json:"bookmark_note,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	// Collaboration is the optional compact aggregate for root Sessions with
	// an indexed collaboration graph (three counts + precision only).
	Collaboration *CollaborationSummary `json:"collaboration_summary,omitempty"`
	// RecordStatus is the compact list projection of record completeness.
	// Absolute source paths and raw warnings must not appear here.
	RecordStatus *model.RecordStatus `json:"record_status,omitempty"`
	// Import markers are set only for sessions that arrived via a bundle
	// (agent_type "imported"); live sessions omit them.
	Imported          bool   `json:"imported,omitempty"`
	OriginHost        string `json:"origin_host,omitempty"`
	OriginalAgentType string `json:"original_agent_type,omitempty"`
	CaseLabel         string `json:"case_label,omitempty"`
}

func New(database *db.DB, readers []reader.BaseSessionReader) *Server {
	s := &Server{
		DB:               database,
		Readers:          readers,
		Mux:              http.NewServeMux(),
		events:           newEventHub(),
		startNano:        time.Now().UnixNano(),
		replay:           newReplayCache(),
		terminalLauncher: terminal.NewSystemLauncher(),
		resumeInFlight:   make(map[string]bool),
		changeRegistry:   changehost.NewDefaultRegistry(),
		hostPolicy:       changehost.NewHostPolicy(nil),
		approvedHosts:    make(map[string]*changehost.ApprovedHost),
		codingQuotaManager: quota.NewManager(
			quota.NewDefaultProviders(quota.DefaultProviderOptions()),
			quota.ManagerOptions{},
		),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.Mux.HandleFunc("GET /api/bookmarks", s.handleListBookmarks)
	s.Mux.HandleFunc("GET /api/snippets", s.handleListSnippets)
	s.Mux.HandleFunc("POST /api/snippets", s.handleCreateSnippet)
	s.Mux.HandleFunc("DELETE /api/snippets/{id}", s.handleDeleteSnippet)
	s.Mux.HandleFunc("GET /api/events", s.handleEvents)
	s.Mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.Mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	s.Mux.HandleFunc("GET /api/sessions/{id}/resume", s.handleGetResumePlan)
	s.Mux.HandleFunc("POST /api/sessions/{id}/resume", s.handleResumeSession)
	s.Mux.HandleFunc("GET /api/sessions/{id}/terminal", s.handleGetSessionTerminal)
	s.Mux.HandleFunc("POST /api/sessions/{id}/terminal/focus", s.handleFocusSessionTerminal)
	s.Mux.HandleFunc("GET /api/sessions/{id}/collaboration", s.handleGetCollaboration)
	s.Mux.HandleFunc("GET /api/sessions/{id}/git-evidence", s.handleGetGitEvidence)
	s.Mux.HandleFunc("GET /api/sessions/{id}/git-evidence/files/{fileKey}/patch", s.handleGetGitEvidencePatch)
	s.Mux.HandleFunc("GET /api/sessions/{id}/change-requests", s.handleGetSessionChangeRequests)
	s.Mux.HandleFunc("POST /api/sessions/{id}/change-requests/bind", s.handleBindSessionChangeRequest)
	s.Mux.HandleFunc("DELETE /api/sessions/{id}/change-requests/{linkID}", s.handleDeleteSessionChangeRequest)
	s.Mux.HandleFunc("POST /api/change-requests/resolve", s.handleResolveChangeRequest)
	s.Mux.HandleFunc("GET /api/change-requests/{changeID}/sessions", s.handleGetChangeRequestSessions)
	s.Mux.HandleFunc("GET /api/change-requests/{changeID}", s.handleGetChangeRequest)
	s.Mux.HandleFunc("GET /api/change-hosts", s.handleListChangeHosts)
	s.Mux.HandleFunc("POST /api/change-hosts/preview", s.handlePreviewChangeHost)
	s.Mux.HandleFunc("POST /api/change-hosts/{hostKey}/approve", s.handleApproveChangeHost)
	s.Mux.HandleFunc("POST /api/change-hosts/{hostKey}/revoke", s.handleRevokeChangeHost)
	s.Mux.HandleFunc("POST /api/change-host-profiles/import", s.handleImportChangeHostProfile)
	s.Mux.HandleFunc("GET /api/change-host-profiles", s.handleListChangeHostProfiles)
	s.Mux.HandleFunc("GET /api/change-host-profiles/{profileId}", s.handleGetChangeHostProfile)
	s.Mux.HandleFunc("POST /api/change-host-profiles/{profileId}/probe", s.handleProbeChangeHostProfile)
	s.Mux.HandleFunc("PATCH /api/change-host-profiles/{profileId}/mapping", s.handlePatchChangeHostProfileMapping)
	s.Mux.HandleFunc("POST /api/change-host-profiles/{profileId}/verify", s.handleVerifyChangeHostProfile)
	s.Mux.HandleFunc("POST /api/change-host-profiles/{profileId}/revoke", s.handleRevokeChangeHostProfile)
	s.Mux.HandleFunc("GET /api/change-hosts/{hostKey}/status", s.handleGetChangeHostStatus)
	s.Mux.HandleFunc("POST /api/change-hosts/{hostKey}/refresh", s.handleRefreshChangeHost)
	s.Mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	// Remove a source_missing tombstone from the SI index only (not agent source).
	s.Mux.HandleFunc("DELETE /api/sessions/{id}/index", s.handleRemoveFromIndex)
	s.Mux.HandleFunc("POST /api/sessions/{id}/stop", s.handleStopSession)
	s.Mux.HandleFunc("PUT /api/sessions/{id}/bookmark", s.handleAddBookmark)
	s.Mux.HandleFunc("DELETE /api/sessions/{id}/bookmark", s.handleRemoveBookmark)
	s.Mux.HandleFunc("PUT /api/sessions/{id}/bookmark/note", s.handleUpdateBookmarkNote)
	s.Mux.HandleFunc("GET /api/sessions/{id}/analytics", s.handleSessionAnalytics)
	s.Mux.HandleFunc("GET /api/agents", s.handleListAgents)
	s.Mux.HandleFunc("GET /api/search", s.handleSearch)
	s.Mux.HandleFunc("GET /api/index/status", s.handleIndexStatus)
	s.Mux.HandleFunc("GET /api/version", s.handleVersion)
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
	s.Mux.HandleFunc("GET /api/coding-quotas", s.handleGetCodingQuotas)
	s.Mux.HandleFunc("POST /api/coding-quotas/refresh", s.handleRefreshCodingQuotas)

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
	s.Mux.HandleFunc("POST /api/insight/targets/revoke", s.handleRevokeInsightTargets)

	// Session migration (export/import bundle).
	s.Mux.HandleFunc("POST /api/sessions/export-bundle", s.handleExportBundle)
	s.Mux.HandleFunc("POST /api/sessions/import-bundle", s.handleImportBundle)
	s.Mux.HandleFunc("GET /api/imports", s.handleListImportBundles)
	s.Mux.HandleFunc("DELETE /api/imports/{bundle}", s.handleDeleteImportBundle)
}
