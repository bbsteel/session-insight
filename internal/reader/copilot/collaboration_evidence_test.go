package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Collaboration evidence for archetype 3: lifecycle-only child invocation.
//
// These tests lock the facts the future shared AgentInvocation/Delegation
// contract will rely on. They intentionally assert current adapter behavior,
// including gaps (render/TurnVM paths consume only subagent.started), so the
// evidence is reproducible without private local records. Fixture provenance
// and layout: testdata/collaboration-lifecycle-only/README.md.

func collabCopilotFixtureStateDir(t *testing.T) string {
	t.Helper()
	return "testdata/collaboration-lifecycle-only"
}

func collabCopilotEventsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(collabCopilotFixtureStateDir(t), "collab-copilot-1", "events.jsonl")
}

// Lifecycle identity is the parent task call's toolCallId, keyed evidence is
// deterministic across two independent parses, and a started-without-completed
// invocation is distinguishable from a completed one.
func TestCollaborationLifecycleStableIdentity(t *testing.T) {
	path := collabCopilotEventsPath(t)

	parse := func(t *testing.T) *model.InsightEvidence {
		t.Helper()
		ev, err := parseInsightEvidence(path)
		if err != nil {
			t.Fatalf("parseInsightEvidence: %v", err)
		}
		return ev
	}
	first, second := parse(t), parse(t)
	if len(first.Subagents) != 2 {
		t.Fatalf("want 2 subagent windows, got %d", len(first.Subagents))
	}
	if len(first.Subagents) != len(second.Subagents) {
		t.Fatalf("window count differs across parses: %d vs %d", len(first.Subagents), len(second.Subagents))
	}
	for i := range first.Subagents {
		if first.Subagents[i] != second.Subagents[i] {
			t.Errorf("window %d differs across parses: %+v vs %+v", i, first.Subagents[i], second.Subagents[i])
		}
	}

	byID := map[string]model.SubagentEvidence{}
	for _, s := range first.Subagents {
		byID[s.ToolCallID] = s
	}

	a, ok := byID["call-task-A"]
	if !ok {
		t.Fatal("completed window call-task-A missing")
	}
	// Exact launch join: delegation facts come from the task
	// tool.execution_start with the same toolCallId.
	if a.Description != "Implement parser change" || a.Model != "m1" || a.Mode != "sync" {
		t.Errorf("A delegation facts wrong: %+v", a)
	}
	const promptA = "Implement the parser change in reader.go"
	if a.PromptChars != len(promptA) {
		t.Errorf("A PromptChars = %d, want %d", a.PromptChars, len(promptA))
	}
	// Exact lifecycle timestamps.
	if a.StartedAt != "2026-01-01T00:01:00Z" || a.CompletedAt != "2026-01-01T00:01:10Z" {
		t.Errorf("A window = %q..%q, want 00:01:00Z..00:01:10Z", a.StartedAt, a.CompletedAt)
	}
	if a.DurationMs != 10_000 {
		t.Errorf("A DurationMs = %d, want 10000", a.DurationMs)
	}
	// Reconstructed-window content is aggregate-only.
	if a.RequestCount != 2 || a.OutputTokens != 30 {
		t.Errorf("A attribution = %d reqs / %d tokens, want 2 / 30", a.RequestCount, a.OutputTokens)
	}

	b, ok := byID["call-task-B"]
	if !ok {
		t.Fatal("orphaned window call-task-B missing")
	}
	// Orphan evidence: started, never completed — emitted with an open end,
	// excluded from response attribution.
	if b.StartedAt != "2026-01-01T00:03:00Z" {
		t.Errorf("B StartedAt = %q, want 2026-01-01T00:03:00Z", b.StartedAt)
	}
	if b.CompletedAt != "" || b.DurationMs != 0 {
		t.Errorf("B must have an open end, got CompletedAt=%q DurationMs=%d", b.CompletedAt, b.DurationMs)
	}
	if b.RequestCount != 0 || b.OutputTokens != 0 {
		t.Errorf("orphaned window must be excluded from attribution, got %d reqs / %d tokens",
			b.RequestCount, b.OutputTokens)
	}
}

// copyCollabFixture copies the fixture into a temp dir: GetSession opens
// <session>/session.db via mattn/go-sqlite3, which creates the file if
// absent, and committed testdata must stay pristine.
func copyCollabFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join(collabCopilotFixtureStateDir(t), "collab-copilot-1")
	dst := filepath.Join(t.TempDir(), "collab-copilot-1")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"workspace.yaml", "events.jsonl"} {
		body, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Dir(dst)
}

// Current render/TurnVM behavior, locked as evidence: both subagents surface
// as started markers and turn-level names, but subagent.completed is not
// consumed on either path, so an orphaned child looks identical to a finished
// one in replay. No child session exists for the lifecycle-only shape.
func TestCollaborationLifecycleRenderCurrentBehavior(t *testing.T) {
	r := New(copyCollabFixture(t))

	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 || list[0].ID != "collab-copilot-1" {
		t.Fatalf("want exactly the parent session, got %+v", list)
	}

	events, err := r.GetRenderEvents("collab-copilot-1")
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	started := map[string]bool{}
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "subagent_started" {
			started[e.Text] = true
		}
		if e.Subtype == "subagent_completed" {
			t.Error("render path must not emit a completed marker today (gap evidence)")
		}
	}
	if !started["Impl Agent"] || !started["Review Agent"] {
		t.Errorf("want started markers for both subagents, got %v", started)
	}

	detail, err := r.GetSession("collab-copilot-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	names := map[string]bool{}
	for _, tr := range detail.Turns {
		for _, n := range tr.Subagents {
			names[n] = true
		}
	}
	if !names["Impl Agent"] || !names["Review Agent"] {
		t.Errorf("want turn-level subagent names for both, got %v", names)
	}
}
