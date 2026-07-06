package render

import (
	"strings"
	"testing"
	"time"

	"session-insight/internal/model"
)

func chrysEvents() []model.RenderEvent {
	ts := time.Now()
	return []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			Metadata: map[string]any{"agent_label": "Code Agent"}},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "改一下布局"},
		{EventID: "t0", Type: "ThinkingStart", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "先看代码"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "我先探索一下。"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "bash", ToolCallID: "c1",
			ToolInput: map[string]any{"command": "ls", "reason": "看看目录"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolCallID: "c1", Stdout: "a.txt\nb.txt"},
		{EventID: "i1", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "grep", ToolCallID: "c2", ToolInput: map[string]any{"pattern": "x"}},
		{EventID: "r1", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolCallID: "c2", Stderr: "boom", ExitCode: 1},
		{EventID: "x1", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "搞定了。"},
	}
}

func TestChrysProfileLayout(t *testing.T) {
	out := FormatEvents(chrysEvents(), 100)

	for _, want := range []string{
		"❯ You",              // user header instead of "> "
		"◇ Code Agent",       // assistant header from agent_label metadata
		"▼ Tools (1/2)",      // group header: 2 invocations, 1 succeeded
		"• bash",             // tool bullet line
		"╭── 看看目录 ",      // rounded box with promoted reason header
		"╭── Output ",        // result box header
		" Completed ──╯",     // success footer, right-aligned
		" Failed ──╯",        // failure footer
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chrys layout missing %q\n%s", want, out)
		}
	}

	// The promoted reason must not repeat inside the box body.
	if strings.Contains(out, "reason:") {
		t.Errorf("promoted reason leaked into box body")
	}
	// Legacy shapes must be gone for chrys.
	for _, absent := range []string{"╔", "║", "Tool: bash", "> 改一下布局"} {
		if strings.Contains(out, absent) {
			t.Errorf("chrys layout still contains legacy shape %q", absent)
		}
	}
}

func TestDefaultProfileUnchangedShapes(t *testing.T) {
	events := chrysEvents()
	for i := range events {
		events[i].AgentType = "claude" // any agent without a profile
	}
	out := FormatEvents(events, 100)

	for _, want := range []string{"╔══ Tool: bash ", "║", "> ", "reason: 看看目录"} {
		if !strings.Contains(out, want) {
			t.Errorf("default layout missing %q", want)
		}
	}
	for _, absent := range []string{"❯ You", "◇ ", "▼ Tools", "• bash", "╭", "Completed"} {
		if strings.Contains(out, absent) {
			t.Errorf("default layout unexpectedly contains %q", absent)
		}
	}
}

func TestChrysEmptyResultCollapses(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "sleep", ToolCallID: "c1", ToolInput: map[string]any{"seconds": 1.0}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c1"},
	}
	out := FormatEvents(events, 100)
	if !strings.Contains(out, "✓ Completed") {
		t.Errorf("empty result should collapse to a status line\n%s", out)
	}
	if strings.Contains(out, "╭── Output") {
		t.Errorf("empty result should not draw an Output box")
	}
}

