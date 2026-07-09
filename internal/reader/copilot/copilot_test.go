package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Sanitized replica of a real session.shutdown payload. The usage.* blocks
// deliberately carry inclusive input semantics (input includes cache reads)
// while tokenDetails.* carries exclusive semantics — the parser must read
// tokenDetails, so the totals below only pass when the right source is used.
const shutdownEvent = `{"type":"session.shutdown","timestamp":"2026-01-01T00:00:02Z","data":{"shutdownType":"routine","totalPremiumRequests":3,"totalNanoAiu":1500000000,"tokenDetails":{"input":{"tokenCount":1000},"cache_read":{"tokenCount":9000},"output":{"tokenCount":500},"cache_write":{"tokenCount":200}},"modelMetrics":{"gpt-x":{"requests":{"count":10,"cost":3},"totalNanoAiu":1000000000,"usage":{"inputTokens":9600,"outputTokens":400,"cacheReadTokens":9000,"cacheWriteTokens":200,"reasoningTokens":50},"tokenDetails":{"input":{"tokenCount":600},"cache_read":{"tokenCount":9000},"output":{"tokenCount":400}}},"gpt-x-mini":{"requests":{"count":2,"cost":0},"totalNanoAiu":500000000,"usage":{"reasoningTokens":5},"tokenDetails":{"input":{"tokenCount":400},"cache_read":{"tokenCount":0},"output":{"tokenCount":100}}}}}}`

func writeBillingSession(t *testing.T, dir, id string, events []string) {
	t.Helper()
	sessionDir := filepath.Join(dir, id)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	wsYAML := "id: " + id + "\ncreated_at: 2026-06-12T10:00:00Z\nupdated_at: 2026-06-12T11:00:00Z\n"
	os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(wsYAML), 0644)
	os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(strings.Join(events, "\n")), 0644)
}

func TestGetSessionBillingFromShutdown(t *testing.T) {
	dir := t.TempDir()
	writeBillingSession(t, dir, "sess-bill", []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:00:01Z","data":{"content":"hello","outputTokens":42}}`,
		shutdownEvent,
	})

	detail, err := New(dir).GetSession("sess-bill")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	b := detail.Billing
	if b == nil {
		t.Fatal("expected billing from session.shutdown")
	}
	if b.Precision != model.PrecisionExact || b.BillingUnit != "aiu" {
		t.Errorf("precision/unit mismatch: %+v", b)
	}
	if b.BillingAmount != 1.5 {
		t.Errorf("expected 1.5 AIU (1.5e9 nano), got %v", b.BillingAmount)
	}
	// prompt=1000 proves tokenDetails (exclusive) was used; the inclusive
	// usage numbers would have produced 9600+.
	tot := b.Totals
	if tot.PromptTokens != 1000 || tot.CacheReadTokens != 9000 || tot.CompletionTokens != 500 || tot.CacheWriteTokens != 200 {
		t.Errorf("totals mismatch (wrong semantics source?): %+v", tot)
	}
	if tot.PremiumRequests != 3 {
		t.Errorf("expected premium=3 from totalPremiumRequests, got %d", tot.PremiumRequests)
	}
	if tot.ReasoningTokens != 55 || tot.Present.Reasoning != model.PresenceExact {
		t.Errorf("expected reasoning=55 rolled up from models, got %d (%s)", tot.ReasoningTokens, tot.Present.Reasoning)
	}

	if len(b.ByModel) != 2 || b.ByModel[0].Model != "gpt-x" {
		t.Fatalf("expected 2 models sorted by AIU desc, got %+v", b.ByModel)
	}
	top := b.ByModel[0]
	if top.Requests != 10 || top.BillingAmount != 1.0 {
		t.Errorf("gpt-x requests/amount mismatch: %+v", top)
	}
	if top.Usage.PromptTokens != 600 {
		t.Errorf("per-model prompt must come from tokenDetails (600), got %d", top.Usage.PromptTokens)
	}
	// cache_write is absent from gpt-x tokenDetails and must fall back to
	// usage.cacheWriteTokens.
	if top.Usage.CacheWriteTokens != 200 || top.Usage.Present.CacheWrite != model.PresenceExact {
		t.Errorf("cache_write fallback failed: %+v", top.Usage)
	}
	if top.Usage.ReasoningTokens != 50 {
		t.Errorf("expected reasoning=50, got %d", top.Usage.ReasoningTokens)
	}

	// Turn-level: only output is known for Copilot.
	u := detail.Turns[0].TokenUsage
	if u.CompletionTokens != 42 || u.Present.Output != model.PresenceExact {
		t.Errorf("turn output mismatch: %+v", u)
	}
	if u.Present.Input != model.PresenceMissing {
		t.Errorf("turn input presence should be missing, got %s", u.Present.Input)
	}
}

