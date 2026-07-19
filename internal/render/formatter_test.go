package render

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/model"
)

// hasFgColor checks that the output contains an indexed foreground ANSI code
// for the given semantic palette slot.
func hasFgColor(result string, c Color) bool {
	return strings.Contains(result, fmt.Sprintf("\x1b[38;5;%dm", int(c)))
}

// hasBgColor checks that the output contains an indexed background ANSI code
// for the given semantic palette slot.
func hasBgColor(result string, c Color) bool {
	return strings.Contains(result, fmt.Sprintf("\x1b[48;5;%dm", int(c)))
}

func TestFormatEventsEmpty(t *testing.T) {
	result := FormatEvents(nil, 0)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestSeparator(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), Depth: 0},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, " Turn 0 ") || !strings.Contains(result, "━") {
		t.Errorf("expected turn banner, got:\n%s", result)
	}
	// The badge must carry the banner accent as background (slot 12, resolved
	// by the client theme) so a turn start is findable at a glance, and the
	// label must use the terminal background color (slot 0) as fg — plain
	// white-on-accent once collided with the bold→bright palette remap.
	if !strings.Contains(result, "\x1b[48;5;12m") {
		t.Errorf("expected banner background color in output:\n%s", result)
	}
	if !strings.Contains(result, "\x1b[38;5;0m") {
		t.Errorf("expected banner label fg to be terminal background color:\n%s", result)
	}
}

func TestUserPrompt(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "hello world"},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "> ") {
		t.Errorf("expected prompt prefix, got:\n%s", result)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("expected prompt text, got:\n%s", result)
	}
	if !hasFgColor(result, ColUser) {
		t.Errorf("expected user color, got:\n%s", result)
	}
}

func TestThinking(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ThinkingStart", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "let me think..."},
		{Type: "ThinkingChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "more thinking"},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "let me think...") {
		t.Errorf("expected thinking text, got:\n%s", result)
	}
	if !strings.Contains(result, "more thinking") {
		t.Errorf("expected thinking chunk text, got:\n%s", result)
	}
	if !strings.Contains(result, italicCode) {
		t.Errorf("expected italic ANSI code, got:\n%s", result)
	}
}

func TestTextChunk(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "plain response"},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "plain response") {
		t.Errorf("expected text content, got:\n%s", result)
	}
}

func TestTextChunkWithDiff(t *testing.T) {
	diffText := "--- original\n+++ modified\n@@ -1,3 +1,3 @@\n+added line\n-removed line\n+another add\nunchanged line"
	events := []model.RenderEvent{
		{Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: diffText},
	}
	result := FormatEvents(events, 0)

	if !hasBgColor(result, ColDiffAdd) {
		t.Errorf("expected diff add bg color, got:\n%s", result)
	}
	if !hasBgColor(result, ColDiffDel) {
		t.Errorf("expected diff del bg color, got:\n%s", result)
	}
	if !strings.Contains(result, "unchanged line") {
		t.Errorf("expected unchanged line, got:\n%s", result)
	}
}

func TestTextChunkNoFalsePositiveDiff(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "regular text\nwith no diff markers"},
	}
	result := FormatEvents(events, 0)
	if hasBgColor(result, ColDiffAdd) || hasBgColor(result, ColDiffDel) {
		t.Errorf("unexpected diff coloring for non-diff text:\n%s", result)
	}
}

func TestCompactionBoundaryEmitsPosition(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), Depth: 0},
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "before compact"},
		{Type: "CompactionBoundary", TurnIndex: 0, Timestamp: time.Now(), Depth: 0},
	}
	_, positions := FormatEventsWithPositions(events, 80)

	var sawCompaction bool
	for _, pos := range positions {
		if pos.Kind == "compaction" && pos.TurnIndex == 0 {
			sawCompaction = true
		}
		if pos.Kind == "compaction" && pos.Label != "压缩" {
			t.Errorf("unexpected compaction label: %q", pos.Label)
		}
	}
	if !sawCompaction {
		t.Fatalf("expected CompactionBoundary to produce a compaction position, got %+v", positions)
	}
}

