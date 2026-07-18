package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func writeSession(t *testing.T, root, cwdEnc, id string, sum summaryFile, updates string, events string) sessionLoc {
	t.Helper()
	dir := filepath.Join(root, cwdEnc, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if sum.Info.ID == "" {
		sum.Info.ID = id
	}
	if sum.Info.CWD == "" {
		sum.Info.CWD = "/tmp/demo"
	}
	if sum.CreatedAt == "" {
		sum.CreatedAt = "2026-01-01T00:00:00Z"
	}
	if sum.UpdatedAt == "" {
		sum.UpdatedAt = "2026-01-01T00:01:00Z"
	}
	if sum.GeneratedTitle == "" {
		sum.GeneratedTitle = "demo title"
	}
	if sum.CurrentModelID == "" {
		sum.CurrentModelID = "grok-4.5"
	}
	b, _ := json.Marshal(sum)
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o644); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if updates != "" {
		if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(updates), 0o644); err != nil {
			t.Fatalf("updates: %v", err)
		}
	}
	if events != "" {
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
			t.Fatalf("events: %v", err)
		}
	}
	return sessionLoc{
		ID:          id,
		Dir:         dir,
		ProjectDir:  filepath.Join(root, cwdEnc),
		SummaryPath: filepath.Join(dir, "summary.json"),
	}
}

func sampleUpdatesClosed() string {
	return `{"timestamp":1700000000,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello"}}}}
{"timestamp":1700000001,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking about it"}}}}
{"timestamp":1700000002,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"read_file","rawInput":{"target_file":"/tmp/a.go"},"_meta":{"x.ai/tool":{"name":"read_file"}}}}}
{"timestamp":1700000003,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"package main"}}]}}}
{"timestamp":1700000004,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done reading"}}}}
{"timestamp":1700000005,"method":"_x.ai/session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn","usage":{"inputTokens":100,"outputTokens":20,"cachedReadTokens":40,"reasoningTokens":5,"modelCalls":1,"apiDurationMs":500,"modelUsage":{"grok-4.5":{"inputTokens":100,"outputTokens":20,"cachedReadTokens":40,"reasoningTokens":5,"modelCalls":1,"apiDurationMs":500}}}}}}
`
}

func sampleEventsClosed() string {
	return `{"ts":"2026-01-01T00:00:00Z","type":"turn_started","turn_number":0}
{"ts":"2026-01-01T00:00:05Z","type":"turn_ended","outcome":"completed"}
`
}

func sampleUpdatesOpen() string {
	return `{"timestamp":1700000000,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"still going"}}}}
{"timestamp":1700000001,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"working"}}}}
`
}

func sampleEventsOpen() string {
	return `{"ts":"2026-01-01T00:00:00Z","type":"turn_started","turn_number":0}
`
}

func TestListAndGetSession(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "%2Ftmp%2Fdemo", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())

	r := New(root)
	list, err := r.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 session, got %d", len(list))
	}
	if list[0].AgentType != "grok" {
		t.Errorf("agent_type=%s", list[0].AgentType)
	}
	if list[0].Name != "demo title" {
		t.Errorf("name=%q", list[0].Name)
	}
	if list[0].CWD != "/tmp/demo" {
		t.Errorf("cwd=%q", list[0].CWD)
	}
	if list[0].ResumeID != list[0].ID {
		t.Errorf("resume_id should equal id")
	}

	detail, err := r.GetSession(list[0].ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(detail.Turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(detail.Turns))
	}
	if detail.Turns[0].UserMessage != "hello" {
		t.Errorf("user=%q", detail.Turns[0].UserMessage)
	}
	if detail.Turns[0].ToolCallCount != 1 {
		t.Errorf("tool_call_count=%d", detail.Turns[0].ToolCallCount)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionExact {
		t.Errorf("billing=%+v", detail.Billing)
	}
	// Inclusive input 100 with cache 40 → exclusive prompt 60
	if detail.Billing.Totals.PromptTokens != 60 {
		t.Errorf("prompt_tokens=%d want 60", detail.Billing.Totals.PromptTokens)
	}
	if detail.Billing.Totals.CacheReadTokens != 40 {
		t.Errorf("cache_read=%d", detail.Billing.Totals.CacheReadTokens)
	}
}

func TestBuildSessionIgnoresBackgroundRecapActivity(t *testing.T) {
	root := t.TempDir()
	id := "abababab-bbbb-cccc-dddd-eeeeeeeeeeee"
	lastActive := time.Now().Add(-2 * model.LiveWindow).Truncate(time.Second)
	recapTime := time.Now().Truncate(time.Second)
	loc := writeSession(t, root, "proj", id, summaryFile{
		LastActiveAt: lastActive.Format(time.RFC3339),
		UpdatedAt:    recapTime.Format(time.RFC3339),
	}, sampleUpdatesClosed(), sampleEventsClosed())

	for _, name := range []string{"updates.jsonl", "events.jsonl"} {
		if err := os.Chtimes(filepath.Join(loc.Dir, name), lastActive, lastActive); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}
	if err := os.Chtimes(loc.SummaryPath, recapTime, recapTime); err != nil {
		t.Fatalf("chtimes summary: %v", err)
	}

	sum, err := readSummary(loc.SummaryPath)
	if err != nil {
		t.Fatalf("readSummary: %v", err)
	}
	session := New(root).buildSession(loc, sum)
	if !session.UpdatedAt.Equal(lastActive) {
		t.Fatalf("updated_at=%s, want interactive activity %s", session.UpdatedAt, lastActive)
	}
	if model.IsSessionLive(session.UpdatedAt) {
		t.Fatal("background recap must not reactivate a completed session")
	}
}

