package copilot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Focused tests for the Copilot lifecycle-only collaboration mapping.
// Fixture provenance and layout:
// testdata/collaboration-lifecycle-only/README.md.

// collabCopilotRoot builds the root Session for the fixture. UpdatedAt is
// the fixture's recorded update time (long past the live window), so the
// root is not live unless a test says otherwise.
func collabCopilotRoot(t *testing.T) model.Session {
	t.Helper()
	updated, err := time.Parse(time.RFC3339, "2026-01-01T00:05:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return model.Session{
		ID:        "collab-copilot-1",
		AgentType: "copilot",
		UpdatedAt: updated,
	}
}

func collabCopilotChild(t *testing.T, g collaboration.CollaborationGraph, nativeID string) collaboration.AgentInvocation {
	t.Helper()
	want := collaboration.ChildInvocationID("copilot", "collab-copilot-1", nativeID)
	for _, inv := range g.Invocations {
		if inv.ID == want {
			return inv
		}
	}
	t.Fatalf("invocation %q not in graph: %+v", want, g.Invocations)
	return collaboration.AgentInvocation{}
}

func collabCopilotDelegation(t *testing.T, g collaboration.CollaborationGraph, nativeID string) collaboration.Delegation {
	t.Helper()
	child := collaboration.ChildInvocationID("copilot", "collab-copilot-1", nativeID)
	root := collaboration.RootInvocationID("copilot", "collab-copilot-1")
	want := collaboration.DelegationIDFor(root, child)
	for _, d := range g.Delegations {
		if d.ID == want {
			return d
		}
	}
	t.Fatalf("delegation %q not in graph: %+v", want, g.Delegations)
	return collaboration.Delegation{}
}

