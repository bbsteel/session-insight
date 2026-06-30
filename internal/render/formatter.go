package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"session-insight/internal/model"
)

const maxStdoutLines = 10

// RenderPosition is one MiniMap marker emitted during ANSI formatting.
type RenderPosition struct {
	PositionKey string
	Kind        string
	TurnIndex   int
	LineStart   int
	LineEnd     *int
	Label       string
	Severity    string
	Payload     map[string]any
}

// FormatEvents renders events as ANSI text. cols is the terminal column count
// reported by the frontend (term.cols after fitAddon.fit()); pass 0 to use
// the package default (TermWidth).
func FormatEvents(events []model.RenderEvent, cols int) string {
	ansi, _ := FormatEventsWithPositions(events, cols)
	return ansi
}

// FormatEventsWithPositions renders events as ANSI text and simultaneously
// records terminal line positions for each significant event kind.
// Returns the ANSI string and a slice of positions ordered by line_start.
func FormatEventsWithPositions(events []model.RenderEvent, cols int) (string, []RenderPosition) {
	if cols <= 0 {
		cols = TermWidth
	}
	bWidth := cols - 4
	if bWidth < 40 {
		bWidth = 40
	}

	tb := newTrackingBuilder(cols)
	var positions []RenderPosition
	prevTurnIndex := -1
	editSeqByTurn := make(map[int]int)

	emit := func(kind, label, severity string, turnIndex int, payload map[string]any) {
		lineStart := tb.CurrentLine()
		key := fmt.Sprintf("%s:%d:%d", kind, turnIndex, lineStart)
		positions = append(positions, RenderPosition{
			PositionKey: key,
			Kind:        kind,
			TurnIndex:   turnIndex,
			LineStart:   lineStart,
			Label:       label,
			Severity:    severity,
			Payload:     payload,
		})
	}

	for _, evt := range events {
		if evt.TurnIndex != prevTurnIndex {
			emit("turn", fmt.Sprintf("Turn %d", evt.TurnIndex), "", evt.TurnIndex, nil)
			writeSeparator(tb, evt.TurnIndex, cols)
			prevTurnIndex = evt.TurnIndex
		}

		prefix := depthPrefix(evt.Depth)

		switch evt.Type {
		case "TurnBoundary":
		case "UserPrompt":
			emit("user", "用户输入", "", evt.TurnIndex, nil)
			writeUserPrompt(tb, evt, prefix)
		case "ThinkingStart":
			writeThinking(tb, evt, prefix)
		case "ThinkingChunk":
			writeThinking(tb, evt, prefix)
		case "ThinkingEnd":
		case "TextChunk":
			writeTextChunk(tb, evt, prefix, cols)
		case "ToolInvocation":
			writeToolInvocation(tb, evt, prefix, bWidth, func(filePath string) {
				seq := editSeqByTurn[evt.TurnIndex]
				editSeqByTurn[evt.TurnIndex]++
				emit("edit", filePath, "", evt.TurnIndex, map[string]any{"edit_seq": float64(seq)})
			})
		case "ToolResult":
			if evt.ExitCode != 0 || evt.Stderr != "" {
				emit("error", "工具错误", "error", evt.TurnIndex, map[string]any{"tool": evt.ToolName})
			}
			writeToolResult(tb, evt, prefix)
		case "CompactionBoundary":
			emit("compaction", "压缩", "", evt.TurnIndex, nil)
		case "AgentSpecific":
			writeAgentSpecific(tb, evt, prefix)
		}
	}

	// Deduplicate: if two positions share the same position_key, keep first.
	seen := make(map[string]struct{}, len(positions))
	deduped := positions[:0]
	for _, p := range positions {
		if _, ok := seen[p.PositionKey]; ok {
			continue
		}
		seen[p.PositionKey] = struct{}{}
		deduped = append(deduped, p)
	}

	return tb.String(), deduped
}

