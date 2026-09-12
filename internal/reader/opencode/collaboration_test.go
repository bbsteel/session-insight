package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

const (
	opencodeCollaborationRootID       = "ses_oc_collab_root"
	opencodeCollaborationChildID      = "ses_oc_collab_child"
	opencodeCollaborationGrandchildID = "ses_oc_collab_grandchild"
)

func TestOpenCodeReadCollaborationStandaloneSessionTree(t *testing.T) {
	reader, db, _ := setupRealSchemaDB(t)
	seedOpenCodeCollaborationFixture(t, db)

	rootSession := openCodeCollaborationRoot(t, reader)
	if rootSession.ResumeID != opencodeCollaborationRootID {
		t.Fatalf("root ResumeID = %q, want %q", rootSession.ResumeID, opencodeCollaborationRootID)
	}

	listedSessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	listedByID := make(map[string]model.Session, len(listedSessions))
	for _, session := range listedSessions {
		listedByID[session.ID] = session
	}
	for _, childID := range []string{opencodeCollaborationChildID, opencodeCollaborationGrandchildID} {
		child, ok := listedByID[childID]
		if !ok {
			t.Fatalf("child session %q is not listed", childID)
		}
		if !child.IsSubagent || child.ParentSessionID == "" {
			t.Errorf("child %q lineage = IsSubagent:%v ParentSessionID:%q", childID, child.IsSubagent, child.ParentSessionID)
		}
	}
	if listedByID[opencodeCollaborationRootID].IsSubagent || listedByID[opencodeCollaborationRootID].ParentSessionID != "" {
		t.Errorf("root session must not be marked as a child: %+v", listedByID[opencodeCollaborationRootID])
	}

	graph, err := reader.ReadCollaboration(context.Background(), rootSession)
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	if graph.RootAgentType != "opencode" || graph.RootSessionID != opencodeCollaborationRootID {
		t.Fatalf("graph coordinates = %s/%s", graph.RootAgentType, graph.RootSessionID)
	}
	if graph.Revision != model.SessionRevision(rootSession) {
		t.Errorf("graph revision = %d, want %d", graph.Revision, model.SessionRevision(rootSession))
	}
	if graph.Completeness != collaboration.ExactFact() {
		t.Errorf("graph completeness = %+v, want exact", graph.Completeness)
	}
	if validation := collaboration.Validate(&graph); !validation.OK() {
		t.Fatalf("graph validation = %+v", validation.Issues)
	}

	rootInvocationID := collaboration.RootInvocationID("opencode", opencodeCollaborationRootID)
	childInvocationID := collaboration.ChildInvocationID("opencode", opencodeCollaborationRootID, opencodeCollaborationChildID)
	grandchildInvocationID := collaboration.ChildInvocationID("opencode", opencodeCollaborationRootID, opencodeCollaborationGrandchildID)
	invocationsByID := make(map[string]collaboration.AgentInvocation, len(graph.Invocations))
	for _, invocation := range graph.Invocations {
		invocationsByID[invocation.ID] = invocation
	}
	if len(invocationsByID) != 3 {
		t.Fatalf("invocations = %d, want root + child + grandchild", len(invocationsByID))
	}
	rootInvocation, ok := invocationsByID[rootInvocationID]
	if !ok || rootInvocation.BackingSession != nil {
		t.Fatalf("root invocation = %+v", rootInvocation)
	}
	childInvocation := invocationsByID[childInvocationID]
	if childInvocation.DisplayName != "Inspect files" || childInvocation.RoleLabel != "explore" {
		t.Errorf("child display/role = %q/%q", childInvocation.DisplayName, childInvocation.RoleLabel)
	}
	if childInvocation.Status != collaboration.StatusCompleted || childInvocation.ContentPrecision != collaboration.ExactFact() {
		t.Errorf("child state = status:%q content:%+v", childInvocation.Status, childInvocation.ContentPrecision)
	}
	if childInvocation.BackingSession == nil || childInvocation.BackingSession.SessionID != opencodeCollaborationChildID {
		t.Errorf("child backing session = %+v", childInvocation.BackingSession)
	}
	if childInvocation.SourceIdentity.Kind != collaboration.IdentitySessionID || childInvocation.SourceIdentity.NativeID != opencodeCollaborationChildID {
		t.Errorf("child source identity = %+v", childInvocation.SourceIdentity)
	}
	if childInvocation.StartedAt == nil || childInvocation.StartedAt.UnixMilli() != 2000 || childInvocation.EndedAt == nil || childInvocation.EndedAt.UnixMilli() != 3200 {
		t.Errorf("child timing = started:%v ended:%v", childInvocation.StartedAt, childInvocation.EndedAt)
	}
	grandchildInvocation := invocationsByID[grandchildInvocationID]
	if grandchildInvocation.BackingSession == nil || grandchildInvocation.BackingSession.SessionID != opencodeCollaborationGrandchildID {
		t.Errorf("grandchild backing session = %+v", grandchildInvocation.BackingSession)
	}

	delegationsByChildID := make(map[string]collaboration.Delegation, len(graph.Delegations))
	for _, delegation := range graph.Delegations {
		delegationsByChildID[delegation.ChildInvocationID] = delegation
	}
	if len(delegationsByChildID) != 2 {
		t.Fatalf("delegations = %d, want child + grandchild", len(delegationsByChildID))
	}
	childDelegation := delegationsByChildID[childInvocationID]
	if childDelegation.ParentInvocationID != rootInvocationID || childDelegation.TaskSummary != "Inspect files" || childDelegation.ExecutionMode != collaboration.ExecutionBlocking {
		t.Errorf("child delegation = %+v", childDelegation)
	}
	assertOpenCodeTaskAnchors(t, childDelegation, "call-child", opencodeCollaborationRootID, 2000, 3000)

	grandchildDelegation := delegationsByChildID[grandchildInvocationID]
	if grandchildDelegation.ParentInvocationID != childInvocationID || grandchildDelegation.TaskSummary != "Review output" || grandchildDelegation.ExecutionMode != collaboration.ExecutionBackground {
		t.Errorf("grandchild delegation = %+v", grandchildDelegation)
	}
	assertOpenCodeTaskAnchors(t, grandchildDelegation, "call-grandchild", opencodeCollaborationChildID, 2300, 2600)

	rootEvents, err := reader.GetRenderEvents(opencodeCollaborationRootID)
	if err != nil {
		t.Fatalf("GetRenderEvents(root): %v", err)
	}
	for _, event := range rootEvents {
		if event.InvocationID != "" {
			t.Errorf("root event %s has child InvocationID %q", event.EventID, event.InvocationID)
		}
	}
	childEvents, err := reader.GetRenderEvents(opencodeCollaborationChildID)
	if err != nil {
		t.Fatalf("GetRenderEvents(child): %v", err)
	}
	if len(childEvents) == 0 {
		t.Fatal("child render stream is empty")
	}
	for _, event := range childEvents {
		if event.InvocationID != childInvocationID {
			t.Errorf("child event %s InvocationID = %q, want %q", event.EventID, event.InvocationID, childInvocationID)
		}
	}

	childDetail, err := reader.GetSession(opencodeCollaborationChildID)
	if err != nil {
		t.Fatalf("GetSession(child): %v", err)
	}
	if !childDetail.IsSubagent || childDetail.ParentSessionID != opencodeCollaborationRootID || childDetail.ResumeID != opencodeCollaborationChildID {
		t.Errorf("child detail lineage/resume = IsSubagent:%v ParentSessionID:%q ResumeID:%q", childDetail.IsSubagent, childDetail.ParentSessionID, childDetail.ResumeID)
	}
}

