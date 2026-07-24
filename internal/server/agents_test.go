package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestHandleListAgentsReturnsFullCatalog(t *testing.T) {
	// No discovered readers: every catalog Agent must still appear.
	srv := New(nil, nil)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}

	var agents []AgentInfo
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}

	defs := reader.AgentDefinitions()
	if len(agents) != len(defs) {
		t.Fatalf("agents = %d, want %d catalog entries", len(agents), len(defs))
	}
	if len(agents) != 6 {
		t.Fatalf("want 6 agents, got %d", len(agents))
	}

	byType := map[string]AgentInfo{}
	for _, a := range agents {
		byType[a.Type] = a
		if a.Discovered {
			t.Errorf("%s: discovered=true with no readers", a.Type)
		}
		if a.SessionCount != 0 {
			t.Errorf("%s: session_count=%d want 0 when not discovered", a.Type, a.SessionCount)
		}
		if a.DisplayName == "" {
			t.Errorf("%s: empty display_name", a.Type)
		}
		if a.AdapterRevision < 1 {
			t.Errorf("%s: adapter_revision=%d", a.Type, a.AdapterRevision)
		}
		if len(a.Capabilities) != 10 {
			t.Errorf("%s: capabilities=%d want 10", a.Type, len(a.Capabilities))
		}
		// can_delete / can_terminate must match the declaration, not discovery.
		wantDel := a.Capabilities[capability.CapabilityDelete].State == capability.CapabilityExact
		if a.CanDelete != wantDel {
			t.Errorf("%s: can_delete=%v want %v", a.Type, a.CanDelete, wantDel)
		}
		wantTerm := a.Capabilities[capability.CapabilityTerminate].State == capability.CapabilityExact
		if a.CanTerminate != wantTerm {
			t.Errorf("%s: can_terminate=%v want %v", a.Type, a.CanTerminate, wantTerm)
		}
		// Static declarations must not use missing.
		for id, decl := range a.Capabilities {
			if decl.State == capability.CapabilityMissing {
				t.Errorf("%s.%s: static missing forbidden", a.Type, id)
			}
			if decl.State != capability.CapabilityExact && decl.ReasonCode == "" {
				t.Errorf("%s.%s: non-exact needs reason_code", a.Type, id)
			}
		}
	}

	// Spot-check known non-exact declarations.
	if got := byType["copilot"].Capabilities[capability.CapabilityResume]; got.State != capability.CapabilityUnsupported {
		t.Errorf("copilot.resume state = %s", got.State)
	}
	if got := byType["chrys"].Capabilities[capability.CapabilityTerminate]; got.State != capability.CapabilityUnsupported {
		t.Errorf("chrys.terminate state = %s", got.State)
	}
	if got := byType["grok"].Capabilities[capability.CapabilitySubtasks]; got.State != capability.CapabilityUnsupported {
		t.Errorf("grok.subtasks state = %s", got.State)
	}
}

func TestHandleListAgentsMarksDiscoveredAndCountsSessions(t *testing.T) {
	rd := &stubReader{
		agentType: "claude",
		sessions: []model.Session{
			{ID: "a", AgentType: "claude"},
			{ID: "b", AgentType: "claude"},
			{ID: "child", AgentType: "claude", IsSubagent: true},
		},
	}
	srv := New(nil, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var agents []AgentInfo
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(agents) != 6 {
		t.Fatalf("catalog length = %d, want 6", len(agents))
	}

	var claude *AgentInfo
	var othersDiscovered int
	for i := range agents {
		a := &agents[i]
		if a.Type == "claude" {
			claude = a
			continue
		}
		if a.Discovered {
			othersDiscovered++
		}
	}
	if claude == nil {
		t.Fatal("claude missing from catalog")
	}
	if !claude.Discovered {
		t.Error("claude should be discovered")
	}
	if claude.SessionCount != 2 {
		t.Errorf("claude session_count = %d, want 2 (subagent excluded)", claude.SessionCount)
	}
	if othersDiscovered != 0 {
		t.Errorf("only claude reader registered; other discovered count = %d", othersDiscovered)
	}
	if !claude.CanDelete {
		t.Error("claude can_delete should be true from declaration")
	}
}

func TestHandleListAgentsStableOrder(t *testing.T) {
	srv := New(nil, nil)
	req := httptest.NewRequest("GET", "/api/agents", nil)

	w1 := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w1, req)
	w2 := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w2, req)

	var a, b []AgentInfo
	if err := json.NewDecoder(w1.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(w2.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("length mismatch")
	}
	for i := range a {
		if a[i].Type != b[i].Type {
			t.Fatalf("order not stable at %d: %s vs %s", i, a[i].Type, b[i].Type)
		}
	}
	// Sorted by agent type ascending (matches AgentDefinitions).
	for i := 1; i < len(a); i++ {
		if a[i-1].Type > a[i].Type {
			t.Fatalf("not sorted: %s before %s", a[i-1].Type, a[i].Type)
		}
	}
}
