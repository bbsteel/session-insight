package chrys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Collaboration evidence for archetype 2: embedded child transcript.
//
// These tests lock the facts the future shared AgentInvocation/Delegation
// contract will rely on. They intentionally assert current adapter behavior,
// including gaps (child status/timing fields unused), so the evidence is
// reproducible without private local records. Fixture provenance and layout:
// testdata/collaboration-embedded-child/README.md.

const collabChrysRoot = "28491d6d491e"

func collabChrysFixtureRoot(t *testing.T) string {
	t.Helper()
	return "testdata/collaboration-embedded-child"
}

// The parent-child join is exact and two-sided: the child sidecar's
// meta.parent_provider_call_id equals the parent function_call's call_id.
// This is the strongest launch/result anchor among the six adapters.
func TestCollaborationEmbeddedChildExactJoin(t *testing.T) {
	root := collabChrysFixtureRoot(t)
	sessionDir := filepath.Join(root, collabChrysRoot)

	index := buildSubagentIndex(sessionDir)
	childPath, ok := index["call_sub_1"]
	if !ok {
		t.Fatalf("subagent index has no entry for parent call_id call_sub_1: %v", index)
	}

	child, err := readSessionFile(childPath)
	if err != nil {
		t.Fatalf("read child sidecar: %v", err)
	}
	if child.Meta.RecordType != "sub_agent_session" {
		t.Errorf("record_type = %q, want sub_agent_session", child.Meta.RecordType)
	}
	if child.Meta.ParentProviderCallID != "call_sub_1" {
		t.Errorf("parent_provider_call_id = %q, want call_sub_1", child.Meta.ParentProviderCallID)
	}
	// Source records an explicit terminal status and child timestamps; the
	// adapter parses but never consumes them (running vs orphaned children
	// are indistinguishable in adapter output today).
	if child.Meta.Status != "completed" {
		t.Errorf("source status = %q, want completed", child.Meta.Status)
	}

	// The other side of the join: the parent must contain the function_call
	// whose call_id the child points at.
	main, err := readSessionFile(filepath.Join(sessionDir, "session.json"))
	if err != nil {
		t.Fatalf("read main session: %v", err)
	}
	foundCall := false
	for _, m := range main.State.Messages {
		for _, c := range m.Contents {
			if c.Type == "function_call" && c.CallID == child.Meta.ParentProviderCallID {
				foundCall = true
			}
		}
	}
	if !foundCall {
		t.Error("parent has no function_call matching the child's parent_provider_call_id")
	}

	// The sidecar's parent_session_id must name the parent session record.
	// sessionMeta does not parse this field, so read it from the raw JSON.
	var raw struct {
		Meta struct {
			ParentSessionID string `json:"parent_session_id"`
		} `json:"meta"`
	}
	body, err := os.ReadFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Meta.ParentSessionID != main.Meta.SessionID {
		t.Errorf("child parent_session_id = %q, want parent session_id %q",
			raw.Meta.ParentSessionID, main.Meta.SessionID)
	}
}

// The embedded child is spliced into the parent's render stream at Depth+1
// between the launch ToolInvocation and the summary ToolResult, and the whole
// sequence is byte-identical across two independent parses.
func TestCollaborationEmbeddedChildStableAcrossParses(t *testing.T) {
	root := collabChrysFixtureRoot(t)

	getEvents := func(t *testing.T) []model.RenderEvent {
		t.Helper()
		events, err := New(root).GetRenderEvents(collabChrysRoot)
		if err != nil {
			t.Fatalf("GetRenderEvents: %v", err)
		}
		return events
	}
	first, second := getEvents(t), getEvents(t)
	if len(first) != len(second) {
		t.Fatalf("event count differs across parses: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Errorf("event %d differs across parses: %+v vs %+v", i, first[i], second[i])
		}
	}

	var (
		launchIdx, subStartIdx, subSummaryIdx, resultIdx = -1, -1, -1, -1
		nestedResult                                     bool
	)
	for i, e := range first {
		switch {
		case e.Type == "ToolInvocation" && e.ToolCallID == "call_sub_1":
			launchIdx = i
			if e.EventID != "call-call_sub_1" {
				t.Errorf("launch invocation EventID = %q, want call-call_sub_1", e.EventID)
			}
		case e.Type == "AgentSpecific" && e.Subtype == "subagent_started":
			subStartIdx = i
			if e.Depth != 1 {
				t.Errorf("subagent_started depth = %d, want 1", e.Depth)
			}
		case e.Type == "AgentSpecific" && e.Subtype == "subagent_summary":
			subSummaryIdx = i
		case e.Type == "ToolResult" && e.ToolCallID == "call_sub_1":
			resultIdx = i
			if e.ParentEventID != "call-call_sub_1" {
				t.Errorf("summary ToolResult ParentEventID = %q, want call-call_sub_1", e.ParentEventID)
			}
		case e.Type == "ToolResult" && e.ToolCallID == "call_glob_1" && e.Depth == 1:
			nestedResult = true
		}
	}
	if launchIdx < 0 || subStartIdx < 0 || subSummaryIdx < 0 || resultIdx < 0 {
		t.Fatalf("missing anchors: launch=%d sub_start=%d sub_summary=%d result=%d",
			launchIdx, subStartIdx, subSummaryIdx, resultIdx)
	}
	if launchIdx >= subStartIdx || subStartIdx >= subSummaryIdx || subSummaryIdx >= resultIdx {
		t.Errorf("splice order wrong: launch=%d sub_start=%d sub_summary=%d result=%d",
			launchIdx, subStartIdx, subSummaryIdx, resultIdx)
	}
	if !nestedResult {
		t.Error("embedded child transcript content missing (no nested call_glob_1 result at depth 1)")
	}
}

// Embedded child records can never surface as root sessions: they live below
// the scanned session directories.
func TestCollaborationEmbeddedChildNotListed(t *testing.T) {
	list, err := New(collabChrysFixtureRoot(t)).ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 || list[0].ID != collabChrysRoot {
		t.Fatalf("want exactly the root session %q, got %+v", collabChrysRoot, list)
	}
}
