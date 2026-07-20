package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/render"
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

func TestCodexToRenderEventsUnwrapsExecApplyPatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"timestamp":"2026-06-20T01:00:00.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-06-20T01:00:01.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call-1","name":"exec","input":"const patch = \"*** Begin Patch\\n*** Update File: notes.md\\n@@\\n-old\\n+new\\n*** End Patch\";\\nconst r = await tools.apply_patch(patch);"}}`,
		`{"timestamp":"2026-06-20T01:00:02.000Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"call-1","stdout":"Success","success":true}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "ToolInvocation" {
			if event.ToolName != "apply_patch" {
				t.Fatalf("tool name = %q, want apply_patch", event.ToolName)
			}
			if got, _ := event.ToolInput["args"].(string); !strings.Contains(got, "*** Update File: notes.md") {
				t.Fatalf("tool input = %q, want extracted patch", got)
			}
			return
		}
	}
	t.Fatal("apply_patch invocation not found")
}

func TestCodexToRenderEventsCoalescesCellWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-20T12:00:00Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-07-20T12:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"exec-1","name":"exec","input":"const r = await tools.exec_command({\"cmd\":\"node frontend/scripts/validate-i18n.mjs\"});"}}`,
		`{"timestamp":"2026-07-20T12:00:11Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"exec-1","output":"Script running with cell ID 75\nWall time 10.0 seconds\nOutput:\n"}}`,
		`{"timestamp":"2026-07-20T12:00:12Z","type":"response_item","payload":{"type":"function_call","call_id":"wait-1","name":"wait","arguments":"{\"cell_id\":\"75\",\"yield_time_ms\":30000}"}}`,
		`{"timestamp":"2026-07-20T12:00:20Z","type":"response_item","payload":{"type":"function_call_output","call_id":"wait-1","output":[{"type":"input_text","text":"Script completed\nOutput:\nPASS: i18n\n"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	var invocations, results []model.RenderEvent
	for _, event := range events {
		switch event.Type {
		case "ToolInvocation":
			invocations = append(invocations, event)
		case "ToolResult":
			results = append(results, event)
		}
	}
	if len(invocations) != 1 || invocations[0].ToolName != "exec" {
		t.Fatalf("invocations = %#v, want one exec", invocations)
	}
	if got, _ := invocations[0].ToolInput["command"].(string); got != "node frontend/scripts/validate-i18n.mjs" {
		t.Fatalf("command summary = %q", got)
	}
	if len(results) != 1 || results[0].ToolCallID != "exec-1" || !strings.Contains(results[0].Stdout, "PASS: i18n") {
		t.Fatalf("results = %#v, want completed exec result", results)
	}
}

func TestCodexRenderRollbackCreatesFoldAndActivePath(t *testing.T) {
	path := writeCodexRollbackFixture(t,
		`{"timestamp":"2026-07-14T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"one"}}`,
		`{"timestamp":"2026-07-14T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"active one"}}`,
		`{"timestamp":"2026-07-14T10:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"one"}}`,
		`{"timestamp":"2026-07-14T10:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"old"}}`,
		`{"timestamp":"2026-07-14T10:01:01Z","type":"event_msg","payload":{"type":"user_message","message":"old branch"}}`,
		`{"timestamp":"2026-07-14T10:01:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"old"}}`,
		`{"timestamp":"2026-07-14T10:02:00Z","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":1}}`,
		`{"timestamp":"2026-07-14T10:03:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"new"}}`,
		`{"timestamp":"2026-07-14T10:03:01Z","type":"event_msg","payload":{"type":"user_message","message":"new branch"}}`,
		`{"timestamp":"2026-07-14T10:03:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"new"}}`,
	)

	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, event := range events {
		kinds = append(kinds, event.Type)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "RollbackStart,TurnBoundary,UserPrompt,RollbackEnd,TurnBoundary,UserPrompt") {
		t.Fatalf("rollback segment order = %s", joined)
	}
	if got := events[len(events)-2].TurnIndex; got != 1 {
		t.Fatalf("new active branch index = %d, want 1", got)
	}

	ansi, positions := render.FormatEventsWithPositions(events, 100)
	if !strings.Contains(ansi, "已回滚 1 个 turn") || !strings.Contains(ansi, "old branch") {
		t.Fatalf("rollback transcript missing from render:\n%s", ansi)
	}
	var rollbackFold bool
	for _, position := range positions {
		if position.Kind == "fold" && position.Payload["level"] == "rollback" {
			rollbackFold = true
			if position.LineEnd == nil || *position.LineEnd <= position.LineStart {
				t.Errorf("invalid rollback fold extent: %+v", position)
			}
		}
	}
	if !rollbackFold {
		t.Fatalf("rollback fold position missing: %+v", positions)
	}
}
