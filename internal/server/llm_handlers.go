package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/llm"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

// generateTimeout bounds one model generation end to end, including a
// first-time `npx` adapter download for ACP providers.
const generateTimeout = 5 * time.Minute

// testProviderTimeout bounds a connection test / model list fetch.
const testProviderTimeout = 3 * time.Minute

// providerJSON is the wire shape for llm_providers: the stored API key never
// leaves the server, only the fact that one exists. Headers are returned in
// full (they are not secrets in the same sense as the key; values may still
// be sensitive tokens the user chose to put there).
type providerJSON struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	BaseURL    string            `json:"base_url"`
	HasAPIKey  bool              `json:"has_api_key"`
	Headers    map[string]string `json:"headers,omitempty"`
	Agent      string            `json:"agent"`
	ModelID    string            `json:"model_id"`
	ModelLabel string            `json:"model_label"`
	IsDefault  bool              `json:"is_default"`
	CreatedAt  string            `json:"created_at"`
}

func toProviderJSON(p db.LLMProvider, defaultID int64) providerJSON {
	return providerJSON{
		ID:         p.ID,
		Name:       p.Name,
		Kind:       p.Kind,
		BaseURL:    p.BaseURL,
		HasAPIKey:  p.APIKey != "",
		Headers:    decodeProviderHeaders(p.Headers),
		Agent:      p.Agent,
		ModelID:    p.ModelID,
		ModelLabel: p.ModelLabel,
		IsDefault:  p.ID == defaultID,
		CreatedAt:  p.CreatedAt,
	}
}

type providerRequest struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	BaseURL    string            `json:"base_url"`
	APIKey     string            `json:"api_key"`
	Headers    map[string]string `json:"headers"`
	Agent      string            `json:"agent"`
	ModelID    string            `json:"model_id"`
	ModelLabel string            `json:"model_label"`
}

func (req *providerRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.Headers = normalizeProviderHeaders(req.Headers)
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
			return fmt.Errorf("agent must be one of %s", strings.Join(llm.LocalAgents, ", "))
		}
		// ACP has no HTTP hop; drop accidental header payloads.
		req.Headers = nil
	default:
		return fmt.Errorf("kind must be api or acp")
	}
	return nil
}

// normalizeProviderHeaders trims keys/values, drops empty keys, and rejects
// hop-by-hop / forbidden names that must not be user-controlled.
func normalizeProviderHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if isForbiddenProviderHeader(k) {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isForbiddenProviderHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding",
		"keep-alive", "upgrade", "te", "trailer", "proxy-connection":
		return true
	default:
		return false
	}
}

