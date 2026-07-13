package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/llm"
	"github.com/bbsteel/session-insight/internal/model"
)

// generateTimeout bounds one model generation end to end, including a
// first-time `npx` adapter download for ACP providers.
const generateTimeout = 5 * time.Minute

// testProviderTimeout bounds a connection test / model list fetch.
const testProviderTimeout = 3 * time.Minute

// providerJSON is the wire shape for llm_providers: the stored API key never
// leaves the server, only the fact that one exists.
type providerJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	HasAPIKey  bool   `json:"has_api_key"`
	Agent      string `json:"agent"`
	ModelID    string `json:"model_id"`
	ModelLabel string `json:"model_label"`
	IsDefault  bool   `json:"is_default"`
	CreatedAt  string `json:"created_at"`
}

func toProviderJSON(p db.LLMProvider, defaultID int64) providerJSON {
	return providerJSON{
		ID:         p.ID,
		Name:       p.Name,
		Kind:       p.Kind,
		BaseURL:    p.BaseURL,
		HasAPIKey:  p.APIKey != "",
		Agent:      p.Agent,
		ModelID:    p.ModelID,
		ModelLabel: p.ModelLabel,
		IsDefault:  p.ID == defaultID,
		CreatedAt:  p.CreatedAt,
	}
}

type providerRequest struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	Agent      string `json:"agent"`
	ModelID    string `json:"model_id"`
	ModelLabel string `json:"model_label"`
}

func (req *providerRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.ModelID = strings.TrimSpace(req.ModelID)
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.ModelID == "" {
		return fmt.Errorf("model_id is required: 模型必须显式选择")
	}
	switch req.Kind {
	case "api":
		if req.BaseURL == "" {
			return fmt.Errorf("base_url is required for api providers")
		}
	case "acp":
		if llm.AgentBinary(req.Agent) == "" {
			return fmt.Errorf("agent must be one of claude, codex, gemini")
		}
	default:
		return fmt.Errorf("kind must be api or acp")
	}
	return nil
}