func TestRollbackFoldPositionUsesVisibleHeaderLine(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "RollbackStart", TurnIndex: 1, Metadata: map[string]any{"count": 2, "resume_turn": 1}},
		{Type: "TurnBoundary", TurnIndex: 1, Metadata: map[string]any{"rolled_back": true, "original_turn_index": 1}},
		{Type: "UserPrompt", TurnIndex: 1, Text: "abandoned prompt"},
		{Type: "RollbackEnd", TurnIndex: 1},
	}
	ansi, positions := FormatEventsWithPositions(events, 100)
	lines := strings.Split(ansi, "\n")

	var fold *RenderPosition
	for i := range positions {
		if positions[i].Kind == "fold" && positions[i].Payload["level"] == "rollback" {
			fold = &positions[i]
			break
		}
	}
	if fold == nil {
		t.Fatalf("expected rollback fold position, got %+v", positions)
	}
	if got := stripANSIForTest(lines[fold.LineStart]); !strings.Contains(got, "已回滚 2 个 turn") {
		t.Fatalf("rollback fold display header points at %q, want the visible rollback row", got)
	}
	headerLogical := int(fold.Payload["header_logical"].(float64))
	if got := stripANSIForTest(lines[headerLogical]); !strings.Contains(got, "已回滚 2 个 turn") {
		t.Fatalf("rollback fold logical header points at %q, want the visible rollback row", got)
	}
	if headerLogical != fold.LineStart {
		t.Fatalf("no-wrap fixture should share display/logical header row: display=%d logical=%d", fold.LineStart, headerLogical)
	}
}

func TestToolInvocationBox(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Bash", ToolCallID: "tool_001",
			ToolInput: map[string]any{"command": "ls -la", "timeout": float64(30)}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "╔") {
		t.Errorf("expected box top border, got:\n%s", result)
	}
	if !strings.Contains(result, "Tool: Bash") {
		t.Errorf("expected tool name in box, got:\n%s", result)
	}
	if !strings.Contains(result, "command") {
		t.Errorf("expected tool input key, got:\n%s", result)
	}
	if !strings.Contains(result, "ls -la") {
		t.Errorf("expected tool input value, got:\n%s", result)
	}
	if !strings.Contains(result, "╚") {
		t.Errorf("expected box bottom border, got:\n%s", result)
	}
}

func TestToolInvocationAgentBox(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Agent", ToolCallID: "tool_002",
			ToolInput: map[string]any{"prompt": "do something"}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "Tool: Agent") {
		t.Errorf("expected Agent tool name, got:\n%s", result)
	}
	if !hasFgColor(result, ColSubagent) {
		t.Errorf("expected subagent color for Agent tool, got:\n%s", result)
	}
}

func TestToolResultSuccess(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolResult", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ExitCode: 0, Stdout: "output line 1\noutput line 2"},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "✓") {
		t.Errorf("expected success check, got:\n%s", result)
	}
	if !hasFgColor(result, ColSuccessBright) {
		t.Errorf("expected success color, got:\n%s", result)
	}
}

func TestToolResultError(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolResult", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ExitCode: 1, Stderr: "command not found"},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "✗") {
		t.Errorf("expected error cross, got:\n%s", result)
	}
	if !hasFgColor(result, ColErrorBright) {
		t.Errorf("expected error color, got:\n%s", result)
	}
}

func TestToolResultTruncation(t *testing.T) {
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	events := []model.RenderEvent{
		{Type: "ToolResult", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ExitCode: 0, Stdout: strings.Join(lines, "\n")},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "被截断") {
		t.Errorf("expected truncation message, got:\n%s", result)
	}
	if !strings.Contains(result, "5") {
		t.Errorf("expected remaining line count 5, got:\n%s", result)
	}
	if !hasFgColor(result, ColWarning) {
		t.Errorf("expected warning color for truncation, got:\n%s", result)
	}
}

