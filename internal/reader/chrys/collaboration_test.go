package chrys

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Focused tests for the Chrys embedded-child collaboration mapping.
// Fixture provenance and layout:
// testdata/collaboration-embedded-child/README.md.

func collabChrysRootSession(t *testing.T, r *ChrysReader) model.Session {
	t.Helper()
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 || list[0].ID != collabChrysRoot {
		t.Fatalf("want exactly the root session, got %+v", list)
	}
	return list[0]
}

func TestChrysReadCollaborationEmbeddedChild(t *testing.T) {
	r := New(collabChrysFixtureRoot(t))
	root := collabChrysRootSession(t, r)

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}

	if g.RootAgentType != "chrys" || g.RootSessionID != collabChrysRoot {
		t.Errorf("graph coordinates = %s/%s", g.RootAgentType, g.RootSessionID)
	}
	if g.Revision != model.SessionRevision(root) {
		t.Errorf("revision = %d, want %d", g.Revision, model.SessionRevision(root))
	}
	if g.Completeness.State != collaboration.EvidenceExact {
		t.Errorf("completeness = %+v, want exact", g.Completeness)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("want 2 invocations, got %d: %+v", len(g.Invocations), g.Invocations)
	}

	rootInv := g.Invocations[0]
	if rootInv.ID != collaboration.RootInvocationID("chrys", collabChrysRoot) {
		t.Errorf("root invocation ID = %q", rootInv.ID)
	}
	if rootInv.BackingSession != nil {
		t.Error("root invocation must not carry a BackingSessionRef")
	}

	child := g.Invocations[1]
	wantChildID := collaboration.ChildInvocationID("chrys", collabChrysRoot, "call_sub_1")
	if child.ID != wantChildID {
		t.Errorf("child invocation ID = %q, want %q", child.ID, wantChildID)
	}
	if child.DisplayName != "Explore Agent" {
		t.Errorf("display name = %q, want source-recorded agent_display_name", child.DisplayName)
	}
	if child.RoleLabel != "explore_agent" {
		t.Errorf("role label = %q, want source-recorded tool_name", child.RoleLabel)
	}
	// Recorded status and timing are normalized, not left unused.
	if child.Status != collaboration.StatusCompleted {
		t.Errorf("status = %q, want completed from meta.status", child.Status)
	}
	wantStart := time.Date(2026, 7, 6, 4, 18, 43, 191495000, time.UTC)
	wantEnd := time.Date(2026, 7, 6, 4, 19, 33, 948229000, time.UTC)
	if child.StartedAt == nil || !child.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v (meta.created_at)", child.StartedAt, wantStart)
	}
	if child.EndedAt == nil || !child.EndedAt.Equal(wantEnd) {
		t.Errorf("EndedAt = %v, want %v (meta.updated_at)", child.EndedAt, wantEnd)
	}
	if child.TimePrecision.State != collaboration.EvidenceExact {
		t.Errorf("time precision = %+v, want exact", child.TimePrecision)
	}
	if child.ContentPrecision.State != collaboration.EvidenceExact {
		t.Errorf("content precision = %+v, want exact (full embedded transcript)", child.ContentPrecision)
	}
	if child.BackingSession != nil {
		t.Errorf("embedded child must not carry a BackingSessionRef: %+v", child.BackingSession)
	}
	if child.SourceIdentity.Kind != collaboration.IdentityProviderCallID ||
		child.SourceIdentity.NativeID != "call_sub_1" {
		t.Errorf("source identity = %+v, want provider_call_id/call_sub_1", child.SourceIdentity)
	}
	if child.SourceIdentity.Attributes["invocation_id"] != "e9a4ee5e36db" {
		t.Errorf("invocation_id attribute = %q", child.SourceIdentity.Attributes["invocation_id"])
	}

	if len(g.Delegations) != 1 {
		t.Fatalf("want 1 delegation, got %d", len(g.Delegations))
	}
	d := g.Delegations[0]
	if d.ID != collaboration.DelegationIDFor(rootInv.ID, wantChildID) {
		t.Errorf("delegation ID = %q", d.ID)
	}
	if d.ParentInvocationID != rootInv.ID || d.ChildInvocationID != wantChildID {
		t.Errorf("delegation endpoints = %q -> %q", d.ParentInvocationID, d.ChildInvocationID)
	}
	// Exact two-sided join anchors on existing render event/tool-call IDs.
	if d.Trigger == nil || d.Trigger.ToolCallID != "call_sub_1" || d.Trigger.EventID != "call-call_sub_1" {
		t.Errorf("trigger anchor = %+v, want exact call_id anchor", d.Trigger)
	}
	if d.Trigger.Precision.State != collaboration.EvidenceExact {
		t.Errorf("trigger precision = %+v, want exact", d.Trigger.Precision)
	}
	wantTriggerTS := time.Date(2026, 7, 6, 4, 18, 40, 0, time.UTC)
	if d.Trigger.Timestamp == nil || !d.Trigger.Timestamp.Equal(wantTriggerTS) {
		t.Errorf("trigger timestamp = %v, want %v", d.Trigger.Timestamp, wantTriggerTS)
	}
	if d.Result == nil || d.Result.ToolCallID != "call_sub_1" {
		t.Errorf("result anchor = %+v, want exact call_id anchor", d.Result)
	}
	if d.Result != nil && d.Result.EventID != "result-call_sub_1" {
		t.Errorf("result anchor EventID = %q, want result-call_sub_1", d.Result.EventID)
	}
	if d.Result.Precision.State != collaboration.EvidenceExact {
		t.Errorf("result precision = %+v, want exact", d.Result.Precision)
	}
	if d.ExecutionMode != collaboration.ExecutionUnknown {
		t.Errorf("execution mode = %q, want unknown", d.ExecutionMode)
	}
	// The delegated prompt is never stored as a task summary.
	if d.TaskSummary != "" {
		t.Errorf("task summary must stay empty, got %q", d.TaskSummary)
	}
	if d.Evidence.Task.State != collaboration.EvidenceMissing ||
		d.Evidence.Task.ReasonCode != collaboration.ReasonSourceNotRecorded {
		t.Errorf("evidence.task = %+v, want missing/source_not_recorded", d.Evidence.Task)
	}
	for name, fact := range map[string]collaboration.FactEvidence{
		"trigger": d.Evidence.Trigger, "timing": d.Evidence.Timing, "result": d.Evidence.Result,
	} {
		if fact.State != collaboration.EvidenceExact {
			t.Errorf("evidence.%s = %+v, want exact", name, fact)
		}
	}

	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// Shared conformance: two-parse full-graph equality, root-session ownership,
