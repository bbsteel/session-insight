package worktreereview

import (
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func testRoot(t *testing.T) string {
	t.Helper()
	return "testdata"
}

func TestListSessions(t *testing.T) {
	r := New(testRoot(t))
	sessions, complete, err := r.ListSessionsDetailed()
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		// The v99 fixture directory has unsupported metadata: the inventory
		// must report incomplete rather than silently dropping it.
		t.Fatal("complete=true with an unsupported-version fixture present")
	}

	ids := map[string]model.Session{}
	for _, session := range sessions {
		ids[session.ID] = session
	}
	for _, want := range []string{fixturePassed, fixtureBlocked, fixtureError, fixtureRunning, fixtureInterrupted, fixtureCorrupt, fixtureChild} {
		if _, ok := ids[want]; !ok {
			t.Errorf("session %q not listed", want)
		}
	}
	if _, ok := ids[fixtureV99]; ok {
		t.Error("unsupported-version fixture must be skipped, not listed")
	}

	passed := ids[fixturePassed]
	if passed.AgentType != AgentType {
		t.Errorf("agent_type=%q", passed.AgentType)
	}
	if passed.Name != "acme/session-insight · local-worktree" {
		t.Errorf("name=%q", passed.Name)
	}
	if passed.Repository != "acme/session-insight" {
		t.Errorf("repository=%q", passed.Repository)
	}
	if passed.IsLive {
		t.Error("completed attempt must not be live")
	}
	if ids[fixtureRunning].IsLive != true {
		t.Error("running fixture (fresh heartbeat, no terminal state) must be live")
	}
	if ids[fixtureInterrupted].IsLive {
		t.Error("interrupted fixture (stale heartbeat) must not be live")
	}
}

func TestListSessionsMissingRoot(t *testing.T) {
	r := New(filepath_nonexistent(t))
	sessions, complete, err := r.ListSessionsDetailed()
	if err != nil {
		t.Fatal(err)
	}
	if !complete || len(sessions) != 0 {
		t.Fatalf("missing root: complete=%v sessions=%d", complete, len(sessions))
	}
}

func filepath_nonexistent(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/no-such-journal-root"
}

func TestGetSessionPassed(t *testing.T) {
	r := New(testRoot(t))
	detail, err := r.GetSession(fixturePassed)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != fixturePassed {
		t.Fatalf("id=%q", detail.ID)
	}
	if len(detail.Turns) == 0 {
		t.Fatal("no turns")
	}
	if detail.Turns[0].UserMessage == "" {
		t.Fatal("request turn has no user message")
	}
	// Every pipeline stage got its own turn after the request turn.
	if len(detail.Turns) != 10 {
		t.Fatalf("turns=%d want 10 (request + 9 stages)", len(detail.Turns))
	}
	// Gate text lands as the final assistant message.
	last := detail.Turns[len(detail.Turns)-1]
	if !strings.Contains(last.AssistantMessage, "Passed") {
		t.Fatalf("final assistant message=%q", last.AssistantMessage)
	}
	// No provider calls in the passed fixture: no billing block.
	if detail.Billing != nil {
		t.Fatalf("billing=%+v want nil", detail.Billing)
	}
	if detail.Provenance == nil {
		t.Fatal("provenance missing")
	}
	if detail.Provenance.State != "complete" {
		t.Fatalf("provenance state=%q", detail.Provenance.State)
	}
}

func TestGetSessionBlocked(t *testing.T) {
	r := New(testRoot(t))
	detail, err := r.GetSession(fixtureBlocked)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ModelName != "gpt-5.6" || detail.ModelProvider != "openai" {
		t.Fatalf("model=%q provider=%q", detail.ModelName, detail.ModelProvider)
	}
	if detail.Billing == nil {
		t.Fatal("billing missing despite recorded provider call")
	}
	if detail.Billing.Totals.PromptTokens != 12400 || detail.Billing.Totals.CompletionTokens != 1600 {
		t.Fatalf("totals=%+v", detail.Billing.Totals)
	}
	if detail.Billing.Precision != model.PrecisionExact {
		t.Fatalf("precision=%q", detail.Billing.Precision)
	}
	if detail.Billing.BillingAmount != 0.16 {
		t.Fatalf("amount=%v", detail.Billing.BillingAmount)
	}
	foundFinding := false
	for _, turn := range detail.Turns {
		for _, tool := range turn.ToolDetails {
			if tool.Name == "finding:major" {
				foundFinding = true
			}
		}
	}
	if !foundFinding {
		t.Fatal("verified finding missing from tool details")
	}
}

