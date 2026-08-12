package hermes

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Hermes stores parallel tool calls as one assistant message with N
// tool_calls followed by N tool messages. The render stream must pair each
// result with its invocation (call1, result1, call2, result2), not emit all
// invocations before all results.
func TestRenderMessagesInterleavesParallelToolResults(t *testing.T) {
	messages := []hermesMessage{
		{ID: "1", Role: "user", Content: "search two things"},
		{
			ID:      "2",
			Role:    "assistant",
			Content: "let me search",
			ToolCalls: `[
				{"id": "call-1", "name": "web_search", "arguments": {"query": "first"}},
				{"id": "call-2", "name": "web_search", "arguments": {"query": "second"}}
			]`,
		},
		{ID: "3", Role: "tool", ToolName: "web_search", ToolCallID: "call-1", Content: `{"result": "first answer"}`},
		{ID: "4", Role: "tool", ToolName: "web_search", ToolCallID: "call-2", Content: `{"result": "second answer"}`},
		{ID: "5", Role: "assistant", Content: "done", FinishReason: "stop"},
	}
	events := (&HermesReader{}).renderMessages(sessionRow{EndedAtSet: true}, messages)

	var order []string
	for _, event := range events {
		if event.Type == "ToolInvocation" || event.Type == "ToolResult" {
			order = append(order, event.Type+":"+event.ToolCallID)
		}
	}
	want := []string{
		"ToolInvocation:call-1",
		"ToolResult:call-1",
		"ToolInvocation:call-2",
		"ToolResult:call-2",
	}
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
	got := interleaveToolResults(events)
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