func TestGetSessionBillingMissingShutdown(t *testing.T) {
	dir := t.TempDir()
	writeBillingSession(t, dir, "sess-killed", []string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-01T00:00:01Z","data":{"content":"hello","outputTokens":42}}`,
	})

	detail, err := New(dir).GetSession("sess-killed")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionMissing {
		t.Errorf("killed session must report a missing bill, got %+v", detail.Billing)
	}
	if !detail.AnomalySummary.MissingShutdown {
		t.Error("expected MissingShutdown anomaly")
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()

	sessionDir := filepath.Join(dir, "test-session-id")
	os.MkdirAll(sessionDir, 0755)

	wsYAML := `id: test-session-id
cwd: /home/test/project
repository: owner/repo
branch: main
name: My Test Session
user_named: true
created_at: 2026-06-12T10:00:00Z
updated_at: 2026-06-12T11:00:00Z
`
	os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(wsYAML), 0644)
	os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`), 0644)

	reader := New(dir)
	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s.ID != "test-session-id" {
		t.Errorf("expected ID 'test-session-id', got '%s'", s.ID)
	}
	if s.Name != "My Test Session" {
		t.Errorf("expected Name 'My Test Session', got '%s'", s.Name)
	}
	if s.Repository != "owner/repo" {
		t.Errorf("expected Repository 'owner/repo', got '%s'", s.Repository)
	}
}

func TestListSessionsUserNotNamed(t *testing.T) {
	dir := t.TempDir()

	sessionDir := filepath.Join(dir, "auto-session")
	os.MkdirAll(sessionDir, 0755)

	wsYAML := `id: auto-session
name: "# Instructions (read first)\n\nThis is a long system prompt..."
user_named: false
created_at: 2026-06-12T10:00:00Z
updated_at: 2026-06-12T11:00:00Z
`
	os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(wsYAML), 0644)
	os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`), 0644)

	reader := New(dir)
	sessions, _ := reader.ListSessions()

	if len(sessions) == 0 {
		t.Fatal("expected 1 session")
	}
	if sessions[0].Name != "" {
		t.Errorf("expected empty Name for user_named=false, got '%s'", sessions[0].Name)
	}
}

func TestListSessionsSkipsInvalid(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, "empty-session"), 0755)

	badDir := filepath.Join(dir, "bad-session")
	os.MkdirAll(badDir, 0755)
	os.WriteFile(filepath.Join(badDir, "workspace.yaml"), []byte("not: valid: yaml: ["), 0644)

	reader := New(dir)
	sessions, err := reader.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestParseCopilotRenderEventsUsesCurrentEventSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lines := []string{
		`{"type":"session.model_change","timestamp":"2026-06-20T01:00:00.000Z","data":{"newModel":"gpt-5.4"}}`,
		`{"type":"user.message","timestamp":"2026-06-20T01:00:01.000Z","data":{"content":"hello"}}`,
		`{"type":"assistant.message","timestamp":"2026-06-20T01:00:02.000Z","data":{"encryptedContent":"ciphertext-must-not-render"}}`,
		`{"type":"tool.execution_start","timestamp":"2026-06-20T01:00:03.000Z","data":{"toolCallId":"call-1","toolName":"view","arguments":{"path":"/tmp/file"}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-06-20T01:00:04.000Z","data":{"toolCallId":"call-1","success":true,"result":{"content":"file contents"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-06-20T01:00:05.000Z","data":{"toolCallId":"call-2","toolName":"shell","arguments":{"command":"false"}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-06-20T01:00:06.000Z","data":{"toolCallId":"call-2","success":false,"error":{"message":"Permission denied","code":"denied"}}}`,
		`{"type":"user.message","timestamp":"2026-06-20T01:00:07.000Z","data":{"content":"   "}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := parseCopilotRenderEvents(path)
	if err != nil {
		t.Fatalf("parseCopilotRenderEvents() failed: %v", err)
	}

	var boundaries, invocations, results int
	for _, event := range events {
		if event.TurnIndex < 0 {
			t.Errorf("event %q has negative turn index %d", event.Type, event.TurnIndex)
		}
		if event.Text == "ciphertext-must-not-render" {
			t.Error("encrypted assistant content must not be rendered as plaintext")
		}
		switch event.Type {
		case "TurnBoundary":
			boundaries++
		case "ToolInvocation":
			invocations++
			if event.ToolCallID == "call-1" && event.ToolInput["path"] != "/tmp/file" {
				t.Errorf("tool arguments not preserved: %#v", event.ToolInput)
			}
		case "ToolResult":
			results++
			switch event.ToolCallID {
			case "call-1":
				if event.Stdout != "file contents" || event.ExitCode != 0 || event.ParentEventID == "" {
					t.Errorf("unexpected successful result: %#v", event)
				}
			case "call-2":
				if event.Stderr != "Permission denied" || event.ExitCode == 0 || event.ParentEventID == "" {
					t.Errorf("unexpected failed result: %#v", event)
				}
			}
		}
	}
	if boundaries != 1 || invocations != 2 || results != 2 {
		t.Fatalf("expected 1 boundary, 2 invocations, and 2 results; got %d, %d, and %d", boundaries, invocations, results)
	}
}

func TestRenderANSIRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "events.jsonl"), []byte(`{"type":"user.message","data":{"content":"secret"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New(sessionDir).RenderANSI("../outside", 0); err == nil {
		t.Fatal("RenderANSI accepted a path-traversal session ID")
	}
	if _, err := New(sessionDir).GetSession("../outside"); err == nil {
		t.Fatal("GetSession accepted a path-traversal session ID")
	}
}