func encodeProviderHeaders(h map[string]string) string {
	h = normalizeProviderHeaders(h)
	if len(h) == 0 {
		return ""
	}
	b, err := json.Marshal(h)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeProviderHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return normalizeProviderHeaders(m)
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
	if err := s.ensureUniqueModelID(req.ModelID, 0); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	id, err := s.DB.AddLLMProvider(db.LLMProvider{
		Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, APIKey: req.APIKey,
		Headers: encodeProviderHeaders(req.Headers),
		Agent:   req.Agent, ModelID: req.ModelID, ModelLabel: req.ModelLabel,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			http.Error(w, fmt.Sprintf("model_id %q 已被其他模型源占用", req.ModelID), http.StatusConflict)
			return
		}
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
	if err := s.ensureUniqueModelID(req.ModelID, id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	// Empty api_key means "unchanged": the client never saw the stored key,
	// so it cannot round-trip it. Headers are always fully replaced from the
	// request (the client round-trips the whole map).
	apiKey := existing.APIKey
	if req.APIKey != "" {
		apiKey = req.APIKey
	}
	err = s.DB.UpdateLLMProvider(db.LLMProvider{
		ID: id, Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, APIKey: apiKey,
		Headers: encodeProviderHeaders(req.Headers),
		Agent:   req.Agent, ModelID: req.ModelID, ModelLabel: req.ModelLabel,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			http.Error(w, fmt.Sprintf("model_id %q 已被其他模型源占用", req.ModelID), http.StatusConflict)
			return
		}
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
//
// Listing a model id is not treated as proof it can generate — that only
// fails or succeeds on a real Generate call — so this endpoint never claims
// "model available".
func (s *Server) handleTestLLMProvider(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) {
		return
	}
	var req struct {
		providerRequest
		ProviderID int64 `json:"provider_id"`
		Force      bool  `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Headers = normalizeProviderHeaders(req.Headers)
	// Empty api_key falls back to the saved provider so "测试连接" works
	// without re-entering the key. Headers always come from the request body
	// (the editor round-trips them; empty means none).
	if req.APIKey == "" && req.ProviderID > 0 && s.DB != nil {
		if p, err := s.DB.GetLLMProvider(req.ProviderID); err == nil && p != nil {
			req.APIKey = p.APIKey
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), testProviderTimeout)
	defer cancel()

	models, err := s.listModelsForProvider(ctx, req.Kind, req.Agent, req.BaseURL, req.APIKey, req.Headers, req.Force)
	if err != nil {
		// Config errors (unsupported agent / missing base_url) are 400; live
		// fetch failures are 502.
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "must be") || strings.Contains(err.Error(), "requires") ||
			strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "unknown provider") {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]any{"models": models})
}

// ensureUniqueModelID rejects a model_id already owned by another provider.
func (s *Server) ensureUniqueModelID(modelID string, excludeID int64) error {
	other, err := s.DB.FindLLMProviderByModelID(modelID, excludeID)
	if err != nil {
		return err
	}
	if other != nil {
		return fmt.Errorf("model_id %q 已被模型源「%s」占用，不可重复配置", modelID, other.Name)
	}
	return nil
}

func (s *Server) listModelsForProvider(ctx context.Context, kind, agent, baseURL, apiKey string, headers map[string]string, force bool) ([]llm.Model, error) {
	switch kind {
	case "acp":
		if llm.AgentBinary(agent) == "" {
			return nil, fmt.Errorf("agent must be one of %s", strings.Join(llm.LocalAgents, ", "))
		}
		return llm.ListACPModelsCached(ctx, agent, force)
	case "api":
		client, err := llm.New(llm.Config{
			Kind: "api", BaseURL: baseURL, APIKey: apiKey,
			Headers: normalizeProviderHeaders(headers),
		})
		if err != nil {
			return nil, err
		}
		return client.ListModels(ctx)
	default:
		return nil, fmt.Errorf("kind must be api or acp")
	}
}

func providerLLMConfig(p *db.LLMProvider) llm.Config {
	return llm.Config{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Headers: decodeProviderHeaders(p.Headers),
		Agent:   p.Agent, ModelID: p.ModelID,
	}
}

func (s *Server) generationClient(cfg llm.Config) (llm.Client, error) {
	if s.newGenerationClient != nil {
		return s.newGenerationClient(cfg)
	}
	return llm.New(cfg)
}

func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint failed")
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
	// Deep Insight has its own snapshot/redaction/confirmation path and does
	// not use the summary-oriented prompt builder.
	if kind == insightKind {
		s.generateInsight(w, r, id)
		return
	}
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

	var candidates []string
	if kind == string(llm.KindHandoff) {
		candidates = s.handoffCandidates()
	}
	prompt, err := llm.BuildPrompt(llm.GenerationKind(kind), detail, candidates)
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
	var streamMu sync.Mutex
	sendEvent := func(event string, v any) {
		// ACP output notifications arrive from its stdout reader goroutine.
		// Serialize SSE writes so a status sent for the first output cannot
		// interleave with the handler's final done/error event.
		streamMu.Lock()
		defer streamMu.Unlock()
		payload, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		flusher.Flush()
	}
	sendEvent("status", map[string]string{"stage": "已选择模型 " + providerModelLabel(provider)})
	sendEvent("status", map[string]string{"stage": "构建上下文"})

	client, err := s.generationClient(providerLLMConfig(provider))
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
		var unavailable *llm.ModelUnavailableError
		if errors.As(err, &unavailable) {
			sendEvent("error", map[string]any{
				"message": err.Error(), "code": "model_unavailable", "provider_id": provider.ID,
			})
			return
		}
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
	metadata := ""
	if kind == string(llm.KindHandoff) {
		content, metadata = llm.ParseHandoffOutput(content)
	}

	gen := db.AIGeneration{
		Kind:         kind,
		AgentType:    detail.AgentType,
		SessionID:    detail.ID,
		ProviderName: provider.Name,
		ModelID:      provider.ModelID,
		Content:      content,
		Metadata:     metadata,
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

// handoffCandidates assembles the executor candidates offered to the
// handoff recommendation: agent CLIs present on this machine plus models
// actually seen in recent sessions — grounded in what the user can run,
// not a hardcoded market survey.
func (s *Server) handoffCandidates() []string {
	var out []string
	for _, agent := range llm.DetectACPAgents() {
		out = append(out, llm.LocalAgentLabel(agent)+"（本机已安装）")
	}

	// Distinct models from recent sessions, most recently used first.
	latest := map[string]time.Time{} // model name -> newest updated_at
	for _, rd := range s.Readers {
		list, err := rd.ListSessions()
		if err != nil {
			continue
		}
		for _, sess := range list {
			if sess.ModelName == "" {
				continue
			}
			if cur, ok := latest[sess.ModelName]; !ok || sess.UpdatedAt.After(cur) {
				latest[sess.ModelName] = sess.UpdatedAt
			}
		}
	}
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return latest[names[i]].After(latest[names[j]]) })
	const maxRecentModels = 8
	if len(names) > maxRecentModels {
		names = names[:maxRecentModels]
	}
	for _, name := range names {
		out = append(out, name+"（用户最近使用过的模型）")
	}
	return out
}

// handleAILatest returns the newest saved generation of a kind for a
// session — the summary cache path. ?agent= avoids a disk read.
func (s *Server) handleAILatest(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	kind := r.PathValue("kind")
	agentType := r.URL.Query().Get("agent")
	if (!llm.ValidKind(kind) && kind != insightKind) || agentType == "" {
		http.Error(w, "invalid kind or missing agent", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	gen, err := s.DB.LatestAIGeneration(kind, agentType, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gen == nil {
		http.Error(w, "no generation yet", http.StatusNotFound)
		return
	}
	// Insight carries explainable freshness so the UI can flag a stale result
	// (session changed, or rule/skill version moved) instead of presenting it
	// as a current conclusion.
	if kind == insightKind {
		if rd := s.readerForSession(agentType, id); rd != nil {
			writeJSON(w, map[string]any{"generation": gen, "freshness": s.insightFreshness(gen, rd, id)})
			return
		}
	}
	writeJSON(w, gen)
}

// readerForSession returns the reader matching an agent type (and that can read
// the session), or nil.
func (s *Server) readerForSession(agentType, id string) reader.BaseSessionReader {
	for _, rd := range s.Readers {
		if rd.AgentType() != agentType {
			continue
		}
		if d, err := rd.GetSession(id); err == nil && d != nil {
			return rd
		}
	}
	return nil
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