func writeSeparator(sb *trackingBuilder, turnIdx int, termWidth int) {
	label := fmt.Sprintf(" Turn %d ", turnIdx)
	half := (termWidth - len(label)) / 2
	if half < 1 {
		half = 1
	}
	line := strings.Repeat("─", half) + label + strings.Repeat("─", termWidth-half-len(label))
	sb.WriteString("\n")
	sb.WriteString(fgWrap(line, ColMuted))
	sb.WriteString("\n")
}

func writeUserPrompt(sb *trackingBuilder, evt model.RenderEvent, prefix string) {
	prompt := fgWrap("> ", ColUser) + fgWrap(sanitizeControlChars(evt.Text), ColFg)
	for _, line := range strings.Split(prompt, "\n") {
		sb.WriteString(prefix)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
}

func writeThinking(sb *trackingBuilder, evt model.RenderEvent, prefix string) {
	text := italicWrap(fgWrap(sanitizeControlChars(evt.Text), ColMuted))
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

func writeTextChunk(sb *trackingBuilder, evt model.RenderEvent, prefix string, termWidth int) {
	lines := strings.Split(sanitizeControlChars(evt.Text), "\n")
	hasDiff := scanForDiff(lines)
	inCodeBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if hasDiff && isDiffAdd(line) {
			padded := padRight(line, termWidth)
			sb.WriteString(prefix)
			sb.WriteString(bgWrap(fgWrap(padded, ColFg), ColDiffAdd))
		} else if hasDiff && isDiffDel(line) {
			padded := padRight(line, termWidth)
			sb.WriteString(prefix)
			sb.WriteString(bgWrap(fgWrap(padded, ColFg), ColDiffDel))
		} else if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			sb.WriteString(prefix)
			sb.WriteString(fgWrap(line, ColMuted))
		} else if inCodeBlock {
			sb.WriteString(prefix)
			sb.WriteString(fgWrap(line, ColWarning))
		} else {
			sb.WriteString(prefix)
			sb.WriteString(renderMarkdownLine(line, ColFg))
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

func writeToolInvocation(sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, onEditStart func(filePath string)) {
	if model.IsEditTool(evt.ToolName) {
		if evt.ToolName == "apply_patch" {
			// apply_patch carries a raw patch string (under args/input/patch),
			// not pre-normalised file_path/old_string/new_string.  Parse it
			// into per-file EditCalls and render each as its own diff block.
			calls := model.ExtractEditCalls(evt)
			for _, call := range calls {
				if onEditStart != nil {
					onEditStart(call.FilePath)
				}
				syn := evt
				syn.ToolInput = map[string]any{
					"file_path":  call.FilePath,
					"old_string": call.OldString,
					"new_string": call.NewString,
				}
				writeEditDiff(sb, syn, prefix, bWidth)
			}
			if len(calls) > 0 {
				return
			}
			// Empty or malformed patch: fall through to generic tool box.
		} else {
			filePath, _ := evt.ToolInput["file_path"].(string)
			if onEditStart != nil {
				onEditStart(filePath)
			}
			writeEditDiff(sb, evt, prefix, bWidth)
			return
		}
	}

	borderColor := ColTool
	if evt.ToolName == "Agent" || evt.ToolName == "Task" {
		borderColor = ColSubagent
	}

	header := fmt.Sprintf(" Tool: %s ", sanitizeControlChars(evt.ToolName))
	// Guard against tool names long enough to overflow the box width: a
	// naive bWidth-4-len(header) goes negative and strings.Repeat panics.
	// Truncate the header itself rather than crash the whole render.
	//
	// Must truncate/measure by display width, not rune count: a tool name
	// full of CJK characters can have a small rune count while already
	// exceeding the box width in actual terminal columns, which let the
	// header silently overflow the box (rune-count check never tripped).
	maxHeaderWidth := bWidth - 6 // leave room for "╔══" + at least "═" + "╗"
	if displayWidth(header) > maxHeaderWidth {
		header = truncateToWidth(header, maxHeaderWidth)
	}
	fillLen := bWidth - 4 - displayWidth(header)
	if fillLen < 1 {
		fillLen = 1
	}
	top := "╔══" + header + strings.Repeat("═", fillLen) + "╗"

	inputLines := formatToolInput(evt.ToolInput)

	sb.WriteString(prefix)
	sb.WriteString(fgWrap(top, borderColor))
	sb.WriteString("\n")
	if len(inputLines) > 0 {
		contentWidth := bWidth - 2
		for _, il := range inputLines {
			for _, wl := range wrapInBox(il, contentWidth) {
				wl = padRight(wl, contentWidth)
				sb.WriteString(prefix)
				sb.WriteString(fgWrap("║", borderColor))
				sb.WriteString(fgWrap(wl, ColFg))
				sb.WriteString(fgWrap("║", borderColor))
				sb.WriteString("\n")
			}
		}
	}
	bottom := prefix + fgWrap("╚"+strings.Repeat("═", bWidth-2)+"╝", borderColor)
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
		if clean == "" {
			return `""`
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

func writeToolResult(sb *trackingBuilder, evt model.RenderEvent, prefix string) {
	// The checkmark/cross gets its own line, ending in "\n", rather than
	// being followed directly by the first output line on the same line.
	// The latter (the original draft's behavior) duplicated the depth
	// prefix mid-line at Depth>0: formatToolOutput also writes `prefix`
	// before its first line, so depth>0 output rendered as
	// "│ ✓ │ first output line" — the branch marker appearing twice on one
	// visual line.
	sb.WriteString(prefix)
	if evt.ExitCode == 0 && evt.Stderr == "" {
		sb.WriteString(fgWrap("✓", ColSuccess))
	} else {
		sb.WriteString(fgWrap("✗", ColError))
	}
	sb.WriteString("\n")

	if evt.Stdout != "" {
		sb.WriteString(formatToolOutput(evt.Stdout, prefix, false))
	}
	if evt.Stderr != "" {
		sb.WriteString(prefix)
		sb.WriteString(fgWrap("stderr:\n", ColWarning))
		sb.WriteString(formatToolOutput(evt.Stderr, prefix, true))
	}
	sb.WriteString("\n")
}

func formatToolOutput(content string, prefix string, isError bool) string {
	lines := strings.Split(sanitizeControlChars(content), "\n")
	color := ColFg
	if isError {
		color = ColError
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
		sb.WriteString(fgWrap(fmt.Sprintf("[+] %d 行被截断（点击展开）", remaining), ColWarning))
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

func writeAgentSpecific(sb *trackingBuilder, evt model.RenderEvent, prefix string) {
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
		sb.WriteString(fgWrap(fmt.Sprintf("⚠ 子agent转录加载失败: %s", reason), ColWarning))
		sb.WriteString("\n")
	case "skill_invoked":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("⚙ %s", sanitizeControlChars(evt.Text)), ColSkill))
		sb.WriteString("\n")
	case "subagent_started":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("@ %s", sanitizeControlChars(evt.Text)), ColSubagent))
		sb.WriteString("\n")
	case "model_change":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("↪ model: %s", sanitizeControlChars(evt.Text)), ColWarning))
		sb.WriteString("\n")
	default:
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("[agent:%s]", evt.Subtype), ColWarning))
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

