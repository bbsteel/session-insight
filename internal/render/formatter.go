package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"session-insight/internal/model"
)

const (
	maxStdoutLines = 10
	boxWidth       = 64
)

func FormatEvents(events []model.RenderEvent) string {
	var sb strings.Builder
	prevTurnIndex := -1

	for _, evt := range events {
		if evt.TurnIndex != prevTurnIndex {
			writeSeparator(&sb, evt.TurnIndex)
			prevTurnIndex = evt.TurnIndex
		}

		prefix := depthPrefix(evt.Depth)

		switch evt.Type {
		case "TurnBoundary":
		case "UserPrompt":
			writeUserPrompt(&sb, evt, prefix)
		case "ThinkingStart":
			writeThinking(&sb, evt, prefix)
		case "ThinkingChunk":
			writeThinking(&sb, evt, prefix)
		case "ThinkingEnd":
		case "TextChunk":
			writeTextChunk(&sb, evt, prefix)
		case "ToolInvocation":
			writeToolInvocation(&sb, evt, prefix)
		case "ToolResult":
			writeToolResult(&sb, evt, prefix)
		case "AgentSpecific":
			writeAgentSpecific(&sb, evt, prefix)
		}
	}

	return sb.String()
}

func writeSeparator(sb *strings.Builder, turnIdx int) {
	label := fmt.Sprintf(" Turn %d ", turnIdx)
	half := (TermWidth - len(label)) / 2
	line := strings.Repeat("─", half) + label + strings.Repeat("─", TermWidth-half-len(label))
	sb.WriteString("\n")
	sb.WriteString(fgWrap(line, HexSeparator))
	sb.WriteString("\n")
}

func writeUserPrompt(sb *strings.Builder, evt model.RenderEvent, prefix string) {
	prompt := fgWrap("> ", HexUser) + fgWrap(sanitizeControlChars(evt.Text), HexFg)
	for _, line := range strings.Split(prompt, "\n") {
		sb.WriteString(prefix)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
}

func writeThinking(sb *strings.Builder, evt model.RenderEvent, prefix string) {
	text := italicWrap(fgWrap(sanitizeControlChars(evt.Text), HexThinking))
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			sb.WriteString("\n")
			continue
		}
		sb.WriteString(prefix)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
}

func writeTextChunk(sb *strings.Builder, evt model.RenderEvent, prefix string) {
	lines := strings.Split(sanitizeControlChars(evt.Text), "\n")
	hasDiff := scanForDiff(lines)

	for i, line := range lines {
		if hasDiff && isDiffAdd(line) {
			padded := padRight(line, TermWidth)
			sb.WriteString(prefix)
			sb.WriteString(bgWrap(fgWrap(padded, HexFg), HexDiffAdd))
		} else if hasDiff && isDiffDel(line) {
			padded := padRight(line, TermWidth)
			sb.WriteString(prefix)
			sb.WriteString(bgWrap(fgWrap(padded, HexFg), HexDiffDel))
		} else {
			sb.WriteString(prefix)
			sb.WriteString(fgWrap(line, HexFg))
		}
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")
}

// scanForDiff requires an actual unified-diff hunk marker ("@@ ... @@")
// before treating any +/- prefixed lines as diff content. Without this,
// ordinary markdown bullet lists (very common in assistant responses, e.g.
// "- 修复了 A 的问题" / "- 修复了 B 的问题") would be miscategorized as
// diff-delete lines and rendered with a red background across completely
// unrelated prose. Real diff content pasted into a text block reliably
// carries "@@" hunk headers; plain bullet lists never do.
func scanForDiff(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "@@") {
			return true
		}
	}
	return false
}

func isDiffAdd(line string) bool {
	return strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")
}

func isDiffDel(line string) bool {
	return strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")
}

