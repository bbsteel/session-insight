package render

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
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
	plain := stripANSIForTest(out)

	for _, want := range []string{
		"❯ You",                // user header instead of "> "
		"◇ Code Agent",         // assistant header from agent_label metadata
		"▼ Tools (1/2)",        // group header: 2 invocations, 1 succeeded
		"• bash",               // tool bullet line
		"╭── 看看目录 ",            // rounded box with promoted reason header
		"╭── Output ",          // result box header
		" Completed ──╯",       // success footer, right-aligned
		" Failed (exit 1) ──╯", // failure footer with exit code
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("chrys layout missing %q\n%s", want, plain)
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
		events[i].AgentType = "codex" // any agent without a profile
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
	// Everything in the body must be tool content. A blank separator line
	// is emitted before the ◇ header, so le is the blank and le+1 is the header.
	if lines[le] != "" {
		t.Errorf("line after fold body should be blank separator, got %q", lines[le])
	}
	if !strings.Contains(lines[le+1], "◇") {
		t.Errorf("line after blank separator should be the assistant header, got %q", lines[le+1])
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
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "codex"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "codex", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "codex",
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "x"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "codex",
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

func TestClaudeGroupHeaderStatsAndFolds(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "claude"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "claude", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "claude",
			ToolName: "Grep", ToolCallID: "c1", ToolInput: map[string]any{"pattern": "x"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "claude", ToolCallID: "c1", Stdout: "hit"},
		{EventID: "i1", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "claude",
			ToolName: "Bash", ToolCallID: "c2", ToolInput: map[string]any{"command": "ls"}},
		{EventID: "r1", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "claude", ToolCallID: "c2", Stdout: "ok"},
		{EventID: "i2", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "claude",
			ToolName: "Edit", ToolCallID: "c3", ToolInput: map[string]any{"file_path": "/tmp/a.go", "old_string": "a", "new_string": "b"}},
		{EventID: "r2", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "claude", ToolCallID: "c3"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "claude", Text: "done"},
	}
	ansi, positions := FormatEventsWithPositions(events, 120)

	if !strings.Contains(ansi, "▼ Tools (3/3) · 1 search · 1 edit · 1 shell") {
		t.Errorf("claude group header with stats missing:\n%s", ansi[:min(len(ansi), 600)])
	}
	// Default box charset must be untouched for claude.
	if !strings.Contains(ansi, "╔") || strings.Contains(ansi, "╭") {
		t.Errorf("claude must keep the default box charset")
	}
	// claude has no ToolBullet but ToolFoldHeader: each non-edit tool gets its
	// own fold whose header is the dedicated "▼ Tool: …" line above the box
	// (Edit renders a diff and stays unfolded). 1 group fold + 2 tool folds.
	var groupFolds, toolFolds int
	groupKey := ""
	for _, p := range positions {
		if p.Kind != "fold" {
			continue
		}
		switch p.Payload["level"] {
		case "group":
			groupFolds++
			groupKey = p.PositionKey
		case "tool":
			toolFolds++
		}
	}
	if groupFolds != 1 || toolFolds != 2 {
		t.Errorf("claude should emit 1 group fold + 2 tool folds, got group=%d tool=%d", groupFolds, toolFolds)
	}
	for _, p := range positions {
		if p.Kind == "fold" && p.Payload["level"] == "tool" {
			if gk, _ := p.Payload["group_key"].(string); gk != groupKey {
				t.Errorf("tool fold group_key %q != group %q", gk, groupKey)
			}
		}
	}
	// Fold headers carry the full untruncated summary; the folded tool's box
	// top no longer embeds a header.
	for _, want := range []string{"▼ Tool: Grep · x", "▼ Tool: Bash · ls"} {
		if !strings.Contains(stripANSIForTest(ansi), want) {
			t.Errorf("claude tool fold header %q missing:\n%s", want, ansi[:min(len(ansi), 900)])
		}
	}
	if strings.Contains(ansi, "╔══ Tool: Grep") {
		t.Errorf("folded tool must not keep an embedded box-top header:\n%s", ansi[:min(len(ansi), 900)])
	}
}

// The fold header keeps the full path (no 48-column truncation) so a collapsed
// tool row still shows the whole invocation.
func TestClaudeToolFoldHeaderFullPath(t *testing.T) {
	ts := time.Now()
	longPath := "/home/deck/projects/session-insight/.claude/worktrees/follow-mode-active-session/.runtime/shots/1-live-follow-on.png"
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "claude"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "claude", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "claude",
			ToolName: "Read", ToolCallID: "c1", ToolInput: map[string]any{"file_path": longPath}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "claude", ToolCallID: "c1", Stdout: "data"},
	}
	ansi, positions := FormatEventsWithPositions(events, 120)
	plain := stripANSIForTest(ansi)

	if !strings.Contains(plain, "▼ Tool: Read · "+longPath) {
		t.Errorf("fold header should carry the full untruncated path:\n%s", plain)
	}
	// The fold header line is the tool fold's LineStart.
	var fold *RenderPosition
	for i := range positions {
		if positions[i].Kind == "fold" && positions[i].Payload["level"] == "tool" {
			fold = &positions[i]
		}
	}
	if fold == nil {
		t.Fatalf("claude tool fold missing: %+v", positions)
	}
	lines := strings.Split(ansi, "\n")
	if !strings.Contains(stripANSIForTest(lines[fold.LineStart]), "▼ Tool: Read") {
		t.Errorf("tool fold LineStart should point at the ▼ Tool header, got %q", lines[fold.LineStart])
	}
}

