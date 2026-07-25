package reader

import (
	"errors"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// ---- fakes ----

type capsFakeReader struct {
	agentType string
	live      *bool // nil = no SessionLivenessProvider
	liveErr   error
	hasRev    bool
}

func (f *capsFakeReader) AgentType() string   { return f.agentType }
func (f *capsFakeReader) DisplayName() string { return f.agentType }
func (f *capsFakeReader) ListSessions() ([]model.Session, error) {
	return nil, nil
}
func (f *capsFakeReader) GetSession(id string) (*model.SessionDetail, error) {
	return nil, errors.New("not used")
}
func (f *capsFakeReader) RenderANSI(id string, cols int) (string, error) {
	return "", errors.New("n/a")
}
func (f *capsFakeReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, errors.New("n/a")
}
func (f *capsFakeReader) SessionLive(id string) (bool, error) {
	if f.liveErr != nil {
		return false, f.liveErr
	}
	if f.live == nil {
		return false, errors.New("no provider")
	}
	return *f.live, nil
}
func (f *capsFakeReader) LiveRevision(id string) (int64, error) {
	if !f.hasRev {
		return 0, errors.New("no rev")
	}
	return 1, nil
}

// Compile-time optional interfaces.
var (
	_ BaseSessionReader       = (*capsFakeReader)(nil)
	_ SessionLivenessProvider = (*capsFakeReader)(nil)
	_ LiveRevisionProvider    = (*capsFakeReader)(nil)
)

func boolPtr(v bool) *bool { return &v }

func fullExactStatic(agentType string) capability.AgentCapabilities {
	caps := make(map[capability.CapabilityID]capability.CapabilityDeclaration, 10)
	for _, id := range capability.BaselineIDs() {
		caps[id] = capability.Exact()
	}
	return capability.AgentCapabilities{
		AgentType:       agentType,
		DisplayName:     agentType,
		AdapterRevision: 1,
		Capabilities:    caps,
	}
}

func detailBase(agentType, id string) *model.SessionDetail {
	now := time.Now()
	return &model.SessionDetail{
		Session: model.Session{
			ID:        id,
			AgentType: agentType,
			ResumeID:  id,
			UpdatedAt: now,
			CreatedAt: now.Add(-time.Hour),
		},
		Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "hi"}},
	}
}

// ---- tests ----

func TestResolveStaticExactRemainsExact(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(false), hasRev: true}

	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range capability.BaselineIDs() {
		if got.Status[id].State != capability.CapabilityExact {
			t.Errorf("%s state=%s want exact", id, got.Status[id].State)
		}
	}
	if len(got.Status) != 10 {
		t.Fatalf("status count=%d", len(got.Status))
	}
}

func TestResolveStaticUnsupportedPreserved(t *testing.T) {
	static := fullExactStatic("copilot")
	static.Capabilities[capability.CapabilityResume] = capability.Unsupported(capability.ReasonAdapterNotImplemented)
	d := detailBase("copilot", "s1")
	d.ResumeID = "" // even if empty, must not become missing
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "copilot", live: boolPtr(false), hasRev: true}

	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityResume]
	if st.State != capability.CapabilityUnsupported {
		t.Fatalf("resume state=%s want unsupported", st.State)
	}
	if st.ReasonCode != capability.ReasonAdapterNotImplemented {
		t.Fatalf("reason=%s", st.ReasonCode)
	}
	if got.Actions[capability.CapabilityResume].Availability != capability.ActionUnavailable {
		t.Fatalf("resume action=%s", got.Actions[capability.CapabilityResume].Availability)
	}
}

func TestResolveStaticNotApplicablePreserved(t *testing.T) {
	static := fullExactStatic("x")
	static.Capabilities[capability.CapabilitySubtasks] = capability.NotApplicable(capability.ReasonConceptAbsent)
	d := detailBase("x", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "x", live: boolPtr(false), hasRev: true}

	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilitySubtasks].State != capability.CapabilityNotApplicable {
		t.Fatalf("state=%s", got.Status[capability.CapabilitySubtasks].State)
	}
}