// splitAtWidth returns the longest byte-aligned prefix of s whose display
// width is at most maxWidth, plus the remaining suffix.
func splitAtWidth(s string, maxWidth int) (string, string) {
	w := 0
	for i, r := range s {
		rw := 1
		if isWideRune(r) {
			rw = 2
		}
		if w+rw > maxWidth {
			return s[:i], s[i:]
		}
		w += rw
	}
	return s, ""
}

// wrapInBox wraps a tool input field (il) into display lines that fit within
// contentWidth columns. Every output line is prefixed with "  " (two spaces).
// Actual newlines in il are treated as hard line breaks; each resulting
// segment is then soft-wrapped at contentWidth if it is still too wide.
func wrapInBox(il string, contentWidth int) []string {
	const indent = "  "
	available := contentWidth - displayWidth(indent)
	if available < 1 {
		available = 1
	}
	var lines []string
	for _, seg := range strings.Split(il, "\n") {
		if displayWidth(indent+seg) <= contentWidth {
			lines = append(lines, indent+seg)
			continue
		}
		remaining := seg
		for len(remaining) > 0 {
			chunk, rest := splitAtWidth(remaining, available)
			if chunk == "" {
				runes := []rune(remaining)
				chunk = string(runes[0])
				remaining = string(runes[1:])
			} else {
				remaining = rest
			}
			lines = append(lines, indent+chunk)
		}
	}
	return lines
}