// contract validation, and the embedded-child no-backing-Session rule.
func TestChrysCollaborationConformance(t *testing.T) {
	r := New(collabChrysFixtureRoot(t))
	adaptertest.RunCollaboration(t, r, adaptertest.CollaborationExpect{
		RootSession:          collabChrysRootSession(t, r),
		MinChildren:          1,
		ForbidBackingSession: true,
	})
}

// Embedded child render events carry the child invocation ID; parent events
// (including the launch ToolInvocation and summary ToolResult that serve as
// anchors) stay root-associated. Splice ordering is locked by the evidence
// tests; this locks the association layer.
func TestChrysCollaborationRenderAssociation(t *testing.T) {
	events, err := New(collabChrysFixtureRoot(t)).GetRenderEvents(collabChrysRoot)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	wantChildID := collaboration.ChildInvocationID("chrys", collabChrysRoot, "call_sub_1")
	sawChild := false
	for _, e := range events {
		if e.Depth >= 1 {
			sawChild = true
			if e.InvocationID != wantChildID {
				t.Errorf("child event %s (%s/%s) InvocationID = %q, want %q",
					e.EventID, e.Type, e.Subtype, e.InvocationID, wantChildID)
			}
			continue
		}
		if e.InvocationID != "" {
			t.Errorf("root event %s (%s/%s) must stay root-associated, got InvocationID %q",
				e.EventID, e.Type, e.Subtype, e.InvocationID)
		}
	}
	if !sawChild {
		t.Fatal("no embedded child events found in the render stream")
	}
}

