package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/analytics"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/insight"
	"github.com/bbsteel/session-insight/internal/llm"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

const insightKind = "insight"

// confirmedTargetsKey stores the set of model targets the user has approved to
// receive Deep Insight data, keyed by a local fingerprint of the target.
const confirmedTargetsKey = "insight_confirmed_targets"

// modelTargetFingerprint identifies a send destination by provider kind plus
// the concrete endpoint (base_url for API, agent for ACP). Changing the
// endpoint changes the fingerprint, so a re-confirmation is required — a
// provider config ID alone must not grant a permanent grant.
// providerModelLabel is a short human-readable name for the model being used,
// shown in the generation progress so the user can see which model ran.
func providerModelLabel(p *db.LLMProvider) string {
	name := p.ModelLabel
	if name == "" {
		name = p.ModelID
	}
	if name == "" {
		name = p.Agent
	}
	if p.Name != "" && p.Name != name {
		return name + "（" + p.Name + "）"
	}
	return name
}

func modelTargetFingerprint(p *db.LLMProvider) string {
	var target string
	switch p.Kind {
	case "acp":
		target = "acp:" + p.Agent
	default:
		target = "api:" + strings.TrimRight(p.BaseURL, "/")
	}
	sum := sha256.Sum256([]byte(target))
	return fmt.Sprintf("%x", sum[:16])
}

func (s *Server) targetConfirmed(fp string) bool {
	raw, _ := s.DB.GetSetting(confirmedTargetsKey)
	if raw == "" {
		return false
	}
	var set []string
	if json.Unmarshal([]byte(raw), &set) != nil {
		return false
	}
	for _, v := range set {
		if v == fp {
			return true
		}
	}
	return false
}

// handleRevokeInsightTargets clears the user's approved send targets, so the
// next Deep Insight generation re-shows the send preview and confirmation. This
// is the "撤销" the privacy disclosure promises.
func (s *Server) handleRevokeInsightTargets(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) || !s.requireDB(w) {
		return
	}
	if err := s.DB.SetSetting(confirmedTargetsKey, ""); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "insight_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) confirmTarget(fp string) {
	raw, _ := s.DB.GetSetting(confirmedTargetsKey)
	var set []string
	json.Unmarshal([]byte(raw), &set)
	for _, v := range set {
		if v == fp {
			return
		}
	}
	set = append(set, fp)
	data, _ := json.Marshal(set)
	s.DB.SetSetting(confirmedTargetsKey, string(data))
}

// insightSnapshot is a consistency-checked read: SessionDetail, Findings,
// evidence bundle and revision all come from one reader at one logical
// revision, so the model never sees a spliced session.
type insightSnapshot struct {
	reader   reader.BaseSessionReader
	detail   *model.SessionDetail
	bundle   insight.Bundle
	revision int64
}

// liveRevision returns a cheap change marker for a session (mtime+size for
// readers that expose it), used to detect a session mutating mid-snapshot.
// Falls back to the detail's updated_at when the reader has no live revision.
func liveRevision(rd reader.BaseSessionReader, id string, detail *model.SessionDetail) int64 {
	if p, ok := rd.(reader.LiveRevisionProvider); ok {
		if rev, err := p.LiveRevision(id); err == nil {
			return rev
		}
	}
	return detail.UpdatedAt.UnixNano()
}

// buildInsightSnapshot binds the reader that actually reads the session, then
// reads detail + reader-specific evidence at a stable revision, retrying when
// the session changes mid-read. Returns (nil, httpStatus, message) on failure:
// 404 not found, 409 session_active (live), 409 session_changing (unstable).
func (s *Server) buildInsightSnapshot(id string) (*insightSnapshot, int, string) {
	var bound reader.BaseSessionReader
	for _, rd := range s.Readers {
		if d, err := rd.GetSession(id); err == nil && d != nil {
			bound = rd
			break
		}
	}
	if bound == nil {
		return nil, http.StatusNotFound, "session not found"
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		detail, err := bound.GetSession(id)
		if err != nil || detail == nil {
			return nil, http.StatusNotFound, "session not found"
		}
		// Active sessions are excluded on both UI and API: their data and bill
		// are still changing, so analyzing them repeatedly wastes money.
		if model.IsSessionLive(detail.UpdatedAt) {
			return nil, http.StatusConflict, "session_active"
		}
		revBefore := liveRevision(bound, id, detail)

		res := analytics.Compute(detail)
		var ev *model.InsightEvidence
		if p, ok := bound.(reader.InsightEvidenceProvider); ok {
			if e, _, err := p.GetInsightEvidence(id, revBefore); err == nil {
				ev = e
			}
		}
		bundle := insight.BuildBundle(detail, res, ev)

		revAfter := liveRevision(bound, id, detail)
		if revBefore == revAfter {
			return &insightSnapshot{reader: bound, detail: detail, bundle: bundle, revision: revBefore}, 0, ""
		}
	}
	return nil, http.StatusConflict, "session_changing"
}