func TestToolResultNoTruncation(t *testing.T) {
	var lines []string
	for i := 0; i < 8; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	events := []model.RenderEvent{
		{Type: "ToolResult", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ExitCode: 0, Stdout: strings.Join(lines, "\n")},
	}
	result := FormatEvents(events, 0)
	if strings.Contains(result, "被截断") {
		t.Errorf("unexpected truncation for short output:\n%s", result)
	}
}

func TestDepthIndentation(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: time.Now(), Depth: 1, Text: "subagent task"},
	}
	result := FormatEvents(events, 0)
	if !hasFgColor(result, ColSubagent) {
		t.Errorf("expected subagent color for depth prefix, got:\n%s", result)
	}
	if !strings.Contains(result, "│") {
		t.Errorf("expected depth indent pipe, got:\n%s", result)
	}
}

func TestAgentSpecificSubagentError(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "AgentSpecific", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			Subtype: "subagent_load_error", Payload: map[string]any{"reason": "file not found"}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "子agent转录加载失败") {
		t.Errorf("expected subagent error message, got:\n%s", result)
	}
	if !strings.Contains(result, "file not found") {
		t.Errorf("expected error reason, got:\n%s", result)
	}
}

func TestAgentSpecificTurnDurationSkipped(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "AgentSpecific", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			Subtype: "turn_duration", DurationMs: 5000},
	}
	result := FormatEvents(events, 0)
	// The separator is rendered (Turn 0 boundary), but no turn_duration-specific text
	if strings.Contains(result, "turn_duration") || strings.Contains(result, "5000") {
		t.Errorf("turn_duration should not emit duration text, got:\n%s", result)
	}
}

func TestThinkingEndSkipped(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ThinkingStart", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "thinking"},
		{Type: "ThinkingEnd", TurnIndex: 0, Timestamp: time.Now(), Depth: 0},
	}
	result := FormatEvents(events, 0)
	count := strings.Count(result, "thinking")
	if count != 1 {
		t.Errorf("ThinkingEnd should not add extra 'thinking' text, got %d occurrences:\n%s", count, result)
	}
}

func TestMultiTurnSeparators(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "first"},
		{Type: "UserPrompt", TurnIndex: 1, Timestamp: time.Now(), Depth: 0, Text: "second"},
		{Type: "UserPrompt", TurnIndex: 2, Timestamp: time.Now(), Depth: 0, Text: "third"},
	}
	result := FormatEvents(events, 0)
	if strings.Count(result, " Turn ") != 3 {
		t.Errorf("expected 3 turn banners, got:\n%s", result)
	}
}

func TestToolInputStringDisplay(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Bash", ToolCallID: "t1",
			ToolInput: map[string]any{"command": "git commit -m 'fix'", "cwd": "/home"}},
	}
	result := FormatEvents(events, 0)
	// Values are displayed without quoting; the raw content should appear.
	if !strings.Contains(result, "git commit -m 'fix'") {
		t.Errorf("expected raw command value, got:\n%s", result)
	}
	if !strings.Contains(result, "/home") {
		t.Errorf("expected path value, got:\n%s", result)
	}
}

func TestToolInputNumberFormatting(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Bash", ToolCallID: "t1",
			ToolInput: map[string]any{"timeout": float64(30), "retry": float64(3)}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "30") {
		t.Errorf("expected integer number formatting, got:\n%s", result)
	}
}

func TestEmptyToolInputBox(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Bash", ToolCallID: "t1",
			ToolInput: map[string]any{}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "╔") {
		t.Errorf("expected box even with empty input, got:\n%s", result)
	}
	if !strings.Contains(result, "╚") {
		t.Errorf("expected bottom border, got:\n%s", result)
	}
}

// Regression tests added during review — each one caught a real bug in the
// original draft before it shipped.