func TestOpenCodeCollaborationConformance(t *testing.T) {
	reader, db, _ := setupRealSchemaDB(t)
	seedOpenCodeCollaborationFixture(t, db)
	adaptertest.RunCollaboration(t, reader, adaptertest.CollaborationExpect{
		RootSession:           openCodeCollaborationRoot(t, reader),
		MinChildren:           2,
		RequireBackingSession: true,
	})
}

func TestOpenCodeReadCollaborationRejectsChildAsRoot(t *testing.T) {
	reader, db, _ := setupRealSchemaDB(t)
	seedOpenCodeCollaborationFixture(t, db)
	var childSession model.Session
	for _, session := range mustListOpenCodeSessions(t, reader) {
		if session.ID == opencodeCollaborationChildID {
			childSession = session
		}
	}
	if childSession.ID == "" {
		t.Fatal("child session not listed")
	}
	if _, err := reader.ReadCollaboration(context.Background(), childSession); err == nil {
		t.Fatal("child-as-root collaboration must be rejected")
	} else if !strings.Contains(err.Error(), "root sessions only") {
		t.Errorf("child-as-root error = %v", err)
	}
}

func TestOpenCodeReadCollaborationLegacyDatabaseIsRootOnly(t *testing.T) {
	reader, db, _ := setupTestDB(t)
	seedSession(t, db, "ses_legacy_root", "/tmp/project", "legacy", "test-model")
	rootSession := openCodeCollaborationRootByID(t, reader, "ses_legacy_root")

	graph, err := reader.ReadCollaboration(context.Background(), rootSession)
	if err != nil {
		t.Fatalf("ReadCollaboration(legacy): %v", err)
	}
	if len(graph.Invocations) != 1 || len(graph.Delegations) != 0 {
		t.Fatalf("legacy graph = invocations:%d delegations:%d", len(graph.Invocations), len(graph.Delegations))
	}
	if graph.Completeness.State != collaboration.EvidenceEstimated || graph.Completeness.ReasonCode != collaboration.ReasonSourceNotRecorded {
		t.Errorf("legacy completeness = %+v", graph.Completeness)
	}
}

