package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexToRenderEventsCustomToolInputAndSingleResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"timestamp":"2026-06-20T01:00:00.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-06-20T01:00:01.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call-1","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"}}`,
		`{"timestamp":"2026-06-20T01:00:02.000Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"call-1","stdout":"Success","success":true}}`,
		`{"timestamp":"2026-06-20T01:00:03.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-1","output":"Exit code: 0\nOutput:\nSuccess"}}`,
		`{"timestamp":"2026-06-20T01:00:04.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatalf("codexToRenderEvents() failed: %v", err)
	}

	var invocationInput string
	var boundaries, results int
	for _, event := range events {
		if event.TurnIndex < 0 {
			t.Errorf("event %q has negative turn index %d", event.Type, event.TurnIndex)
		}
		switch event.Type {
		case "TurnBoundary":
			boundaries++
		case "ToolInvocation":
			invocationInput, _ = event.ToolInput["args"].(string)
		case "ToolResult":
			results++
			if event.ToolCallID != "call-1" || event.ParentEventID == "" {
				t.Errorf("tool result is not linked to its invocation: %#v", event)
			}
		}
	}
	if invocationInput != "*** Begin Patch\n*** End Patch" {
		t.Errorf("custom tool input not preserved, got %q", invocationInput)
	}
	if results != 1 {
		t.Fatalf("expected one tool result, got %d", results)
	}
	if boundaries != 1 {
		t.Fatalf("expected trailing empty turn to be dropped, got %d boundaries", boundaries)
	}
}
