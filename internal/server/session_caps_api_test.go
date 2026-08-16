package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// capsAPIReader is a catalog-backed reader for GET session capability tests.
type capsAPIReader struct {
	detail *model.SessionDetail
	// optional hooks
	live    *bool
	liveErr error
	hasRev  bool
	pids    []int
}

func (r *capsAPIReader) AgentType() string {
	if r.detail != nil {
		return r.detail.AgentType
	}
	return "claude"
}
func (r *capsAPIReader) DisplayName() string { return "Claude Code" }
func (r *capsAPIReader) ListSessions() ([]model.Session, error) {
	if r.detail == nil {
		return nil, nil
	}
	return []model.Session{r.detail.Session}, nil
}
func (r *capsAPIReader) GetSession(id string) (*model.SessionDetail, error) {
	if r.detail != nil && r.detail.ID == id {
		// Return a shallow copy so tests can observe non-mutation.
		cp := *r.detail
		return &cp, nil
	}
	return nil, nil
}
func (r *capsAPIReader) RenderANSI(id string, cols int) (string, error) { return "", nil }
func (r *capsAPIReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, nil
}
func (r *capsAPIReader) SessionLive(id string) (bool, error) {
	if r.liveErr != nil {
		return false, r.liveErr
	}
	if r.live == nil {
		return false, nil
	}
	return *r.live, nil
}
func (r *capsAPIReader) LiveRevision(id string) (int64, error) {
	if !r.hasRev {
		return 0, nil
	}
	return 7, nil
}
func (r *capsAPIReader) SessionProcesses(id string) ([]int, error) {
	return r.pids, nil
}

var (
	_ reader.BaseSessionReader       = (*capsAPIReader)(nil)
	_ reader.SessionLivenessProvider = (*capsAPIReader)(nil)
	_ reader.LiveRevisionProvider    = (*capsAPIReader)(nil)
)

type sessionDetailAPIResponse struct {
	model.SessionDetail
	AgentCapabilities capability.SessionCapabilities `json:"agent_capabilities"`
}

