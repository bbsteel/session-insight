package chrys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSession mirrors the real ~/.chrys/sessions layout: one main
// session.json plus one sub-agent transcript joined via call_id.
const fixtureMain = `{
  "meta": {
    "schema_version": 1,
    "session_id": "28491d6d-491e-4c83-80b2-4fdd57e6fb7f",
    "agent_profile": "Code",
    "agent_display_name": "Code Agent",
    "model_provider": "deepseek-openai",
    "model_id": "deepseek-v4-pro",
    "created_at": "2026-07-06T04:22:06.342847+00:00",
    "updated_at": "2026-07-06T04:22:06.342847+00:00",
    "message_count": 8,
    "primary_cwd": "/tmp/proj",
    "title": "手机端套装列表调整",
    "custom_title": "",
    "generated_title": ""
  },
  "state": {
    "messages": [
      {
        "type": "message", "role": "user",
        "contents": [
          {"type": "text", "text": "手机端套装列表调整", "additional_properties": {}},
          {"type": "data", "uri": "data:image/png;base64,AAAA", "media_type": "image/png", "additional_properties": {}}
        ],
        "additional_properties": {"_chrys_created_at": "2026-07-06T04:18:34.064555+00:00"}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [
          {"type": "text_reasoning", "text": "先探索代码。", "additional_properties": {}},
          {"type": "function_call", "call_id": "call_sub_1", "name": "explore_agent",
           "arguments": "{\"prompt\": \"找到筛选组件\"}", "additional_properties": {}}
        ],
        "additional_properties": {"_chrys_created_at": "2026-07-06T04:22:06.293096+00:00"}
      },
      {
        "type": "message", "role": "tool",
        "contents": [
          {"type": "function_result", "call_id": "call_sub_1", "result": "组件在 SetsList.vue", "additional_properties": {}}
        ],
        "additional_properties": {}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [
          {"type": "function_call", "call_id": "call_edit_1", "name": "edit_file",
           "arguments": "{\"path\": \"/tmp/proj/a.css\", \"old_string\": \"gap: 1px;\", \"new_string\": \"gap: 2px;\"}",
           "additional_properties": {}}
        ],
        "additional_properties": {
          "_intermediate_text": "改成 3 列布局。",
          "_chrys_created_at": "2026-07-06T04:22:06.293096+00:00"
        }
      },
      {
        "type": "message", "role": "tool",
        "contents": [
          {"type": "function_result", "call_id": "call_edit_1",
           "exception": "1 validation error", "result": "Error: Argument parsing failed.",
           "items": [{"type": "text", "text": "Error: Argument parsing failed."}],
           "additional_properties": {"failed": true, "tool_error_message": "Argument parsing failed."}}
        ],
        "additional_properties": {}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [{"type": "text", "text": "Error code: 400", "additional_properties": {}}],
        "additional_properties": {"_chrys_kind": "interrupted", "_interrupted_by": "error"}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [{"type": "text", "text": "", "additional_properties": {}}],
        "additional_properties": {"_chrys_kind": "turn", "_turn_id": "turn_1", "_turn": 1}
      }
    ],
    "compressed_msgs": [],
    "turn_counter": 1,
    "total_session_input_tokens": 555593,
    "total_session_output_tokens": 10759,
    "total_session_cache_hit_tokens": 488192
  }
}`

const fixtureSub = `{
  "meta": {
    "schema_version": 1,
    "record_type": "sub_agent_session",
    "parent_session_id": "28491d6d-491e-4c83-80b2-4fdd57e6fb7f",
    "parent_provider_call_id": "call_sub_1",
    "invocation_id": "e9a4ee5e36db",
    "tool_name": "explore_agent",
    "agent_profile": "Explore",
    "agent_display_name": "Explore Agent",
    "status": "completed",
    "created_at": "2026-07-06T04:18:43.191495+00:00",
    "updated_at": "2026-07-06T04:19:33.948229+00:00"
  },
  "state": {
    "messages": [
      {
        "type": "message", "role": "user",
        "contents": [{"type": "text", "text": "找到筛选组件", "additional_properties": {}}],
        "additional_properties": {}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [
          {"type": "function_call", "call_id": "call_glob_1", "name": "glob",
           "arguments": "{\"pattern\": \"**/*set*\"}", "additional_properties": {}}
        ],
        "additional_properties": {}
      },
      {
        "type": "message", "role": "tool",
        "contents": [
          {"type": "function_result", "call_id": "call_glob_1", "result": "SetsList.vue", "additional_properties": {}}
        ],
        "additional_properties": {}
      }
    ],
    "compressed_msgs": [],
    "turn_counter": 1
  }
}`