func TestTextChunkMarkdownBulletsAreNotMisrenderedAsDiff(t *testing.T) {
	// Ordinary markdown bullet lists are extremely common in assistant
	// responses and start with "- ", which the original diff heuristic
	// (>=2 lines starting with +/-) flagged as diff-delete content,
	// painting unrelated prose with a red background.
	text := "下面是改动列表：\n- 修复了 A 的问题\n- 修复了 B 的问题\n- 修复了 C 的问题\n以上完成。"
	events := []model.RenderEvent{
		{Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: text},
	}
	result := FormatEvents(events, 0)
	if hasBgColor(result, ColDiffDel) || hasBgColor(result, ColDiffAdd) {
		t.Errorf("markdown bullet list should not be colored as a diff:\n%s", result)
	}
}

func TestToolInvocationLongToolNameDoesNotPanic(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName:  "mcp__some-really-long-mcp-server-name__some_really_long_tool_name_here",
			ToolInput: map[string]any{"x": "y"}},
	}
	// The original draft panicked here with "strings: negative Repeat
	// count" because the box header computation didn't account for tool
	// names long enough to exceed boxWidth.
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "╔") || !strings.Contains(result, "╚") {
		t.Errorf("expected a complete box even for an overlong tool name, got:\n%s", result)
	}
}

func TestToolInputOrderIsDeterministic(t *testing.T) {
	// Go map iteration order is randomized. The original draft iterated
	// ToolInput directly, so the same input rendered its keys in a
	// different order on every call — confirmed via a 20-run probe during
	// review, where consecutive runs differed.
	input := map[string]any{"alpha": "1", "beta": "2", "gamma": "3", "delta": "4", "epsilon": "5"}
	var first string
	for i := 0; i < 10; i++ {
		events := []model.RenderEvent{
			{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, ToolName: "Bash", ToolInput: input},
		}
		out := FormatEvents(events, 0)
		if i == 0 {
			first = out
		} else if out != first {
			t.Errorf("ToolInput rendering is nondeterministic across calls (run %d differs from run 0)", i)
		}
	}
}

func TestToolInvocationCJKBoxBorderAligns(t *testing.T) {
	// Chinese tool input previously under-padded the box's right border
	// because padRight counted display width by rune count, and CJK
	// characters occupy 2 terminal columns each, not 1.
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Bash", ToolInput: map[string]any{"command": "检查一下post-commit的rsync为什么这么慢了"}},
	}
	result := FormatEvents(events, 0)
	var topW, bodyW, bottomW int
	for _, line := range strings.Split(result, "\n") {
		stripped := stripANSIForTest(line)
		switch {
		case strings.Contains(stripped, "╔"):
			topW = displayWidth(stripped)
		case strings.Contains(stripped, "║"):
			bodyW = displayWidth(stripped)
		case strings.Contains(stripped, "╚"):
			bottomW = displayWidth(stripped)
		}
	}
	if topW == 0 || bodyW == 0 || bottomW == 0 {
		t.Fatalf("expected to find top/body/bottom box lines, got:\n%s", result)
	}
	// Compare display width (terminal columns), not rune count: CJK runes
	// are 1 rune but 2 columns, so rune-count equality is the wrong
	// invariant here — display-width equality is what makes the border
	// actually line up on screen.
	if topW != bodyW || bodyW != bottomW {
		t.Errorf("box lines are not the same display width (top=%d body=%d bottom=%d) — border misaligned for CJK content:\n%s", topW, bodyW, bottomW, result)
	}
}

// Regression tests added during the Codex-reviewed second pass — each one
// caught a real bug Codex flagged that I'd missed in the first integration.

func TestControlCharsAreSanitized(t *testing.T) {
	// \x1b]52;...\x07 is an OSC 52 clipboard-write sequence; \x1b[2J clears
	// the screen. If session content (assistant text here, but the same
	// path applies to tool input/stdout/stderr) contained either, it would
	// previously pass straight through into the ANSI stream — including
	// when cat'd to a real terminal, not just inside xterm.js.
	malicious := "hello\x1b]52;c;ZXZpbA==\x07world\x1b[2Jdone"
	events := []model.RenderEvent{
		{Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: malicious},
	}
	result := FormatEvents(events, 0)
	if strings.Contains(result, "\x1b]52") {
		t.Errorf("OSC 52 clipboard-write sequence leaked into output:\n%q", result)
	}
	if strings.Contains(result, "\x1b[2J") {
		t.Errorf("clear-screen escape sequence leaked into output:\n%q", result)
	}
	if !strings.Contains(result, "hello") || !strings.Contains(result, "world") || !strings.Contains(result, "done") {
		t.Errorf("expected surrounding plain text to survive sanitization:\n%q", result)
	}
}