func TestChrysPerToolFolds(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "ls -la"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c1", Stdout: "a\nb\nc"},
		{EventID: "i1", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "Grep", ToolCallID: "c2", ToolInput: map[string]any{"pattern": "foo"}},
		{EventID: "r1", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c2", Stdout: "hit"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "done"},
	}
	ansi, positions := FormatEventsWithPositions(events, 120)

	var groupFolds, toolFolds int
	groupKey := ""
	for _, p := range positions {
		if p.Kind != "fold" {
			continue
		}
		switch p.Payload["level"] {
		case "group":
			groupFolds++
			groupKey = p.PositionKey
		case "tool":
			toolFolds++
			if gk, _ := p.Payload["group_key"].(string); gk == "" {
				t.Errorf("tool fold missing group_key: %+v", p.Payload)
			}
		}
	}
	if groupFolds != 1 || toolFolds != 2 {
		t.Fatalf("chrys should emit 1 group fold + 2 tool folds, got group=%d tool=%d", groupFolds, toolFolds)
	}
	// Every tool fold references the enclosing group so a collapsed group can
	// subsume them on the client.
	for _, p := range positions {
		if p.Kind == "fold" && p.Payload["level"] == "tool" {
			if gk, _ := p.Payload["group_key"].(string); gk != groupKey {
				t.Errorf("tool fold group_key %q != group %q", gk, groupKey)
			}
		}
	}
	// Compact per-tool header stays visible with the command summary, indented
	// two columns under the group header.
	hasIndentedHeader := false
	for _, l := range strings.Split(ansi, "\n") {
		if strings.Contains(stripANSIForTest(l), "  ▼ • Bash  ls -la") {
			hasIndentedHeader = true
			break
		}
	}
	if !hasIndentedHeader {
		t.Errorf("Bash tool fold header (indented, with summary) missing:\n%s", ansi[:min(len(ansi), 700)])
	}
}

// TestChrysPairsBatchedToolResults verifies that chrys's parallel-call order
// (all invocations, then all results) is reordered so each tool fold's body
// covers that tool's own output — not the next tool's.
func TestChrysPairsBatchedToolResults(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		// Batched: two invocations first, then two results (chrys parallel calls).
		{EventID: "i0", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "echo one"}},
		{EventID: "i1", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "Bash", ToolCallID: "c2", ToolInput: map[string]any{"command": "echo two"}},
		{EventID: "r0", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c1", Stdout: "OUT_ONE"},
		{EventID: "r1", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "c2", Stdout: "OUT_TWO"},
	}
	ansi, positions := FormatEventsWithPositions(events, 120)
	lines := strings.Split(ansi, "\n")

	// Locate each tool fold and assert its body lines contain that tool's own
	// output (paired), which the pre-pairing batched order would not satisfy.
	strip := func(s string) string { return stripANSIForTest(s) }
	find := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(strip(l), sub) {
				return i
			}
		}
		return -1
	}
	oneHdr, twoHdr := find("echo one"), find("echo two")
	oneOut, twoOut := find("OUT_ONE"), find("OUT_TWO")
	if oneHdr < 0 || twoHdr < 0 || oneOut < 0 || twoOut < 0 {
		t.Fatalf("missing rows: oneHdr=%d twoHdr=%d oneOut=%d twoOut=%d\n%s", oneHdr, twoHdr, oneOut, twoOut, ansi)
	}
	// Paired order: echo one … OUT_ONE … echo two … OUT_TWO.
	if oneHdr >= oneOut || oneOut >= twoHdr || twoHdr >= twoOut {
		t.Errorf("tool results not paired with invocations; order oneHdr=%d oneOut=%d twoHdr=%d twoOut=%d", oneHdr, oneOut, twoHdr, twoOut)
	}
	_ = positions
}