func (s *Server) requireDB(w http.ResponseWriter) bool {
	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleListLLMProviders(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	providers, err := s.DB.ListLLMProviders()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defaultID, _ := s.DB.DefaultLLMProviderID()
	out := make([]providerJSON, 0, len(providers))
	for _, p := range providers {
		out = append(out, toProviderJSON(p, defaultID))
	}
	writeJSON(w, map[string]any{
		"providers":  out,
		"acp_agents": llm.DetectACPAgents(),
	})
}

func (s *Server) handleAddLLMProvider(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.DB.AddLLMProvider(db.LLMProvider{
		Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, APIKey: req.APIKey,
		Agent: req.Agent, ModelID: req.ModelID, ModelLabel: req.ModelLabel,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// First provider becomes the default automatically — the common case is
	// exactly one configured source, and requiring a second explicit
	// "set default" step there is pure friction.
	if defaultID, _ := s.DB.DefaultLLMProviderID(); defaultID == 0 {
		s.DB.SetDefaultLLMProviderID(id)
	}
	writeJSON(w, map[string]int64{"id": id})
}

func (s *Server) handleUpdateLLMProvider(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid provider id", http.StatusBadRequest)
		return
	}
	existing, err := s.DB.GetLLMProvider(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Empty api_key means "unchanged": the client never saw the stored key,
	// so it cannot round-trip it.
	apiKey := existing.APIKey
	if req.APIKey != "" {
		apiKey = req.APIKey
	}
	err = s.DB.UpdateLLMProvider(db.LLMProvider{
		ID: id, Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, APIKey: apiKey,
		Agent: req.Agent, ModelID: req.ModelID, ModelLabel: req.ModelLabel,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid provider id", http.StatusBadRequest)
		return
	}
	if err := s.DB.DeleteLLMProvider(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if defaultID, _ := s.DB.DefaultLLMProviderID(); defaultID == id {
		s.DB.SetDefaultLLMProviderID(0)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetDefaultLLMProvider(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	var req struct {
		ProviderID int64 `json:"provider_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	p, err := s.DB.GetLLMProvider(req.ProviderID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	if err := s.DB.SetDefaultLLMProviderID(req.ProviderID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestLLMProvider validates a (possibly unsaved) provider config by
// fetching its model list. provider_id lets a saved provider refresh its
// models without re-entering the API key.
func (s *Server) handleTestLLMProvider(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) {
		return
	}
	var req struct {
		providerRequest
		ProviderID int64 `json:"provider_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.APIKey == "" && req.ProviderID > 0 && s.DB != nil {
		if p, err := s.DB.GetLLMProvider(req.ProviderID); err == nil && p != nil {
			req.APIKey = p.APIKey
		}
	}
	cfg := llm.Config{
		Kind: req.Kind, BaseURL: strings.TrimSpace(req.BaseURL),
		APIKey: req.APIKey, Agent: req.Agent,
	}
	client, err := llm.New(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), testProviderTimeout)
	defer cancel()
	models, err := client.ListModels(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"models": models})
}

// resolveProvider picks the provider for one generation: an explicit
// provider_id from the request wins, otherwise the global default.
func (s *Server) resolveProvider(providerID int64) (*db.LLMProvider, error) {
	if providerID == 0 {
		var err error
		providerID, err = s.DB.DefaultLLMProviderID()
		if err != nil {
			return nil, err
		}
	}
	if providerID == 0 {
		return nil, nil
	}
	return s.DB.GetLLMProvider(providerID)
}

// handleAIGenerate runs one generation task over SSE so the client sees
// stage progress ("启动 ACP 适配器" → "请求模型" → done) instead of a
// black-box spinner. Events: status {stage}, done {generation}, error
// {message}.
func (s *Server) handleAIGenerate(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	id := r.PathValue("id")
	kind := r.PathValue("kind")
	if !llm.ValidKind(kind) {
		http.Error(w, "kind must be summary, title or handoff", http.StatusBadRequest)
		return
	}

	var req struct {
		ProviderID int64 `json:"provider_id"`
	}
	json.NewDecoder(r.Body).Decode(&req) // empty body is fine

	provider, err := s.resolveProvider(req.ProviderID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if provider == nil {
		http.Error(w, "未配置模型:请先在设置中添加 AI 模型", http.StatusPreconditionFailed)
		return
	}

	var detail *model.SessionDetail
	for _, rd := range s.Readers {
		d, err := rd.GetSession(id)
		if err == nil && d != nil {
			detail = d
			break
		}
	}
	if detail == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	prompt, err := llm.BuildPrompt(llm.GenerationKind(kind), detail)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	sendEvent := func(event string, v any) {
		payload, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		flusher.Flush()
	}
	sendEvent("status", map[string]string{"stage": "构建上下文"})

	client, err := llm.New(llm.Config{
		Kind: provider.Kind, BaseURL: provider.BaseURL, APIKey: provider.APIKey,
		Agent: provider.Agent, ModelID: provider.ModelID,
	})
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), generateTimeout)
	defer cancel()
	content, err := client.Generate(ctx, prompt, func(stage string) {
		sendEvent("status", map[string]string{"stage": stage})
	})
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}
	if kind == string(llm.KindTitle) {
		content = llm.SanitizeTitle(content)
		if content == "" {
			sendEvent("error", map[string]string{"message": "模型未返回可用标题"})
			return
		}
	}

	gen := db.AIGeneration{
		Kind:         kind,
		AgentType:    detail.AgentType,
		SessionID:    detail.ID,
		ProviderName: provider.Name,
		ModelID:      provider.ModelID,
		Content:      content,
	}
	genID, err := s.DB.AddAIGeneration(gen)
	if err != nil {
		sendEvent("error", map[string]string{"message": "保存生成结果失败: " + err.Error()})
		return
	}
	gen.ID = genID
	gen.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	sendEvent("done", gen)
}

// handleAILatest returns the newest saved generation of a kind for a
// session — the summary cache path. ?agent= avoids a disk read.
func (s *Server) handleAILatest(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	kind := r.PathValue("kind")
	agentType := r.URL.Query().Get("agent")
	if !llm.ValidKind(kind) || agentType == "" {
		http.Error(w, "invalid kind or missing agent", http.StatusBadRequest)
		return
	}
	gen, err := s.DB.LatestAIGeneration(kind, agentType, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gen == nil {
		http.Error(w, "no generation yet", http.StatusNotFound)
		return
	}
	writeJSON(w, gen)
}

func (s *Server) handleListAIGenerations(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	q := r.URL.Query()
	gens, err := s.DB.ListAIGenerations(q.Get("kind"), q.Get("agent"), q.Get("session"), 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gens == nil {
		gens = []db.AIGeneration{}
	}
	writeJSON(w, gens)
}

func (s *Server) handleDeleteAIGeneration(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid generation id", http.StatusBadRequest)
		return
	}
	if err := s.DB.DeleteAIGeneration(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetTitle applies a title override chosen by the user (usually an AI
// draft they confirmed). Display-only: agent log files are never touched.
func (s *Server) handleSetTitle(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	agentType := r.URL.Query().Get("agent")
	if agentType == "" {
		http.Error(w, "missing agent", http.StatusBadRequest)
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if err := s.DB.SetTitleOverride(agentType, r.PathValue("id"), title); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.NotifySessionsChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveTitle(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	agentType := r.URL.Query().Get("agent")
	if agentType == "" {
		http.Error(w, "missing agent", http.StatusBadRequest)
		return
	}
	if err := s.DB.RemoveTitleOverride(agentType, r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.NotifySessionsChanged()
	w.WriteHeader(http.StatusNoContent)
}