func TestFoldAndTruncPositions(t *testing.T) {
	ts := time.Now()
	long := strings.Repeat("line\n", 30) // 31 lines → truncated
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "x"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c1", Stdout: long},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "done"},
	}
	ansi, positions := FormatEventsWithPositions(events, 100)
	lines := strings.Split(ansi, "\n")

	var fold, trunc *RenderPosition
	for i := range positions {
		switch positions[i].Kind {
		case "fold":
			fold = &positions[i]
		case "trunc":
			trunc = &positions[i]
		}
	}
	if fold == nil || trunc == nil {
		t.Fatalf("missing fold/trunc positions: fold=%v trunc=%v", fold, trunc)
	}

	// No soft wraps in this fixture (cols=100, short rows), so display rows
	// equal logical lines and both can be verified against the split ANSI.
	if !strings.Contains(lines[fold.LineStart], "▼ Tools (1/1)") {
		t.Errorf("fold header line mismatch: %q", lines[fold.LineStart])
	}
	pl := fold.Payload
	ls, le := int(pl["logical_start"].(float64)), int(pl["logical_end"].(float64))
	if ls != fold.LineStart+1 {
		t.Errorf("fold body should start right after header: %d vs %d", ls, fold.LineStart)
	}
	// Everything in the body must be tool content; the line after the body
	// is the "done" text block (with its ◇ header).
	if !strings.Contains(lines[le], "◇") {
		t.Errorf("line after fold body should be the assistant header, got %q", lines[le])
	}
	if trunc.LineStart <= fold.LineStart || trunc.LineStart >= le {
		t.Errorf("trunc line %d should sit inside fold body (%d, %d)", trunc.LineStart, fold.LineStart, le)
	}
	if !strings.Contains(lines[trunc.LineStart], "行被截断") {
		t.Errorf("trunc line mismatch: %q", lines[trunc.LineStart])
	}
	if idx := int(trunc.Payload["output_index"].(float64)); idx != 0 {
		t.Errorf("output_index = %d", idx)
	}

	outputs := CollectTruncatedOutputs(events)
	if len(outputs) != 1 || outputs[0].ToolName != "bash" || outputs[0].Kind != "stdout" {
		t.Errorf("collected outputs = %+v", outputs)
	}
}

func TestTruncPositionsFlatLayoutOrder(t *testing.T) {
	ts := time.Now()
	long := strings.Repeat("l\n", 30)
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "claude"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "claude", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "claude",
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "x"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "claude",
			ToolCallID: "c1", Stdout: long, Stderr: long, ExitCode: 1},
	}
	_, positions := FormatEventsWithPositions(events, 100)
	var truncs []RenderPosition
	for _, p := range positions {
		if p.Kind == "trunc" {
			truncs = append(truncs, p)
		}
	}
	// stdout + stderr each truncated → two entries, indexes 0 and 1, and no
	// fold positions for agents without grouping.
	if len(truncs) != 2 {
		t.Fatalf("want 2 trunc positions, got %d", len(truncs))
	}
	for i, tr := range truncs {
		if int(tr.Payload["output_index"].(float64)) != i {
			t.Errorf("trunc %d has output_index %v", i, tr.Payload["output_index"])
		}
	}
	outputs := CollectTruncatedOutputs(events)
	if len(outputs) != 2 || outputs[0].Kind != "stdout" || outputs[1].Kind != "stderr" {
		t.Errorf("outputs order mismatch: %+v", outputs)
	}
	for _, p := range positions {
		if p.Kind == "fold" {
			t.Errorf("default profile must not emit fold positions")
		}
	}
}

func TestChrysGroupHeaderCoversSubagentRun(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "explore_agent", ToolCallID: "c1", ToolInput: map[string]any{"prompt": "找组件"}},
		{EventID: "s0", Type: "AgentSpecific", Subtype: "subagent_started", TurnIndex: 0, Depth: 1, AgentType: "chrys", Text: "Explore Agent"},
		{EventID: "n0", Type: "ToolInvocation", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys",
			ToolName: "glob", ToolCallID: "c2", ToolInput: map[string]any{"pattern": "*"}},
		{EventID: "n1", Type: "ToolResult", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys", ToolCallID: "c2", Stdout: "f.vue"},
		{EventID: "s1", Type: "AgentSpecific", Subtype: "subagent_summary", TurnIndex: 0, Depth: 1, AgentType: "chrys", Text: "Tool calls: 1"},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c1", Stdout: "组件在 f.vue"},
	}
	out := FormatEvents(events, 100)

	// One group header for the whole run (nested events don't break or
	// double-count it), counting only the depth-0 invocation.
	if got := strings.Count(out, "▼ Tools"); got != 1 {
		t.Errorf("want exactly 1 group header, got %d\n%s", got, out)
	}
	if !strings.Contains(out, "▼ Tools (1/1)") {
		t.Errorf("group header should count depth-0 tools only")
	}
	if !strings.Contains(out, "◉ Explore Agent") {
		t.Errorf("subagent badge missing")
	}
	if !strings.Contains(out, "Tool calls: 1") {
		t.Errorf("subagent summary missing")
	}
}