func TestRenderEventsFromUpdates(t *testing.T) {
	root := t.TempDir()
	id := "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "proj", id, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
	r := New(root)

	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	types := map[string]int{}
	for _, e := range events {
		types[e.Type]++
		if e.AgentType != "grok" {
			t.Errorf("event agent_type=%s", e.AgentType)
		}
	}
	for _, want := range []string{"TurnBoundary", "UserPrompt", "ThinkingStart", "ThinkingChunk", "ThinkingEnd", "ToolInvocation", "ToolResult", "TextChunk"} {
		if types[want] == 0 {
			t.Errorf("missing event type %s in %v", want, types)
		}
	}
	if hasInProgress(events) {
		t.Error("closed turn must not emit in_progress")
	}

	ansi, err := r.RenderANSI(id, 80)
	if err != nil {
		t.Fatalf("RenderANSI: %v", err)
	}
	if ansi == "" {
		t.Error("expected non-empty ANSI")
	}
}

func TestChatHistoryFallback(t *testing.T) {
	root := t.TempDir()
	id := "cccccccc-bbbb-cccc-dddd-eeeeeeeeeeee"
	dir := filepath.Join(root, "proj", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := summaryFile{
		Info: struct {
			ID  string `json:"id"`
			CWD string `json:"cwd"`
		}{ID: id, CWD: "/tmp/x"},
		GeneratedTitle: "chat only",
		CreatedAt:      "2026-01-01T00:00:00Z",
		UpdatedAt:      "2026-01-01T00:00:01Z",
		CurrentModelID: "grok-4.5",
	}
	b, _ := json.Marshal(sum)
	os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o644)
	chat := `{"type":"system","content":"sys"}
{"type":"user","content":[{"type":"text","text":"<user_query>\nhi from chat\n</user_query>"}]}
{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"think"}],"status":"completed"}
{"type":"assistant","content":"hello back","model_id":"grok-4.5"}
`
	os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(chat), 0o644)

	r := New(root)
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatalf("GetRenderEvents: %v", err)
	}
	var user, text string
	for _, e := range events {
		if e.Type == "UserPrompt" {
			user = e.Text
		}
		if e.Type == "TextChunk" {
			text = e.Text
		}
	}
	if user != "hi from chat" {
		t.Errorf("user=%q", user)
	}
	if text != "hello back" {
		t.Errorf("text=%q", text)
	}
}

func TestLiveRevision(t *testing.T) {
	root := t.TempDir()
	id := "dddddddd-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "proj", id, summaryFile{}, sampleUpdatesClosed(), "")
	r := New(root)
	rev1, err := r.LiveRevision(id)
	if err != nil {
		t.Fatalf("LiveRevision: %v", err)
	}
	// Grow updates
	loc, _ := r.findSession(id)
	f, _ := os.OpenFile(filepath.Join(loc.Dir, "updates.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("\n")
	f.Close()
	// Touch mtime
	now := time.Now().Add(time.Second)
	os.Chtimes(filepath.Join(loc.Dir, "updates.jsonl"), now, now)
	rev2, err := r.LiveRevision(id)
	if err != nil {
		t.Fatalf("LiveRevision2: %v", err)
	}
	if rev2 <= rev1 {
		t.Errorf("revision should increase: %d -> %d", rev1, rev2)
	}

	// A background recap only rewrites summary.json; it does not change the
	// terminal transcript and must not look like live output.
	summaryPath := filepath.Join(loc.Dir, "summary.json")
	recapTime := now.Add(time.Hour)
	if err := os.Chtimes(summaryPath, recapTime, recapTime); err != nil {
		t.Fatalf("chtimes summary: %v", err)
	}
	rev3, err := r.LiveRevision(id)
	if err != nil {
		t.Fatalf("LiveRevision3: %v", err)
	}
	if rev3 != rev2 {
		t.Fatalf("summary-only recap advanced live revision: %d -> %d", rev2, rev3)
	}
}

func TestRejectPathTraversal(t *testing.T) {
	root := t.TempDir()
	r := New(root)
	if _, err := r.GetSession("../evil"); err == nil {
		t.Fatal("expected error for path traversal")
	}
	if _, err := r.GetRenderEvents("a/b"); err == nil {
		t.Fatal("expected error for slash id")
	}
	if err := r.DeleteSession(".."); err == nil {
		t.Fatal("expected delete error")
	}
}

func TestWatchRoots(t *testing.T) {
	r := New("/tmp/fake-sessions")
	roots := r.WatchRoots()
	if len(roots) != 1 || roots[0] != "/tmp/fake-sessions" {
		t.Errorf("WatchRoots=%v", roots)
	}
}

func hasInProgress(events []model.RenderEvent) bool {
	for _, e := range events {
		if e.Type == "AgentSpecific" && e.Subtype == "in_progress" {
			return true
		}
	}
	return false
}