// A sidecar without parent_provider_call_id falls back to the
// contract-accepted native invocation_id and, with no two-sided join, lands
// in the Unlinked group with its transcript preserved.
func TestChrysReadCollaborationInvocationIDFallback(t *testing.T) {
	sessionsDir := t.TempDir()
	dir := filepath.Join(sessionsDir, "rootfb")
	writeChrysCollabSession(t, dir, `{
  "meta": {"schema_version": 1, "session_id": "rootfb-full", "created_at": "2026-01-01T00:00:00+00:00",
    "updated_at": "2026-01-01T00:01:00+00:00", "message_count": 1, "primary_cwd": "/tmp/proj", "title": "fb"},
  "state": {"messages": [
    {"type": "message", "role": "user",
     "contents": [{"type": "text", "text": "fallback case", "additional_properties": {}}],
     "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:00+00:00"}}
  ], "compressed_msgs": [], "turn_counter": 1}
}`)
	writeChrysCollabSidecar(t, dir, "plan_agent_ab12cd34.json", `{
  "meta": {"schema_version": 1, "record_type": "sub_agent_session", "invocation_id": "ab12cd34",
    "tool_name": "plan_agent", "status": "running",
    "created_at": "2026-01-01T00:00:10+00:00", "updated_at": "2026-01-01T00:00:20+00:00"},
  "state": {"messages": [], "compressed_msgs": [], "turn_counter": 0}
}`)

	r := New(sessionsDir)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("embedded sidecar must never list as a root session, got %+v", list)
	}
	g, err := r.ReadCollaboration(context.Background(), list[0])
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 || len(g.Delegations) != 0 {
		t.Fatalf("want unlinked child with no delegation, got %+v", g)
	}
	child := g.Invocations[1]
	wantID := collaboration.ChildInvocationID("chrys", "rootfb", "ab12cd34")
	if child.ID != wantID {
		t.Errorf("fallback child ID = %q, want %q", child.ID, wantID)
	}
	if child.Status != collaboration.StatusRunning {
		t.Errorf("status = %q, want running from meta.status", child.Status)
	}
	// A running child's updated_at is not an end boundary.
	if child.EndedAt != nil {
		t.Errorf("running child must not get an end timestamp, got %v", child.EndedAt)
	}
	if child.TimePrecision.State != collaboration.EvidenceEstimated ||
		child.TimePrecision.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("time precision = %+v, want estimated/completion_not_recorded", child.TimePrecision)
	}
	v := collaboration.Validate(&g)
	if !v.OK() {
		t.Fatalf("graph must validate clean, got %+v", v.Issues)
	}
	if len(v.Unlinked) != 1 || v.Unlinked[0] != wantID {
		t.Errorf("unlinked = %v, want [%s]", v.Unlinked, wantID)
	}
}

// Two sidecars claiming one native ID are malformed source; the first in
// deterministic path order wins and the read still succeeds.
func TestChrysReadCollaborationDuplicateSidecar(t *testing.T) {
	sessionsDir := t.TempDir()
	dir := filepath.Join(sessionsDir, "rootdup")
	writeChrysCollabSession(t, dir, `{
  "meta": {"schema_version": 1, "session_id": "rootdup-full", "created_at": "2026-01-01T00:00:00+00:00",
    "updated_at": "2026-01-01T00:01:00+00:00", "message_count": 2, "primary_cwd": "/tmp/proj", "title": "dup"},
  "state": {"messages": [
    {"type": "message", "role": "user",
     "contents": [{"type": "text", "text": "dup case", "additional_properties": {}}],
     "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:00+00:00"}},
    {"type": "message", "role": "assistant",
     "contents": [{"type": "function_call", "call_id": "call_dup", "name": "explore_agent",
       "arguments": "{\"prompt\": \"dup\"}", "additional_properties": {}}],
     "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:05+00:00"}}
  ], "compressed_msgs": [], "turn_counter": 1}
}`)
	sidecar := func(name, status, createdAt string) {
		t.Helper()
		writeChrysCollabSidecar(t, dir, name, `{
  "meta": {"schema_version": 1, "record_type": "sub_agent_session", "parent_provider_call_id": "call_dup",
    "tool_name": "explore_agent", "status": "`+status+`",
    "created_at": "`+createdAt+`", "updated_at": "2026-01-01T00:00:30+00:00"},
  "state": {"messages": [], "compressed_msgs": [], "turn_counter": 0}
}`)
	}
	sidecar("explore_agent_aaaaaaaa.json", "completed", "2026-01-01T00:00:06+00:00")
	sidecar("explore_agent_bbbbbbbb.json", "running", "2026-01-01T00:00:07+00:00")

	r := New(sessionsDir)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	g, err := r.ReadCollaboration(context.Background(), list[0])
	if err != nil {
		t.Fatalf("duplicate sidecars must not fail the whole read: %v", err)
	}
	if len(g.Invocations) != 2 || len(g.Delegations) != 1 {
		t.Fatalf("want root + one deduplicated child, got %+v", g)
	}
	// The lexicographically first sidecar wins deterministically: the child
	// reflects explore_agent_aaaaaaaa.json (completed, 00:00:06), not
	// explore_agent_bbbbbbbb.json (running, 00:00:07).
	child := g.Invocations[1]
	if child.Status != collaboration.StatusCompleted {
		t.Errorf("deduped child status = %q, want completed from the first sidecar", child.Status)
	}
	wantStart := time.Date(2026, 1, 1, 0, 0, 6, 0, time.UTC)
	if child.StartedAt == nil || !child.StartedAt.Equal(wantStart) {
		t.Errorf("deduped child StartedAt = %v, want %v from the first sidecar", child.StartedAt, wantStart)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean after dedupe, got %+v", v.Issues)
	}
}

