package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Focused tests for the Codex standalone-child collaboration mapping.
// Fixture provenance and layout:
// testdata/collaboration-standalone-child/README.md.

func collabRootSession(t *testing.T, r *CodexReader) model.Session {
	t.Helper()
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, s := range list {
		if s.ID == collabRootID {
			return s
		}
	}
	t.Fatalf("root session %s not listed", collabRootID)
	return model.Session{}
}

func TestCodexReadCollaborationStandaloneChild(t *testing.T) {
	r := New(collabFixtureSessionsDir(t))
	root := collabRootSession(t, r)

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}

	if g.RootAgentType != "codex" || g.RootSessionID != collabRootID {
		t.Errorf("graph coordinates = %s/%s, want codex/%s", g.RootAgentType, g.RootSessionID, collabRootID)
	}
	if g.Revision != model.SessionRevision(root) {
		t.Errorf("revision = %d, want session revision %d", g.Revision, model.SessionRevision(root))
	}
	if g.Completeness.State != collaboration.EvidenceEstimated ||
		g.Completeness.ReasonCode != collaboration.ReasonSourceNotRecorded {
		t.Errorf("completeness = %+v, want estimated/source_not_recorded", g.Completeness)
	}

	if len(g.Invocations) != 2 {
		t.Fatalf("want 2 invocations (root + child), got %d: %+v", len(g.Invocations), g.Invocations)
	}
	rootInv := g.Invocations[0]
	if rootInv.ID != collaboration.RootInvocationID("codex", collabRootID) {
		t.Errorf("root invocation ID = %q, want deterministic root", rootInv.ID)
	}
	if rootInv.SourceIdentity.Kind != collaboration.IdentityRootSession ||
		rootInv.SourceIdentity.NativeID != collabRootID {
		t.Errorf("root source identity = %+v", rootInv.SourceIdentity)
	}
	if rootInv.BackingSession != nil {
		t.Error("root invocation must not carry a BackingSessionRef")
	}

	child := g.Invocations[1]
	wantChildID := collaboration.ChildInvocationID("codex", collabRootID, collabChildNative)
	if child.ID != wantChildID {
		t.Errorf("child invocation ID = %q, want %q", child.ID, wantChildID)
	}
	if child.DisplayName != "audit" || child.RoleLabel != "audit" {
		t.Errorf("child display/role = %q/%q, want audit/audit (agent_path last segment)",
			child.DisplayName, child.RoleLabel)
	}
	if child.Status != collaboration.StatusUnknown {
		t.Errorf("child status = %q, want unknown (no completion evidence)", child.Status)
	}
	if child.StartedAt == nil {
		t.Error("child StartedAt must be set from exact session timestamps")
	}
	if child.EndedAt != nil {
		t.Errorf("child EndedAt must stay open, got %v", child.EndedAt)
	}
	if child.TimePrecision.State != collaboration.EvidenceEstimated ||
		child.TimePrecision.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("child time precision = %+v, want estimated/completion_not_recorded", child.TimePrecision)
	}
	if child.ContentPrecision.State != collaboration.EvidenceExact {
		t.Errorf("child content precision = %+v, want exact (full standalone transcript)", child.ContentPrecision)
	}
	if child.BackingSession == nil ||
		child.BackingSession.AgentType != "codex" || child.BackingSession.SessionID != collabChildID {
		t.Errorf("child backing session = %+v, want codex/%s", child.BackingSession, collabChildID)
	}
	if child.SourceIdentity.Kind != collaboration.IdentityPayloadID ||
		child.SourceIdentity.NativeID != collabChildNative {
		t.Errorf("child source identity = %+v, want payload_id/%s", child.SourceIdentity, collabChildNative)
	}
	if child.SourceIdentity.Attributes["rollout_stem"] != collabChildID {
		t.Errorf("rollout_stem attribute = %q, want %q",
			child.SourceIdentity.Attributes["rollout_stem"], collabChildID)
	}

	if len(g.Delegations) != 1 {
		t.Fatalf("want 1 delegation, got %d", len(g.Delegations))
	}
	d := g.Delegations[0]
	if d.ID != collaboration.DelegationIDFor(rootInv.ID, wantChildID) {
		t.Errorf("delegation ID = %q, want %q", d.ID, collaboration.DelegationIDFor(rootInv.ID, wantChildID))
	}
	if d.ParentInvocationID != rootInv.ID || d.ChildInvocationID != wantChildID {
		t.Errorf("delegation endpoints = %q -> %q", d.ParentInvocationID, d.ChildInvocationID)
	}
	// No guessed launch/result anchors.
	if d.Trigger != nil || d.Result != nil {
		t.Errorf("codex anchors must be absent, not synthesized: trigger=%+v result=%+v", d.Trigger, d.Result)
	}
	if d.ExecutionMode != collaboration.ExecutionUnknown {
		t.Errorf("execution mode = %q, want unknown", d.ExecutionMode)
	}
	if d.TaskSummary != "" {
		t.Errorf("task summary must stay empty (full delegated prompts are never stored), got %q", d.TaskSummary)
	}
	missing := collaboration.ReasonSourceNotRecorded
	for name, fact := range map[string]collaboration.FactEvidence{
		"trigger": d.Evidence.Trigger, "task": d.Evidence.Task, "result": d.Evidence.Result,
	} {
		if fact.State != collaboration.EvidenceMissing || fact.ReasonCode != missing {
			t.Errorf("evidence.%s = %+v, want missing/source_not_recorded", name, fact)
		}
	}
	if d.Evidence.Timing.State != collaboration.EvidenceEstimated ||
		d.Evidence.Timing.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("evidence.timing = %+v, want estimated/completion_not_recorded", d.Evidence.Timing)
	}

	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// Shared conformance: two-parse full-graph equality, root-session ownership,