func TestCopilotReadCollaborationLifecycle(t *testing.T) {
	r := New(collabCopilotFixtureStateDir(t))
	root := collabCopilotRoot(t)

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}

	if g.RootAgentType != "copilot" || g.RootSessionID != "collab-copilot-1" {
		t.Errorf("graph coordinates = %s/%s", g.RootAgentType, g.RootSessionID)
	}
	if g.Revision != model.SessionRevision(root) {
		t.Errorf("revision = %d, want %d", g.Revision, model.SessionRevision(root))
	}
	if g.Completeness.State != collaboration.EvidenceExact {
		t.Errorf("completeness = %+v, want exact", g.Completeness)
	}
	// Root + completed child + orphaned child, root first, children in
	// first-observed (launch) order.
	if len(g.Invocations) != 3 {
		t.Fatalf("want 3 invocations, got %d: %+v", len(g.Invocations), g.Invocations)
	}
	rootInv := g.Invocations[0]
	if rootInv.ID != collaboration.RootInvocationID("copilot", "collab-copilot-1") {
		t.Errorf("root invocation ID = %q", rootInv.ID)
	}
	// The fixture records no session.shutdown and is past the live window.
	if rootInv.Status != collaboration.StatusUnknown {
		t.Errorf("root status = %q, want unknown (no shutdown evidence, not live)", rootInv.Status)
	}
	if rootInv.BackingSession != nil {
		t.Error("root invocation must not carry a BackingSessionRef")
	}
	if g.Invocations[1].SourceIdentity.NativeID != "call-task-A" ||
		g.Invocations[2].SourceIdentity.NativeID != "call-task-B" {
		t.Errorf("children out of launch order: %q, %q",
			g.Invocations[1].SourceIdentity.NativeID, g.Invocations[2].SourceIdentity.NativeID)
	}

	// --- Completed child (call-task-A) ---
	a := collabCopilotChild(t, g, "call-task-A")
	if a.DisplayName != "Impl Agent" {
		t.Errorf("A display name = %q, want agentDisplayName label", a.DisplayName)
	}
	if a.RoleLabel != "impl" {
		t.Errorf("A role label = %q, want task arguments.name", a.RoleLabel)
	}
	if a.Status != collaboration.StatusCompleted {
		t.Errorf("A status = %q, want completed", a.Status)
	}
	wantStart := time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 1, 1, 0, 1, 10, 0, time.UTC)
	if a.StartedAt == nil || !a.StartedAt.Equal(wantStart) {
		t.Errorf("A StartedAt = %v, want %v", a.StartedAt, wantStart)
	}
	if a.EndedAt == nil || !a.EndedAt.Equal(wantEnd) {
		t.Errorf("A EndedAt = %v, want %v", a.EndedAt, wantEnd)
	}
	if a.TimePrecision.State != collaboration.EvidenceExact {
		t.Errorf("A time precision = %+v, want exact", a.TimePrecision)
	}
	if a.ContentPrecision.State != collaboration.EvidenceEstimated ||
		a.ContentPrecision.ReasonCode != collaboration.ReasonAggregateWindow {
		t.Errorf("A content precision = %+v, want estimated/aggregate_window", a.ContentPrecision)
	}
	if a.BackingSession != nil {
		t.Errorf("lifecycle-only child must not carry a BackingSessionRef: %+v", a.BackingSession)
	}
	if a.SourceIdentity.Kind != collaboration.IdentityToolCallID ||
		a.SourceIdentity.NativeID != "call-task-A" {
		t.Errorf("A source identity = %+v", a.SourceIdentity)
	}

	da := collabCopilotDelegation(t, g, "call-task-A")
	if da.Trigger == nil || da.Trigger.ToolCallID != "call-task-A" ||
		da.Trigger.Precision.State != collaboration.EvidenceExact {
		t.Errorf("A trigger = %+v, want exact toolCallId anchor", da.Trigger)
	}
	wantTrigger := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	if da.Trigger.Timestamp == nil || !da.Trigger.Timestamp.Equal(wantTrigger) {
		t.Errorf("A trigger timestamp = %v, want %v", da.Trigger.Timestamp, wantTrigger)
	}
	if da.Result == nil || da.Result.ToolCallID != "call-task-A" ||
		da.Result.Precision.State != collaboration.EvidenceExact {
		t.Errorf("A result = %+v, want exact lifecycle anchor", da.Result)
	}
	if da.Result.Timestamp == nil || !da.Result.Timestamp.Equal(wantEnd) {
		t.Errorf("A result timestamp = %v, want %v", da.Result.Timestamp, wantEnd)
	}
	if da.TaskSummary != "Implement parser change" {
		t.Errorf("A task summary = %q, want source-recorded description", da.TaskSummary)
	}
	// Task-summary privacy: the full delegation prompt must never be stored.
	if da.TaskSummary == "Implement the parser change in reader.go" {
		t.Error("full delegation prompt stored as task summary")
	}
	if da.ExecutionMode != collaboration.ExecutionBlocking {
		t.Errorf("A execution mode = %q, want blocking (sync)", da.ExecutionMode)
	}
	for name, fact := range map[string]collaboration.FactEvidence{
		"trigger": da.Evidence.Trigger, "timing": da.Evidence.Timing,
		"task": da.Evidence.Task, "result": da.Evidence.Result,
	} {
		if fact.State != collaboration.EvidenceExact {
			t.Errorf("A evidence.%s = %+v, want exact", name, fact)
		}
	}

	// --- Orphaned child (call-task-B): started, never completed, root closed ---
	b := collabCopilotChild(t, g, "call-task-B")
	if b.Status != collaboration.StatusOrphaned {
		t.Errorf("B status = %q, want orphaned (started, no completion, root not live)", b.Status)
	}
	wantBStart := time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC)
	if b.StartedAt == nil || !b.StartedAt.Equal(wantBStart) {
		t.Errorf("B StartedAt = %v, want %v", b.StartedAt, wantBStart)
	}
	if b.EndedAt != nil {
		t.Errorf("B must not guess an end timestamp, got %v", b.EndedAt)
	}
	if b.TimePrecision.State != collaboration.EvidenceEstimated ||
		b.TimePrecision.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("B time precision = %+v, want estimated/completion_not_recorded", b.TimePrecision)
	}

	db := collabCopilotDelegation(t, g, "call-task-B")
	if db.Trigger == nil || db.Trigger.Precision.State != collaboration.EvidenceExact {
		t.Errorf("B trigger = %+v, want exact lifecycle anchor", db.Trigger)
	}
	if db.Result != nil {
		t.Errorf("B result anchor must be absent, got %+v", db.Result)
	}
	if db.Evidence.Result.State != collaboration.EvidenceMissing ||
		db.Evidence.Result.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("B evidence.result = %+v, want missing/completion_not_recorded", db.Evidence.Result)
	}
	if db.TaskSummary != "Review the diff" {
		t.Errorf("B task summary = %q, want source-recorded description", db.TaskSummary)
	}
	if db.ExecutionMode != collaboration.ExecutionBackground {
		t.Errorf("B execution mode = %q, want background (async)", db.ExecutionMode)
	}

	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// Liveness-sensitive status: the same started-without-completed child is
// running while the root is live, orphaned only once it is not.
func TestCopilotReadCollaborationLivenessSensitiveStatus(t *testing.T) {
	r := New(collabCopilotFixtureStateDir(t))
	root := collabCopilotRoot(t)
	root.IsLive = true

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	b := collabCopilotChild(t, g, "call-task-B")
	if b.Status != collaboration.StatusRunning {
		t.Errorf("B status with live root = %q, want running", b.Status)
	}
	a := collabCopilotChild(t, g, "call-task-A")
	if a.Status != collaboration.StatusCompleted {
		t.Errorf("A status with live root = %q, want completed (lifecycle is explicit)", a.Status)
	}
}

// Shared conformance: two-parse full-graph equality, root-session ownership,
// contract validation, and the lifecycle-only no-backing-Session rule.
func TestCopilotCollaborationConformance(t *testing.T) {
	r := New(collabCopilotFixtureStateDir(t))
	adaptertest.RunCollaboration(t, r, adaptertest.CollaborationExpect{
		RootSession:          collabCopilotRoot(t),
		MinChildren:          2,
		ForbidBackingSession: true,
	})
}