func writeToolInvocation(sb *strings.Builder, evt model.RenderEvent, prefix string) {
	borderColor := HexTool
	if evt.ToolName == "Agent" || evt.ToolName == "Task" {
		borderColor = HexSubagent
	}

	header := fmt.Sprintf(" Tool: %s ", sanitizeControlChars(evt.ToolName))
	// Guard against tool names long enough to overflow the box width: a
	// naive boxWidth-4-len(header) goes negative and strings.Repeat panics.
	// Truncate the header itself rather than crash the whole render.
	//
	// Must truncate/measure by display width, not rune count: a tool name
	// full of CJK characters can have a small rune count while already
	// exceeding the box width in actual terminal columns, which let the
	// header silently overflow the box (rune-count check never tripped).
	maxHeaderWidth := boxWidth - 6 // leave room for "╔══" + at least "═" + "╗"
	if displayWidth(header) > maxHeaderWidth {
		header = truncateToWidth(header, maxHeaderWidth)
	}
	fillLen := boxWidth - 4 - displayWidth(header)
	if fillLen < 1 {
		fillLen = 1
	}
	top := "╔══" + header + strings.Repeat("═", fillLen) + "╗"

	inputLines := formatToolInput(evt.ToolInput)

	sb.WriteString(prefix)
	sb.WriteString(fgWrap(top, borderColor))
	sb.WriteString("\n")
	if len(inputLines) > 0 {
		contentWidth := boxWidth - 2
		for _, il := range inputLines {
			bodyText := "  " + il
			// Same display-width concern as the header above: a line with
			// many CJK characters can have a rune count under contentWidth
			// while its actual display width already exceeds it, so the
			// old rune-count check let it skip truncation, and padRight
			// then left it unpadded-and-overflowing instead of clamped —
			// the body line ends up wider than the box, shifting the right
			// border out of alignment with the top/bottom borders.
			if displayWidth(bodyText) > contentWidth {
				bodyText = truncateToWidth(bodyText, contentWidth)
			}
			bodyText = padRight(bodyText, contentWidth)

			sb.WriteString(prefix)
			sb.WriteString(fgWrap("║", borderColor))
			sb.WriteString(fgWrap(bodyText, HexFg))
			sb.WriteString(fgWrap("║", borderColor))
			sb.WriteString("\n")
		}
	}
	bottom := prefix + fgWrap("╚"+strings.Repeat("═", boxWidth-2)+"╝", borderColor)
	sb.WriteString(bottom)
	sb.WriteString("\n")
}

func formatToolInput(input map[string]any) []string {
	if len(input) == 0 {
		return nil
	}
	// Go map iteration order is randomized; without sorting, the same
	// ToolInput would render its keys in a different order every time
	// (confirmed via a 20-run probe during review — every run differed).
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		valStr := formatAny(input[k])
		lines = append(lines, fmt.Sprintf("%s: %s", k, valStr))
	}
	return lines
}

func formatAny(v any) string {
	switch val := v.(type) {
	case string:
		clean := sanitizeControlChars(val)
		if shouldQuote(clean) {
			return fmt.Sprintf("%q", clean)
		}
		return clean
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%v", val)
	case bool:
		return fmt.Sprintf("%v", val)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(v)
		s := sanitizeControlChars(string(b))
		// Truncate by rune, not byte: s[:57] on a byte index can split a
		// multi-byte UTF-8 sequence in half (very likely with embedded
		// Chinese text in nested JSON values), producing invalid UTF-8 /
		// garbled output downstream.
		if displayWidth(s) > 60 {
			s = truncateToWidth(s, 60)
		}
		return s
	}
}