func TestToolInvocationLongCJKToolNameStaysAligned(t *testing.T) {
	// 36 runes but 72 display columns — small enough to have skipped the
	// old rune-count-based truncation check, while already overflowing the
	// box in actual terminal columns.
	longCJKName := strings.Repeat("超长工具名称", 6)
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: longCJKName, ToolInput: map[string]any{"x": "y"}},
	}
	result := FormatEvents(events, 0)
	var topW, bottomW int
	for _, line := range strings.Split(result, "\n") {
		stripped := stripANSIForTest(line)
		if strings.Contains(stripped, "╔") {
			topW = displayWidth(stripped)
		}
		if strings.Contains(stripped, "╚") {
			bottomW = displayWidth(stripped)
		}
	}
	if topW == 0 || bottomW == 0 || topW != bottomW {
		t.Errorf("top/bottom border width mismatch for long CJK tool name: top=%d bottom=%d\n%s", topW, bottomW, result)
	}
}

func TestToolInputLongCJKValueDoesNotOverflowBox(t *testing.T) {
	// 40 runes, 80 display columns — exceeds contentWidth (62) while still
	// being well under the old rune-count truncation threshold.
	longCJKValue := strings.Repeat("中文内容超长测试", 5)
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName: "Bash", ToolInput: map[string]any{"command": longCJKValue}},
	}
	result := FormatEvents(events, 0)
	var topW, bodyW, bottomW int
	for _, line := range strings.Split(result, "\n") {
		stripped := stripANSIForTest(line)
		switch {
		case strings.Contains(stripped, "╔"):
			topW = displayWidth(stripped)
		case strings.Contains(stripped, "║"):
			bodyW = displayWidth(stripped)
		case strings.Contains(stripped, "╚"):
			bottomW = displayWidth(stripped)
		}
	}
	if topW == 0 || bodyW == 0 || bottomW == 0 || topW != bodyW || bodyW != bottomW {
		t.Errorf("box overflowed for long CJK input value: top=%d body=%d bottom=%d\n%s", topW, bodyW, bottomW, result)
	}
}

func TestFormatAnyJSONFallbackDoesNotSplitUTF8(t *testing.T) {
	// A slice value forces the json.Marshal fallback branch. Many Chinese
	// characters near the old 60-byte truncation boundary risked slicing a
	// multi-byte UTF-8 sequence in half via s[:57].
	value := []string{strings.Repeat("测试一二三四五六七八九十", 3)}
	result := formatAny(value)
	if !utf8.ValidString(result) {
		t.Errorf("formatAny produced invalid UTF-8: %q", result)
	}
}

func TestToolResultAtDepthDoesNotDuplicatePrefix(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolResult", TurnIndex: 0, Timestamp: time.Now(), Depth: 1,
			ExitCode: 0, Stdout: "output line"},
	}
	result := FormatEvents(events, 0)
	for _, line := range strings.Split(result, "\n") {
		stripped := stripANSIForTest(line)
		if count := strings.Count(stripped, "│"); count > 1 {
			t.Errorf("depth marker appears more than once on a single line: %q", stripped)
		}
	}
}

func TestToolInvocationApplyPatchShowsFilePathAndColors(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: src/app.go\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-old line\n" +
		"+new line\n" +
		" context\n" +
		"*** End Patch"
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName:  "apply_patch",
			ToolInput: map[string]any{"args": patch}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "src/app.go") {
		t.Errorf("expected file path in apply_patch diff output, got:\n%s", result)
	}
	if !hasBgColor(result, ColDiffAdd) {
		t.Errorf("expected add background for apply_patch, got:\n%s", result)
	}
	if !hasBgColor(result, ColDiffDel) {
		t.Errorf("expected del background for apply_patch, got:\n%s", result)
	}
}