// sendPreview is the pre-flight privacy disclosure shown before the first send
// to an unconfirmed model target. Counts are computed from the redacted bundle.
type sendPreview struct {
	NeedsConfirmation bool     `json:"needs_confirmation"`
	TargetFingerprint string   `json:"target_fingerprint"`
	TargetLabel       string   `json:"target_label"`
	DataCategories    []string `json:"data_categories"`
	FactCount         int      `json:"fact_count"`
	CharCount         int      `json:"char_count"`
	TruncatedCount    int      `json:"truncated_count"`
	RedactedCount     int      `json:"redacted_count"`
	Note              string   `json:"note"`
}

// generateInsight runs the Deep Insight generation: consistency snapshot,
// redaction, model-target confirmation gate, model call, output validation and
// persistence. It is invoked from handleAIGenerate for kind == "insight".
func (s *Server) generateInsight(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		ProviderID    int64  `json:"provider_id"`
		ConfirmTarget bool   `json:"confirm_target"`
		Locale        string `json:"locale"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}

	provider, err := s.resolveProvider(req.ProviderID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "insight_failed", err.Error())
		return
	}
	if provider == nil {
		writeAPIError(w, http.StatusPreconditionFailed, "no_provider")
		return
	}

	snap, status, msg := s.buildInsightSnapshot(id)
	if snap == nil {
		code := msg
		if code == "session not found" {
			code = "session_not_found"
		}
		writeAPIError(w, status, code)
		return
	}
	if len(snap.bundle.Findings) == 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "no_findings")
		return
	}

	// Redact before anything can be sent; the preview counts reflect the
	// redacted, truncated payload the model will actually receive.
	redacted, redactStats := insight.Redact(snap.bundle)
	fp := modelTargetFingerprint(provider)

	if !s.targetConfirmed(fp) && !req.ConfirmTarget {
		writeJSON(w, buildSendPreview(redacted, redactStats, provider, fp, req.Locale))
		return
	}
	// Confirmation covers only this target; a later endpoint change re-triggers
	// the gate because the fingerprint changes.
	s.confirmTarget(fp)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	sendEvent := func(event string, v any) {
		mu.Lock()
		defer mu.Unlock()
		payload, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		flusher.Flush()
	}
	sendEvent("status", map[string]string{"stage": "已选择模型 " + providerModelLabel(provider)})
	sendEvent("status", map[string]string{"stage": "构建证据"})

	userMsg, err := insight.BuildUserMessage(redacted)
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	client, err := llm.New(providerLLMConfig(provider))
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), generateTimeout)
	defer cancel()
	onStatus := func(stage string) { sendEvent("status", map[string]string{"stage": stage}) }

	var raw string
	if sg, ok := client.(llm.SystemPromptGenerator); ok {
		// API path: real system role separating instruction from untrusted data.
		raw, err = sg.GenerateWithSystem(ctx, insight.SystemInstructionForLocale(req.Locale), userMsg, onStatus)
	} else {
		// ACP path: weaker boundary — instruction then JSON in one tool-less
		// message; safety rests on isolation + validation, not the separator.
		combined, cErr := insight.BuildCombinedPrompt(redacted, req.Locale)
		if cErr != nil {
			sendEvent("error", map[string]string{"message": cErr.Error()})
			return
		}
		raw, err = client.Generate(ctx, combined, onStatus)
	}
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	gen, genErr := s.persistInsight(snap, redacted, raw, provider)
	if genErr != nil {
		sendEvent("error", map[string]string{"message": genErr.Error()})
		return
	}
	// Re-read the revision: if the session changed during generation, the
	// result is saved but must be reported stale immediately.
	freshness := s.insightFreshness(gen, snap.reader, id)
	sendEvent("done", map[string]any{"generation": gen, "freshness": freshness})
}

// persistInsight validates the model output, renders safe Markdown, and stores
// the generation with its freshness fingerprint. An unparseable output is kept
// as escaped plain text with parse_failed set, never discarded.
func (s *Server) persistInsight(snap *insightSnapshot, redacted insight.Bundle, raw string, provider *db.LLMProvider) (*db.AIGeneration, error) {
	fingerprint := insight.SourceFingerprint(redacted)
	gen := db.AIGeneration{
		Kind:              insightKind,
		AgentType:         snap.detail.AgentType,
		SessionID:         snap.detail.ID,
		ProviderName:      provider.Name,
		ModelID:           provider.ModelID,
		SourceRevision:    snap.revision,
		PromptVersion:     insight.PromptVersion,
		SourceFingerprint: fingerprint,
	}

	out, warnings, err := insight.ParseAndValidate(raw, redacted)
	if err != nil {
		// Structured parse failed: keep the raw output as escaped plain text.
		gen.Content = insight.EscapePlainText(raw)
		meta := insight.StoredMetadata{ParseFailed: true, Warnings: []string{err.Error()}}
		data, _ := json.Marshal(meta)
		gen.Metadata = string(data)
	} else {
		gen.Content = insight.RenderMarkdown(out, redacted)
		meta := insight.BuildMetadata(out, redacted, warnings)
		data, _ := json.Marshal(meta)
		gen.Metadata = string(data)
	}

	genID, err := s.DB.AddAIGeneration(gen)
	if err != nil {
		return nil, fmt.Errorf("保存生成结果失败: %w", err)
	}
	gen.ID = genID
	gen.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	return &gen, nil
}

// insightFreshness compares a stored generation against the session's current
// state and rule/schema/skill versions. When the read path did not rebuild a
// bundle it does not claim a full fingerprint comparison — it judges from the
// current revision plus the versions embedded in the generation.
func (s *Server) insightFreshness(gen *db.AIGeneration, rd reader.BaseSessionReader, id string) map[string]any {
	var reasons []string
	currentRev := int64(0)
	if detail, err := rd.GetSession(id); err == nil && detail != nil {
		currentRev = liveRevision(rd, id, detail)
	}
	if currentRev != 0 && currentRev != gen.SourceRevision {
		reasons = append(reasons, "session_revision_changed")
	}
	if gen.PromptVersion != insight.PromptVersion {
		reasons = append(reasons, "skill_version_changed")
	}
	return map[string]any{
		"stale":              len(reasons) > 0,
		"reasons":            reasons,
		"source_revision":    gen.SourceRevision,
		"current_revision":   currentRev,
		"source_fingerprint": gen.SourceFingerprint,
		"prompt_version":     gen.PromptVersion,
	}
}

func buildSendPreview(redacted insight.Bundle, stats insight.RedactionStats, provider *db.LLMProvider, fp, locale string) sendPreview {
	charCount, truncated := 0, 0
	for _, f := range redacted.Facts {
		charCount += len([]rune(f.Statement))
		if strings.Contains(f.Statement, "…(截断)") {
			truncated++
		}
	}
	label := provider.Name
	if provider.Kind == "acp" {
		label += "（ACP:" + provider.Agent + "）"
	} else if provider.BaseURL != "" {
		label += "（" + provider.BaseURL + "）"
	}
	categories := []string{"会话元数据", "初步 Findings 指标", "证据事实（Turn 摘要/工具错误/subagent 委派）", "账单与上下文指标"}
	note := "已对已知凭据格式、邮箱与本机 home 路径做确定性脱敏；这不能保证识别所有自然语言 PII。仅本次目标授权，可在设置中撤销。"
	if locale == "en" {
		label = provider.Name
		if provider.Kind == "acp" {
			label += " (ACP:" + provider.Agent + ")"
		} else if provider.BaseURL != "" {
			label += " (" + provider.BaseURL + ")"
		}
		categories = []string{"Session metadata", "Preliminary finding metrics", "Evidence facts (turn summaries, tool errors, and subagent delegation)", "Billing and context metrics"}
		note = "Known credential formats, email addresses, and local home paths were deterministically redacted. This cannot guarantee detection of all natural-language PII. Authorization applies only to this target and can be revoked in Settings."
	}
	return sendPreview{
		NeedsConfirmation: true,
		TargetFingerprint: fp,
		TargetLabel:       label,
		DataCategories:    categories,
		FactCount:         len(redacted.Facts),
		CharCount:         charCount,
		TruncatedCount:    truncated,
		RedactedCount:     stats.Total(),
		Note:              note,
	}
}