func TestResolveDoesNotMutateStaticMap(t *testing.T) {
	static := fullExactStatic("claude")
	// Snapshot a pointer to the original map entry by key after resolve.
	before := static.Capabilities[capability.CapabilityResume]
	d := detailBase("claude", "s1")
	d.ResumeID = "" // Claude still exact via session id; static map must not change
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(false), hasRev: true}

	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityResume].State != capability.CapabilityExact {
		t.Fatalf("resolved resume=%s", got.Status[capability.CapabilityResume].State)
	}
	after := static.Capabilities[capability.CapabilityResume]
	if after.State != before.State || after.ReasonCode != before.ReasonCode {
		t.Fatalf("static map mutated: before=%+v after=%+v", before, after)
	}
	if static.Capabilities[capability.CapabilityResume].State != capability.CapabilityExact {
		t.Fatal("static resume must remain exact")
	}
}

func TestResolveTokensExactBilling(t *testing.T) {
	static := fullExactStatic("copilot")
	d := detailBase("copilot", "s1")
	d.Billing = &model.SessionBilling{
		Precision: model.PrecisionExact,
		Totals: model.TokenUsage{
			PromptTokens: 0, // exact zero
			Present:      model.TokenPresence{Input: model.PresenceExact, Output: model.PresenceExact},
		},
	}
	src := &capsFakeReader{agentType: "copilot", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityTokens].State != capability.CapabilityExact {
		t.Fatalf("tokens=%s", got.Status[capability.CapabilityTokens].State)
	}
}

func TestResolveTokensEstimatedBilling(t *testing.T) {
	static := fullExactStatic("x")
	static.Capabilities[capability.CapabilityTokens] = capability.Estimated("heuristic")
	d := detailBase("x", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionEstimated}
	src := &capsFakeReader{agentType: "x", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityTokens]
	if st.State != capability.CapabilityEstimated {
		t.Fatalf("state=%s", st.State)
	}
}

func TestResolveTokensEstimatedStaticNeverPromotesOnExactBilling(t *testing.T) {
	// Skeptic: static estimated + Billing.PrecisionExact must not return exact
	// (illegal_promotion → ValidateResolved/GET 500).
	static := fullExactStatic("x")
	static.Capabilities[capability.CapabilityTokens] = capability.Estimated("heuristic")
	d := detailBase("x", "s1")
	d.Billing = &model.SessionBilling{
		Precision: model.PrecisionExact,
		Totals: model.TokenUsage{
			PromptTokens: 10,
			Present:      model.TokenPresence{Input: model.PresenceExact},
		},
	}
	src := &capsFakeReader{agentType: "x", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatalf("must not fail validation: %v", err)
	}
	st := got.Status[capability.CapabilityTokens]
	if st.State != capability.CapabilityEstimated {
		t.Fatalf("tokens=%+v want estimated (no promote to exact)", st)
	}
	if st.ReasonCode != "heuristic" {
		t.Fatalf("reason=%q want static heuristic", st.ReasonCode)
	}
}

func TestResolveTokensEstimatedStaticNeverPromotesOnTurnPresence(t *testing.T) {
	static := fullExactStatic("x")
	static.Capabilities[capability.CapabilityTokens] = capability.Estimated("heuristic")
	d := detailBase("x", "s1")
	d.Billing = nil
	d.Turns = []model.TurnVM{{
		TokenUsage: model.TokenUsage{
			PromptTokens: 5,
			Present:      model.TokenPresence{Input: model.PresenceExact, Output: model.PresenceExact},
		},
	}}
	src := &capsFakeReader{agentType: "x", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatalf("must not fail validation: %v", err)
	}
	st := got.Status[capability.CapabilityTokens]
	if st.State != capability.CapabilityEstimated || st.ReasonCode != "heuristic" {
		t.Fatalf("tokens=%+v want estimated/heuristic", st)
	}
}