// renderMarkdownLine renders a single non-fenced-code-block text line with
// full Markdown block-level and inline formatting.
//
// Block-level (detected at line start):
//   - ATX headings: # / ## / ### ... ######
//   - Horizontal rules: ---, ***, ___ (3+ same chars, optional spaces)
//   - Blockquotes: > text
//   - Unordered lists: - / * / + followed by space
//   - Ordered lists: N. followed by space
//
// Inline (within line content): **bold**, ***bold+italic***, *italic*,
// `code`, ~~strikethrough~~, [text](url).
func renderMarkdownLine(line string, defaultFg Color) string {
	trimmed := strings.TrimSpace(line)

	if headingLevel(trimmed) > 0 {
		return styled(line, ColSkill, ColNone, true, false)
	}
	if isHorizontalRule(trimmed) {
		return fgWrap(strings.Repeat("─", TermWidth), ColMuted)
	}
	if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
		leadSpaces := len(line) - len(strings.TrimLeft(line, " \t"))
		content := strings.TrimPrefix(trimmed, "> ")
		return strings.Repeat(" ", leadSpaces) +
			fgWrap("│ ", ColMuted) +
			styled(content, ColMuted, ColNone, false, true)
	}
	if m, ok := matchUnorderedList(line); ok {
		return m.indent + fgWrap("•", ColUser) + " " + renderInlineMd(m.content, defaultFg)
	}
	if m, ok := matchOrderedList(line); ok {
		return m.indent + fgWrap(m.marker, ColUser) + " " + renderInlineMd(m.content, defaultFg)
	}
	return renderInlineMd(line, defaultFg)
}

// headingLevel returns 1-6 for ATX headings, 0 otherwise.
func headingLevel(s string) int {
	lvl := 0
	for _, r := range s {
		if r != '#' {
			break
		}
		lvl++
	}
	if lvl >= 1 && lvl <= 6 && lvl < len(s) && s[lvl] == ' ' {
		return lvl
	}
	return 0
}

// isHorizontalRule returns true when s is composed of 3+ identical
// '-'/'*'/'_' characters with optional spaces between them.
func isHorizontalRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	ch := s[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ch {
			count++
		} else if s[i] != ' ' {
			return false
		}
	}
	return count >= 3
}

type listItem struct {
	indent  string
	marker  string
	content string
}

func matchUnorderedList(line string) (listItem, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]
	if len(trimmed) >= 2 &&
		(trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') &&
		trimmed[1] == ' ' {
		return listItem{indent, string(trimmed[0]), trimmed[2:]}, true
	}
	return listItem{}, false
}

func matchOrderedList(line string) (listItem, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(trimmed) && trimmed[i] == '.' && trimmed[i+1] == ' ' {
		return listItem{indent, trimmed[:i+1], trimmed[i+2:]}, true
	}
	return listItem{}, false
}