func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "28491d6d491e")
	subDir := filepath.Join(dir, "sub_agents", "sessions")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(fixtureMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "explore_agent_e9a4ee5e36db.json"), []byte(fixtureSub), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory without session.json must be skipped, not break listing.
	if err := os.MkdirAll(filepath.Join(root, "attribution"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestListSessions(t *testing.T) {
	r := New(writeFixture(t))
	sessions, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.ID != "28491d6d491e" || s.AgentType != "chrys" {
		t.Errorf("unexpected identity: %+v", s)
	}
	if s.Name != "手机端套装列表调整" {
		t.Errorf("name = %q", s.Name)
	}
	if s.ModelName != "deepseek-v4-pro" {
		t.Errorf("model = %q", s.ModelName)
	}
	if s.TurnCount != 1 || s.MessageCount != 8 {
		t.Errorf("turn/message counts: %d/%d", s.TurnCount, s.MessageCount)
	}
	// created_at must come from the first message (earlier than meta's
	// save-time stamp).
	if got := s.CreatedAt.UTC().Format("15:04:05"); got != "04:18:34" {
		t.Errorf("createdAt = %s", got)
	}
	if !strings.Contains(s.PreviewText, "手机端套装列表调整") {
		t.Errorf("preview = %q", s.PreviewText)
	}
}

func TestGetSession(t *testing.T) {
	r := New(writeFixture(t))
	d, err := r.GetSession("28491d6d491e")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(d.Turns))
	}
	turn := d.Turns[0]
	if turn.ToolCallCount != 2 {
		t.Errorf("tool calls = %d", turn.ToolCallCount)
	}
	// One failed tool + one interrupted marker.
	if turn.ErrorCount != 2 {
		t.Errorf("errors = %d", turn.ErrorCount)
	}
	if turn.RequestCount != 2 {
		t.Errorf("requests = %d", turn.RequestCount)
	}
	if len(turn.Subagents) != 1 || turn.Subagents[0] != "explore_agent" {
		t.Errorf("subagents = %v", turn.Subagents)
	}
	if !strings.Contains(turn.AssistantMessage, "改成 3 列布局") {
		t.Errorf("intermediate text missing from assistant message: %q", turn.AssistantMessage)
	}
	// 04:18:34.064 user → 04:22:06.293 last assistant stamp ≈ 212s.
	if turn.DurationMs < 212000 || turn.DurationMs > 213000 {
		t.Errorf("duration = %dms", turn.DurationMs)
	}
	hasInterrupted := false
	for _, a := range turn.Anomalies {
		if a == "interrupted" {
			hasInterrupted = true
		}
	}
	if !hasInterrupted {
		t.Errorf("anomalies = %v", turn.Anomalies)
	}

	if d.Billing == nil {
		t.Fatal("billing missing")
	}
	b := d.Billing
	if b.Precision != "exact" {
		t.Errorf("precision = %q", b.Precision)
	}
	// Inclusive input converted to exclusive prompt bucket.
	if b.Totals.PromptTokens != 555593-488192 {
		t.Errorf("prompt tokens = %d", b.Totals.PromptTokens)
	}
	if b.Totals.CacheReadTokens != 488192 || b.Totals.CompletionTokens != 10759 {
		t.Errorf("totals = %+v", b.Totals)
	}
}

