package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// Sanitized synthetic fixtures for Grok collaboration. Field shapes and
// relationships match real Grok subagent records; no private prompts,
// outputs, paths, or business content.

const (
	gRootID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeee0001"
	gChildID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeee0002"
	gGrandID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeee0003"
	gLifecycleID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeee0004"
	gProj        = "%2Ftmp%2Fdemo"
)

func writeSubagentMeta(t *testing.T, parentDir, subagentID string, meta map[string]any) {
	t.Helper()
	dir := filepath.Join(parentDir, "subagents", subagentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir subagent: %v", err)
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

func lifecycleLine(sessionID, updateJSON string, eventID string, tsSec, agentMS int64) string {
	meta := ""
	if eventID != "" || agentMS > 0 {
		parts := make([]string, 0, 2)
		if eventID != "" {
			parts = append(parts, `"eventId":"`+eventID+`"`)
		}
		if agentMS > 0 {
			parts = append(parts, `"agentTimestampMs":`+formatInt(agentMS))
		}
		meta = `,"_meta":{` + strings.Join(parts, ",") + `}`
	}
	return `{"timestamp":` + formatInt(tsSec) + `,"method":"_x.ai/session/update","params":{"sessionId":"` + sessionID + `","update":` + updateJSON + meta + `}}`
}

// fixtureStandaloneChild: root + one completed standalone child with meta
// and matching spawn/finish events.
func fixtureStandaloneChild(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rootUpdates := strings.Join([]string{
		`{"timestamp":1700000000,"method":"session/update","params":{"sessionId":"` + gRootID + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"plan the work"}}}}`,
		lifecycleLine(gRootID,
			`{"sessionUpdate":"subagent_spawned","subagent_id":"`+gChildID+`","parent_session_id":"`+gRootID+`","child_session_id":"`+gChildID+`","subagent_type":"general-purpose","description":"plan writer"}`,
			gRootID+"-spawn-1", 1700000010, 1700000010000),
		lifecycleLine(gRootID,
			`{"sessionUpdate":"subagent_finished","subagent_id":"`+gChildID+`","child_session_id":"`+gChildID+`","status":"completed"}`,
			gRootID+"-finish-1", 1700000020, 1700000020000),
		`{"timestamp":1700000030,"method":"_x.ai/session/update","params":{"sessionId":"` + gRootID + `","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn","usage":{"inputTokens":10,"outputTokens":5,"cachedReadTokens":0,"reasoningTokens":0,"modelCalls":1,"apiDurationMs":100,"modelUsage":{"m":{"inputTokens":10,"outputTokens":5,"cachedReadTokens":0,"reasoningTokens":0,"modelCalls":1,"apiDurationMs":100}}}}}}`,
	}, "\n") + "\n"
	writeSession(t, root, gProj, gRootID, summaryFile{
		GeneratedTitle: "root session",
		CreatedAt:      "2026-01-01T00:00:00Z",
		UpdatedAt:      "2026-01-01T00:10:00Z",
	}, rootUpdates, sampleEventsClosed())

	parentDir := filepath.Join(root, gProj, gRootID)
	writeSubagentMeta(t, parentDir, gChildID, map[string]any{
		"subagent_id":       gChildID,
		"parent_session_id": gRootID,
		"child_session_id":  gChildID,
		"subagent_type":     "general-purpose",
		"description":       "plan writer",
		"status":            "completed",
		"started_at":        "2026-01-01T00:00:10.000000000Z",
		"completed_at":      "2026-01-01T00:00:20.000000000Z",
		"duration_ms":       10000,
	})

	childUpdates := `{"timestamp":1700000011,"method":"session/update","params":{"sessionId":"` + gChildID + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"child task brief"}}}}
{"timestamp":1700000015,"method":"session/update","params":{"sessionId":"` + gChildID + `","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"child plan result"}}}}
{"timestamp":1700000019,"method":"_x.ai/session/update","params":{"sessionId":"` + gChildID + `","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn","usage":{"inputTokens":8,"outputTokens":4,"cachedReadTokens":0,"reasoningTokens":0,"modelCalls":1,"apiDurationMs":50,"modelUsage":{"m":{"inputTokens":8,"outputTokens":4,"cachedReadTokens":0,"reasoningTokens":0,"modelCalls":1,"apiDurationMs":50}}}}}}
`
	writeSession(t, root, gProj, gChildID, summaryFile{
		GeneratedTitle: "child session",
		CreatedAt:      "2026-01-01T00:00:10Z",
		UpdatedAt:      "2026-01-01T00:00:20Z",
	}, childUpdates, sampleEventsClosed())
	return root
}

func fixtureNoSubagents(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSession(t, root, gProj, gRootID, summaryFile{GeneratedTitle: "solo"}, sampleUpdatesClosed(), sampleEventsClosed())
	return root
}

func fixtureNested(t *testing.T) string {
	t.Helper()
	root := fixtureStandaloneChild(t)
	// Child has its own subagent grandchild with standalone session.
	childDir := filepath.Join(root, gProj, gChildID)
	writeSubagentMeta(t, childDir, gGrandID, map[string]any{
		"subagent_id":       gGrandID,
		"parent_session_id": gChildID,
		"child_session_id":  gGrandID,
		"subagent_type":     "explore",
		"description":       "nested explorer",
		"status":            "completed",
		"started_at":        "2026-01-01T00:00:12.000000000Z",
		"completed_at":      "2026-01-01T00:00:18.000000000Z",
	})
	// Append lifecycle on child stream.
	childUpdatesPath := filepath.Join(childDir, "updates.jsonl")
	extra := lifecycleLine(gChildID,
		`{"sessionUpdate":"subagent_spawned","subagent_id":"`+gGrandID+`","parent_session_id":"`+gChildID+`","child_session_id":"`+gGrandID+`","subagent_type":"explore","description":"nested explorer"}`,
		gChildID+"-spawn-g", 1700000012, 1700000012000) + "\n" +
		lifecycleLine(gChildID,
			`{"sessionUpdate":"subagent_finished","subagent_id":"`+gGrandID+`","child_session_id":"`+gGrandID+`","status":"completed"}`,
			gChildID+"-finish-g", 1700000018, 1700000018000) + "\n"
	f, err := os.OpenFile(childUpdatesPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	writeSession(t, root, gProj, gGrandID, summaryFile{
		GeneratedTitle: "grandchild session",
		CreatedAt:      "2026-01-01T00:00:12Z",
		UpdatedAt:      "2026-01-01T00:00:18Z",
	}, sampleUpdatesClosed(), sampleEventsClosed())
	return root
}

func fixtureSpawnOnly(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Root updated far in the past so it is not live.
	updates := lifecycleLine(gRootID,
		`{"sessionUpdate":"subagent_spawned","subagent_id":"`+gLifecycleID+`","parent_session_id":"`+gRootID+`","child_session_id":"`+gLifecycleID+`","subagent_type":"general-purpose","description":"open child"}`,
		gRootID+"-spawn-open", 1700000010, 1700000010000) + "\n"
	writeSession(t, root, gProj, gRootID, summaryFile{
		GeneratedTitle: "root open",
		CreatedAt:      "2026-01-01T00:00:00Z",
		UpdatedAt:      "2026-01-01T00:01:00Z",
		LastActiveAt:   "2026-01-01T00:01:00Z",
	}, updates, sampleEventsClosed())
	// Sidecar without completed_at.
	writeSubagentMeta(t, filepath.Join(root, gProj, gRootID), gLifecycleID, map[string]any{
		"subagent_id":       gLifecycleID,
		"parent_session_id": gRootID,
		"child_session_id":  gLifecycleID,
		"subagent_type":     "general-purpose",
		"description":       "open child",
		"started_at":        "2026-01-01T00:00:10.000000000Z",
	})
	// Content mtimes default to "now" and would make the session live; pin
	// them to the recorded activity so open-end status resolves to orphaned.
	old, err := time.Parse(time.RFC3339, "2026-01-01T00:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(root, gProj, gRootID)
	for _, name := range []string{"updates.jsonl", "events.jsonl", "summary.json"} {
		p := filepath.Join(sessionDir, name)
		if err := os.Chtimes(p, old, old); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return root
}

func fixtureLifecycleOnlyNoBacking(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeee0005"
	updates := strings.Join([]string{
		lifecycleLine(gRootID,
			`{"sessionUpdate":"subagent_spawned","subagent_id":"`+id+`","parent_session_id":"`+gRootID+`","child_session_id":"`+id+`","subagent_type":"general-purpose","description":"ephemeral"}`,
			gRootID+"-spawn-e", 1700000010, 1700000010000),
		lifecycleLine(gRootID,
			`{"sessionUpdate":"subagent_finished","subagent_id":"`+id+`","child_session_id":"`+id+`","status":"completed"}`,
			gRootID+"-finish-e", 1700000020, 1700000020000),
	}, "\n") + "\n"
	writeSession(t, root, gProj, gRootID, summaryFile{GeneratedTitle: "root"}, updates, sampleEventsClosed())
	writeSubagentMeta(t, filepath.Join(root, gProj, gRootID), id, map[string]any{
		"subagent_id":       id,
		"parent_session_id": gRootID,
		"child_session_id":  id,
		"subagent_type":     "general-purpose",
		"description":       "ephemeral",
		"status":            "completed",
		"started_at":        "2026-01-01T00:00:10.000000000Z",
		"completed_at":      "2026-01-01T00:00:20.000000000Z",
	})
	// No standalone child session directory.
	return root
}

func listRoot(t *testing.T, r *GrokReader, id string) model.Session {
	t.Helper()
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s not listed", id)
	return model.Session{}
}

func TestGrokReadCollaborationStandaloneChild(t *testing.T) {
	r := New(fixtureStandaloneChild(t))
	root := listRoot(t, r, gRootID)

	g, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if g.RootAgentType != "grok" || g.RootSessionID != gRootID {
		t.Fatalf("coordinates = %s/%s", g.RootAgentType, g.RootSessionID)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("want 2 invocations, got %d", len(g.Invocations))
	}
	child := g.Invocations[1]
	wantID := collaboration.ChildInvocationID("grok", gRootID, gChildID)
	if child.ID != wantID {
		t.Errorf("child id = %q, want %q", child.ID, wantID)
	}
	if child.SourceIdentity.Kind != collaboration.IdentitySubagentID || child.SourceIdentity.NativeID != gChildID {
		t.Errorf("source identity = %+v", child.SourceIdentity)
	}
	if child.Status != collaboration.StatusCompleted {
		t.Errorf("status = %q", child.Status)
	}
	if child.StartedAt == nil || child.EndedAt == nil || !child.EndedAt.After(*child.StartedAt) {
		t.Errorf("timing start=%v end=%v", child.StartedAt, child.EndedAt)
	}
	if child.TimePrecision.State != collaboration.EvidenceExact {
		t.Errorf("time precision = %+v", child.TimePrecision)
	}
	if child.ContentPrecision.State != collaboration.EvidenceExact {
		t.Errorf("content precision = %+v", child.ContentPrecision)
	}
	if child.BackingSession == nil || child.BackingSession.SessionID != gChildID || child.BackingSession.AgentType != "grok" {
		t.Errorf("backing = %+v", child.BackingSession)
	}
	if child.DisplayName != "plan writer" || child.RoleLabel != "general-purpose" {
		t.Errorf("display/role = %q/%q", child.DisplayName, child.RoleLabel)
	}
	if len(g.Delegations) != 1 {
		t.Fatalf("delegations = %d", len(g.Delegations))
	}
	d := g.Delegations[0]
	if d.TaskSummary != "plan writer" {
		t.Errorf("task summary = %q (must be description, not prompt)", d.TaskSummary)
	}
	if d.Trigger == nil || d.Trigger.EventID == "" || d.Result == nil || d.Result.EventID == "" {
		t.Errorf("anchors trigger=%+v result=%+v", d.Trigger, d.Result)
	}
	if d.ExecutionMode != collaboration.ExecutionUnknown {
		t.Errorf("execution mode = %q", d.ExecutionMode)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("validate: %+v", v.Issues)
	}
}

func TestGrokCollaborationConformance(t *testing.T) {
	r := New(fixtureStandaloneChild(t))
	adaptertest.RunCollaboration(t, r, adaptertest.CollaborationExpect{
		RootSession:           listRoot(t, r, gRootID),
		MinChildren:           1,
		RequireBackingSession: true,
	})
}

func TestGrokReadCollaborationNoSubagents(t *testing.T) {
	r := New(fixtureNoSubagents(t))
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 1 || len(g.Delegations) != 0 {
		t.Fatalf("want zero-child graph, got inv=%d del=%d", len(g.Invocations), len(g.Delegations))
	}
	if g.Invocations[0].ID != collaboration.RootInvocationID("grok", gRootID) {
		t.Errorf("root id = %q", g.Invocations[0].ID)
	}
}

func TestGrokReadCollaborationNested(t *testing.T) {
	r := New(fixtureNested(t))
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 3 {
		t.Fatalf("want root+child+grandchild, got %d", len(g.Invocations))
	}
	childID := collaboration.ChildInvocationID("grok", gRootID, gChildID)
	grandID := collaboration.ChildInvocationID("grok", gRootID, gGrandID)
	rootID := collaboration.RootInvocationID("grok", gRootID)
	// Find delegations.
	var rootToChild, childToGrand bool
	for _, d := range g.Delegations {
		if d.ParentInvocationID == rootID && d.ChildInvocationID == childID {
			rootToChild = true
		}
		if d.ParentInvocationID == childID && d.ChildInvocationID == grandID {
			childToGrand = true
		}
	}
	if !rootToChild || !childToGrand {
		t.Fatalf("nested chain missing: root→child=%v child→grand=%v dels=%+v", rootToChild, childToGrand, g.Delegations)
	}
}

func TestGrokReadCollaborationSpawnOnlyOpenEnd(t *testing.T) {
	r := New(fixtureSpawnOnly(t))
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("invocations = %d", len(g.Invocations))
	}
	child := g.Invocations[1]
	if child.EndedAt != nil {
		t.Errorf("end must stay open, got %v", child.EndedAt)
	}
	if child.TimePrecision.State != collaboration.EvidenceEstimated ||
		child.TimePrecision.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("time precision = %+v", child.TimePrecision)
	}
	if child.Status != collaboration.StatusOrphaned {
		// Root is not live (old UpdatedAt).
		t.Errorf("status = %q, want orphaned", child.Status)
	}
	d := g.Delegations[0]
	if d.Evidence.Result.ReasonCode != collaboration.ReasonCompletionNotRecorded {
		t.Errorf("result evidence = %+v", d.Evidence.Result)
	}
}

func TestGrokReadCollaborationLifecycleNoBacking(t *testing.T) {
	r := New(fixtureLifecycleOnlyNoBacking(t))
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("must retain lifecycle invocation, got %d", len(g.Invocations))
	}
	child := g.Invocations[1]
	if child.BackingSession != nil {
		t.Errorf("backing must be omitted when session missing, got %+v", child.BackingSession)
	}
	if child.ContentPrecision.State != collaboration.EvidenceMissing {
		t.Errorf("content precision = %+v", child.ContentPrecision)
	}
	if child.Status != collaboration.StatusCompleted {
		t.Errorf("status = %q", child.Status)
	}
}

func TestGrokReadCollaborationMergeNoDuplicate(t *testing.T) {
	// Standalone fixture has both meta and stream for same child.
	r := New(fixtureStandaloneChild(t))
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, inv := range g.Invocations {
		if inv.SourceIdentity.NativeID == gChildID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("duplicate invocations for same native id: %d", n)
	}
}

func TestGrokReadCollaborationMalformedSidecar(t *testing.T) {
	root := fixtureStandaloneChild(t)
	// Broken meta next to valid one.
	badDir := filepath.Join(root, gProj, gRootID, "subagents", "not-a-uuid")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "meta.json"), []byte(`{not-json`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(root)
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatalf("malformed sidecar must not fail whole graph: %v", err)
	}
	if len(g.Invocations) != 2 {
		t.Fatalf("want still one child, got %d", len(g.Invocations))
	}
}

func TestGrokReadCollaborationDuplicateNativeAndWrongParent(t *testing.T) {
	root := fixtureStandaloneChild(t)
	// Second sidecar dir claiming same subagent_id via different folder name —
	// discover uses subagent_id field so same id merges.
	writeSubagentMeta(t, filepath.Join(root, gProj, gRootID), "duplicate-folder", map[string]any{
		"subagent_id":       gChildID,
		"parent_session_id": gRootID,
		"child_session_id":  gChildID,
		"subagent_type":     "general-purpose",
		"description":       "plan writer",
		"status":            "completed",
		"started_at":        "2026-01-01T00:00:10.000000000Z",
		"completed_at":      "2026-01-01T00:00:20.000000000Z",
	})
	// Conflicting parent id on a new child: should not attach under root if parent differs.
	other := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeee0099"
	writeSubagentMeta(t, filepath.Join(root, gProj, gRootID), other, map[string]any{
		"subagent_id":       other,
		"parent_session_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeaaaa",
		"child_session_id":  other,
		"subagent_type":     "general-purpose",
		"description":       "wrong parent",
		"status":            "completed",
		"started_at":        "2026-01-01T00:00:11.000000000Z",
		"completed_at":      "2026-01-01T00:00:12.000000000Z",
	})

	r := New(root)
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatal(err)
	}
	// One child for gChildID; wrong-parent child excluded.
	if len(g.Invocations) != 2 {
		t.Fatalf("want only root+valid child, got %d: %+v", len(g.Invocations), g.Invocations)
	}
	rootID := collaboration.RootInvocationID("grok", gRootID)
	wantChild := collaboration.ChildInvocationID("grok", gRootID, gChildID)
	wrongID := collaboration.ChildInvocationID("grok", gRootID, other)
	var sawChild, sawWrong bool
	for _, inv := range g.Invocations {
		switch inv.ID {
		case wantChild:
			sawChild = true
		case wrongID:
			sawWrong = true
		}
	}
	if !sawChild {
		t.Fatal("valid child invocation missing")
	}
	if sawWrong {
		t.Fatal("wrong-parent child must not be attached under root")
	}
	if len(g.Delegations) != 1 ||
		g.Delegations[0].ParentInvocationID != rootID ||
		g.Delegations[0].ChildInvocationID != wantChild {
		t.Fatalf("delegation must be root→valid child, got %+v", g.Delegations)
	}
	if v := collaboration.Validate(&g); !v.OK() {
		t.Errorf("validate: %+v", v.Issues)
	}
}

func TestGrokReadCollaborationCancelled(t *testing.T) {
	r := New(fixtureStandaloneChild(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ReadCollaboration(ctx, listRoot(t, r, gRootID)); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestGrokReadCollaborationTwoParseStable(t *testing.T) {
	r := New(fixtureStandaloneChild(t))
	root := listRoot(t, r, gRootID)
	a, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.ReadCollaboration(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Invocations) != len(b.Invocations) || len(a.Delegations) != len(b.Delegations) {
		t.Fatal("count drift")
	}
	for i := range a.Invocations {
		if a.Invocations[i].ID != b.Invocations[i].ID {
			t.Fatalf("invocation order/id drift at %d", i)
		}
	}
	for i := range a.Delegations {
		if a.Delegations[i].ID != b.Delegations[i].ID {
			t.Fatalf("delegation order/id drift at %d", i)
		}
	}
}

func TestGrokLineageListAndColdGetAgree(t *testing.T) {
	dir := fixtureStandaloneChild(t)
	// Cold GetSession before ListSessions.
	r1 := New(dir)
	detail, err := r1.GetSession(gChildID)
	if err != nil {
		t.Fatalf("cold GetSession: %v", err)
	}
	if !detail.IsSubagent || detail.ParentSessionID != gRootID {
		t.Fatalf("cold detail lineage: IsSubagent=%v Parent=%q", detail.IsSubagent, detail.ParentSessionID)
	}

	r2 := New(dir)
	list, err := r2.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	var listed model.Session
	for _, s := range list {
		if s.ID == gChildID {
			listed = s
		}
	}
	if !listed.IsSubagent || listed.ParentSessionID != gRootID {
		t.Fatalf("list lineage: IsSubagent=%v Parent=%q", listed.IsSubagent, listed.ParentSessionID)
	}
	if listed.IsSubagent != detail.IsSubagent || listed.ParentSessionID != detail.ParentSessionID {
		t.Fatal("ListSessions and cold GetSession lineage disagree")
	}
}

func TestGrokChildAsRootRejected(t *testing.T) {
	r := New(fixtureStandaloneChild(t))
	child := listRoot(t, r, gChildID)
	if !child.IsSubagent {
		t.Fatal("fixture child must be marked subagent")
	}
	if _, err := r.ReadCollaboration(context.Background(), child); err == nil {
		t.Fatal("child-as-root must be rejected")
	} else if !strings.Contains(err.Error(), "root sessions only") {
		t.Errorf("error = %v", err)
	}
}

func TestGrokChildRenderBackingTranscript(t *testing.T) {
	r := New(fixtureStandaloneChild(t))
	events, err := r.GetRenderEvents(gChildID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.Text, "child plan result") {
			found = true
		}
	}
	if !found {
		t.Error("child transcript not available via GetRenderEvents")
	}
}

func TestGrokImpossibleEndTimestampDropped(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, gProj, gRootID, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
	writeSubagentMeta(t, filepath.Join(root, gProj, gRootID), gChildID, map[string]any{
		"subagent_id":       gChildID,
		"parent_session_id": gRootID,
		"child_session_id":  gChildID,
		"subagent_type":     "general-purpose",
		"description":       "bad times",
		"status":            "completed",
		"started_at":        "2026-01-01T00:00:20.000000000Z",
		"completed_at":      "2026-01-01T00:00:10.000000000Z", // before start
	})
	r := New(root)
	g, err := r.ReadCollaboration(context.Background(), listRoot(t, r, gRootID))
	if err != nil {
		t.Fatal(err)
	}
	child := g.Invocations[1]
	if child.EndedAt != nil {
		t.Errorf("impossible end must be dropped, got %v", child.EndedAt)
	}
}