// Chrys sub-agents batch their own parallel calls at Depth 1+ just like the
// root agent. Pairing only depth-0 events leaves several read_file headers
// together followed by several outputs, as seen in session 5983649c7979.
func TestChrysPairsNestedBatchedToolResults(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		{EventID: "parent-inv", Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, AgentType: "chrys",
			ToolName: "explore_agent", ToolCallID: "parent", ToolInput: map[string]any{"prompt": "inspect"}},
		{EventID: "started", Type: "AgentSpecific", Subtype: "subagent_started", TurnIndex: 0, Depth: 1, AgentType: "chrys", Text: "Explore Agent"},
		{EventID: "read-a-inv", Type: "ToolInvocation", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys",
			ToolName: "read_file", ToolCallID: "read-a", ToolInput: map[string]any{"path": "a.ts"}},
		{EventID: "read-b-inv", Type: "ToolInvocation", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys",
			ToolName: "read_file", ToolCallID: "read-b", ToolInput: map[string]any{"path": "b.ts"}},
		{EventID: "read-a-res", Type: "ToolResult", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys", ToolCallID: "read-a", Stdout: "CONTENT_A"},
		{EventID: "read-b-res", Type: "ToolResult", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys", ToolCallID: "read-b", Stdout: "CONTENT_B"},
		{EventID: "summary", Type: "AgentSpecific", Subtype: "subagent_summary", TurnIndex: 0, Depth: 1, AgentType: "chrys", Text: "Tool calls: 2"},
		{EventID: "parent-res", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "parent", Stdout: "PARENT_DONE"},
	}

	ansi, _ := FormatEventsWithPositions(events, 120)
	plain := stripANSIForTest(ansi)
	ordered := []string{"a.ts", "CONTENT_A", "b.ts", "CONTENT_B", "PARENT_DONE"}
	last := -1
	for _, marker := range ordered {
		idx := strings.Index(plain, marker)
		if idx < 0 {
			t.Fatalf("missing %q in rendered output:\n%s", marker, plain)
		}
		if idx <= last {
			t.Fatalf("nested tools are not call→result paired; %q at %d after %d:\n%s", marker, idx, last, plain)
		}
		last = idx
	}
}

func TestChrysPairsNestedToolsAfterShallowOrphanResult(t *testing.T) {
	ts := time.Now()
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, AgentType: "chrys"},
		{EventID: "u0", Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", Text: "hi"},
		// A malformed shallow result must stay in place without preventing the
		// valid depth-1 batch after it from being paired.
		{EventID: "orphan", Type: "ToolResult", TurnIndex: 0, Timestamp: ts, AgentType: "chrys", ToolCallID: "missing", Stdout: "ORPHAN"},
		{EventID: "read-a-inv", Type: "ToolInvocation", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys",
			ToolName: "read_file", ToolCallID: "read-a", ToolInput: map[string]any{"path": "a.ts"}},
		{EventID: "read-b-inv", Type: "ToolInvocation", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys",
			ToolName: "read_file", ToolCallID: "read-b", ToolInput: map[string]any{"path": "b.ts"}},
		{EventID: "read-a-res", Type: "ToolResult", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys", ToolCallID: "read-a", Stdout: "CONTENT_A"},
		{EventID: "read-b-res", Type: "ToolResult", TurnIndex: 0, Depth: 1, Timestamp: ts, AgentType: "chrys", ToolCallID: "read-b", Stdout: "CONTENT_B"},
	}

	ansi, _ := FormatEventsWithPositions(events, 120)
	plain := stripANSIForTest(ansi)
	ordered := []string{"ORPHAN", "a.ts", "CONTENT_A", "b.ts", "CONTENT_B"}
	last := -1
	for _, marker := range ordered {
		idx := strings.Index(plain, marker)
		if idx <= last {
			t.Fatalf("deeper tools were not paired after shallow orphan; %q at %d after %d:\n%s", marker, idx, last, plain)
		}
		last = idx
	}
}

func TestFencedCodeBlockHighlight(t *testing.T) {
	text := "before\n```go\npackage main\n\nfunc main() {}\n```\nafter"
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), AgentType: "codex"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), AgentType: "codex", Text: text},
	}
	out := FormatEvents(events, 120)
	lines := strings.Split(out, "\n")

	var pkgLine, emptyIdx string
	for i, l := range lines {
		if strings.Contains(l, "package") {
			pkgLine = l
		}
		if strings.Contains(l, "func main") {
			emptyIdx = lines[i] // keep the compiler happy about usage
		}
	}
	// Syntax colours stay in theme-remapped slots 0-15 so they remain legible
	// when xterm switches between the dark and light palettes.
	if !regexp.MustCompile(`\x1b\[38;5;([0-9]|1[0-5])m`).MatchString(pkgLine) {
		t.Errorf("go code line should carry a theme-remapped color, got %q", pkgLine)
	}
	if regexp.MustCompile(`\x1b\[38;5;(1[6-9]|[2-9][0-9]|1[0-9][0-9]|2[0-5][0-9])m`).MatchString(pkgLine) {
		t.Errorf("go code line must not carry a fixed 256-cube color, got %q", pkgLine)
	}
	if !strings.HasSuffix(strings.TrimRight(pkgLine, "\r"), "\x1b[0m") {
		t.Errorf("highlighted line must end with a reset, got %q", pkgLine)
	}
	_ = emptyIdx

	// Unknown language falls back to the flat code color and never 256-color.
	events[1].Text = "```notalanguage\nsome code\n```"
	out = FormatEvents(events, 120)
	re := regexp.MustCompile(`\x1b\[38;5;(\d+)m`)
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "some code") {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(l, -1) {
			if n, _ := strconv.Atoi(m[1]); n >= 16 {
				t.Errorf("unknown lang must stay on theme slots (<16), got slot %d in %q", n, l)
			}
		}
	}

	// Line-count invariant: highlighting adds zero lines.
	events[1].Text = text
	plain := len(strings.Split(FormatEvents([]model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), AgentType: "codex"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), AgentType: "codex", Text: strings.ReplaceAll(text, "go", "notalang")},
	}, 120), "\n"))
	highlightedCount := len(strings.Split(FormatEvents(events, 120), "\n"))
	if plain != highlightedCount {
		t.Errorf("line count drift: plain %d vs highlighted %d", plain, highlightedCount)
	}
}

func TestFencedPythonCodeUsesThemeForegroundForNames(t *testing.T) {
	text := "```python\nNON_OFFICIAL_SET_PREFIX = \"DATABASE-\"\n\ndef public_set_clause():\n    return ~Set.set_num.ilike(f\"{NON_OFFICIAL_SET_PREFIX}%\")\n```"
	events := []model.RenderEvent{
		{EventID: "b0", Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), AgentType: "codex"},
		{EventID: "x0", Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), AgentType: "codex", Text: text},
	}
	out := FormatEvents(events, 120)

	constantLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "NON_OFFICIAL_SET_PREFIX") {
			constantLine = line
			break
		}
	}
	if constantLine == "" {
		t.Fatalf("missing Python constant line in render:\n%s", out)
	}
	if !strings.Contains(constantLine, "\x1b[38;5;7mNON_OFFICIAL_SET_PREFIX") {
		t.Errorf("ordinary Python names should use theme foreground slot 7, got %q", constantLine)
	}
	if regexp.MustCompile(`\x1b\[38;5;(1[6-9]|[2-9][0-9]|1[0-9][0-9]|2[0-5][0-9])m`).MatchString(out) {
		t.Errorf("Python code must not contain fixed 256-cube foregrounds:\n%q", out)
	}
}
