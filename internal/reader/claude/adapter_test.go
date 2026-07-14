package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// These tests use optional, anonymized fixtures. They are skipped until the
// corresponding fixture is added, so clones never depend on a local session.

func claudeFixturePath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", name)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not present: %v", err)
	}
	return path
}

func TestParseClaudeRenderEvents_MainSessionWithBashEmbeds(t *testing.T) {
	path := claudeFixturePath(t, "main-session-with-bash-embeds.jsonl")

	events, modelName, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}
	t.Logf("model=%s total_events=%d", modelName, len(events))

	var bashInvocations, bashResults, turnBoundaries int
	seenIDs := make(map[string]bool)
	for _, e := range events {
		if e.EventID == "" {
			t.Errorf("event with empty EventID: type=%s", e.Type)
		}
		if seenIDs[e.EventID] {
			t.Errorf("duplicate EventID: %s", e.EventID)
		}
		seenIDs[e.EventID] = true

		if e.Depth != 0 {
			t.Errorf("main-file parse should have Depth=0 everywhere, got Depth=%d on event %s (%s)", e.Depth, e.EventID, e.Type)
		}

		switch {
		case e.Type == "ToolInvocation" && e.ToolName == "Bash" && e.ToolInput != nil:
			bashInvocations++
		case e.Type == "ToolResult" && e.Stdout != "" && e.ToolCallID != "":
			bashResults++
		case e.Type == "TurnBoundary":
			turnBoundaries++
		}
	}

	if bashInvocations == 0 {
		t.Error("expected at least one embedded <bash-input> ToolInvocation, found none")
	}
	t.Logf("turn_boundaries=%d bash_invocations=%d bash_results_with_stdout=%d", turnBoundaries, bashInvocations, bashResults)

	// Print first 25 events for eye-check.
	for i, e := range events {
		if i >= 25 {
			break
		}
		text := e.Text
		if len(text) > 60 {
			text = text[:60] + "..."
		}
		fmt.Printf("[%2d] depth=%d turn=%-3d type=%-16s tool=%-12s stream=%-28s text=%q\n",
			i, e.Depth, e.TurnIndex, e.Type, e.ToolName, e.StreamID, text)
	}
}

func TestParseClaudeRenderEvents_SubagentFileAsBaseDepth1(t *testing.T) {
	path := claudeFixturePath(t, "subagent-base-depth-1.jsonl")

	events, _, err := ParseClaudeRenderEvents(path, 1, "main-agent-tool-invocation-evt-id")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}

	for i, e := range events {
		if e.Depth != 1 {
			t.Errorf("subagent-file parse should have Depth=1 everywhere, got Depth=%d on event %s (%s)", e.Depth, e.EventID, e.Type)
		}
		if i == 0 && e.Type == "TurnBoundary" && e.ParentEventID != "main-agent-tool-invocation-evt-id" {
			t.Errorf("first TurnBoundary should carry the supplied parentEventID, got %q", e.ParentEventID)
		}
	}

	t.Logf("subagent total_events=%d", len(events))
	for i, e := range events {
		if i >= 15 {
			break
		}
		text := e.Text
		if len(text) > 60 {
			text = text[:60] + "..."
		}
		fmt.Printf("[%2d] depth=%d turn=%-3d type=%-16s tool=%-12s parent=%-20s text=%q\n",
			i, e.Depth, e.TurnIndex, e.Type, e.ToolName, e.ParentEventID, text)
	}
}

// TestTokenUsageAttachedOncePerAssistantMessage guards against the
// double-counting bug found in review: the draft attached the full
// message-level TokenUsage to every content block of an assistant message,
// which would multiply-count tokens when summed by the Token Analysis panel.
func TestTokenUsageAttachedOncePerAssistantMessage(t *testing.T) {
	path := claudeFixturePath(t, "token-usage-once-per-message.jsonl")

	events, _, err := ParseClaudeRenderEvents(path, 0, "")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Group consecutive non-tool-result, non-boundary events by their
	// originating assistant message is hard to do post-hoc without message
	// IDs, so instead just assert: across the whole file, the number of
	// events carrying non-nil TokenUsage should be roughly equal to the
	// number of distinct token totals — i.e. we should NOT see the exact
	// same (input,output,cache) tuple repeated 3+ times in a row, which is
	// what the double-attach bug produced.
	type usage = [4]int64
	var run usage
	repeat := 0
	maxRepeat := 0
	for _, e := range events {
		if e.TokenUsage == nil {
			run = usage{}
			repeat = 0
			continue
		}
		u := usage{e.TokenUsage.InputTokens, e.TokenUsage.OutputTokens, e.TokenUsage.CacheReadTokens, e.TokenUsage.CacheCreationTokens}
		if u == run {
			repeat++
			if repeat > maxRepeat {
				maxRepeat = repeat
			}
		} else {
			run = u
			repeat = 0
		}
	}
	if maxRepeat > 0 {
		t.Errorf("found %d consecutive RenderEvents with identical non-nil TokenUsage — double-attachment bug regressed", maxRepeat+1)
	}
}
