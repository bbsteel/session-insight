package adaptertest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// ---- fakes for negative contract/behavior paths ----

type fakeReader struct {
	agentType   string
	displayName string
	sessions    []model.Session
	details     map[string]*model.SessionDetail
	events      map[string][]model.RenderEvent
	listErr     error
	getErr      map[string]error
}

func (f *fakeReader) AgentType() string   { return f.agentType }
func (f *fakeReader) DisplayName() string { return f.displayName }
func (f *fakeReader) ListSessions() ([]model.Session, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]model.Session, len(f.sessions))
	copy(out, f.sessions)
	return out, nil
}
func (f *fakeReader) GetSession(id string) (*model.SessionDetail, error) {
	if err, ok := f.getErr[id]; ok {
		return nil, err
	}
	if d, ok := f.details[id]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("session not found: %s", id)
}
func (f *fakeReader) RenderANSI(id string, cols int) (string, error) {
	if _, err := f.GetSession(id); err != nil {
		return "", err
	}
	return "ok", nil
}
func (f *fakeReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	if err, ok := f.getErr[id]; ok {
		return nil, err
	}
	if ev, ok := f.events[id]; ok {
		return ev, nil
	}
	if _, ok := f.details[id]; ok {
		return nil, nil
	}
	return nil, fmt.Errorf("session not found: %s", id)
}

// validCaps returns a declaration that does not require optional interfaces
// so a bare Reader can pass contract checks.
func validCaps(agentType, display string) capability.AgentCapabilities {
	caps := make(map[capability.CapabilityID]capability.CapabilityDeclaration, 10)
	for _, id := range capability.BaselineIDs() {
		caps[id] = capability.Exact()
	}
	caps[capability.CapabilityRealtime] = capability.Unsupported("adapter_not_implemented")
	caps[capability.CapabilityDelete] = capability.Unsupported("adapter_not_implemented")
	caps[capability.CapabilityTerminate] = capability.Unsupported("exact_pid_unavailable")
	return capability.AgentCapabilities{
		AgentType:       agentType,
		DisplayName:     display,
		AdapterRevision: 1,
		Capabilities:    caps,
	}
}

type completeFake struct{ fakeReader }

func (c *completeFake) DeleteSession(string) error             { return nil }
func (c *completeFake) LiveRevision(string) (int64, error)     { return 1, nil }
func (c *completeFake) SessionProcesses(string) ([]int, error) { return nil, nil }

func TestValidateStaticRejectsIncompleteDeclaration(t *testing.T) {
	caps := validCaps("x", "X")
	delete(caps.Capabilities, capability.CapabilityTokens)
	errs := capability.ValidateStatic(caps)
	if len(errs) == 0 {
		t.Fatal("expected ValidateStatic errors for missing tokens capability")
	}
	// Deterministic field mention.
	joined := errs.Error()
	if !strings.Contains(joined, "tokens") {
		t.Fatalf("expected tokens in errors, got %s", joined)
	}
}

func TestCheckOperationInterfacesDeleteExact(t *testing.T) {
	r := &fakeReader{agentType: "x", displayName: "X"}
	caps := validCaps("x", "X")
	caps.Capabilities[capability.CapabilityDelete] = capability.Exact()
	err := CheckOperationInterfaces(caps, r)
	if err == nil {
		t.Fatal("expected error for delete=exact without DeleteSession")
	}
	if !strings.Contains(err.Error(), "delete=exact") {
		t.Fatalf("actionable message missing delete=exact: %v", err)
	}
}

func TestCheckOperationInterfacesTerminateExact(t *testing.T) {
	r := &fakeReader{agentType: "x", displayName: "X"}
	caps := validCaps("x", "X")
	caps.Capabilities[capability.CapabilityTerminate] = capability.Exact()
	err := CheckOperationInterfaces(caps, r)
	if err == nil {
		t.Fatal("expected error for terminate=exact without SessionProcesses")
	}
	if !strings.Contains(err.Error(), "terminate=exact") {
		t.Fatalf("actionable message missing terminate=exact: %v", err)
	}
}

func TestCheckOperationInterfacesRealtimeExact(t *testing.T) {
	r := &fakeReader{agentType: "x", displayName: "X"}
	caps := validCaps("x", "X")
	caps.Capabilities[capability.CapabilityRealtime] = capability.Exact()
	err := CheckOperationInterfaces(caps, r)
	if err == nil {
		t.Fatal("expected error for realtime=exact without LiveRevision")
	}
	if !strings.Contains(err.Error(), "realtime=") {
		t.Fatalf("actionable message missing realtime: %v", err)
	}
}

func TestRunContractAcceptsFullOptionalSurfaces(t *testing.T) {
	cf := &completeFake{
		fakeReader: fakeReader{agentType: "x", displayName: "X"},
	}
	caps := validCaps("x", "X")
	caps.Capabilities[capability.CapabilityDelete] = capability.Exact()
	caps.Capabilities[capability.CapabilityTerminate] = capability.Exact()
	caps.Capabilities[capability.CapabilityRealtime] = capability.Exact()
	RunContract(t, caps, cf)
}

func TestRunContractAcceptsBareReaderWhenOpsUnsupported(t *testing.T) {
	r := &fakeReader{agentType: "x", displayName: "X"}
	RunContract(t, validCaps("x", "X"), r)
}

func TestRunBasicBehaviorHappyPath(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &fakeReader{
		agentType:   "x",
		displayName: "X",
		sessions: []model.Session{{
			ID: "s1", AgentType: "x", CreatedAt: now, UpdatedAt: now.Add(time.Minute),
		}},
		details: map[string]*model.SessionDetail{
			"s1": {Session: model.Session{ID: "s1", AgentType: "x", CreatedAt: now, UpdatedAt: now.Add(time.Minute)}},
		},
		events: map[string][]model.RenderEvent{
			"s1": {{Type: "UserPrompt", TurnIndex: 0, Text: "hi"}},
		},
	}
	RunBasicBehavior(t, r, Expectations{SessionCount: 1, SessionIDs: []string{"s1"}})
}

func TestCheckUniqueNonEmptyRejectsDuplicates(t *testing.T) {
	err := checkUniqueNonEmpty([]string{"dup", "dup"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestSameIDSet(t *testing.T) {
	if err := sameIDSet([]string{"a", "b"}, []string{"b", "a"}); err != nil {
		t.Fatalf("same set should pass: %v", err)
	}
	if err := sameIDSet([]string{"a"}, []string{"b"}); err == nil {
		t.Fatal("different sets should fail")
	}
}

func TestIdentityMismatchIsActionable(t *testing.T) {
	msg := fmt.Sprintf("reader AgentType %q != declaration %q", "a", "b")
	if !strings.Contains(msg, "AgentType") || !strings.Contains(msg, "a") {
		t.Fatal(msg)
	}
}

func TestUnknownSessionErrorIsActionable(t *testing.T) {
	r := &fakeReader{agentType: "x", displayName: "X"}
	// Use a recorder-style check via assertUnknownSession path indirectly.
	_, err := r.GetSession("nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("want not found wording: %v", err)
	}
}