// Subagent lifecycle markers carry the child invocation ID; every other
// parent-stream event stays root-associated so a reconstructed window is
// never presented as an exact child transcript.
func TestCopilotCollaborationRenderAssociation(t *testing.T) {
	r := New(collabCopilotFixtureStateDir(t))
	events, err := r.GetRenderEvents("collab-copilot-1")
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	marked := map[string]bool{}
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "subagent_started" {
			switch e.Text {
			case "Impl Agent":
				marked["call-task-A"] = e.InvocationID ==
					collaboration.ChildInvocationID("copilot", "collab-copilot-1", "call-task-A")
			case "Review Agent":
				marked["call-task-B"] = e.InvocationID ==
					collaboration.ChildInvocationID("copilot", "collab-copilot-1", "call-task-B")
			}
			continue
		}
		if e.InvocationID != "" {
			t.Errorf("parent-stream event %s (%s) must stay root-associated, got InvocationID %q",
				e.EventID, e.Type, e.InvocationID)
		}
	}
	if !marked["call-task-A"] || !marked["call-task-B"] {
		t.Errorf("subagent_started markers must carry child invocation IDs: %v", marked)
	}
}

// Malformed lines are skipped and a missing events.jsonl is an explicit
// error, matching the other Copilot read paths.
func TestCopilotReadCollaborationPartialSource(t *testing.T) {
	r := New(collabCopilotFixtureStateDir(t))
	root := collabCopilotRoot(t)
	if _, err := r.ReadCollaboration(context.Background(), model.Session{
		ID: "no-such-session", AgentType: "copilot",
	}); err == nil {
		t.Error("missing events.jsonl must be an explicit error")
	}
	_ = root

	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "partial-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := `{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"partial"}}
{not json
{"type":"tool.execution_start","timestamp":"2026-01-01T00:00:01Z","data":{"toolName":"task","toolCallId":"call-p1","arguments":{"description":"Partial child","mode":"sync"}}}
{"type":"subagent.started","timestamp":"2026-01-01T00:00:02Z","data":{"toolCallId":"call-p1","agentDisplayName":"Partial Agent"}}
`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := New(stateDir)
	g, err := pr.ReadCollaboration(context.Background(), model.Session{
		ID:        "partial-1",
		AgentType: "copilot",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 || len(g.Delegations) != 1 {
		t.Fatalf("want root + one child despite the malformed line, got %+v", g)
	}
	child := g.Invocations[1]
	if child.Status != collaboration.StatusOrphaned {
		t.Errorf("partial child status = %q, want orphaned (root not live)", child.Status)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// A task event with an unparseable timestamp still yields an exact
// toolCallId anchor, but never a zero-time timestamp presented as exact.
func TestCopilotTriggerAnchorUnparseableTimestamp(t *testing.T) {
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "badts-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := `{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"bad ts"}}
{"type":"tool.execution_start","timestamp":"not-a-time","data":{"toolName":"task","toolCallId":"call-badts","arguments":{"description":"Bad timestamp child"}}}
{"type":"subagent.started","timestamp":"2026-01-01T00:00:02Z","data":{"toolCallId":"call-badts","agentDisplayName":"Bad TS Agent"}}
`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(stateDir)
	g, err := r.ReadCollaboration(context.Background(), model.Session{
		ID:        "badts-1",
		AgentType: "copilot",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Delegations) != 1 {
		t.Fatalf("want 1 delegation, got %+v", g.Delegations)
	}
	trigger := g.Delegations[0].Trigger
	if trigger == nil || trigger.ToolCallID != "call-badts" ||
		trigger.Precision.State != collaboration.EvidenceExact {
		t.Fatalf("trigger = %+v, want exact toolCallId anchor", trigger)
	}
	if trigger.Timestamp != nil {
		t.Errorf("unparseable task timestamp must stay absent, got %v", trigger.Timestamp)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// Cancellation is honored during the stream scan.
func TestCopilotReadCollaborationCancelled(t *testing.T) {
	r := New(collabCopilotFixtureStateDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ReadCollaboration(ctx, collabCopilotRoot(t)); err == nil {
		t.Fatal("cancelled context must abort the read")
	}
}

// A recorded session.shutdown is explicit root completion evidence.
func TestCopilotReadCollaborationRootShutdown(t *testing.T) {
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "shutdown-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := `{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"done"}}
{"type":"session.shutdown","timestamp":"2026-01-01T00:10:00Z","data":{}}
`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(stateDir)
	g, err := r.ReadCollaboration(context.Background(), model.Session{
		ID:        "shutdown-1",
		AgentType: "copilot",
		UpdatedAt: time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if g.Invocations[0].Status != collaboration.StatusCompleted {
		t.Errorf("root status = %q, want completed (session.shutdown recorded)", g.Invocations[0].Status)
	}
}