func TestResolveTokensNilBillingMissing(t *testing.T) {
	static := fullExactStatic("x")
	d := detailBase("x", "s1")
	d.Billing = nil
	src := &capsFakeReader{agentType: "x", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityTokens]
	if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonSourceNotRecorded {
		t.Fatalf("got %+v", st)
	}
}

func TestResolveTokensMissingShutdown(t *testing.T) {
	static := fullExactStatic("copilot")
	d := detailBase("copilot", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionMissing}
	src := &capsFakeReader{agentType: "copilot", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityTokens]
	if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonSessionNotFinalized {
		t.Fatalf("got %+v", st)
	}
}

func TestResolveTokensTurnPresenceExact(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = nil
	d.Turns = []model.TurnVM{{
		TokenUsage: model.TokenUsage{
			PromptTokens: 100,
			Present:      model.TokenPresence{Input: model.PresenceExact, Output: model.PresenceExact},
		},
	}}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityTokens].State != capability.CapabilityExact {
		t.Fatalf("tokens=%s", got.Status[capability.CapabilityTokens].State)
	}
}

func TestResolveZeroToolsDiffSubtasksRemainExact(t *testing.T) {
	static := fullExactStatic("x")
	d := detailBase("x", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	d.Turns = []model.TurnVM{{UserMessage: "hi"}} // zero tools
	src := &capsFakeReader{agentType: "x", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []capability.CapabilityID{
		capability.CapabilityToolResults, capability.CapabilityDiff, capability.CapabilitySubtasks,
	} {
		if got.Status[id].State != capability.CapabilityExact {
			t.Errorf("%s=%s", id, got.Status[id].State)
		}
	}
}

func TestResolveResumeMissingID(t *testing.T) {
	// Codex cannot fall back to the storage/file id (rollout stem).
	static := fullExactStatic("codex")
	d := detailBase("codex", "rollout-stem")
	d.ResumeID = ""
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "codex", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityResume]
	if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonResumeIDMissing {
		t.Fatalf("%+v", st)
	}
	if got.Actions[capability.CapabilityResume].Availability != capability.ActionUnavailable {
		t.Fatal("resume action should be unavailable")
	}
}

func TestResolveResumeClaudeUsesSessionIDWhenResumeIDEmpty(t *testing.T) {
	// Claude CLI: `claude --resume <session-uuid>`; SI session id is that UUID.
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.ResumeID = ""
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityResume].State != capability.CapabilityExact {
		t.Fatalf("claude empty ResumeID should still be exact via session id: %+v", got.Status[capability.CapabilityResume])
	}
	if got.Actions[capability.CapabilityResume].Availability != capability.ActionAvailable {
		t.Fatalf("resume action=%+v", got.Actions[capability.CapabilityResume])
	}
}

func TestResolveResumeCodexEmptyAgentTypeUsesStaticType(t *testing.T) {
	// When detail.AgentType is empty, Codex rule must still apply via static type.
	static := fullExactStatic("codex")
	d := detailBase("codex", "rollout-stem")
	d.AgentType = ""
	d.ResumeID = ""
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "codex", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityResume]
	if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonResumeIDMissing {
		t.Fatalf("codex empty AgentType must not fall back to id: %+v", st)
	}
	if got.Actions[capability.CapabilityResume].Availability != capability.ActionUnavailable {
		t.Fatalf("resume action=%+v", got.Actions[capability.CapabilityResume])
	}
}

func TestResolveResumeExactWithID(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.ResumeID = "native-id"
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityResume].State != capability.CapabilityExact {
		t.Fatal(got.Status[capability.CapabilityResume])
	}
	if got.Actions[capability.CapabilityResume].Availability != capability.ActionAvailable {
		t.Fatal(got.Actions[capability.CapabilityResume])
	}
}