// A malformed sidecar is skipped; the remaining graph still validates.
func TestChrysReadCollaborationMalformedSidecar(t *testing.T) {
	sessionsDir := t.TempDir()
	dir := filepath.Join(sessionsDir, "rootbad")
	writeChrysCollabSession(t, dir, `{
  "meta": {"schema_version": 1, "session_id": "rootbad-full", "created_at": "2026-01-01T00:00:00+00:00",
    "updated_at": "2026-01-01T00:01:00+00:00", "message_count": 1, "primary_cwd": "/tmp/proj", "title": "bad"},
  "state": {"messages": [
    {"type": "message", "role": "user",
     "contents": [{"type": "text", "text": "malformed sidecar case", "additional_properties": {}}],
     "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:00+00:00"}}
  ], "compressed_msgs": [], "turn_counter": 1}
}`)
	writeChrysCollabSidecar(t, dir, "broken.json", `{not json`)

	r := New(sessionsDir)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	g, err := r.ReadCollaboration(context.Background(), list[0])
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 1 || len(g.Delegations) != 0 {
		t.Errorf("malformed sidecar must be skipped, got %+v", g)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}

// Discovery honors context cancellation.
func TestChrysReadCollaborationCancelled(t *testing.T) {
	r := New(collabChrysFixtureRoot(t))
	root := collabChrysRootSession(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ReadCollaboration(ctx, root); err == nil {
		t.Fatal("cancelled context must abort the read")
	}
}

// Status vocabulary normalization keeps unknown first-class.
func TestChrysStatusNormalization(t *testing.T) {
	cases := map[string]collaboration.InvocationStatus{
		"completed":   collaboration.StatusCompleted,
		"failed":      collaboration.StatusFailed,
		"cancelled":   collaboration.StatusCancelled,
		"interrupted": collaboration.StatusCancelled,
		"running":     collaboration.StatusRunning,
		"in_progress": collaboration.StatusRunning,
		"pending":     collaboration.StatusPending,
		"":            collaboration.StatusUnknown,
		"surprise":    collaboration.StatusUnknown,
	}
	for in, want := range cases {
		if got := normalizeChrysStatus(in); got != want {
			t.Errorf("normalizeChrysStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeChrysCollabSession(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeChrysCollabSidecar(t *testing.T, sessionDir, name, body string) {
	t.Helper()
	dir := filepath.Join(sessionDir, "sub_agents", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A parent function_call timestamp that post-dates the child it launched is
// causally impossible (observed in the wild when Chrys's checkpoint rewrite
// collapses a message's _chrys_created_at to the rewrite time). The join
// identity stays exact, but the timestamp must be withheld and the anchor
// precision downgraded — never emitted as an exact fact that stretches
// downstream time domains.
func TestChrysReadCollaborationContradictedTriggerTimestamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(collabChrysFixtureRoot(t))); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	sessionPath := filepath.Join(dir, collabChrysRoot, "session.json")
	raw, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	// The child runs 04:18:43 -> 04:19:33; move the parent's recorded launch
	// message to 04:53:34, after the child's recorded end.
	corrupted := strings.Replace(string(raw),
		`"_chrys_created_at": "2026-07-06T04:18:40.000000+00:00"`,
		`"_chrys_created_at": "2026-07-06T04:53:34.000000+00:00"`, 1)
	if corrupted == string(raw) {
		t.Fatal("fixture no longer carries the expected launch timestamp")
	}
	if err := os.WriteFile(sessionPath, []byte(corrupted), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	r := New(dir)
	root := collabChrysRootSession(t, r)
	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Delegations) != 1 {
		t.Fatalf("want 1 delegation, got %d", len(g.Delegations))
	}
	d := g.Delegations[0]
	if d.Trigger == nil {
		t.Fatal("trigger anchor must survive the downgrade")
	}
	if d.Trigger.Timestamp != nil {
		t.Errorf("contradicted trigger timestamp must be withheld, got %v", d.Trigger.Timestamp)
	}
	if d.Trigger.Precision.State != collaboration.EvidenceMissing ||
		d.Trigger.Precision.ReasonCode != collaboration.ReasonTimestampContradiction {
		t.Errorf("trigger precision = %+v, want missing/timestamp_contradiction", d.Trigger.Precision)
	}
	// The exact two-sided join identity is untouched: jump-to-launch still
	// resolves by event/tool-call ID.
	if d.Trigger.EventID != "call-call_sub_1" || d.Trigger.ToolCallID != "call_sub_1" {
		t.Errorf("join identity = %+v, want exact call_id anchor", d.Trigger)
	}
	if d.Evidence.Trigger.State != collaboration.EvidenceExact {
		t.Errorf("evidence.trigger = %+v, want exact (the join fact is exact)", d.Evidence.Trigger)
	}
	// Child timing is source-recorded and unaffected.
	child := g.Invocations[1]
	if child.StartedAt == nil || child.EndedAt == nil {
		t.Errorf("child boundaries must stay recorded: %+v", child)
	}
	// The withheld timestamp leaves nothing for the causality check to flag.
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("graph must validate clean, got %+v", v.Issues)
	}
}
