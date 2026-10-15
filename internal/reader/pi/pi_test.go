package pi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestParseBasicSession(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	detail, err := New(root).GetSession(basicID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "demo session" {
		t.Fatalf("name=%q", detail.Name)
	}
	if detail.CWD != "/tmp/demo" {
		t.Fatalf("cwd=%q", detail.CWD)
	}
	if detail.ModelName != "claude-sonnet-4" || detail.ModelProvider != "anthropic" {
		t.Fatalf("model=%s provider=%s", detail.ModelName, detail.ModelProvider)
	}
	if detail.ResumeID != basicID {
		t.Fatalf("resume=%q", detail.ResumeID)
	}
	if detail.TurnCount != 1 || len(detail.Turns) != 1 {
		t.Fatalf("turns=%d count=%d", len(detail.Turns), detail.TurnCount)
	}
	if detail.Turns[0].UserMessage != "hello conformance" {
		t.Fatalf("user=%q", detail.Turns[0].UserMessage)
	}
	if detail.Turns[0].AssistantMessage != "hi there" {
		t.Fatalf("assistant=%q", detail.Turns[0].AssistantMessage)
	}
}

func TestParseRichTokensAndTools(t *testing.T) {
	id := "cccccccc-dddd-eeee-ffff-000000000001"
	root := installTestdata(t, id, "rich.jsonl")
	r := New(root)
	detail, err := r.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionExact {
		t.Fatalf("billing=%+v", detail.Billing)
	}
	if detail.Billing.Totals.PromptTokens != 83 || detail.Billing.Totals.CompletionTokens != 28 {
		t.Fatalf("tokens prompt=%d completion=%d", detail.Billing.Totals.PromptTokens, detail.Billing.Totals.CompletionTokens)
	}
	if detail.Billing.Totals.ReasoningTokens != 5 {
		t.Fatalf("reasoning=%d", detail.Billing.Totals.ReasoningTokens)
	}
	events, err := r.GetRenderEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	var edits []model.EditCall
	var results int
	for _, e := range events {
		if e.Type == "ToolInvocation" {
			edits = append(edits, model.ExtractEditCalls(e)...)
		}
		if e.Type == "ToolResult" {
			results++
			if e.ParentEventID == "" {
				t.Fatalf("result %s missing parent", e.ToolCallID)
			}
		}
	}
	if results < 3 {
		t.Fatalf("results=%d", results)
	}
	if len(edits) != 1 || edits[0].FilePath != "/tmp/demo/a.go" || edits[0].OldString != "foo" {
		t.Fatalf("edits=%+v", edits)
	}
}

func TestInterruptedTurnStaysOpenWithinLiveWindow(t *testing.T) {
	id := "dddddddd-eeee-ffff-0000-111111111111"
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "interrupted.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeSessionFile(t, root, id, string(body))
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	events, err := New(root).GetRenderEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	if !sharedHasInProgress(events) {
		t.Fatalf("expected trailing in_progress, last=%+v", events[len(events)-1])
	}
}

func sharedHasInProgress(events []model.RenderEvent) bool {
	if len(events) == 0 {
		return false
	}
	last := events[len(events)-1]
	return last.Type == "AgentSpecific" && last.Subtype == "in_progress"
}

func TestResolveSessionsDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	got, ok := ResolveSessionsDir()
	if !ok || got != dir {
		t.Fatalf("got=%q ok=%v", got, ok)
	}
}

func TestSessionIDFromFilename(t *testing.T) {
	got := sessionIDFromFilename("2026-01-01T00-00-00-000Z_aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	if got != basicID {
		t.Fatalf("got=%q", got)
	}
}