func TestResolveTerminateEndedSessionActionUnavailable(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	d.UpdatedAt = time.Now().Add(-time.Hour) // not live
	src := &capsFakeReader{agentType: "claude", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	// Capability remains exact; action unavailable.
	if got.Status[capability.CapabilityTerminate].State != capability.CapabilityExact {
		t.Fatalf("terminate cap=%s", got.Status[capability.CapabilityTerminate].State)
	}
	act := got.Actions[capability.CapabilityTerminate]
	if act.Availability != capability.ActionUnavailable || act.ReasonCode != capability.ReasonSessionNotLive {
		t.Fatalf("terminate action=%+v", act)
	}
}

func TestResolveLiveTerminateRuntimeCheckRequired(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(true), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	act := got.Actions[capability.CapabilityTerminate]
	if act.Availability != capability.ActionCheckRequired {
		t.Fatalf("%+v", act)
	}
	// Delete must be unavailable while live.
	del := got.Actions[capability.CapabilityDelete]
	if del.Availability != capability.ActionUnavailable || del.ReasonCode != capability.ReasonSessionRunning {
		t.Fatalf("delete action=%+v", del)
	}
}

func TestResolveLivenessExactFromProvider(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(true), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Liveness.IsLive || got.Liveness.State != capability.CapabilityExact {
		t.Fatalf("%+v", got.Liveness)
	}
}

func TestResolveLivenessTimestampHeuristic(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	// No liveness provider: use a bare BaseSessionReader via struct that only has hasRev.
	// capsFakeReader always implements SessionLivenessProvider — use type that doesn't.
	src := liveRevOnly{agentType: "claude"}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Liveness.IsLive {
		t.Fatal("expected live via timestamp window")
	}
	if got.Liveness.State != capability.CapabilityEstimated ||
		got.Liveness.ReasonCode != capability.ReasonTimestampHeuristic {
		t.Fatalf("%+v", got.Liveness)
	}
}

type liveRevOnly struct{ agentType string }

func (l liveRevOnly) AgentType() string   { return l.agentType }
func (l liveRevOnly) DisplayName() string { return l.agentType }
func (l liveRevOnly) ListSessions() ([]model.Session, error) {
	return nil, nil
}
func (l liveRevOnly) GetSession(id string) (*model.SessionDetail, error) {
	return nil, errors.New("n/a")
}
func (l liveRevOnly) RenderANSI(id string, cols int) (string, error) {
	return "", errors.New("n/a")
}
func (l liveRevOnly) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, errors.New("n/a")
}
func (l liveRevOnly) LiveRevision(id string) (int64, error) { return 42, nil }

func TestResolveProviderFailureFallsBackToTimestamp(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "claude", live: boolPtr(true), liveErr: errors.New("boom"), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Liveness.State != capability.CapabilityEstimated ||
		got.Liveness.ReasonCode != capability.ReasonTimestampHeuristic {
		t.Fatalf("%+v", got.Liveness)
	}
	if !got.Liveness.IsLive {
		t.Fatal("inside window should still be live after fallback")
	}
}

func TestValidateResolvedRejectsIllegalPromotion(t *testing.T) {
	static := fullExactStatic("x")
	static.Capabilities[capability.CapabilityResume] = capability.Unsupported("nope")
	resolved := capability.SessionCapabilities{
		AgentType:       "x",
		AdapterRevision: 1,
		Status:          map[capability.CapabilityID]capability.SessionCapabilityStatus{},
		Liveness: capability.SessionLivenessStatus{
			IsLive: false, State: capability.CapabilityEstimated, ReasonCode: capability.ReasonTimestampHeuristic,
		},
	}
	for _, id := range capability.BaselineIDs() {
		resolved.Status[id] = capability.SessionExact()
	}
	// Illegal: unsupported → exact
	resolved.Status[capability.CapabilityResume] = capability.SessionExact()
	errs := capability.ValidateResolved(resolved, static)
	if len(errs) == 0 {
		t.Fatal("expected illegal promotion error")
	}
	found := false
	for _, e := range errs {
		if e.Code == capability.CodeIllegalPromotion {
			found = true
		}
	}
	if !found {
		t.Fatalf("errs=%v", errs)
	}
}

