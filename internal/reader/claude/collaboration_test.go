package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

const (
	collabClaudeRoot    = "cccccccc-dddd-eeee-ffff-000000000001"
	collabClaudeAgentID = "a111explore0001"
)

func collabClaudeFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "collaboration-embedded-child"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func collabClaudeRootSession(t *testing.T, r *ClaudeReader) model.Session {
	t.Helper()
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, s := range list {
		if s.ID == collabClaudeRoot {
			return s
		}
	}
	t.Fatalf("root %s not listed: %+v", collabClaudeRoot, list)
	return model.Session{}
}

func TestClaudeReadCollaborationEmbeddedChild(t *testing.T) {
	r := New(collabClaudeFixtureRoot(t))
	root := collabClaudeRootSession(t, r)

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if g.RootAgentType != "claude" || g.RootSessionID != collabClaudeRoot {
		t.Errorf("coordinates = %s/%s", g.RootAgentType, g.RootSessionID)
	}
	if g.Revision != model.SessionRevision(root) {
		t.Errorf("revision = %d, want %d", g.Revision, model.SessionRevision(root))
	}
	if g.Completeness.State != collaboration.EvidenceExact {
		t.Errorf("completeness = %+v, want exact (toolUseId + task_id joins)", g.Completeness)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("want 2 invocations, got %d: %+v", len(g.Invocations), g.Invocations)
	}

	rootInv := g.Invocations[0]
	if rootInv.ID != collaboration.RootInvocationID("claude", collabClaudeRoot) {
		t.Errorf("root invocation ID = %q", rootInv.ID)
	}
	if rootInv.BackingSession != nil {
		t.Error("root invocation must not carry a BackingSessionRef")
	}

	child := g.Invocations[1]
	wantChildID := collaboration.ChildInvocationID("claude", collabClaudeRoot, collabClaudeAgentID)
	if child.ID != wantChildID {
		t.Errorf("child id = %q, want %q", child.ID, wantChildID)
	}
	if child.DisplayName != "Find the load path" {
		t.Errorf("display name = %q, want source-recorded description", child.DisplayName)
	}
	if child.RoleLabel != "Explore" {
		t.Errorf("role label = %q, want Explore", child.RoleLabel)
	}
	if child.Status != collaboration.StatusCompleted {
		t.Errorf("status = %q, want completed from TaskOutput.task.status", child.Status)
	}
	wantStart := time.Date(2026, 1, 3, 0, 0, 4, 0, time.UTC)
	wantEnd := time.Date(2026, 1, 3, 0, 0, 20, 0, time.UTC)
	if child.StartedAt == nil || !child.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", child.StartedAt, wantStart)
	}
	if child.EndedAt == nil || !child.EndedAt.Equal(wantEnd) {
		t.Errorf("EndedAt = %v, want %v", child.EndedAt, wantEnd)
	}
	if child.TimePrecision.State != collaboration.EvidenceExact {
		t.Errorf("time precision = %+v", child.TimePrecision)
	}
	if child.ContentPrecision.State != collaboration.EvidenceExact {
		t.Errorf("content precision = %+v, want exact embedded transcript", child.ContentPrecision)
	}
	if child.BackingSession != nil {
		t.Errorf("embedded child must not carry a BackingSessionRef: %+v", child.BackingSession)
	}
	if child.SourceIdentity.Kind != collaboration.IdentityAgentID || child.SourceIdentity.NativeID != collabClaudeAgentID {
		t.Errorf("source identity = %+v", child.SourceIdentity)
	}

	if len(g.Delegations) != 1 {
		t.Fatalf("delegations = %d", len(g.Delegations))
	}
	d := g.Delegations[0]
	if d.ID != collaboration.DelegationIDFor(rootInv.ID, wantChildID) {
		t.Errorf("delegation id = %q", d.ID)
	}
	if d.TaskSummary != "Find the load path" {
		t.Errorf("task summary = %q (must be description, not prompt)", d.TaskSummary)
	}
	if d.ExecutionMode != collaboration.ExecutionBackground {
		t.Errorf("execution mode = %q, want background (isAsync + TaskOutput)", d.ExecutionMode)
	}
	if d.Trigger == nil || d.Trigger.EventID != "toolu_explore_1" || d.Trigger.ToolCallID != "toolu_explore_1" {
		t.Errorf("trigger = %+v, want exact Agent tool_use id", d.Trigger)
	}
	if d.Trigger.Precision.State != collaboration.EvidenceExact {
		t.Errorf("trigger precision = %+v, want exact toolUseId join", d.Trigger.Precision)
	}
	if d.Result == nil || d.Result.EventID != "r-done-1-toolresult" || d.Result.ToolCallID != "toolu_taskout_1" {
		t.Errorf("result = %+v, want exact TaskOutput completion", d.Result)
	}
	if d.Result.Precision.State != collaboration.EvidenceExact || d.Evidence.Result.State != collaboration.EvidenceExact {
		t.Errorf("result precision = %+v / evidence %+v", d.Result.Precision, d.Evidence.Result)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("validate: %+v", v.Issues)
	}
}

