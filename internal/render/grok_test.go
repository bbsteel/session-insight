package render

import (
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func grokEvents() []model.RenderEvent {
	ts := time.Now()
	return []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			Metadata: map[string]any{"agent_label": "Grok"}},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "grok", Text: "hello"},
		{EventID: "th0", Type: "ThinkingStart", TurnIndex: 0, Timestamp: ts, AgentType: "grok", Text: "Thinking"},
		{EventID: "th1", Type: "ThinkingChunk", TurnIndex: 0, Timestamp: ts, AgentType: "grok", Text: "thinking..."},
		{EventID: "th2", Type: "ThinkingEnd", TurnIndex: 0, Timestamp: ts, AgentType: "grok"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "grok", Text: "done"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			ToolName: "Run", ToolCallID: "c1", ToolInput: map[string]any{"command": "ls", "reason": "list files"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			ToolCallID: "c1", Stdout: "a.txt\nb.txt"},
		{EventID: "i1", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			ToolName: "Skill", ToolCallID: "c2", ToolInput: map[string]any{"skill": "foo", "reason": "use skill"}},
		{EventID: "r1", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			ToolCallID: "c2", Stdout: "skill output"},
		{EventID: "i2", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			ToolName: "search_replace", ToolCallID: "c3", ToolInput: map[string]any{"file_path": "a.go", "old_string": "foo", "new_string": "bar"}},
		{EventID: "r2", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "grok",
			ToolCallID: "c3"},
	}
}

func TestGrokStyleResolved(t *testing.T) {
	p := profileFor(grokEvents())
	if p.Name != "grok" {
		t.Fatalf("expected grok profile, got %q", p.Name)
	}
	if p.Bullet == nil {
		t.Errorf("grok profile must have a Bullet strategy")
	}
	if p.ToolBox == nil {
		t.Errorf("grok profile must have a ToolBox strategy")
	}
	if p.Thought == nil {
		t.Errorf("grok profile must have a Thought strategy")
	}
	if p.ColorRule == nil {
		t.Errorf("grok profile must have a ColorRule")
	}
	if p.Preprocess == nil {
		t.Errorf("grok profile must have a Preprocess hook")
	}
}

func TestGrokProfileLayout(t *testing.T) {
	ansi, positions := FormatEventsWithPositions(grokEvents(), 100)
	plain := stripANSIForTest(ansi)

	for _, want := range []string{
		"◆ Thought",       // compact thought header (duration may be 0.0s)
		"◆ Run",           // run tool bullet
		"◆ Skill",         // skill tool bullet
		"◆ SearchReplace", // edit tool bullet
		"╭", "╯",          // rounded boxes
		"Completed", // result box footer
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("grok layout missing %q\n%s", want, plain)
		}
	}

	// Legacy shapes must be absent for grok.
	for _, absent := range []string{"╔", "Tool: Run"} {
		if strings.Contains(plain, absent) {
			t.Errorf("grok layout unexpectedly contains legacy shape %q", absent)
		}
	}

	// Run success uses ColSuccess (ANSI slot 2).
	if !hasFgColor(ansi, ColSuccess) {
		t.Errorf("expected Run success color in output")
	}
	// Skill uses ColSkill (ANSI slot 5).
	if !hasFgColor(ansi, ColSkill) {
		t.Errorf("expected Skill color in output")
	}

	// A thought fold position must be emitted.
	var thoughtFold bool
	for _, pos := range positions {
		if pos.Kind == "fold" && pos.Payload["level"] == "thought" {
			thoughtFold = true
			break
		}
	}
	if !thoughtFold {
		t.Errorf("expected a thought fold position, got %+v", positions)
	}
}

func TestGrokRunFailedUsesErrorColor(t *testing.T) {
	evts := grokEvents()
	for i := range evts {
		if evts[i].Type == "ToolResult" && evts[i].ToolCallID == "c1" {
			evts[i].ExitCode = 1
			evts[i].Stderr = "boom"
		}
	}
	ansi := FormatEvents(evts, 100)
	if !hasFgColor(ansi, ColErrorBright) {
		t.Errorf("expected error bright color for failed Run")
	}
}