func TestOpenCodeCollaborationEmptyChildKeepsBackingWithMissingContent(t *testing.T) {
	reader, db, _ := setupRealSchemaDB(t)
	seedOpenCodeCollaborationFixture(t, db)
	seedOpenCodeCollaborationSession(t, db, "ses_oc_empty_child", opencodeCollaborationRootID, "Empty child", 4000, 4500, "explore")

	graph, err := reader.ReadCollaboration(context.Background(), openCodeCollaborationRoot(t, reader))
	if err != nil {
		t.Fatalf("ReadCollaboration: %v", err)
	}
	emptyInvocationID := collaboration.ChildInvocationID("opencode", opencodeCollaborationRootID, "ses_oc_empty_child")
	var emptyInvocation collaboration.AgentInvocation
	var emptyDelegation collaboration.Delegation
	for _, invocation := range graph.Invocations {
		if invocation.ID == emptyInvocationID {
			emptyInvocation = invocation
		}
	}
	for _, delegation := range graph.Delegations {
		if delegation.ChildInvocationID == emptyInvocationID {
			emptyDelegation = delegation
		}
	}
	if emptyInvocation.ID == "" || emptyInvocation.BackingSession == nil || emptyInvocation.BackingSession.SessionID != "ses_oc_empty_child" {
		t.Fatalf("empty child invocation = %+v", emptyInvocation)
	}
	if emptyInvocation.ContentPrecision.State != collaboration.EvidenceMissing || emptyInvocation.ContentPrecision.ReasonCode != collaboration.ReasonSourceNotRecorded {
		t.Errorf("empty child content precision = %+v", emptyInvocation.ContentPrecision)
	}
	if emptyInvocation.Status != collaboration.StatusOrphaned {
		t.Errorf("empty child status = %q, want orphaned", emptyInvocation.Status)
	}
	if emptyDelegation.ID == "" || emptyDelegation.Evidence.Trigger.State != collaboration.EvidenceMissing || emptyDelegation.Evidence.Result.State != collaboration.EvidenceMissing {
		t.Errorf("empty child delegation evidence = %+v", emptyDelegation)
	}
}