func TestClaudeCollaborationConformance(t *testing.T) {
	r := New(collabClaudeFixtureRoot(t))
	adaptertest.RunCollaboration(t, r, adaptertest.CollaborationExpect{
		RootSession:          collabClaudeRootSession(t, r),
		MinChildren:          1,
		ForbidBackingSession: true,
	})
}

func TestClaudeCollaborationRenderAssociation(t *testing.T) {
	r := New(collabClaudeFixtureRoot(t))
	events, err := r.GetRenderEvents(collabClaudeRoot)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	wantChildID := collaboration.ChildInvocationID("claude", collabClaudeRoot, collabClaudeAgentID)
	var childEvents, rootEvents int
	for _, e := range events {
		if e.InvocationID == wantChildID {
			childEvents++
			if e.Depth == 0 {
				t.Errorf("child event %s (%s) must be nested, depth=%d", e.EventID, e.Type, e.Depth)
			}
			continue
		}
		if e.InvocationID != "" {
			t.Errorf("root event %s (%s) must stay root-associated, got InvocationID %q",
				e.EventID, e.Type, e.InvocationID)
		}
		rootEvents++
	}
	if childEvents == 0 {
		t.Fatal("expected spliced child events to carry the child InvocationID")
	}
	if rootEvents == 0 {
		t.Fatal("expected parent-stream events to remain root-associated")
	}
}

func TestClaudeReadCollaborationNoSubagents(t *testing.T) {
	dir, sessionID := writeClaudeBasicFixture(t)
	r := New(dir)
	root := model.Session{ID: sessionID, AgentType: "claude", UpdatedAt: time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC)}
	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 1 || len(g.Delegations) != 0 {
		t.Fatalf("want zero-child graph, got inv=%d del=%d", len(g.Invocations), len(g.Delegations))
	}
	if g.Completeness.State != collaboration.EvidenceExact {
		t.Errorf("empty graph completeness = %+v, want exact", g.Completeness)
	}
}

func TestClaudeReadCollaborationSyncLegacy(t *testing.T) {
	dir, sessionID := writeClaudeRichFixture(t)
	r := New(dir)
	root := collabListed(t, r, sessionID)
	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("invocations = %d, want 2", len(g.Invocations))
	}
	child := g.Invocations[1]
	if child.Status != collaboration.StatusCompleted {
		t.Errorf("sync Agent result status = %q, want completed", child.Status)
	}
	if child.ContentPrecision.State != collaboration.EvidenceExact {
		t.Errorf("content = %+v, want exact", child.ContentPrecision)
	}
	if len(g.Delegations) != 1 {
		t.Fatalf("delegations = %d", len(g.Delegations))
	}
	d := g.Delegations[0]
	if d.ExecutionMode != collaboration.ExecutionBlocking {
		t.Errorf("sync completion mode = %q, want blocking", d.ExecutionMode)
	}
	if d.Trigger == nil || d.Trigger.Precision.State != collaboration.EvidenceExact {
		t.Errorf("sync trigger = %+v, want exact toolUseId", d.Trigger)
	}
	if d.Result == nil || d.Result.Precision.State != collaboration.EvidenceExact {
		t.Errorf("sync result = %+v, want exact agentId ToolResult", d.Result)
	}
}

func TestClaudeReadCollaborationInProgressLive(t *testing.T) {
	dir, sessionID, agentID := writeClaudeAsyncOpenFixture(t)
	r := New(dir)
	root := collabListed(t, r, sessionID)
	root.IsLive = true

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("invocations = %d", len(g.Invocations))
	}
	child := g.Invocations[1]
	if child.ID != collaboration.ChildInvocationID("claude", sessionID, agentID) {
		t.Errorf("child id = %q", child.ID)
	}
	if child.Status != collaboration.StatusRunning {
		t.Errorf("live open child status = %q, want running", child.Status)
	}
	if child.EndedAt != nil {
		t.Errorf("open child must not guess EndedAt: %v", child.EndedAt)
	}
	if child.TimePrecision.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("open child time precision = %+v", child.TimePrecision)
	}
	if len(g.Delegations) != 1 || g.Delegations[0].Result != nil {
		t.Errorf("open child must have no result anchor: %+v", g.Delegations)
	}
	if g.Delegations[0].Evidence.Result.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("open child result evidence = %+v", g.Delegations[0].Evidence.Result)
	}
	if g.Delegations[0].ExecutionMode != collaboration.ExecutionBackground {
		t.Errorf("mode = %q", g.Delegations[0].ExecutionMode)
	}

	root.IsLive = false
	root.UpdatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	g2, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration stale: %v", err)
	}
	if g2.Invocations[1].Status != collaboration.StatusOrphaned {
		t.Errorf("stale open child status = %q, want orphaned", g2.Invocations[1].Status)
	}
}