func TestRenderEvents(t *testing.T) {
	r := New(writeFixture(t))
	events, err := r.GetRenderEvents("28491d6d491e")
	if err != nil {
		t.Fatal(err)
	}

	var (
		gotUserPrompt, gotThinking, gotIntermediate  bool
		gotEditInvocation, gotFailedResult           bool
		gotInterrupted, gotSubStart, gotNestedResult bool
	)
	for _, e := range events {
		switch {
		case e.Type == "UserPrompt":
			gotUserPrompt = true
			if !strings.Contains(e.Text, "[图片: image/png]") {
				t.Errorf("image placeholder missing in prompt: %q", e.Text)
			}
			if strings.Contains(e.Text, "base64") {
				t.Errorf("raw data URI leaked into prompt")
			}
		case e.Type == "ThinkingStart":
			gotThinking = true
		case e.Type == "TextChunk" && strings.Contains(e.Text, "改成 3 列布局"):
			gotIntermediate = true
		case e.Type == "ToolInvocation" && e.ToolName == "edit_file":
			gotEditInvocation = true
			if fp, _ := e.ToolInput["file_path"].(string); fp != "/tmp/proj/a.css" {
				t.Errorf("edit_file path not normalised: %v", e.ToolInput)
			}
		case e.Type == "ToolResult" && e.ExitCode == 1:
			gotFailedResult = true
			if e.Stderr == "" {
				t.Errorf("failed result missing stderr")
			}
		case e.Type == "AgentSpecific" && e.Subtype == "interrupted":
			gotInterrupted = true
		case e.Type == "AgentSpecific" && e.Subtype == "subagent_started":
			gotSubStart = true
			if e.Depth != 1 {
				t.Errorf("subagent start depth = %d", e.Depth)
			}
		case e.Type == "ToolResult" && e.Depth == 1:
			gotNestedResult = true
			if e.TurnIndex != 0 {
				t.Errorf("nested event turn index = %d", e.TurnIndex)
			}
		}
	}

	for name, ok := range map[string]bool{
		"user prompt":        gotUserPrompt,
		"thinking":           gotThinking,
		"intermediate text":  gotIntermediate,
		"edit invocation":    gotEditInvocation,
		"failed result":      gotFailedResult,
		"interrupted marker": gotInterrupted,
		"subagent start":     gotSubStart,
		"nested result":      gotNestedResult,
	} {
		if !ok {
			t.Errorf("missing render event: %s", name)
		}
	}

	// Sub-agent transcript must be spliced before its summary ToolResult.
	subStartIdx, summaryIdx := -1, -1
	for i, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "subagent_started" {
			subStartIdx = i
		}
		if e.Type == "ToolResult" && e.ToolCallID == "call_sub_1" {
			summaryIdx = i
		}
	}
	if subStartIdx < 0 || summaryIdx < 0 || subStartIdx > summaryIdx {
		t.Errorf("splice order wrong: subagent at %d, summary at %d", subStartIdx, summaryIdx)
	}

	ansi, err := r.RenderANSI("28491d6d491e", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi, "手机端套装列表调整") {
		t.Errorf("ANSI output missing user prompt")
	}
	if !strings.Contains(ansi, "中断") {
		t.Errorf("ANSI output missing interrupted line")
	}
}

const fixtureInFlight = `{
  "meta": {"schema_version": 1, "session_id": "beef", "agent_profile": "chrys", "updated_at": "2026-07-06T05:00:10+00:00"},
  "state": {
    "messages": [
      {"type": "message", "role": "user",
       "contents": [{"type": "text", "text": "内存是不是给高了", "additional_properties": {}}],
       "additional_properties": {"_chrys_created_at": "2026-07-06T05:00:00+00:00"}},
      {"type": "message", "role": "assistant",
       "contents": [{"type": "text", "text": "Session closed before the turn finished", "additional_properties": {}}],
       "additional_properties": {"_chrys_kind": "interrupted", "_interrupted_by": "", "_chrys_created_at": "2026-07-06T05:00:10+00:00"}}
    ],
    "compressed_msgs": [], "turn_counter": 1
  }
}`

// A chrys in-flight recovery checkpoint (interrupted marker with an empty
// _interrupted_by) is a turn still running, not an interruption: it renders as
// a neutral "推理中…" placeholder and must not flag the turn as an anomaly.
func TestInFlightCheckpointNotAnomaly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "beef")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(fixtureInFlight), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(root)

	d, err := r.GetSession("beef")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(d.Turns))
	}
	if d.Turns[0].ErrorCount != 0 {
		t.Errorf("in-flight checkpoint counted as error: %d", d.Turns[0].ErrorCount)
	}
	for _, a := range d.Turns[0].Anomalies {
		if a == "interrupted" {
			t.Errorf("in-flight checkpoint flagged as interrupted anomaly")
		}
	}

	events, err := r.GetRenderEvents("beef")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "interrupted" {
			t.Errorf("in-flight checkpoint emitted an interrupted event")
		}
	}
	gotInProgress := false
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "in_progress" {
			gotInProgress = true
		}
	}
	if !gotInProgress {
		t.Errorf("missing in_progress render event")
	}

	ansi, err := r.RenderANSI("beef", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi, "推理中") {
		t.Errorf("ANSI missing in-progress placeholder")
	}
	if strings.Contains(ansi, "中断") {
		t.Errorf("ANSI wrongly shows an interruption for an in-flight checkpoint")
	}
}