// contract validation, and the standalone-child backing-Session rule.
func TestCodexCollaborationConformance(t *testing.T) {
	r := New(collabFixtureSessionsDir(t))
	adaptertest.RunCollaboration(t, r, adaptertest.CollaborationExpect{
		RootSession:           collabRootSession(t, r),
		MinChildren:           1,
		RequireBackingSession: true,
	})
}

// The child stays discoverable by the adapter (the shared backend filters it
// from root lists); an accidental child-as-root request is an explicit,
// deterministic error — never a second root graph.
func TestCodexReadCollaborationChildAsRootRejected(t *testing.T) {
	r := New(collabFixtureSessionsDir(t))
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var child model.Session
	for _, s := range list {
		if s.ID == collabChildID {
			child = s
		}
	}
	if child.ID == "" {
		t.Fatal("child rollout not discoverable by the adapter")
	}
	if _, err := r.ReadCollaboration(context.Background(), child); err == nil {
		t.Fatal("child-as-root must be rejected explicitly")
	} else if !strings.Contains(err.Error(), "root sessions only") {
		t.Errorf("error must name the root-only rule, got %v", err)
	}
}

// Discovery honors context cancellation.
func TestCodexReadCollaborationCancelled(t *testing.T) {
	r := New(collabFixtureSessionsDir(t))
	root := collabRootSession(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ReadCollaboration(ctx, root); err == nil {
		t.Fatal("cancelled context must abort the read")
	}
}

// A root without children yields a valid root-only graph.
func TestCodexReadCollaborationNoChildren(t *testing.T) {
	dir, sessionID := writeCodexBasicFixture(t)
	r := New(dir)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 session, got %d", len(list))
	}
	g, err := r.ReadCollaboration(context.Background(), list[0])
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 1 || len(g.Delegations) != 0 {
		t.Errorf("want root-only graph, got %+v", g)
	}
	if g.Invocations[0].ID != collaboration.RootInvocationID("codex", sessionID) {
		t.Errorf("root invocation ID = %q", g.Invocations[0].ID)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("root-only graph must validate clean, got %+v", v.Issues)
	}
}

// A subagent rollout without its own payload.id has no stable native identity
// and must be left out rather than identified by a positional synthesis.
func TestCodexReadCollaborationChildWithoutPayloadID(t *testing.T) {
	dir := t.TempDir()
	writeRollout(t, dir, "rollout-2026-01-01T00-00-00-019f0000-0000-7000-8000-0000000000cc",
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"019f0000-0000-7000-8000-0000000000cc","cwd":"/tmp/proj"}}
{"timestamp":"2026-01-01T00:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"root with broken child"}}
`)
	// Child claims lineage but has no payload.id of its own.
	writeRollout(t, dir, "rollout-2026-01-01T00-00-01-broken-child",
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"session_meta","payload":{"session_id":"019f0000-0000-7000-8000-0000000000cc","parent_thread_id":"019f0000-0000-7000-8000-0000000000cc","thread_source":"subagent","agent_path":"/root/broken","cwd":"/tmp/proj"}}
{"timestamp":"2026-01-01T00:00:03.000Z","type":"event_msg","payload":{"type":"user_message","message":"broken child"}}
`)
	r := New(dir)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var root model.Session
	for _, s := range list {
		if !s.IsSubagent {
			root = s
		}
	}
	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 1 {
		t.Errorf("identity-less child must be excluded, got %d invocations", len(g.Invocations))
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// Rendering the child through its backing Session marks every event with the
// child invocation ID; root events stay unmarked.
func TestCodexCollaborationRenderAssociation(t *testing.T) {
	r := New(collabFixtureSessionsDir(t))

	childEvents, err := r.GetRenderEvents(collabChildID)
	if err != nil {
		t.Fatalf("GetRenderEvents(child): %v", err)
	}
	if len(childEvents) == 0 {
		t.Fatal("child render events must not be empty")
	}
	wantID := collaboration.ChildInvocationID("codex", collabRootID, collabChildNative)
	for _, e := range childEvents {
		if e.InvocationID != wantID {
			t.Errorf("child event %s InvocationID = %q, want %q", e.EventID, e.InvocationID, wantID)
		}
	}

	rootEvents, err := r.GetRenderEvents(collabRootID)
	if err != nil {
		t.Fatalf("GetRenderEvents(root): %v", err)
	}
	if len(rootEvents) == 0 {
		t.Fatal("root render events must not be empty")
	}
	for _, e := range rootEvents {
		if e.InvocationID != "" {
			t.Errorf("root event %s must stay unmarked, got InvocationID %q", e.EventID, e.InvocationID)
		}
	}
}

// A trailing slash in agent_path must not leak into the role label.
func TestCodexChildRoleLabelTrailingSlash(t *testing.T) {
	inv, _ := codexChildCollaboration("root-1", collaboration.RootInvocationID("codex", "root-1"), model.Session{
		ID:         "rollout-child-1",
		AgentType:  "codex",
		ResumeID:   "child-native-1",
		AgentPath:  "/root/audit/",
		IsSubagent: true,
	})
	if inv.RoleLabel != "audit" || inv.DisplayName != "audit" {
		t.Errorf("role/display = %q/%q, want audit/audit (trailing slash stripped)",
			inv.RoleLabel, inv.DisplayName)
	}
}

// writeRollout writes one rollout JSONL under sessions/YYYY/MM/DD/.
func writeRollout(t *testing.T, sessionsDir, stem, content string) {
	t.Helper()
	day := filepath.Join(sessionsDir, "2026", "01", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(day, stem+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