func TestClaudeReadCollaborationUnlinkedSidecar(t *testing.T) {
	dir, sessionID := writeClaudeBasicFixture(t)
	subDir := filepath.Join(dir, "-tmp-proj", sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentID := "orphan0001"
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".jsonl"), []byte(
		`{"type":"user","uuid":"su1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":"orphan"}}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{"agentType": "Explore", "description": "unlinked child"})
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(dir)
	g, err := r.ReadCollaboration(context.Background(), collabListed(t, r, sessionID))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("invocations = %d, want unlinked child preserved", len(g.Invocations))
	}
	if len(g.Delegations) != 0 {
		t.Errorf("unlinked sidecar must not invent a delegation: %+v", g.Delegations)
	}
	v := collaboration.Validate(&g)
	if !v.OK() {
		t.Errorf("unlinked sidecar graph must still validate: %+v", v.Issues)
	}
	if len(v.Unlinked) != 1 {
		t.Fatalf("unlinked = %v, want the sidecar invocation", v.Unlinked)
	}
	if g.Invocations[1].Status != collaboration.StatusUnknown {
		t.Errorf("unlinked sidecar status = %q, want unknown (no parent launch)", g.Invocations[1].Status)
	}
}

func TestClaudeReadCollaborationRejectsChildAsRoot(t *testing.T) {
	r := New(collabClaudeFixtureRoot(t))
	root := collabClaudeRootSession(t, r)
	root.IsSubagent = true
	root.ParentSessionID = "parent-session"
	_, err := r.ReadCollaboration(context.Background(), root)
	if err == nil {
		t.Fatal("expected error for child-as-root")
	}
}

func TestClaudeReadCollaborationWrongAgentType(t *testing.T) {
	r := New(collabClaudeFixtureRoot(t))
	_, err := r.ReadCollaboration(context.Background(), model.Session{ID: collabClaudeRoot, AgentType: "codex"})
	if err == nil {
		t.Fatal("expected error for non-claude root")
	}
}

func TestClaudeCollaborationSidecarsNotListedAsSessions(t *testing.T) {
	r := New(collabClaudeFixtureRoot(t))
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == collabClaudeAgentID || s.ID == "agent-"+collabClaudeAgentID {
			t.Fatalf("sidecar listed as a Session: %+v", s)
		}
		if s.IsSubagent || s.ParentSessionID != "" {
			t.Fatalf("Claude ListSessions must not mark embedded children as sessions: %+v", s)
		}
	}
}

func collabListed(t *testing.T, r *ClaudeReader, id string) model.Session {
	t.Helper()
	list, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s not listed", id)
	return model.Session{}
}

func writeClaudeAsyncOpenFixture(t *testing.T) (projectsDir, sessionID, agentID string) {
	t.Helper()
	projectsDir = t.TempDir()
	sessionID = "dddddddd-eeee-ffff-0000-111111111111"
	agentID = "a222plan0002"
	proj := filepath.Join(projectsDir, "-tmp-proj")
	subDir := filepath.Join(proj, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := `{"type":"user","uuid":"u1","timestamp":"2026-01-03T00:00:00Z","cwd":"/tmp/proj","sessionId":"` + sessionID + `","message":{"role":"user","content":"plan it"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-03T00:00:04Z","message":{"role":"assistant","model":"claude-sonnet-4","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_plan_1","name":"Agent","input":{"description":"Design the plan","subagent_type":"Plan","prompt":"write a plan"}}]}}
{"type":"user","uuid":"r1","timestamp":"2026-01-03T00:00:05Z","toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"` + agentID + `","description":"Design the plan"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_plan_1","content":"async"}]}}
`
	if err := os.WriteFile(filepath.Join(proj, sessionID+".jsonl"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"agentType": "Plan", "description": "Design the plan", "toolUseId": "toolu_plan_1"}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".meta.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	subBody := `{"type":"user","uuid":"su1","timestamp":"2026-01-03T00:00:06Z","message":{"role":"user","content":"write a plan"}}
{"type":"assistant","uuid":"sa1","timestamp":"2026-01-03T00:00:07Z","message":{"role":"assistant","content":[{"type":"text","text":"still working"}]}}
`
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".jsonl"), []byte(subBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectsDir, sessionID, agentID
}

func TestClaudeTimePrecisionEndOnly(t *testing.T) {
	got := claudeTimePrecision(false, true)
	if got.State != collaboration.EvidenceEstimated || got.ReasonCode != collaboration.ReasonSourceNotRecorded {
		t.Fatalf("end-only precision = %+v, want estimated/source_not_recorded", got)
	}
	if claudeTimePrecision(false, false).State != collaboration.EvidenceMissing {
		t.Fatal("neither boundary must stay missing")
	}
}

func TestClaudeReadCollaborationTaskOutputOutOfOrder(t *testing.T) {
	dir := t.TempDir()
	sessionID := "eeeeeeee-ffff-0000-1111-222222222222"
	firstID, secondID := "a333explore0003", "a444plan0004"
	proj := filepath.Join(dir, "-tmp-proj")
	subDir := filepath.Join(proj, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := `{"type":"user","uuid":"u1","timestamp":"2026-01-03T00:00:00Z","cwd":"/tmp/proj","sessionId":"` + sessionID + `","message":{"role":"user","content":"delegate two"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-03T00:00:01Z","message":{"role":"assistant","model":"claude-sonnet-4","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_first","name":"Agent","input":{"description":"First child","subagent_type":"Explore","prompt":"one"}},{"type":"tool_use","id":"toolu_second","name":"Agent","input":{"description":"Second child","subagent_type":"Plan","prompt":"two"}}]}}
{"type":"user","uuid":"r1","timestamp":"2026-01-03T00:00:02Z","toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"` + firstID + `","description":"First child"}}
{"type":"user","uuid":"r2","timestamp":"2026-01-03T00:00:03Z","toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"` + secondID + `","description":"Second child"}}
{"type":"assistant","uuid":"a2","timestamp":"2026-01-03T00:00:04Z","message":{"role":"assistant","model":"claude-sonnet-4","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_wait_first","name":"TaskOutput","input":{"task_id":"` + firstID + `","block":true}},{"type":"tool_use","id":"toolu_wait_second","name":"TaskOutput","input":{"task_id":"` + secondID + `","block":true}}]}}
{"type":"user","uuid":"done-second","timestamp":"2026-01-03T00:00:10Z","toolUseResult":{"task":{"task_id":"` + secondID + `","status":"completed"}}}
{"type":"user","uuid":"done-first","timestamp":"2026-01-03T00:00:11Z","toolUseResult":{"task":{"task_id":"` + firstID + `","status":"completed"}}}
`
	if err := os.WriteFile(filepath.Join(proj, sessionID+".jsonl"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	writeChild := func(id, role, desc, toolUseID string) {
		t.Helper()
		meta, _ := json.Marshal(map[string]string{"agentType": role, "description": desc, "toolUseId": toolUseID})
		if err := os.WriteFile(filepath.Join(subDir, "agent-"+id+".meta.json"), meta, 0o644); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","uuid":"su-` + id + `","timestamp":"2026-01-03T00:00:05Z","message":{"role":"user","content":"` + desc + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(subDir, "agent-"+id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeChild(firstID, "Explore", "First child", "toolu_first")
	writeChild(secondID, "Plan", "Second child", "toolu_second")

	r := New(dir)
	g, err := r.ReadCollaboration(context.Background(), collabListed(t, r, sessionID))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Delegations) != 2 {
		t.Fatalf("delegations = %d, want 2", len(g.Delegations))
	}
	byChild := map[string]collaboration.Delegation{}
	for _, d := range g.Delegations {
		byChild[d.ChildInvocationID] = d
	}
	first := byChild[collaboration.ChildInvocationID("claude", sessionID, firstID)]
	second := byChild[collaboration.ChildInvocationID("claude", sessionID, secondID)]
	if first.Result == nil || first.Result.ToolCallID != "toolu_wait_first" {
		t.Errorf("first result ToolCallID = %+v, want toolu_wait_first (exact task_id, not FIFO)", first.Result)
	}
	if second.Result == nil || second.Result.ToolCallID != "toolu_wait_second" {
		t.Errorf("second result ToolCallID = %+v, want toolu_wait_second", second.Result)
	}
	if first.Result != nil && first.Result.EventID != "done-first-toolresult" {
		t.Errorf("first result EventID = %q", first.Result.EventID)
	}
	if second.Result != nil && second.Result.EventID != "done-second-toolresult" {
		t.Errorf("second result EventID = %q", second.Result.EventID)
	}
}