func TestGetSessionRejectsPathTraversal(t *testing.T) {
	r := New(writeFixture(t))
	if _, err := r.GetSession("../evil"); err == nil {
		t.Fatal("expected error for path traversal id")
	}
	if _, err := r.GetRenderEvents("../evil"); err == nil {
		t.Fatal("expected error for path traversal id")
	}
}

// makeRecovery derives a recovery-sidecar variant of fixtureMain: given
// updated_at, with one extra user turn appended after the interruption.
func makeRecovery(t *testing.T, updatedAt string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(fixtureMain), &doc); err != nil {
		t.Fatal(err)
	}
	meta := doc["meta"].(map[string]any)
	meta["updated_at"] = updatedAt
	state := doc["state"].(map[string]any)
	msgs := state["messages"].([]any)
	extra := map[string]any{
		"type": "message", "role": "user",
		"contents": []any{map[string]any{"type": "text", "text": "中断后继续的新一轮", "additional_properties": map[string]any{}}},
		"additional_properties": map[string]any{"_chrys_created_at": "2026-07-06T06:20:00+00:00"},
	}
	reply := map[string]any{
		"type": "message", "role": "assistant",
		"contents": []any{map[string]any{"type": "text", "text": "恢复副本里的回复", "additional_properties": map[string]any{}}},
		"additional_properties": map[string]any{"_chrys_created_at": "2026-07-06T06:21:00+00:00"},
	}
	state["messages"] = append(msgs, extra, reply)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRecoverySidecarWinsWhenNewer(t *testing.T) {
	root := writeFixture(t)
	dir := filepath.Join(root, "28491d6d491e")
	// Newer than fixtureMain's 04:22:06 → sidecar is the effective source.
	if err := os.WriteFile(filepath.Join(dir, "session.recovery.json"), makeRecovery(t, "2026-07-06T06:23:51+00:00"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(root)
	d, err := r.GetSession("28491d6d491e")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Turns) != 2 {
		t.Fatalf("want 2 turns from recovery sidecar, got %d", len(d.Turns))
	}
	if !strings.Contains(d.Turns[1].UserMessage, "中断后继续") {
		t.Errorf("post-interruption turn missing: %+v", d.Turns[1])
	}

	ansi, err := r.RenderANSI("28491d6d491e", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi, "恢复副本里的回复") {
		t.Errorf("render missing recovery content")
	}
}

func TestStaleRecoverySidecarIgnored(t *testing.T) {
	root := writeFixture(t)
	dir := filepath.Join(root, "28491d6d491e")
	// Older than fixtureMain's 04:22:06 → primary stays effective.
	if err := os.WriteFile(filepath.Join(dir, "session.recovery.json"), makeRecovery(t, "2026-07-06T04:00:00+00:00"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(root)
	d, err := r.GetSession("28491d6d491e")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Turns) != 1 {
		t.Fatalf("stale sidecar must be ignored, got %d turns", len(d.Turns))
	}
}

func TestRecoveryOnlySessionListed(t *testing.T) {
	root := writeFixture(t)
	dir := filepath.Join(root, "deadbeef0000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Crash before first primary save: only the sidecar exists.
	if err := os.WriteFile(filepath.Join(dir, "session.recovery.json"), makeRecovery(t, "2026-07-06T06:23:51+00:00"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(root)
	sessions, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions (fixture + recovery-only), got %d", len(sessions))
	}
}

// Guard: the fixture JSON must stay parseable as the shapes the reader uses.
func TestFixtureShapes(t *testing.T) {
	var sf sessionFile
	if err := json.Unmarshal([]byte(fixtureMain), &sf); err != nil {
		t.Fatal(err)
	}
	if len(sf.State.Messages) != 7 {
		t.Fatalf("fixture messages = %d", len(sf.State.Messages))
	}
}
