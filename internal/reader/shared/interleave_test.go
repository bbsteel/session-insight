package shared

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Parallel tool calls are stored as one assistant message with N tool_calls
// followed by N result records. The render stream must pair each result with
// its invocation (call1, result1, call2, result2), not emit all invocations
// before all results.
func TestInterleaveToolResultsPairsResultsWithInvocations(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", EventID: "b0", TurnIndex: 0},
		{Type: "ToolInvocation", EventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "ToolInvocation", EventID: "inv-2", TurnIndex: 0, ToolCallID: "call-2"},
		{Type: "ToolResult", EventID: "res-1", ParentEventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "ToolResult", EventID: "res-2", ParentEventID: "inv-2", TurnIndex: 0, ToolCallID: "call-2"},
	}
	got := InterleaveToolResults(events)
	var order []string
	for _, event := range got {
		order = append(order, event.EventID)
	}
	want := []string{"b0", "inv-1", "res-1", "inv-2", "res-2"}
	if len(order) != len(want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v, want %v", order, want)
		}
	}
}

// Multiple results under one invocation (stdout + stderr as separate events)
// keep their relative order when moved.
func TestInterleaveToolResultsKeepsSiblingResultOrder(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", EventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "ToolInvocation", EventID: "inv-2", TurnIndex: 0, ToolCallID: "call-2"},
		{Type: "ToolResult", EventID: "res-1a", ParentEventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "ToolResult", EventID: "res-2", ParentEventID: "inv-2", TurnIndex: 0, ToolCallID: "call-2"},
		{Type: "ToolResult", EventID: "res-1b", ParentEventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
	}
	got := InterleaveToolResults(events)
	var order []string
	for _, event := range got {
		order = append(order, event.EventID)
	}
	want := []string{"inv-1", "res-1a", "res-1b", "inv-2", "res-2"}
	if len(order) != len(want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v, want %v", order, want)
		}
	}
}

// A result without a matching invocation in the same turn must keep its
// stream position instead of being reordered across turns.
func TestInterleaveToolResultsKeepsUnmatchedAndCrossTurn(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", EventID: "b0", TurnIndex: 0},
		{Type: "ToolInvocation", EventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "TurnBoundary", EventID: "b1", TurnIndex: 1},
		{Type: "ToolResult", EventID: "res-late", ParentEventID: "inv-1", TurnIndex: 1, ToolCallID: "call-1"},
		{Type: "ToolResult", EventID: "res-orphan", ParentEventID: "inv-missing", TurnIndex: 1, ToolCallID: "call-x"},
	}
	got := InterleaveToolResults(events)
	var order []string
	for _, event := range got {
		order = append(order, event.EventID)
	}
	want := []string{"b0", "inv-1", "b1", "res-late", "res-orphan"}
	if len(order) != len(want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v, want %v", order, want)
		}
	}
}

// Already-paired streams pass through unchanged.
func TestInterleaveToolResultsNoopWhenAlreadyInterleaved(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", EventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "ToolResult", EventID: "res-1", ParentEventID: "inv-1", TurnIndex: 0, ToolCallID: "call-1"},
		{Type: "ToolInvocation", EventID: "inv-2", TurnIndex: 0, ToolCallID: "call-2"},
		{Type: "ToolResult", EventID: "res-2", ParentEventID: "inv-2", TurnIndex: 0, ToolCallID: "call-2"},
	}
	got := InterleaveToolResults(events)
	var order []string
	for _, event := range got {
		order = append(order, event.EventID)
	}
	want := []string{"inv-1", "res-1", "inv-2", "res-2"}
	if len(order) != len(want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v, want %v", order, want)
		}
	}
}
