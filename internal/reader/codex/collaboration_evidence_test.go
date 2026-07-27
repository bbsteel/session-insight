package codex

import (
	"strings"
	"testing"
)

// Collaboration evidence for archetype 1: standalone child Session.
//
// These tests lock the facts the future shared AgentInvocation/Delegation
// contract will rely on. They intentionally assert current adapter behavior,
// including gaps (no launch/result anchor in the parent), so the evidence is
// reproducible without private local records. Fixture provenance and layout:
// testdata/collaboration-standalone-child/README.md.

const (
	collabRootNative  = "019f0000-0000-7000-8000-0000000000aa"
	collabChildNative = "019f0000-0000-7000-8000-0000000000bb"
	collabRootID      = "rollout-2026-01-02T00-00-00-019f0000-0000-7000-8000-0000000000aa"
	collabChildID     = "rollout-2026-01-02T00-00-01-019f0000-0000-7000-8000-0000000000bb"
)

func collabFixtureSessionsDir(t *testing.T) string {
	t.Helper()
	return "testdata/collaboration-standalone-child/sessions"
}

// A child rollout's identity and lineage must be byte-identical across two
// independent parses: the future contract keys AgentInvocation identity on
// these fields.
func TestCollaborationStandaloneChildStableIdentity(t *testing.T) {
	dir := collabFixtureSessionsDir(t)

	type lineage struct {
		id              string
		resumeID        string
		parentSessionID string
		agentPath       string
		isSubagent      bool
	}
	snapshot := func(t *testing.T) map[string]lineage {
		t.Helper()
		list, err := New(dir).ListSessions()
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		out := map[string]lineage{}
		for _, s := range list {
			out[s.ID] = lineage{s.ID, s.ResumeID, s.ParentSessionID, s.AgentPath, s.IsSubagent}
		}
		return out
	}

	first, second := snapshot(t), snapshot(t)
	if len(first) != 2 {
		t.Fatalf("want 2 sessions (root + child), got %d", len(first))
	}
	if len(first) != len(second) {
		t.Fatalf("session count differs across parses: %d vs %d", len(first), len(second))
	}
	for id, a := range first {
		if b, ok := second[id]; !ok || a != b {
			t.Errorf("lineage for %s differs across parses: %+v vs %+v (present=%v)", id, a, b, ok)
		}
	}

	child, ok := first[collabChildID]
	if !ok {
		t.Fatalf("child rollout %s not listed", collabChildID)
	}
	if !child.isSubagent {
		t.Error("child rollout must be classified IsSubagent (thread_source=subagent)")
	}
	if child.parentSessionID != collabRootNative {
		t.Errorf("ParentSessionID = %q, want parent_thread_id %q", child.parentSessionID, collabRootNative)
	}
	if child.agentPath != "/root/audit" {
		t.Errorf("AgentPath = %q, want /root/audit", child.agentPath)
	}
	// The child's resume identity is its own native payload.id, never the
	// parent's session_id and never the rollout file stem.
	if child.resumeID != collabChildNative {
		t.Errorf("child ResumeID = %q, want native payload.id %q", child.resumeID, collabChildNative)
	}

	root, ok := first[collabRootID]
	if !ok {
		t.Fatalf("root rollout %s not listed", collabRootID)
	}
	if root.isSubagent || root.parentSessionID != "" || root.agentPath != "" {
		t.Errorf("root must not carry child lineage: %+v", root)
	}
	if root.resumeID != collabRootNative {
		t.Errorf("root ResumeID = %q, want %q", root.resumeID, collabRootNative)
	}
}

// The child rollout is a full standalone transcript: it can be loaded and
// rendered on its own, which is what makes it a BackingSessionRef candidate
// (independent resume/delete already work per codex_resume_test.go and
// codex_delete_test.go).
func TestCollaborationStandaloneChildContent(t *testing.T) {
	dir := collabFixtureSessionsDir(t)
	r := New(dir)

	detail, err := r.GetSession(collabChildID)
	if err != nil {
		t.Fatalf("GetSession(child): %v", err)
	}
	if !detail.IsSubagent || detail.ParentSessionID != collabRootNative {
		t.Errorf("detail lineage wrong: IsSubagent=%v ParentSessionID=%q", detail.IsSubagent, detail.ParentSessionID)
	}
	if detail.ResumeID != collabChildNative {
		t.Errorf("detail ResumeID = %q, want %q", detail.ResumeID, collabChildNative)
	}

	events, err := r.GetRenderEvents(collabChildID)
	if err != nil {
		t.Fatalf("GetRenderEvents(child): %v", err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.Text, "child audit result") {
			found = true
		}
	}
	if !found {
		t.Error("child transcript content not retrievable through render events")
	}
}