func TestHandleGetSessionIncludesAgentCapabilities(t *testing.T) {
	now := time.Now()
	live := true
	rd := &capsAPIReader{
		hasRev: true,
		live:   &live,
		detail: &model.SessionDetail{
			Session: model.Session{
				ID: "sess-caps-1", AgentType: "claude", Name: "Demo",
				ResumeID: "sess-caps-1", UpdatedAt: now, CreatedAt: now.Add(-time.Hour),
			},
			Turns:   []model.TurnVM{{UserMessage: "hi", ToolCallCount: 0}},
			Billing: &model.SessionBilling{Precision: model.PrecisionExact},
		},
	}
	srv := New(nil, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest("GET", "/api/sessions/sess-caps-1", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp sessionDetailAPIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	// Existing fields preserved.
	if resp.ID != "sess-caps-1" || resp.Name != "Demo" || resp.AgentType != "claude" {
		t.Fatalf("session fields: id=%s name=%s type=%s", resp.ID, resp.Name, resp.AgentType)
	}
	if !resp.IsLive {
		t.Fatal("expected is_live true from provider")
	}

	caps := resp.AgentCapabilities
	if caps.AgentType != "claude" || caps.AdapterRevision < 1 {
		t.Fatalf("agent meta: %+v", caps)
	}

	// Match GET /api/agents static meta.
	def, ok := reader.AgentDefinition("claude")
	if !ok {
		t.Fatal("missing claude definition")
	}
	if caps.AdapterRevision != def.AdapterRevision {
		t.Fatalf("adapter_revision %d != catalog %d", caps.AdapterRevision, def.AdapterRevision)
	}

	if len(caps.Status) != 10 {
		t.Fatalf("status keys=%d want 10", len(caps.Status))
	}
	for _, id := range capability.BaselineIDs() {
		st, ok := caps.Status[id]
		if !ok {
			t.Errorf("missing status %s", id)
			continue
		}
		if !capability.IsKnownState(st.State) {
			t.Errorf("%s unknown state %q", id, st.State)
		}
		if st.State != capability.CapabilityExact && st.ReasonCode == "" {
			t.Errorf("%s non-exact without reason: %+v", id, st)
		}
	}

	// Exact tokens with zero tools still exact.
	if caps.Status[capability.CapabilityTokens].State != capability.CapabilityExact {
		t.Errorf("tokens=%+v", caps.Status[capability.CapabilityTokens])
	}
	if caps.Status[capability.CapabilityToolResults].State != capability.CapabilityExact {
		t.Errorf("tool_results=%+v", caps.Status[capability.CapabilityToolResults])
	}

	// Actions present for resume/delete/terminate.
	for _, id := range capability.ActionableIDs() {
		if _, ok := caps.Actions[id]; !ok {
			t.Errorf("missing action %s", id)
		}
	}
	if caps.Actions[capability.CapabilityResume].Availability != capability.ActionAvailable {
		t.Errorf("resume action=%+v", caps.Actions[capability.CapabilityResume])
	}
	if caps.Actions[capability.CapabilityDelete].Availability != capability.ActionUnavailable {
		t.Errorf("live delete should be unavailable: %+v", caps.Actions[capability.CapabilityDelete])
	}
	if caps.Actions[capability.CapabilityTerminate].Availability != capability.ActionCheckRequired {
		t.Errorf("live terminate: %+v", caps.Actions[capability.CapabilityTerminate])
	}

	// Liveness quality.
	if !caps.Liveness.IsLive || caps.Liveness.State != capability.CapabilityExact {
		t.Errorf("liveness=%+v", caps.Liveness)
	}
}

func TestHandleGetSessionTokensMissingShutdown(t *testing.T) {
	now := time.Now()
	falseLive := false
	rd := &capsAPIReader{
		hasRev: true,
		live:   &falseLive,
		detail: &model.SessionDetail{
			Session: model.Session{
				ID: "sess-miss", AgentType: "copilot", ResumeID: "sess-miss",
				UpdatedAt: now.Add(-time.Hour), CreatedAt: now.Add(-2 * time.Hour),
			},
			Billing: &model.SessionBilling{Precision: model.PrecisionMissing},
		},
	}
	srv := New(nil, []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/sess-miss", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var resp sessionDetailAPIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	st := resp.AgentCapabilities.Status[capability.CapabilityTokens]
	if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonSessionNotFinalized {
		t.Fatalf("tokens=%+v", st)
	}
	// Copilot resume is statically unsupported.
	if resp.AgentCapabilities.Status[capability.CapabilityResume].State != capability.CapabilityUnsupported {
		t.Fatalf("resume=%+v", resp.AgentCapabilities.Status[capability.CapabilityResume])
	}
}

func TestHandleGetSessionUnknownSession404(t *testing.T) {
	rd := &capsAPIReader{
		hasRev: true,
		detail: &model.SessionDetail{
			Session: model.Session{ID: "exists", AgentType: "claude", ResumeID: "exists", UpdatedAt: time.Now()},
			Billing: &model.SessionBilling{Precision: model.PrecisionExact},
		},
	}
	srv := New(nil, []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/no-such-session", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestHandleGetSessionUnknownAgentType500(t *testing.T) {
	// Reader agent type not in catalog → internal invariant failure.
	rd := &capsAPIReader{
		hasRev: true,
		detail: &model.SessionDetail{
			Session: model.Session{ID: "x", AgentType: "not-a-real-agent", ResumeID: "x", UpdatedAt: time.Now()},
		},
	}
	// Override AgentType() via detail only — capsAPIReader uses detail.AgentType.
	srv := New(nil, []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/x", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleGetSessionResumeMissingID(t *testing.T) {
	// Codex cannot fall back to the storage/file id; empty ResumeID is missing.
	now := time.Now()
	falseLive := false
	rd := &capsAPIReader{
		hasRev: true,
		live:   &falseLive,
		detail: &model.SessionDetail{
			Session: model.Session{
				ID: "sess-no-resume", AgentType: "codex", ResumeID: "",
				UpdatedAt: now.Add(-time.Hour),
			},
			Billing: &model.SessionBilling{Precision: model.PrecisionExact},
		},
	}
	srv := New(nil, []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/sess-no-resume", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var resp sessionDetailAPIResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	st := resp.AgentCapabilities.Status[capability.CapabilityResume]
	if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonResumeIDMissing {
		t.Fatalf("%+v", st)
	}
	if resp.AgentCapabilities.Actions[capability.CapabilityResume].Availability != capability.ActionUnavailable {
		t.Fatal(resp.AgentCapabilities.Actions[capability.CapabilityResume])
	}
}

func TestHandleGetSessionClaudeResumeUsesSessionID(t *testing.T) {
	// Claude: empty ResumeID still exact — CLI uses session UUID.
	now := time.Now()
	falseLive := false
	rd := &capsAPIReader{
		hasRev: true,
		live:   &falseLive,
		detail: &model.SessionDetail{
			Session: model.Session{
				ID: "sess-claude-id", AgentType: "claude", ResumeID: "",
				UpdatedAt: now.Add(-time.Hour),
			},
			Billing: &model.SessionBilling{Precision: model.PrecisionExact},
		},
	}
	srv := New(nil, []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/sess-claude-id", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var resp sessionDetailAPIResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	st := resp.AgentCapabilities.Status[capability.CapabilityResume]
	if st.State != capability.CapabilityExact {
		t.Fatalf("claude resume=%+v want exact", st)
	}
	if resp.AgentCapabilities.Actions[capability.CapabilityResume].Availability != capability.ActionAvailable {
		t.Fatal(resp.AgentCapabilities.Actions[capability.CapabilityResume])
	}
}

func TestHandleListAgentsStillStaticOnly(t *testing.T) {
	// Regression: /api/agents must not gain session-level missing.
	srv := New(nil, nil)
	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var agents []AgentInfo
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		for id, decl := range a.Capabilities {
			if decl.State == capability.CapabilityMissing {
				t.Errorf("%s.%s static missing forbidden", a.Type, id)
			}
		}
	}
}

// panicDeleter panics if destructive methods are invoked during GET session.
type panicDeleter struct {
	*capsAPIReader
}

func (p *panicDeleter) DeleteSession(id string) error {
	panic("DeleteSession must not be called on GET session")
}
func (p *panicDeleter) SessionProcesses(id string) ([]int, error) {
	panic("SessionProcesses must not be called on GET session")
}

func TestHandleGetSessionNoDestructiveCalls(t *testing.T) {
	now := time.Now()
	live := false
	inner := &capsAPIReader{
		hasRev: true,
		live:   &live,
		detail: &model.SessionDetail{
			Session: model.Session{
				ID: "safe-get", AgentType: "claude", ResumeID: "safe-get",
				UpdatedAt: now.Add(-time.Hour),
			},
			Billing: &model.SessionBilling{Precision: model.PrecisionExact},
		},
	}
	rd := &panicDeleter{capsAPIReader: inner}
	srv := New(nil, []reader.BaseSessionReader{rd})
	req := httptest.NewRequest("GET", "/api/sessions/safe-get", nil)
	w := httptest.NewRecorder()
	// Must not panic.
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
}
