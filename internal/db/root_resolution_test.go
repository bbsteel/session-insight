package db

import (
	"testing"
	"time"
)

// ResolveRootSessions maps subagent sessions to their root ancestor so
// search-result landing can target the sidebar's root-only list. The two
// parent-linkage shapes seen in the wild are both covered: Codex children
// record the parent's native UUID (the parent row's resume_id), Grok
// children record the parent row's id.
func TestResolveRootSessions(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	upsert := func(agentType, id, resumeID, parentID string, isSubagent bool, name string) {
		t.Helper()
		if err := db.UpsertSessionMetaWithHistoryAndLineage(
			agentType, id, "/cwd", "", "", "proj", name, "model", resumeID, parentID, "", isSubagent,
			1, 0, 0, 1, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}

	// Codex shape: child.parent_session_id == parent.resume_id.
	upsert("codex", "rollout-root", "native-root", "", false, "root session")
	upsert("codex", "rollout-mid", "native-mid", "native-root", true, "mid session")
	upsert("codex", "rollout-leaf", "native-leaf", "native-mid", true, "leaf session")
	// Grok shape: child.parent_session_id == parent.id.
	upsert("grok", "grok-root", "grok-root", "", false, "grok root")
	upsert("grok", "grok-child", "grok-child", "grok-root", true, "grok child")

	keys := []struct{ AgentType, SessionID string }{
		{AgentType: "codex", SessionID: "rollout-leaf"},
		{AgentType: "codex", SessionID: "rollout-mid"},
		{AgentType: "codex", SessionID: "rollout-root"},
		{AgentType: "grok", SessionID: "grok-child"},
		{AgentType: "claude", SessionID: "unknown"},
	}
	roots, err := db.ResolveRootSessions(keys)
	if err != nil {
		t.Fatal(err)
	}

	leaf := roots["codex\x00rollout-leaf"]
	if leaf.SessionID != "rollout-root" || leaf.AgentType != "codex" || leaf.Name != "root session" {
		t.Errorf("codex leaf root = %+v, want rollout-root", leaf)
	}
	mid := roots["codex\x00rollout-mid"]
	if mid.SessionID != "rollout-root" {
		t.Errorf("codex mid root = %+v, want rollout-root", mid)
	}
	grokChild := roots["grok\x00grok-child"]
	if grokChild.SessionID != "grok-root" {
		t.Errorf("grok child root = %+v, want grok-root", grokChild)
	}
	// Roots and unknown sessions stay absent: callers keep current behavior.
	if _, ok := roots["codex\x00rollout-root"]; ok {
		t.Error("root session must not map to itself")
	}
	if _, ok := roots["claude\x00unknown"]; ok {
		t.Error("unknown session must stay absent")
	}
}

func TestResolveRootSessionsBrokenChain(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// Child whose parent_session_id matches no row (parent deleted).
	if err := db.UpsertSessionMetaWithHistoryAndLineage(
		"codex", "rollout-orphan", "/cwd", "", "", "proj", "orphan", "model", "native-orphan", "native-gone", "", true,
		1, 0, 0, 1, now, now,
	); err != nil {
		t.Fatal(err)
	}
	// Self-loop guard.
	if err := db.UpsertSessionMetaWithHistoryAndLineage(
		"codex", "rollout-loop", "/cwd", "", "", "proj", "loop", "model", "native-loop", "native-loop", "", true,
		1, 0, 0, 1, now, now,
	); err != nil {
		t.Fatal(err)
	}

	roots, err := db.ResolveRootSessions([]struct{ AgentType, SessionID string }{
		{AgentType: "codex", SessionID: "rollout-orphan"},
		{AgentType: "codex", SessionID: "rollout-loop"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Fatalf("broken chains must stay absent, got %+v", roots)
	}
}
