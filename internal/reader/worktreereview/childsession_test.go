package worktreereview

import (
	"strings"
	"testing"
)

// B-202: child agent session linkage (design 5.4). The mapping is
// evidence-only: both identifiers must be recorded on provider_call.completed;
// absence stays absent and is never reconstructed from timing or model names.

func TestChildSessionLinkageWhenRecorded(t *testing.T) {
	r := New("testdata")

	events, err := r.GetRenderEvents(fixtureChild)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for i := range events {
		if events[i].Subtype != "child_session" {
			continue
		}
		found++
		if !strings.Contains(events[i].Text, "codex 019f0000-0000-7000-8000-0000000000cd") {
			t.Fatalf("child link text=%q", events[i].Text)
		}
	}
	if found != 1 {
		t.Fatalf("child_session events=%d want 1 (the second call has no recorded child)", found)
	}

	detail, err := r.GetSession(fixtureChild)
	if err != nil {
		t.Fatal(err)
	}
	var subagents []string
	for _, turn := range detail.Turns {
		subagents = append(subagents, turn.Subagents...)
	}
	if len(subagents) != 1 || subagents[0] != "codex:019f0000-0000-7000-8000-0000000000cd" {
		t.Fatalf("subagents=%v", subagents)
	}
}

func TestChildSessionAbsentWhenNotRecorded(t *testing.T) {
	r := New("testdata")
	events, err := r.GetRenderEvents(fixtureBlocked)
	if err != nil {
		t.Fatal(err)
	}
	for i := range events {
		if events[i].Subtype == "child_session" {
			t.Fatalf("blocked fixture records no child ids, got %q", events[i].Text)
		}
	}
	detail, err := r.GetSession(fixtureBlocked)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range detail.Turns {
		if len(turn.Subagents) != 0 {
			t.Fatalf("subagents guessed without evidence: %v", turn.Subagents)
		}
	}
}

// B-202: incremental JSONL growth is re-read in file order, and a live
// attempt becomes readable without waiting for a terminal state.
func TestIncrementalReadsWhileRunning(t *testing.T) {
	r := New("testdata")

	before, err := r.GetRenderEvents(fixtureRunning)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 4 {
		t.Fatalf("running fixture events=%d want 4", len(before))
	}

	live, err := r.SessionLive(fixtureRunning)
	if err != nil || !live {
		t.Fatalf("running fixture live=%v err=%v", live, err)
	}
	rev, err := r.LiveRevision(fixtureRunning)
	if err != nil {
		t.Fatal(err)
	}
	if rev == 0 {
		t.Fatal("running fixture must have a non-zero live revision")
	}
}