func TestOpenCodeAssistantTurnCompletionDoesNotCloseChildTask(t *testing.T) {
	var message assistantMsgData
	if err := json.Unmarshal([]byte(`{"role":"assistant","time":{"created":2100,"completed":2200}}`), &message); err != nil {
		t.Fatalf("decode assistant message: %v", err)
	}

	state := openCodeAssistantStateFromMessage(message, 2100)
	if state.terminal {
		t.Fatal("time.completed on an assistant turn must not imply child-task completion")
	}
	if state.status != collaboration.StatusUnknown {
		t.Fatalf("assistant turn status = %q, want unknown without finish/error", state.status)
	}

	message.Finish = "tool-calls"
	state = openCodeAssistantStateFromMessage(message, 2100)
	if state.terminal {
		t.Fatal("tool-calls finish reason must leave the child task open")
	}
}

func seedOpenCodeCollaborationFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	seedOpenCodeCollaborationSession(t, db, opencodeCollaborationRootID, "", "OpenCode root", 1000, 5000, "build")
	seedOpenCodeCollaborationSession(t, db, opencodeCollaborationChildID, opencodeCollaborationRootID, "Inspect files (@explore subagent)", 2000, 3500, "explore")
	seedOpenCodeCollaborationSession(t, db, opencodeCollaborationGrandchildID, opencodeCollaborationChildID, "Review output (@audit subagent)", 2300, 2900, "audit")

	seedOpenCodeCollaborationMessage(t, db, "oc-root-user", opencodeCollaborationRootID, 1100, `{"role":"user"}`)
	seedOpenCodeCollaborationPart(t, db, "oc-root-user-text", "oc-root-user", opencodeCollaborationRootID, 1100, `{"type":"text","text":"Inspect the repository"}`)
	seedOpenCodeCollaborationMessage(t, db, "oc-root-assistant", opencodeCollaborationRootID, 1500, `{"role":"assistant","parentID":"oc-root-user","time":{"created":1500,"completed":3100}}`)
	seedOpenCodeCollaborationPart(t, db, "oc-root-task", "oc-root-assistant", opencodeCollaborationRootID, 2000, `{"type":"tool","tool":"task","callID":"call-child","state":{"status":"completed","title":"Inspect files","metadata":{"parentSessionId":"ses_oc_collab_root","sessionId":"ses_oc_collab_child","background":false},"output":"<task id=\"ses_oc_collab_child\" state=\"completed\">done</task>","time":{"start":2000,"end":3000}}}`)

	seedOpenCodeCollaborationMessage(t, db, "oc-child-user", opencodeCollaborationChildID, 2050, `{"role":"user"}`)
	seedOpenCodeCollaborationPart(t, db, "oc-child-user-text", "oc-child-user", opencodeCollaborationChildID, 2050, `{"type":"text","text":"Inspect the files"}`)
	seedOpenCodeCollaborationMessage(t, db, "oc-child-assistant", opencodeCollaborationChildID, 2100, `{"role":"assistant","parentID":"oc-child-user","agent":"explore","finish":"stop","time":{"created":2100,"completed":3200}}`)
	seedOpenCodeCollaborationPart(t, db, "oc-child-text", "oc-child-assistant", opencodeCollaborationChildID, 2100, `{"type":"text","text":"Child inspection complete"}`)
	seedOpenCodeCollaborationPart(t, db, "oc-grandchild-task", "oc-child-assistant", opencodeCollaborationChildID, 2300, `{"type":"tool","tool":"task","callID":"call-grandchild","state":{"status":"completed","title":"Review output","metadata":{"parentSessionId":"ses_oc_collab_child","sessionId":"ses_oc_collab_grandchild","background":true},"output":"<task id=\"ses_oc_collab_grandchild\" state=\"completed\">reviewed</task>","time":{"start":2300,"end":2600}}}`)

	seedOpenCodeCollaborationMessage(t, db, "oc-grandchild-user", opencodeCollaborationGrandchildID, 2350, `{"role":"user"}`)
	seedOpenCodeCollaborationPart(t, db, "oc-grandchild-user-text", "oc-grandchild-user", opencodeCollaborationGrandchildID, 2350, `{"type":"text","text":"Review the output"}`)
	seedOpenCodeCollaborationMessage(t, db, "oc-grandchild-assistant", opencodeCollaborationGrandchildID, 2400, `{"role":"assistant","parentID":"oc-grandchild-user","agent":"audit","finish":"stop","time":{"created":2400,"completed":2800}}`)
	seedOpenCodeCollaborationPart(t, db, "oc-grandchild-text", "oc-grandchild-assistant", opencodeCollaborationGrandchildID, 2400, `{"type":"text","text":"Review complete"}`)
}