// sanitizeControlChars strips ANSI/C0 control characters (including ESC)
// from session-derived text before it's wrapped in this package's own ANSI
// codes. Without this, transcript content containing raw control sequences
// (OSC clipboard writes, cursor moves, screen clear, alternate-screen
// switches, etc.) would pass straight through to whatever terminal renders
// the output — including a real terminal via `cat`, not just xterm.js.
// "\n" is preserved (line splitting depends on it); "\t" is rendered as a
// single space to avoid disrupting box-width math.
func sanitizeControlChars(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			sb.WriteRune(r)
		case r == '\t':
			sb.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// drop ESC and other C0/DEL control characters
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// truncateToWidth returns a prefix of s whose displayWidth is at most
// maxWidth, ending in an ellipsis if anything was cut. Operates rune-by-rune
// (not byte-by-byte) so it never splits a multi-byte UTF-8 sequence, and
// accounts for CJK runes occupying 2 display columns each so the result
// never overflows a fixed-width box — unlike a plain []rune(s)[:n] slice,
// which only bounds rune count, not display width.
func truncateToWidth(s string, maxWidth int) string {
	if displayWidth(s) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	budget := maxWidth - 1 // reserve 1 column for the ellipsis itself
	var sb strings.Builder
	w := 0
	for _, r := range s {
		rw := 1
		if isWideRune(r) {
			rw = 2
		}
		if w+rw > budget {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	sb.WriteRune('…')
	return sb.String()
}

func shouldQuote(s string) bool {
	for _, ch := range s {
		if ch == ' ' || ch == '"' || ch == '\'' {
			return true
		}
	}
	return false
}

func writeToolResult(sb *strings.Builder, evt model.RenderEvent, prefix string) {
	// The checkmark/cross gets its own line, ending in "\n", rather than
	// being followed directly by the first output line on the same line.
	// The latter (the original draft's behavior) duplicated the depth
	// prefix mid-line at Depth>0: formatToolOutput also writes `prefix`
	// before its first line, so depth>0 output rendered as
	// "│ ✓ │ first output line" — the branch marker appearing twice on one
	// visual line.
	sb.WriteString(prefix)
	if evt.ExitCode == 0 && evt.Stderr == "" {
		sb.WriteString(fgWrap("✓", HexSuccess))
	} else {
		sb.WriteString(fgWrap("✗", HexError))
	}
	sb.WriteString("\n")

	if evt.Stdout != "" {
		sb.WriteString(formatToolOutput(evt.Stdout, prefix, false))
	}
	if evt.Stderr != "" {
		sb.WriteString(prefix)
		sb.WriteString(fgWrap("stderr:\n", HexWarning))
		sb.WriteString(formatToolOutput(evt.Stderr, prefix, true))
	}
	sb.WriteString("\n")
}

func formatToolOutput(content string, prefix string, isError bool) string {
	lines := strings.Split(sanitizeControlChars(content), "\n")
	color := HexFg
	if isError {
		color = HexError
	}

	if len(lines) > maxStdoutLines {
		shown := lines[:maxStdoutLines]
		remaining := len(lines) - maxStdoutLines

		var sb strings.Builder
		for _, line := range shown {
			sb.WriteString(prefix)
			sb.WriteString(fgWrap(line, color))
			sb.WriteString("\n")
		}
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("[+] %d 行被截断（点击展开）", remaining), HexWarning))
		sb.WriteString("\n")
		return sb.String()
	}

	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(line, color))
		sb.WriteString("\n")
	}
	return sb.String()
}

func writeAgentSpecific(sb *strings.Builder, evt model.RenderEvent, prefix string) {
	switch evt.Subtype {
	case "turn_duration":
	case "subagent_load_error":
		reason := "未知原因"
		if evt.Payload != nil {
			if r, ok := evt.Payload["reason"]; ok {
				reason = sanitizeControlChars(fmt.Sprintf("%v", r))
			}
		}
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("⚠ 子agent转录加载失败: %s", reason), HexWarning))
		sb.WriteString("\n")
	case "skill_invoked":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("⚙ %s", sanitizeControlChars(evt.Text)), HexSkill))
		sb.WriteString("\n")
	case "subagent_started":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("@ %s", sanitizeControlChars(evt.Text)), HexSubagent))
		sb.WriteString("\n")
	case "model_change":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("↪ model: %s", sanitizeControlChars(evt.Text)), HexWarning))
		sb.WriteString("\n")
	default:
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("[agent:%s]", evt.Subtype), HexWarning))
		sb.WriteString("\n")
	}
}

func padRight(s string, width int) string {
	w := displayWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// displayWidth approximates the terminal column width of s, counting
// common East Asian wide ranges (CJK ideographs, hangul, fullwidth forms,
// etc.) as width 2 and everything else as width 1.
//
// This is a deliberately small approximation, not a full Unicode East Asian
// Width table (no combining marks, no zero-width joiners) — added because
// real session content here is overwhelmingly Chinese, and plain rune
// counting under-pads every line containing it, visibly misaligning the
// right-hand box border. A proper fix would use a runewidth library, but
// that's a new dependency; flagging this trade-off rather than adding one
// silently.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK Radicals .. Yi syllables
		r >= 0xAC00 && r <= 0xD7A3, // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFF00 && r <= 0xFF60, // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD: // CJK Extension planes
		return true
	}
	return false
}