func TestValidateResolvedRejectsEstimatedToExact(t *testing.T) {
	static := fullExactStatic("x")
	static.Capabilities[capability.CapabilityTokens] = capability.Estimated("h")
	resolved := capability.SessionCapabilities{
		AgentType:       "x",
		AdapterRevision: 1,
		Status:          map[capability.CapabilityID]capability.SessionCapabilityStatus{},
		Liveness: capability.SessionLivenessStatus{
			State: capability.CapabilityExact,
		},
	}
	for _, id := range capability.BaselineIDs() {
		resolved.Status[id] = capability.SessionFromStatic(static.Capabilities[id])
	}
	resolved.Status[capability.CapabilityTokens] = capability.SessionExact()
	errs := capability.ValidateResolved(resolved, static)
	found := false
	for _, e := range errs {
		if e.Code == capability.CodeIllegalPromotion {
			found = true
		}
	}
	if !found {
		t.Fatalf("want illegal promotion, got %v", errs)
	}
}

func TestResolveUnsupportedTerminateAction(t *testing.T) {
	static := fullExactStatic("chrys")
	static.Capabilities[capability.CapabilityTerminate] = capability.Unsupported(capability.ReasonExactPIDUnavailable)
	d := detailBase("chrys", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "chrys", live: boolPtr(true), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityTerminate].State != capability.CapabilityUnsupported {
		t.Fatal(got.Status[capability.CapabilityTerminate])
	}
	act := got.Actions[capability.CapabilityTerminate]
	if act.Availability != capability.ActionUnavailable {
		t.Fatalf("%+v", act)
	}
}

func TestResolveAllTenIDsAndAgentMeta(t *testing.T) {
	static := fullExactStatic("codex")
	d := detailBase("codex", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "codex", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentType != "codex" || got.AdapterRevision != 1 {
		t.Fatalf("meta %+v", got)
	}
	if len(got.Status) != 10 {
		t.Fatal(len(got.Status))
	}
	for _, id := range capability.ActionableIDs() {
		if _, ok := got.Actions[id]; !ok {
			t.Errorf("missing action %s", id)
		}
	}
}

func TestIsSessionLiveUnchanged(t *testing.T) {
	// Existing boolean contract: outside window is false regardless of provider.
	src := &capsFakeReader{agentType: "claude", live: boolPtr(true), hasRev: true}
	s := model.Session{ID: "s", UpdatedAt: time.Now().Add(-time.Hour)}
	if IsSessionLive(src, s) {
		t.Fatal("stale session must not be live")
	}
	s.UpdatedAt = time.Now()
	if !IsSessionLive(src, s) {
		t.Fatal("provider live=true inside window")
	}
}

// bareReader implements only BaseSessionReader — no LiveRevisionProvider —
// so resolveRealtime must downgrade static exact realtime.
type bareReader struct{ agentType string }

func (b bareReader) AgentType() string   { return b.agentType }
func (b bareReader) DisplayName() string { return b.agentType }
func (b bareReader) ListSessions() ([]model.Session, error) {
	return nil, nil
}
func (b bareReader) GetSession(id string) (*model.SessionDetail, error) {
	return nil, errors.New("n/a")
}
func (b bareReader) RenderANSI(id string, cols int) (string, error) {
	return "", errors.New("n/a")
}
func (b bareReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return nil, errors.New("n/a")
}

func TestResolveRealtimeDowngradesWithoutLiveRevisionProvider(t *testing.T) {
	static := fullExactStatic("claude")
	d := detailBase("claude", "s1")
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := bareReader{agentType: "claude"}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Status[capability.CapabilityRealtime]
	if st.State != capability.CapabilityEstimated || st.ReasonCode != capability.ReasonRevisionUnavailable {
		t.Fatalf("realtime=%+v want estimated/revision_unavailable", st)
	}
	// Timestamp liveness still works without a liveness provider.
	if got.Liveness.State != capability.CapabilityEstimated ||
		got.Liveness.ReasonCode != capability.ReasonTimestampHeuristic {
		t.Fatalf("liveness=%+v", got.Liveness)
	}
}
