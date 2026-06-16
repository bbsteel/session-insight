package claude

import (
	"fmt"
	"os"
	"testing"

	"session-insight/internal/model"
)

func TestParseClaudeRenderEventsWithSubagents(t *testing.T) {
	mainPath := os.ExpandEnv("$HOME/.claude/projects/-home-deck--openclaw-workspace-projects-external-superpowers/70c07299-f3f8-472b-8a2d-2feeb58d979f.jsonl")
	if _, err := os.Stat(mainPath); err != nil {
		t.Skipf("sample file not present: %v", err)
	}

	events, modelName, err := ParseClaudeRenderEventsWithSubagents(mainPath)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Logf("model=%s total_events=%d", modelName, len(events))

	var (
		depth0, depth1     int
		agentInvocations   int
		spliceCount        int
		lastWasSubagentEnd = -1 // index of last depth-1 event before a depth-0 ToolResult, for ordering check
	)

	idToEvent := make(map[string]model.RenderEvent)
	for _, e := range events {
		idToEvent[e.EventID] = e
	}

	for i, e := range events {
		switch e.Depth {
		case 0:
			depth0++
		case 1:
			depth1++
			lastWasSubagentEnd = i
		}
		if e.Type == "ToolInvocation" && e.ToolName == "Agent" {
			agentInvocations++
		}
		if e.Type == "ToolResult" && e.Payload != nil {
			if agentID, ok := e.Payload["agent_id"].(string); ok && agentID != "" {
				spliceCount++
				// Ordering check: the subagent's events should appear
				// immediately before this wrapping ToolResult, not after.
				if lastWasSubagentEnd != i-1 {
					t.Errorf("expected subagent transcript to be spliced immediately before ToolResult at index %d (agent_id=%s), but the preceding event (index %d) was not part of that subagent's depth-1 block", i, agentID, i-1)
				}
				// The ToolResult's ParentEventID should be the matching
				// Agent ToolInvocation's EventID, and that invocation must
				// actually exist in the merged stream.
				inv, ok := idToEvent[e.ParentEventID]
				if !ok {
					t.Errorf("ToolResult %s ParentEventID %q does not resolve to any event in the merged stream", e.EventID, e.ParentEventID)
				} else if inv.ToolName != "Agent" {
					t.Errorf("ToolResult %s ParentEventID resolves to a non-Agent ToolInvocation (tool_name=%s)", e.EventID, inv.ToolName)
				}
			}
		}
	}

	if depth1 == 0 {
		t.Error("expected at least some depth=1 (subagent) events after splicing, found none")
	}
	if spliceCount == 0 {
		t.Error("expected at least one Agent ToolResult with agent_id payload, found none")
	}
	if agentInvocations != 3 {
		// Known from manual inspection of this fixture: 3 "Agent" tool_use
		// calls in the main file.
		t.Errorf("expected 3 Agent ToolInvocation events (known fixture shape), got %d", agentInvocations)
	}

	t.Logf("depth0=%d depth1=%d agent_invocations=%d spliced=%d", depth0, depth1, agentInvocations, spliceCount)

	// Print a window around the first splice for eye-check.
	printed := 0
	for i, e := range events {
		if e.Depth == 1 || (e.Type == "ToolInvocation" && e.ToolName == "Agent") || (e.Type == "ToolResult" && e.Payload != nil) {
			text := e.Text
			if len(text) > 50 {
				text = text[:50] + "..."
			}
			fmt.Printf("[%3d] depth=%d type=%-16s tool=%-8s parent=%-30s text=%q\n", i, e.Depth, e.Type, e.ToolName, e.ParentEventID, text)
			printed++
			if printed >= 30 {
				break
			}
		}
	}
}