func TestGetSessionErrorAttempt(t *testing.T) {
	r := New(testRoot(t))
	detail, err := r.GetSession(fixtureError)
	if err != nil {
		t.Fatal(err)
	}
	// The failure is visible as a failed stage tool call with safe detail.
	var failed *model.ToolCallVM
	for ti := range detail.Turns {
		for di := range detail.Turns[ti].ToolDetails {
			tool := &detail.Turns[ti].ToolDetails[di]
			if tool.Name == "stage:construct-merge" && tool.ExitCode == 1 {
				failed = tool
			}
		}
	}
	if failed == nil {
		t.Fatal("failed construct-merge stage not surfaced")
	}
	if failed.ErrorKind != "merge_conflict" {
		t.Fatalf("error kind=%q", failed.ErrorKind)
	}
	if !strings.Contains(failed.ErrorMessage, "Merge candidate was not constructed") {
		t.Fatalf("error message=%q", failed.ErrorMessage)
	}
}

func TestGetSessionUnknown(t *testing.T) {
	r := New(testRoot(t))
	_, err := r.GetSession("__no_such_attempt__")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error not actionable: %v", err)
	}
}

func TestGetSessionCorruptJournalDegrades(t *testing.T) {
	r := New(testRoot(t))
	detail, err := r.GetSession(fixtureCorrupt)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Provenance == nil {
		t.Fatal("provenance missing")
	}
	if len(detail.Provenance.Warnings) != 2 {
		t.Fatalf("warnings=%v", detail.Provenance.Warnings)
	}
	// Two valid events survive: replay continues from what is persisted.
	if detail.MessageCount != 2 {
		t.Fatalf("message count=%d want 2", detail.MessageCount)
	}
}

func TestRenderEventsBlocked(t *testing.T) {
	r := New(testRoot(t))
	events, err := r.GetRenderEvents(fixtureBlocked)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 28 {
		t.Fatalf("events=%d want 28", len(events))
	}
	if events[0].Type != "UserPrompt" {
		t.Fatalf("first event type=%q", events[0].Type)
	}

	// provider call pairs by ToolCallID and carries tokens.
	var invocation, result *model.RenderEvent
	for i := range events {
		switch events[i].Type {
		case "ToolInvocation":
			if events[i].ToolName == "provider:openai" {
				invocation = &events[i]
			}
		case "ToolResult":
			if events[i].ToolCallID == "provider-call-1" {
				result = &events[i]
			}
		}
	}
	if invocation == nil || result == nil {
		t.Fatal("provider call invocation/result pair missing")
	}
	if invocation.ToolCallID != result.ToolCallID {
		t.Fatalf("pair mismatch %q vs %q", invocation.ToolCallID, result.ToolCallID)
	}
	if result.TokenUsage == nil || result.TokenUsage.InputTokens != 12400 {
		t.Fatalf("token usage=%+v", result.TokenUsage)
	}

	// The gate decision is assistant text; completion is the terminal marker.
	var gateText, completed bool
	for i := range events {
		if events[i].Type == "TextChunk" && strings.Contains(events[i].Text, "Gate: Blocked") {
			gateText = true
		}
		if events[i].Subtype == "attempt_completed" {
			completed = true
		}
	}
	if !gateText || !completed {
		t.Fatalf("gateText=%v completed=%v", gateText, completed)
	}
}

func TestRenderANSI(t *testing.T) {
	r := New(testRoot(t))
	out, err := r.RenderANSI(fixtureBlocked, 80)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Gate: Blocked") {
		t.Fatalf("ANSI output missing gate text: %q", out[:min(len(out), 400)])
	}
}

func TestLiveRevisionStatOnly(t *testing.T) {
	r := New(testRoot(t))
	rev1, err := r.LiveRevision(fixtureRunning)
	if err != nil {
		t.Fatal(err)
	}
	rev2, err := r.LiveRevision(fixtureRunning)
	if err != nil {
		t.Fatal(err)
	}
	if rev1 != rev2 {
		t.Fatalf("unstable revision %d then %d", rev1, rev2)
	}
}

func TestSessionLive(t *testing.T) {
	r := New(testRoot(t))
	live, err := r.SessionLive(fixtureRunning)
	if err != nil || !live {
		t.Fatalf("running fixture live=%v err=%v", live, err)
	}
	live, err = r.SessionLive(fixtureInterrupted)
	if err != nil || live {
		t.Fatalf("interrupted fixture live=%v err=%v", live, err)
	}
	live, err = r.SessionLive(fixturePassed)
	if err != nil || live {
		t.Fatalf("completed fixture live=%v err=%v", live, err)
	}
}

func TestDeterministicClockOverride(t *testing.T) {
	r := New(testRoot(t))
	r.now = func() time.Time { return time.Date(2026, 9, 8, 9, 0, 3, 0, time.UTC) }
	live, err := r.SessionLive(fixtureInterrupted)
	if err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Fatal("interrupted fixture is live at t+3s after its last heartbeat")
	}
}