func TestToolInvocationApplyPatchEmptyFallsToGenericBox(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName:  "apply_patch",
			ToolInput: map[string]any{"args": ""}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "╔") {
		t.Errorf("expected generic tool box for empty apply_patch, got:\n%s", result)
	}
	// Generic box must not show "Edit:" header (that belongs to writeEditDiff)
	if strings.Contains(result, "Edit:") {
		t.Errorf("empty apply_patch should not render an Edit diff header:\n%s", result)
	}
}

func TestToolInvocationApplyPatchMultiFileProducesMultipleBlocks(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: a.go\n" +
		"@@ -1 +1 @@\n" +
		"-aold\n" +
		"+anew\n" +
		"*** Update File: b.go\n" +
		"@@ -1 +1 @@\n" +
		"-bold\n" +
		"+bnew\n" +
		"*** End Patch"
	events := []model.RenderEvent{
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 0,
			ToolName:  "apply_patch",
			ToolInput: map[string]any{"args": patch}},
	}
	result := FormatEvents(events, 0)
	if !strings.Contains(result, "a.go") || !strings.Contains(result, "b.go") {
		t.Errorf("expected both file paths in output, got:\n%s", result)
	}
}

// stripANSIForTest removes the ANSI escape sequences this package emits so
// tests can measure visible rune width instead of raw string length.
func stripANSIForTest(s string) string {
	var sb strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// The edit-diff header embeds "✏️" (U+270F + VS16). xterm renders the
// variation selector as zero-width, so our width math must too — otherwise
// the box top border comes out one column short of the body rows and the ╗
// corner visibly misaligns.
func TestEditDiffBoxBordersAlign(t *testing.T) {
	if got := displayWidth("✏️"); got != 1 {
		t.Fatalf("displayWidth(✏️) = %d, want 1 (VS16 must be zero-width)", got)
	}
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Depth: 0},
		{Type: "ToolInvocation", TurnIndex: 0, Depth: 0,
			ToolName: "Edit", ToolCallID: "e1",
			ToolInput: map[string]any{
				"file_path":  "/tmp/example.go",
				"old_string": "a",
				"new_string": "b",
			}},
	}
	out := FormatEvents(events, 80)
	var topWidth, bodyWidth int
	for _, line := range strings.Split(out, "\n") {
		plain := stripANSIForTest(line)
		if strings.Contains(plain, "✏️") {
			topWidth = displayWidth(plain)
		}
		if strings.Contains(plain, "-1 lines, +1 lines") {
			bodyWidth = displayWidth(plain)
		}
	}
	if topWidth == 0 || bodyWidth == 0 {
		t.Fatalf("did not find edit box top/body lines in output:\n%s", out)
	}
	if topWidth != bodyWidth {
		t.Errorf("edit box top border width %d != body row width %d", topWidth, bodyWidth)
	}
}