// renderInlineMd applies inline Markdown spans to a single line using a
// state machine. Supported: ***bold+italic***, **bold**, *italic*,
// `code`, ~~strikethrough~~, [text](url).
func renderInlineMd(line string, defaultFg Color) string {
	if !strings.ContainsAny(line, "*`~[") {
		return fgWrap(line, defaultFg)
	}

	type mdSpan int
	const (
		spanNormal mdSpan = iota
		spanBoldItalic
		spanBold
		spanItalic
		spanCode
		spanStrike
	)

	var out strings.Builder
	var buf strings.Builder
	state := spanNormal
	runes := []rune(line)
	n := len(runes)

	flush := func(s mdSpan) {
		text := buf.String()
		buf.Reset()
		if text == "" {
			return
		}
		switch s {
		case spanNormal:
			out.WriteString(fgWrap(text, defaultFg))
		case spanBoldItalic:
			out.WriteString(styled(text, defaultFg, ColNone, true, true))
		case spanBold:
			out.WriteString(styled(text, defaultFg, ColNone, true, false))
		case spanItalic:
			out.WriteString(styled(text, defaultFg, ColNone, false, true))
		case spanCode:
			out.WriteString(fgWrap(text, ColWarning))
		case spanStrike:
			out.WriteString(strikeCode + fgWrap(text, ColMuted) + resetCode)
		}
	}

	runesHave := func(start int, pat string) bool {
		pr := []rune(pat)
		pn := len(pr)
		for i := start; i <= n-pn; i++ {
			match := true
			for j := 0; j < pn; j++ {
				if runes[i+j] != pr[j] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
		return false
	}

	for i := 0; i < n; {
		r := runes[i]
		switch state {
		case spanNormal:
			switch {
			case i+2 < n && r == '*' && runes[i+1] == '*' && runes[i+2] == '*' && runesHave(i+3, "***"):
				flush(spanNormal); state = spanBoldItalic; i += 3
			case i+1 < n && r == '*' && runes[i+1] == '*' && runesHave(i+2, "**"):
				flush(spanNormal); state = spanBold; i += 2
			case r == '*' && (i+1 >= n || runes[i+1] != ' ') && runesHave(i+1, "*"):
				flush(spanNormal); state = spanItalic; i++
			case r == '`' && runesHave(i+1, "`"):
				flush(spanNormal); state = spanCode; i++
			case i+1 < n && r == '~' && runes[i+1] == '~' && runesHave(i+2, "~~"):
				flush(spanNormal); state = spanStrike; i += 2
			case r == '[':
				if text, skip := matchLink(runes, i); skip > 0 {
					flush(spanNormal)
					out.WriteString(fgWrap(text, ColTool))
					i += skip
				} else {
					buf.WriteRune(r); i++
				}
			default:
				buf.WriteRune(r); i++
			}
		case spanBoldItalic:
			if i+2 < n && r == '*' && runes[i+1] == '*' && runes[i+2] == '*' {
				flush(spanBoldItalic); state = spanNormal; i += 3
			} else {
				buf.WriteRune(r); i++
			}
		case spanBold:
			if i+1 < n && r == '*' && runes[i+1] == '*' {
				flush(spanBold); state = spanNormal; i += 2
			} else {
				buf.WriteRune(r); i++
			}
		case spanItalic:
			if r == '*' && (i+1 >= n || runes[i+1] != '*') {
				flush(spanItalic); state = spanNormal; i++
			} else {
				buf.WriteRune(r); i++
			}
		case spanCode:
			if r == '`' {
				flush(spanCode); state = spanNormal; i++
			} else {
				buf.WriteRune(r); i++
			}
		case spanStrike:
			if i+1 < n && r == '~' && runes[i+1] == '~' {
				flush(spanStrike); state = spanNormal; i += 2
			} else {
				buf.WriteRune(r); i++
			}
		}
	}
	flush(state)
	return out.String()
}

// matchLink matches [text](url) at position start in runes. Returns the link
// text and the number of runes consumed (0 if no match).
func matchLink(runes []rune, start int) (text string, consumed int) {
	n := len(runes)
	if runes[start] != '[' {
		return "", 0
	}
	textEnd := -1
	for i := start + 1; i < n; i++ {
		if runes[i] == ']' {
			textEnd = i
			break
		}
	}
	if textEnd < 0 || textEnd+1 >= n || runes[textEnd+1] != '(' {
		return "", 0
	}
	urlEnd := -1
	for i := textEnd + 2; i < n; i++ {
		if runes[i] == ')' {
			urlEnd = i
			break
		}
	}
	if urlEnd < 0 {
		return "", 0
	}
	return string(runes[start+1 : textEnd]), urlEnd - start + 1
}

// lcsLineDiff returns a line-level LCS diff between old and new.
// Falls back to all-remove + all-add when the input is very large.
func lcsLineDiff(old, new []string) []struct{ kind int; text string } {
	type op = struct{ kind int; text string }
	const opEqual, opRemove, opAdd = 0, 1, 2
	m, n := len(old), len(new)
	if m*n > 60000 {
		ops := make([]op, 0, m+n)
		for _, l := range old { ops = append(ops, op{opRemove, l}) }
		for _, l := range new  { ops = append(ops, op{opAdd, l}) }
		return ops
	}
	dp := make([][]int, m+1)
	for i := range dp { dp[i] = make([]int, n+1) }
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	ops := make([]op, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && old[i-1] == new[j-1] {
			ops = append(ops, op{opEqual, old[i-1]}); i--; j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, op{opAdd, new[j-1]}); j--
		} else {
			ops = append(ops, op{opRemove, old[i-1]}); i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 { ops[l], ops[r] = ops[r], ops[l] }
	return ops
}

// writeEditDiff renders an Edit (str_replace) tool invocation as a LCS-based
// unified diff block. Equal lines are shown without background; only truly
// changed lines are highlighted red (removed) or green (added).
func writeEditDiff(sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int) {
	const maxDiffLines = 40

	borderColor := ColTool
	contentWidth := bWidth - 2

	str := func(key string) string {
		if v, ok := evt.ToolInput[key].(string); ok {
			return sanitizeControlChars(v)
		}
		return ""
	}
	filePath := str("file_path")
	oldStr   := str("old_string")
	newStr   := str("new_string")

	oldLines := splitLines(oldStr)
	newLines := splitLines(newStr)
	ops      := lcsLineDiff(oldLines, newLines)

	// Count actual changes for summary
	nDel, nAdd := 0, 0
	for _, op := range ops {
		switch op.kind {
		case 1: nDel++
		case 2: nAdd++
		}
	}

	// Header
	dispPath := filePath
	if dispPath == "" { dispPath = "unknown" }
	headerText := fmt.Sprintf(" ✏️ %s: %s ", evt.ToolName, dispPath)
	maxHW := bWidth - 6
	if displayWidth(headerText) > maxHW {
		headerText = truncateToWidth(headerText, maxHW)
	}
	fillLen := bWidth - 4 - displayWidth(headerText)
	if fillLen < 1 { fillLen = 1 }
	top := "╔══" + headerText + strings.Repeat("═", fillLen) + "╗"
	sb.WriteString(prefix)
	sb.WriteString(fgWrap(top, borderColor))
	sb.WriteString("\n")

	writeDiffLine := func(content string, fgColor, bgColor Color) {
		body := padRight(content, contentWidth)
		sb.WriteString(prefix)
		sb.WriteString(fgWrap("║", borderColor))
		if bgColor != ColNone {
			sb.WriteString(bgWrap(fgWrap(body, fgColor), bgColor))
		} else {
			sb.WriteString(fgWrap(body, fgColor))
		}
		sb.WriteString(fgWrap("║", borderColor))
		sb.WriteString("\n")
	}

	summary := fmt.Sprintf("  -%d lines, +%d lines", nDel, nAdd)
	writeDiffLine(summary, ColMuted, ColNone)

	shown, truncated := 0, 0
	for _, op := range ops {
		if shown >= maxDiffLines {
			truncated++
			continue
		}
		var (
			sigil   string
			fgColor Color
			bgColor Color
		)
		switch op.kind {
		case 0: // equal
			sigil   = "  "
			fgColor = ColFg
			bgColor = ColNone
		case 1: // remove
			sigil   = "- "
			fgColor = ColFg
			bgColor = ColDiffDel
		case 2: // add
			sigil   = "+ "
			fgColor = ColFg
			bgColor = ColDiffAdd
		}
		content := sigil + op.text
		if displayWidth(content) > contentWidth {
			content = truncateToWidth(content, contentWidth)
		}
		writeDiffLine(content, fgColor, bgColor)
		shown++
	}
	if truncated > 0 {
		writeDiffLine(fmt.Sprintf("  … 省略 %d 行", truncated), ColWarning, ColNone)
	}

	bottom := prefix + fgWrap("╚"+strings.Repeat("═", bWidth-2)+"╝", borderColor)
	sb.WriteString(bottom)
	sb.WriteString("\n")
}

// splitLines splits s on "\n", returning an empty slice for an empty string.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