func seedOpenCodeCollaborationSession(t *testing.T, db *sql.DB, sessionID, parentSessionID, title string, createdAt, updatedAt int64, agentName string) {
	t.Helper()
	var parentValue any
	if parentSessionID != "" {
		parentValue = parentSessionID
	}
	mustExec(t, db, `INSERT INTO session (id, parent_id, directory, title, time_created, time_updated, model, agent) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, parentValue, "/tmp/opencode-project", title, createdAt, updatedAt, `{"id":"test-model","providerID":"test"}`, agentName)
}

func seedOpenCodeCollaborationMessage(t *testing.T, db *sql.DB, messageID, sessionID string, createdAt int64, data string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		messageID, sessionID, createdAt, createdAt, data)
}

func seedOpenCodeCollaborationPart(t *testing.T, db *sql.DB, partID, messageID, sessionID string, createdAt int64, data string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		partID, messageID, sessionID, createdAt, createdAt, data)
}

func openCodeCollaborationRoot(t *testing.T, reader *OpenCodeReader) model.Session {
	return openCodeCollaborationRootByID(t, reader, opencodeCollaborationRootID)
}

func openCodeCollaborationRootByID(t *testing.T, reader *OpenCodeReader, sessionID string) model.Session {
	t.Helper()
	for _, session := range mustListOpenCodeSessions(t, reader) {
		if session.ID == sessionID {
			return session
		}
	}
	t.Fatalf("session %q not listed", sessionID)
	return model.Session{}
}

func mustListOpenCodeSessions(t *testing.T, reader *OpenCodeReader) []model.Session {
	t.Helper()
	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return sessions
}

func assertOpenCodeTaskAnchors(t *testing.T, delegation collaboration.Delegation, toolCallID, sessionID string, triggerMillis, resultMillis int64) {
	t.Helper()
	if delegation.Trigger == nil || delegation.Trigger.SessionID != sessionID || delegation.Trigger.ToolCallID != toolCallID || delegation.Trigger.EventID == "" || delegation.Trigger.Timestamp == nil || delegation.Trigger.Timestamp.UnixMilli() != triggerMillis {
		t.Errorf("trigger = %+v", delegation.Trigger)
	}
	if delegation.Result == nil || delegation.Result.SessionID != sessionID || delegation.Result.ToolCallID != toolCallID || delegation.Result.EventID == "" || delegation.Result.Timestamp == nil || delegation.Result.Timestamp.UnixMilli() != resultMillis {
		t.Errorf("result = %+v", delegation.Result)
	}
	if delegation.Evidence.Trigger != collaboration.ExactFact() || delegation.Evidence.Result != collaboration.ExactFact() {
		t.Errorf("anchor evidence = trigger:%+v result:%+v", delegation.Evidence.Trigger, delegation.Evidence.Result)
	}
}