func TestToolInvocationEmitsToolPosition(t *testing.T) {
	start := time.Date(2026, 7, 11, 10, 0, 0, 0, time.Local)
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: start, Depth: 0},
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: start, Depth: 0,
			ToolName: "Bash", ToolCallID: "call-1",
			ToolInput: map[string]any{"command": "npm test"}},
		{Type: "ToolResult", TurnIndex: 0, Timestamp: start.Add(3200 * time.Millisecond), Depth: 0,
			ToolCallID: "call-1", Stdout: "ok"},
	}
	ansi, positions := FormatEventsWithPositions(events, 80)

	var tool *RenderPosition
	for i, pos := range positions {
		if pos.Kind == "tool" {
			tool = &positions[i]
		}
	}
	if tool == nil {
		t.Fatalf("expected a tool position, got %+v", positions)
	}
	if tool.Label != "Bash" {
		t.Errorf("tool label: got %q, want Bash", tool.Label)
	}
	if got := tool.Payload["summary"]; got != "npm test" {
		t.Errorf("payload summary: got %v, want npm test", got)
	}
	if got := tool.Payload["status"]; got != "ok" {
		t.Errorf("payload status: got %v, want ok", got)
	}
	if got := tool.Payload["duration_ms"]; got != float64(3200) {
		t.Errorf("payload duration_ms: got %v, want 3200", got)
	}
	if got := tool.Payload["ts_ms"]; got != float64(start.UnixMilli()) {
		t.Errorf("payload ts_ms: got %v, want %d", got, start.UnixMilli())
	}
	// Default (box) profile: the header line carries the salient arg and the
	// duration so the invocation is readable without opening anything.
	if !strings.Contains(ansi, "Tool: Bash · npm test · 3.2s") {
		t.Errorf("expected enriched box header, got:\n%s", ansi)
	}
}

func TestNestedToolInvocationEmitsNoToolPosition(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), Depth: 0},
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: time.Now(), Depth: 1,
			ToolName: "Read", ToolCallID: "sub-1",
			ToolInput: map[string]any{"file_path": "/tmp/x"}},
	}
	_, positions := FormatEventsWithPositions(events, 80)
	for _, pos := range positions {
		if pos.Kind == "tool" {
			t.Fatalf("depth>0 invocation must not emit a tool position, got %+v", pos)
		}
	}
}

func TestToolOutcomeStatusMerge(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "ToolInvocation", ToolCallID: "c1", ToolName: "Bash"},
		{Type: "ToolResult", ToolCallID: "c1", Stdout: "partial"},
		{Type: "ToolResult", ToolCallID: "c1", Stderr: "boom", ExitCode: 1},
	}
	out := computeToolOutcomes(events)
	if got := out["c1"].status; got != "error" {
		t.Errorf("merged status: got %q, want error (worst wins)", got)
	}
}

func TestTimestampOptions(t *testing.T) {
	ts := time.Date(2026, 7, 11, 9, 5, 7, 0, time.Local)
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, Depth: 0},
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, Depth: 0, Text: "hi"},
		{Type: "TextChunk", TurnIndex: 0, Timestamp: ts.Add(time.Second), Depth: 0, Text: "reply"},
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts.Add(2 * time.Second), Depth: 0,
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "ls"}},
	}

	plain := FormatEventsOpts(events, 80, Options{})
	if strings.Contains(plain, "09:05:07") {
		t.Errorf("timestamps must be off by default, got:\n%s", plain)
	}

	all := FormatEventsOpts(events, 80, Options{TimestampUser: true, TimestampAssistant: true, TimestampTool: true})
	for _, want := range []string{"09:05:07", "09:05:08", "09:05:09"} {
		if !strings.Contains(all, want) {
			t.Errorf("expected timestamp %s in output:\n%s", want, all)
		}
	}
}

func TestUserPromptTrailingBlankLineAndLineEnd(t *testing.T) {
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: time.Now(), Depth: 0},
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "hello world"},
		{Type: "TextChunk", TurnIndex: 0, Timestamp: time.Now(), Depth: 0, Text: "reply"},
	}
	ansi, positions := FormatEventsWithPositions(events, 80)
	lines := strings.Split(ansi, "\n")

	var userPos *RenderPosition
	for i := range positions {
		if positions[i].Kind == "user" {
			userPos = &positions[i]
			break
		}
	}
	if userPos == nil {
		t.Fatalf("expected a user position, got %+v", positions)
	}
	if userPos.LineEnd == nil {
		t.Fatalf("user position must record LineEnd for highlight decoration")
	}
	if *userPos.LineEnd < userPos.LineStart {
		t.Fatalf("LineEnd (%d) must be >= LineStart (%d)", *userPos.LineEnd, userPos.LineStart)
	}
	// The row right after the user body must be the trailing blank separator.
	blankRow := *userPos.LineEnd + 1
	if blankRow >= len(lines) {
		t.Fatalf("blank row %d out of range (only %d lines)", blankRow, len(lines))
	}
	if stripANSIForTest(lines[blankRow]) != "" {
		t.Errorf("expected empty line below user message, got %q at row %d", lines[blankRow], blankRow)
	}
	// logical_end must be recorded and >= logical_start for the frontend to
	// resolve the highlight range through xterm's wrap state.
	ls, _ := userPos.Payload["logical_start"].(float64)
	le, ok := userPos.Payload["logical_end"].(float64)
	if !ok {
		t.Fatalf("user position must record logical_end, got %+v", userPos.Payload)
	}
	if int(le) < int(ls) {
		t.Errorf("logical_end (%d) < logical_start (%d)", int(le), int(ls))
	}
}

func TestParseTimestampKinds(t *testing.T) {
	o := ParseTimestampKinds("tool, user, bogus")
	if !o.TimestampUser || !o.TimestampTool || o.TimestampAssistant {
		t.Errorf("unexpected parse result: %+v", o)
	}
	if o.KindsString() != "user,tool" {
		t.Errorf("canonical form: got %q, want user,tool", o.KindsString())
	}
	if o.Mask() != 5 {
		t.Errorf("mask: got %d, want 5", o.Mask())
	}
}

func TestAssistantPositionsPerTextBlock(t *testing.T) {
	ts := time.Now()
	long := strings.Repeat("长", assistantSummaryMaxRunes+50)
	events := []model.RenderEvent{
		{Type: "TurnBoundary", TurnIndex: 0, Timestamp: ts, Depth: 0},
		{Type: "UserPrompt", TurnIndex: 0, Timestamp: ts, Depth: 0, Text: "q"},
		// One reply split into two chunks → a single assistant position whose
		// text merges both chunks.
		{Type: "TextChunk", TurnIndex: 0, Timestamp: ts, Depth: 0, Text: "part one. "},
		{Type: "TextChunk", TurnIndex: 0, Timestamp: ts, Depth: 0, Text: "part two."},
		// A tool call in between starts a new block.
		{Type: "ToolInvocation", TurnIndex: 0, Timestamp: ts, Depth: 0,
			ToolName: "Bash", ToolCallID: "c1", ToolInput: map[string]any{"command": "ls"}},
		{Type: "ToolResult", TurnIndex: 0, Timestamp: ts, Depth: 0, ToolCallID: "c1", Stdout: "ok"},
		// Oversized reply → summary is rune-capped and marked truncated.
		{Type: "TextChunk", TurnIndex: 0, Timestamp: ts, Depth: 0, Text: long},
		// Subagent text (depth > 0) gets no assistant position.
		{Type: "TextChunk", TurnIndex: 0, Timestamp: ts, Depth: 1, Text: "nested"},
	}

	_, positions := FormatEventsWithPositions(events, 80)
	var assistant []RenderPosition
	for _, p := range positions {
		if p.Kind == "assistant" {
			assistant = append(assistant, p)
		}
	}
	if len(assistant) != 2 {
		t.Fatalf("expected 2 assistant positions (one per text block), got %d: %+v", len(assistant), assistant)
	}
	if got, _ := assistant[0].Payload["text"].(string); got != "part one. part two." {
		t.Errorf("continuation chunks must merge into the block summary, got %q", got)
	}
	if _, ok := assistant[0].Payload["ts_ms"].(float64); !ok {
		t.Errorf("assistant position must record ts_ms, got %+v", assistant[0].Payload)
	}
	if _, ok := assistant[0].Payload["logical_start"].(float64); !ok {
		t.Errorf("assistant position must record logical_start, got %+v", assistant[0].Payload)
	}
	got, _ := assistant[1].Payload["text"].(string)
	if r := []rune(got); len(r) != assistantSummaryMaxRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("long reply must be capped at %d runes + ellipsis, got %d runes", assistantSummaryMaxRunes, len(r))
	}
}
