package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/bbsteel/session-insight/internal/model"
)

// utf16Len returns the number of UTF-16 code units in s — i.e. the index a
// JavaScript string uses (String.length / slice). The client splices the fold
// badge into the header line at a backend-supplied offset, so the offset must
// be in the client's UTF-16 space, not Go's byte space.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

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

	p := profileFor(events)
	// chrys emits a turn's parallel invocations first and all results after;
	// pair each invocation with its result so a per-tool fold covers input+output.
	if p.ToolBullet {
		events = pairToolRuns(events)
	}
	groupStarts := computeToolRuns(p, events)

	tb := newTrackingBuilder(cols)
	var positions []RenderPosition
	prevTurnIndex := -1
	editSeqByTurn := make(map[int]int)
	agentLabel := ""
	// prevDepth0Type tracks the previous depth-0 event kind so the assistant
	// header ("◇ <label>") is emitted once per contiguous text block, not
	// once per TextChunk.
	prevDepth0Type := ""

	// openFold tracks the tool run currently being rendered so its body's
	// line extent (display rows AND logical lines, both needed by the
	// client-side fold composition) can be recorded when the run ends.
	type openFoldState struct {
		run            toolRun
		turnIndex      int
		key            string
		headerDisplay  int
		headerLogical  int
		bodyDisplay    int
		bodyLogical    int
	}
	var openFold *openFoldState
	closeFold := func() {
		if openFold == nil {
			return
		}
		f := openFold
		openFold = nil
		if tb.CurrentLine() <= f.bodyDisplay {
			return // empty body — nothing to fold
		}
		endIncl := tb.CurrentLine() - 1
		positions = append(positions, RenderPosition{
			PositionKey: f.key,
			Kind:        "fold",
			TurnIndex:   f.turnIndex,
			LineStart:   f.headerDisplay,
			LineEnd:     &endIncl,
			Label:       fmt.Sprintf("Tools (%d/%d)", f.run.succeeded, f.run.total),
			Payload: map[string]any{
				"level":          "group",
				"display_start":  float64(f.bodyDisplay),
				"display_end":    float64(tb.CurrentLine()), // exclusive
				"logical_start":  float64(f.bodyLogical),
				"logical_end":    float64(tb.CurrentLogicalLine()), // exclusive
				"header_logical": float64(f.headerLogical),
			},
		})
	}

	// openToolFold tracks the single tool currently being rendered inside a
	// group so its own body (input box + output box) can be folded independently
	// of the group — the collapsed default hides each tool's output/input box
	// while the compact "▼ • Name summary" header line stays visible.
	type toolFoldState struct {
		turnIndex     int
		key           string
		groupKey      string
		headerDisplay int
		headerLogical int
		bodyDisplay   int
		bodyLogical   int
		badgeOffset   int // UTF-16 index in the header line where the "(N 行)" badge goes
	}
	var openToolFold *toolFoldState
	closeToolFold := func() {
		if openToolFold == nil {
			return
		}
		f := openToolFold
		openToolFold = nil
		if tb.CurrentLine() <= f.bodyDisplay {
			return // header only, no body — nothing to fold
		}
		endIncl := tb.CurrentLine() - 1
		positions = append(positions, RenderPosition{
			PositionKey: f.key,
			Kind:        "fold",
			TurnIndex:   f.turnIndex,
			LineStart:   f.headerDisplay,
			LineEnd:     &endIncl,
			Label:       "tool",
			Payload: map[string]any{
				"level":          "tool",
				"group_key":      f.groupKey,
				"display_start":  float64(f.bodyDisplay),
				"display_end":    float64(tb.CurrentLine()), // exclusive
				"logical_start":  float64(f.bodyLogical),
				"logical_end":    float64(tb.CurrentLogicalLine()), // exclusive
				"header_logical": float64(f.headerLogical),
				"badge_offset":   float64(f.badgeOffset),
			},
		})
	}

	// truncSeq numbers truncated output segments in document order; the
	// /tool-outputs endpoint (render.CollectTruncatedOutputs) enumerates the
	// same segments in the same order, so payload.output_index is a direct
	// index into its response.
	truncSeq := 0
	makeOnTrunc := func(turnIndex int) func() {
		return func() {
			positions = append(positions, RenderPosition{
				PositionKey: fmt.Sprintf("trunc:%d:%d", turnIndex, tb.CurrentLine()),
				Kind:        "trunc",
				TurnIndex:   turnIndex,
				LineStart:   tb.CurrentLine(),
				Label:       "输出截断",
				Payload:     map[string]any{"output_index": float64(truncSeq)},
			})
			truncSeq++
		}
	}

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

	for i, evt := range events {
		if openFold != nil && i >= openFold.run.endIdx {
			closeToolFold() // close the group's last tool before the group itself
			closeFold()
		}

		if evt.TurnIndex != prevTurnIndex {
			closeToolFold() // never let a tool fold span a turn boundary
			emit("turn", fmt.Sprintf("Turn %d", evt.TurnIndex), "", evt.TurnIndex, nil)
			writeSeparator(tb, evt.TurnIndex, cols)
			prevTurnIndex = evt.TurnIndex
			prevDepth0Type = ""
		}

		if g, ok := groupStarts[i]; ok {
			// Vertical spacing before the group header.
			if p.ToolBullet {
				tb.WriteString("\n")
			}
			headerDisplay := tb.CurrentLine()
			headerLogical := tb.CurrentLogicalLine()
			writeToolGroupHeader(p, tb, g)
			openFold = &openFoldState{
				run:           g,
				turnIndex:     evt.TurnIndex,
				key:           fmt.Sprintf("fold:%d:%d", evt.TurnIndex, headerDisplay),
				headerDisplay: headerDisplay,
				headerLogical: headerLogical,
				bodyDisplay:   tb.CurrentLine(),
				bodyLogical:   tb.CurrentLogicalLine(),
			}
		}

		prefix := depthPrefix(evt.Depth)
		// Indent a group's depth-0 tools two columns under the "▼ Tools" header
		// so the per-tool fold rows read as children of the group.
		if p.ToolBullet && openFold != nil && evt.Depth == 0 &&
			(evt.Type == "ToolInvocation" || evt.Type == "ToolResult") {
			prefix = "  " + prefix
		}

		switch evt.Type {
		case "TurnBoundary":
			if evt.Metadata != nil {
				if l, ok := evt.Metadata["agent_label"].(string); ok && l != "" {
					agentLabel = l
				}
			}
		case "UserPrompt":
			emit("user", "用户输入", "", evt.TurnIndex, nil)
			writeUserPrompt(p, tb, evt, prefix)
		case "ThinkingStart":
			writeThinking(tb, evt, prefix)
		case "ThinkingChunk":
			writeThinking(tb, evt, prefix)
		case "ThinkingEnd":
		case "TextChunk":
			if p.AssistantHeader && evt.Depth == 0 && prevDepth0Type != "TextChunk" {
				if prevDepth0Type != "" {
					tb.WriteString("\n")
				}
				writeAssistantHeader(tb, agentLabel)
			}
			writeTextChunk(tb, evt, prefix, cols)
		case "ToolInvocation":
			onEdit := func(filePath string) {
				seq := editSeqByTurn[evt.TurnIndex]
				editSeqByTurn[evt.TurnIndex]++
				emit("edit", filePath, "", evt.TurnIndex, map[string]any{"edit_seq": float64(seq)})
			}
			// Each non-edit tool inside a group folds independently: its compact
			// "▼ • Name summary" line stays visible while the input/output boxes
			// (the byte-heavy part) collapse by default. Edit tools render as
			// diffs (no bullet header) and are left unfolded.
			//
			// Only a depth-0 tool ends the previous one; nested subagent tools
			// (depth>0) stay inside their parent Agent's fold so collapsing it
			// hides the whole sub-transcript.
			if evt.Depth == 0 {
				closeToolFold()
			}
			// Per-tool folds need the bullet line as their "▼ …" header, so
			// they're limited to bullet profiles (chrys). Non-bullet profiles
			// (claude/codex) keep the existing group-only fold.
			if openFold != nil && evt.Depth == 0 && p.ToolBullet && !model.IsEditTool(evt.ToolName) {
				headerDisplay := tb.CurrentLine()
				headerLogical := tb.CurrentLogicalLine()
				bodyD, bodyL, badgeOff := writeToolInvocation(p, tb, evt, prefix, bWidth, onEdit, true)
				openToolFold = &toolFoldState{
					turnIndex:     evt.TurnIndex,
					key:           fmt.Sprintf("tfold:%d:%d", evt.TurnIndex, headerDisplay),
					groupKey:      openFold.key,
					headerDisplay: headerDisplay,
					headerLogical: headerLogical,
					bodyDisplay:   bodyD,
					bodyLogical:   bodyL,
					badgeOffset:   badgeOff,
				}
			} else {
				writeToolInvocation(p, tb, evt, prefix, bWidth, onEdit, false)
			}
		case "ToolResult":
			if evt.ExitCode != 0 || evt.Stderr != "" {
				emit("error", "工具错误", "error", evt.TurnIndex, map[string]any{"tool": evt.ToolName})
			}
			writeToolResult(p, tb, evt, prefix, bWidth, makeOnTrunc(evt.TurnIndex))
		case "CompactionBoundary":
			emit("compaction", "压缩", "", evt.TurnIndex, nil)
		case "AgentSpecific":
			writeAgentSpecific(p, tb, evt, prefix)
		}

		if evt.Depth == 0 && evt.Type != "TurnBoundary" && evt.Type != "ThinkingEnd" {
			prevDepth0Type = evt.Type
		}
	}
	closeToolFold()
	closeFold()

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

// FormatVersion increments whenever the ANSI layout changes in a way that
// shifts line numbers, so cached line positions keyed on it are invalidated.
const FormatVersion int64 = 19

// pairToolRuns reorders each contiguous tool run so every depth-0
// ToolInvocation is immediately followed by its matching ToolResult (and the
// nested sub-agent block emitted just before that result). chrys emits all of a
// turn's parallel invocations first and all results after; pairing them makes
// each tool a self-contained input→output unit so per-tool folds cover both.
func pairToolRuns(events []model.RenderEvent) []model.RenderEvent {
	out := make([]model.RenderEvent, 0, len(events))
	i := 0
	for i < len(events) {
		if !isToolRunMember(events[i]) {
			out = append(out, events[i])
			i++
			continue
		}
		start := i
		turn := events[i].TurnIndex
		for i < len(events) && isToolRunMember(events[i]) && events[i].TurnIndex == turn {
			i++
		}
		out = append(out, pairRun(events[start:i])...)
	}
	return out
}

// pairRun reorders one run: [inv1 inv2 … res1 res2 …] → [inv1 res1 inv2 res2 …].
// Depth>0 / AgentSpecific events (a spliced sub-agent transcript) accumulate
// into the block of the result they precede. Results with no matching
// invocation and any trailing remainder keep their order at the end so nothing
// is dropped or reordered across turns.
func pairRun(run []model.RenderEvent) []model.RenderEvent {
	order := make([]string, 0, len(run))
	invByID := make(map[string]model.RenderEvent, len(run))
	blockByID := make(map[string][]model.RenderEvent, len(run))
	var pending []model.RenderEvent
	var leftover []model.RenderEvent

	for _, e := range run {
		switch {
		case e.Depth == 0 && e.Type == "ToolInvocation":
			if len(pending) > 0 { // orphan block with no owning result
				leftover = append(leftover, pending...)
				pending = nil
			}
			invByID[e.ToolCallID] = e
			order = append(order, e.ToolCallID)
		case e.Depth == 0 && e.Type == "ToolResult":
			block := make([]model.RenderEvent, 0, len(pending)+1)
			block = append(block, pending...)
			block = append(block, e)
			pending = nil
			if _, ok := invByID[e.ToolCallID]; ok {
				blockByID[e.ToolCallID] = block
			} else {
				leftover = append(leftover, block...)
			}
		default:
			pending = append(pending, e)
		}
	}
	leftover = append(leftover, pending...)

	out := make([]model.RenderEvent, 0, len(run))
	for _, id := range order {
		out = append(out, invByID[id])
		out = append(out, blockByID[id]...)
	}
	return append(out, leftover...)
}

// toolRun summarizes one contiguous run of tool events for the group header.
// endIdx is the index just past the run's last event.
type toolRun struct {
	total     int
	succeeded int
	endIdx    int
	// stats counts invocations per category (search/read/edit/shell/agent/
	// tool) for GroupHeaderStats profiles; nil otherwise.
	stats map[string]int
}

// statOrder fixes the header's category ordering.
var statOrder = []string{"search", "read", "edit", "shell", "agent", "tool"}

func toolCategory(name string) string {
	switch name {
	case "Grep", "Glob", "WebSearch", "grep", "glob", "search":
		return "search"
	case "Read", "read_file", "ReadFile", "read":
		return "read"
	case "Bash", "bash", "shell", "run_shell_command", "BashOutput":
		return "shell"
	case "Task", "Agent":
		return "agent"
	}
	if model.IsEditTool(name) || name == "Write" || name == "write_file" || name == "NotebookEdit" {
		return "edit"
	}
	return "tool"
}

func formatRunStats(stats map[string]int) string {
	parts := make([]string, 0, len(stats))
	for _, cat := range statOrder {
		if n := stats[cat]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, cat))
		}
	}
	return strings.Join(parts, " · ")
}

// isToolRunMember reports whether an event belongs to a tool run: tool
// invocations/results, anything nested (spliced sub-agent transcripts sit
// between their invocation and summary result), and sub-agent bookkeeping.
func isToolRunMember(e model.RenderEvent) bool {
	if e.Depth > 0 {
		return true
	}
	switch e.Type {
	case "ToolInvocation", "ToolResult":
		return true
	case "AgentSpecific":
		switch e.Subtype {
		case "subagent_started", "subagent_summary", "subagent_load_error":
			return true
		}
	}
	return false
}

// computeToolRuns pre-scans the event stream and returns, for each index that
// starts a maximal contiguous tool run (within one turn), that run's summary.
func computeToolRuns(p *Profile, events []model.RenderEvent) map[int]toolRun {
	if !p.GroupToolRuns {
		return nil
	}
	starts := make(map[int]toolRun)
	i := 0
	for i < len(events) {
		if !isToolRunMember(events[i]) {
			i++
			continue
		}
		start := i
		turn := events[i].TurnIndex
		var run toolRun
		for i < len(events) && isToolRunMember(events[i]) && events[i].TurnIndex == turn {
			e := events[i]
			if e.Depth == 0 {
				switch e.Type {
				case "ToolInvocation":
					run.total++
					if p.GroupHeaderStats {
						if run.stats == nil {
							run.stats = make(map[string]int)
						}
						run.stats[toolCategory(e.ToolName)]++
					}
				case "ToolResult":
					if e.ExitCode == 0 && e.Stderr == "" {
						run.succeeded++
					}
				}
			}
			i++
		}
		if run.total > 0 {
			run.endIdx = i
			starts[start] = run
		}
	}
	return starts
}

func writeToolGroupHeader(p *Profile, sb *trackingBuilder, g toolRun) {
	label := fmt.Sprintf("▼ Tools (%d/%d)", g.succeeded, g.total)
	if p.GroupHeaderStats && len(g.stats) > 0 {
		label += " · " + formatRunStats(g.stats)
	}
	sb.WriteString(styled(label, ColWarning, ColNone, true, false))
	sb.WriteString("\n")
}

func writeAssistantHeader(sb *trackingBuilder, label string) {
	if label == "" {
		label = "Agent"
	}
	sb.WriteString(styled("◇ "+sanitizeControlChars(label), ColSuccess, ColNone, false, false))
	sb.WriteString("\n")
}

func writeSeparator(sb *trackingBuilder, turnIdx int, termWidth int) {
	// A turn start must be findable at a glance when scrolling or after a
	// jump: solid inverse-video badge followed by a heavy rule, instead of
	// the earlier single muted line that blended into the transcript.
	label := fmt.Sprintf(" Turn %d ", turnIdx)
	rest := termWidth - len(label)
	if rest < 1 {
		rest = 1
	}
	sb.WriteString("\n")
	// No bold on the badge: xterm's drawBoldTextInBrightColors would remap the
	// fg slot to its bright variant, which this palette repurposes (slot 7→15
	// is the Claude-blue bold fg) — that once made the label invisible on its
	// own background. ColBg as fg guarantees contrast on any accent color.
	sb.WriteString(styled(label, ColBg, ColBanner, false, false))
	sb.WriteString(fgWrap(strings.Repeat("━", rest), ColBanner))
	sb.WriteString("\n")
}

func writeUserPrompt(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string) {
	if p.UserHeader != "" {
		sb.WriteString(prefix)
		sb.WriteString(styled(p.UserHeader, ColUser, ColNone, true, false))
		sb.WriteString("\n")
		for _, line := range strings.Split(sanitizeControlChars(evt.Text), "\n") {
			sb.WriteString(prefix)
			sb.WriteString(fgWrap(line, ColFg))
			sb.WriteString("\n")
		}
		return
	}
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
	text := sanitizeControlChars(evt.Text)
	lines := strings.Split(text, "\n")

	// A pasted unified diff (carries "@@ … @@" hunk markers) is a distinct
	// content type, not Markdown: it is rendered line-by-line with add/del
	// backgrounds, bypassing the Markdown parser (which would otherwise grab
	// its "- " deletion lines as a bullet list). See scanForDiff.
	if scanForDiff(lines) {
		for i, line := range lines {
			sb.WriteString(prefix)
			switch {
			case isDiffAdd(line):
				sb.WriteString(bgWrap(fgWrap(padRight(line, termWidth), ColFg), ColDiffAdd))
			case isDiffDel(line):
				sb.WriteString(bgWrap(fgWrap(padRight(line, termWidth), ColFg), ColDiffDel))
			default:
				sb.WriteString(fgWrap(line, ColFg))
			}
			if i < len(lines)-1 {
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
		return
	}

	for _, line := range renderMarkdownDoc(text, ColFg, termWidth) {
		sb.WriteString(prefix)
		sb.WriteString(line)
		sb.WriteString("\n")
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

// writeBoxTop draws the box top border with an optional embedded header.
// Guard against headers long enough to overflow the box width: a naive
// bWidth-4-len(header) goes negative and strings.Repeat panics. Truncate
// the header itself rather than crash the whole render. Must truncate and
// measure by display width, not rune count: CJK-heavy headers have a small
// rune count while already exceeding the box width in terminal columns.
func writeBoxTop(p *Profile, sb *trackingBuilder, prefix, header string, bWidth int, borderColor Color) {
	if header != "" {
		maxHeaderWidth := bWidth - 6 // room for corner+2 fill chars each side
		if displayWidth(header) > maxHeaderWidth {
			header = truncateToWidth(header, maxHeaderWidth)
		}
	}
	fillLen := bWidth - 4 - displayWidth(header)
	if fillLen < 1 {
		fillLen = 1
	}
	top := p.BoxTL + p.BoxH + p.BoxH + header + strings.Repeat(p.BoxH, fillLen) + p.BoxTR
	sb.WriteString(prefix)
	sb.WriteString(fgWrap(top, borderColor))
	sb.WriteString("\n")
}

// writeBoxBottom draws the box bottom border with an optional right-aligned
// footer label (e.g. " Completed ").
func writeBoxBottom(p *Profile, sb *trackingBuilder, prefix, footer string, bWidth int, borderColor Color) {
	inner := bWidth - 2
	var body string
	if footer != "" {
		maxFooterWidth := inner - 4
		if displayWidth(footer) > maxFooterWidth {
			footer = truncateToWidth(footer, maxFooterWidth)
		}
		fillLen := inner - 2 - displayWidth(footer)
		if fillLen < 0 {
			fillLen = 0
		}
		body = strings.Repeat(p.BoxH, fillLen) + footer + strings.Repeat(p.BoxH, 2)
	} else {
		body = strings.Repeat(p.BoxH, inner)
	}
	sb.WriteString(prefix)
	sb.WriteString(fgWrap(p.BoxBL+body+p.BoxBR, borderColor))
	sb.WriteString("\n")
}

// writeBoxRow draws one wrapped content row inside the box borders.
func writeBoxRow(p *Profile, sb *trackingBuilder, prefix, content string, bWidth int, borderColor, contentColor Color) {
	contentWidth := bWidth - 2
	for _, wl := range wrapInBox(content, contentWidth) {
		wl = padRight(wl, contentWidth)
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(p.BoxV, borderColor))
		sb.WriteString(fgWrap(wl, contentColor))
		sb.WriteString(fgWrap(p.BoxV, borderColor))
		sb.WriteString("\n")
	}
}

// promoteBoxHeader extracts a human-readable purpose argument (reason /
// description / title) from the tool input to embed in the box header,
// returning the header text and the input with that key removed.
func promoteBoxHeader(input map[string]any) (string, map[string]any) {
	for _, key := range []string{"reason", "description", "title"} {
		if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
			rest := make(map[string]any, len(input)-1)
			for k, val := range input {
				if k != key {
					rest[k] = val
				}
			}
			return v, rest
		}
	}
	return "", input
}

// writeToolInvocation renders a tool's input. When asFoldHeader is true (a
// non-edit tool inside a group) the bullet line becomes the fold header
// "▼ • Name summary" and the returned (bodyDisplay, bodyLogical) mark the line
// just past it — the caller folds [body … tool end) so only the header shows
// when collapsed. Edit tools ignore asFoldHeader (they render diffs, no bullet)
// and return (0, 0).
func writeToolInvocation(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, onEditStart func(filePath string), asFoldHeader bool) (bodyDisplay, bodyLogical, headerBadgeOffset int) {
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
				writeEditDiff(p, sb, syn, prefix, bWidth)
			}
			if len(calls) > 0 {
				return 0, 0, 0
			}
			// Empty or malformed patch: fall through to generic tool box.
		} else {
			filePath, _ := evt.ToolInput["file_path"].(string)
			if onEditStart != nil {
				onEditStart(filePath)
			}
			writeEditDiff(p, sb, evt, prefix, bWidth)
			return 0, 0, 0
		}
	}

	borderColor := ColTool
	if evt.ToolName == "Agent" || evt.ToolName == "Task" {
		borderColor = ColSubagent
	}

	toolName := sanitizeControlChars(evt.ToolName)
	header := fmt.Sprintf(" Tool: %s ", toolName)
	input := evt.ToolInput

	if p.ToolBullet {
		var purpose string
		if purpose, input = promoteBoxHeader(evt.ToolInput); purpose != "" {
			header = " " + sanitizeControlChars(purpose) + " "
		} else {
			header = ""
		}
		sb.WriteString(prefix)
		if asFoldHeader {
			// Emit the name and the summary as two independent SGR runs so the
			// client can splice the fold's "(N 行)" badge between them without any
			// knowledge of this profile's byte shape: headerBadgeOffset marks the
			// UTF-16 index just past the name run (right after its trailing reset),
			// which is where the badge goes. Keeping name/summary in separate runs
			// means the badge needs no style reopen — the summary run restyles
			// itself. This is the ONLY place that encodes "where chrys's tool
			// header ends"; the client stays profile-agnostic.
			nameRun := styled("▼ • "+toolName, ColFg, ColNone, true, false)
			sb.WriteString(nameRun)
			headerBadgeOffset = utf16Len(prefix) + utf16Len(nameRun)
			if s := toolSummary(purpose, evt.ToolInput); s != "" {
				sb.WriteString(styled("  "+s, ColFg, ColNone, true, false))
			}
		} else {
			sb.WriteString(styled("• "+toolName, ColFg, ColNone, true, false))
		}
		sb.WriteString("\n")
	}

	// Body starts after the (possibly wrapped) header line: everything from
	// here to the tool's end is what a collapsed fold hides.
	bodyDisplay = sb.CurrentLine()
	bodyLogical = sb.CurrentLogicalLine()

	inputLines := formatToolInput(input)

	writeBoxTop(p, sb, prefix, header, bWidth, borderColor)
	for _, il := range inputLines {
		writeBoxRow(p, sb, prefix, il, bWidth, borderColor, ColFg)
	}
	writeBoxBottom(p, sb, prefix, "", bWidth, borderColor)
	return bodyDisplay, bodyLogical, headerBadgeOffset
}

// toolSummary returns a compact one-line description for a tool's fold header:
// the promoted purpose when present, else the most salient input argument
// (command / path / pattern / …), truncated so the header rarely soft-wraps.
func toolSummary(purpose string, input map[string]any) string {
	// Not width-truncated: the full command/path stays on the header (it soft-
	// wraps if long) so the collapsed row shows the whole invocation.
	if s := oneLine(purpose); s != "" {
		return sanitizeControlChars(s)
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "query", "url", "prompt", "description"} {
		if v, ok := input[k].(string); ok {
			if s := oneLine(v); s != "" {
				return sanitizeControlChars(s)
			}
		}
	}
	return ""
}

// oneLine collapses s to its first non-empty line, trimmed.
func oneLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
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

func writeToolResult(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, onTrunc func()) {
	ok := evt.ExitCode == 0 && evt.Stderr == ""

	if p.ResultBox {
		writeToolResultBox(p, sb, evt, prefix, bWidth, ok, onTrunc)
		return
	}

	// The checkmark/cross gets its own line, ending in "\n", rather than
	// being followed directly by the first output line on the same line.
	// The latter (the original draft's behavior) duplicated the depth
	// prefix mid-line at Depth>0: formatToolOutput also writes `prefix`
	// before its first line, so depth>0 output rendered as
	// "│ ✓ │ first output line" — the branch marker appearing twice on one
	// visual line.
	sb.WriteString(prefix)
	if ok {
		sb.WriteString(fgWrap("✓", ColSuccess))
	} else {
		sb.WriteString(fgWrap("✗", ColError))
	}
	sb.WriteString("\n")

	if evt.Stdout != "" {
		writeToolOutputFlat(sb, evt.Stdout, prefix, false, onTrunc)
	}
	if evt.Stderr != "" {
		sb.WriteString(prefix)
		sb.WriteString(fgWrap("stderr:\n", ColWarning))
		writeToolOutputFlat(sb, evt.Stderr, prefix, true, onTrunc)
	}
	sb.WriteString("\n")
}

// writeToolResultBox renders a tool result as a bordered "Output" box with a
// Completed/Failed footer (chrys-native layout). Output-less results collapse
// to a single status line so the transcript doesn't fill with empty boxes.
func writeToolResultBox(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, ok bool, onTrunc func()) {
	borderColor := ColSuccess
	footer := " Completed "
	if !ok {
		borderColor = ColError
		footer = " Failed "
	}

	if evt.Stdout == "" && evt.Stderr == "" {
		sb.WriteString(prefix)
		if ok {
			sb.WriteString(fgWrap("✓ Completed", ColSuccess))
		} else {
			sb.WriteString(fgWrap("✗ Failed", ColError))
		}
		sb.WriteString("\n\n")
		return
	}

	writeBoxTop(p, sb, prefix, " Output ", bWidth, borderColor)

	writeLines := func(content string, contentColor Color) {
		lines := strings.Split(sanitizeControlChars(content), "\n")
		if len(lines) > maxStdoutLines {
			remaining := len(lines) - maxStdoutLines
			lines = lines[:maxStdoutLines]
			for _, l := range lines {
				writeBoxRow(p, sb, prefix, l, bWidth, borderColor, contentColor)
			}
			if onTrunc != nil {
				onTrunc()
			}
			// Same truncation copy as the flat layout: the expand affordance
			// matches on this exact text.
			writeBoxRow(p, sb, prefix, fmt.Sprintf("[+] %d 行被截断（点击展开）", remaining), bWidth, borderColor, ColWarning)
			return
		}
		for _, l := range lines {
			writeBoxRow(p, sb, prefix, l, bWidth, borderColor, contentColor)
		}
	}

	if evt.Stdout != "" {
		writeLines(evt.Stdout, ColFg)
	}
	if evt.Stderr != "" {
		writeLines(evt.Stderr, ColError)
	}

	writeBoxBottom(p, sb, prefix, footer, bWidth, borderColor)
	sb.WriteString("\n")
}

// writeToolOutputFlat writes tool output through the tracking builder
// (byte-identical to the old string-building formatToolOutput) so the
// truncation note's display row can be recorded via onTrunc at write time.
func writeToolOutputFlat(sb *trackingBuilder, content string, prefix string, isError bool, onTrunc func()) {
	lines := strings.Split(sanitizeControlChars(content), "\n")
	color := ColFg
	if isError {
		color = ColError
	}

	if len(lines) > maxStdoutLines {
		shown := lines[:maxStdoutLines]
		remaining := len(lines) - maxStdoutLines

		for _, line := range shown {
			sb.WriteString(prefix)
			sb.WriteString(fgWrap(line, color))
			sb.WriteString("\n")
		}
		if onTrunc != nil {
			onTrunc()
		}
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("[+] %d 行被截断（点击展开）", remaining), ColWarning))
		sb.WriteString("\n")
		return
	}

	for _, line := range lines {
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(line, color))
		sb.WriteString("\n")
	}
}

// CollectTruncatedOutputs enumerates, in document order, every tool output
// segment the formatter truncates ("[+] N 行被截断" lines). The order matches
// the "trunc" positions' payload.output_index exactly: per ToolResult event,
// stdout first, then stderr, each truncated independently when it exceeds
// maxStdoutLines.
type TruncatedOutput struct {
	ToolName  string `json:"tool_name"`
	Kind      string `json:"kind"` // "stdout" | "stderr"
	TurnIndex int    `json:"turn_index"`
	Content   string `json:"content"`
}

func CollectTruncatedOutputs(events []model.RenderEvent) []TruncatedOutput {
	names := make(map[string]string)
	var out []TruncatedOutput
	overflow := func(content string) bool {
		return content != "" && len(strings.Split(sanitizeControlChars(content), "\n")) > maxStdoutLines
	}
	for _, e := range events {
		if e.Type == "ToolInvocation" && e.ToolCallID != "" {
			names[e.ToolCallID] = e.ToolName
		}
		if e.Type != "ToolResult" {
			continue
		}
		name := e.ToolName
		if name == "" {
			name = names[e.ToolCallID]
		}
		if overflow(e.Stdout) {
			out = append(out, TruncatedOutput{ToolName: name, Kind: "stdout", TurnIndex: e.TurnIndex, Content: e.Stdout})
		}
		if overflow(e.Stderr) {
			out = append(out, TruncatedOutput{ToolName: name, Kind: "stderr", TurnIndex: e.TurnIndex, Content: e.Stderr})
		}
	}
	return out
}

func writeAgentSpecific(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string) {
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
		if p.SubagentBadge {
			// Bold is safe here: slot 6's bright pair (14) mirrors it in the
			// default themes, and agent skins must keep 6/14 in sync.
			sb.WriteString(styled(fmt.Sprintf("◉ %s", sanitizeControlChars(evt.Text)), ColSubagent, ColNone, true, false))
		} else {
			sb.WriteString(fgWrap(fmt.Sprintf("@ %s", sanitizeControlChars(evt.Text)), ColSubagent))
		}
		sb.WriteString("\n")
	case "subagent_summary":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(sanitizeControlChars(evt.Text), ColMuted))
		sb.WriteString("\n")
	case "model_change":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("↪ model: %s", sanitizeControlChars(evt.Text)), ColWarning))
		sb.WriteString("\n")
	case "interrupted":
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(fmt.Sprintf("⚠ 中断: %s", sanitizeControlChars(evt.Text)), ColError))
		sb.WriteString("\n")
	case "in_progress":
		// A turn still running (chrys in-flight checkpoint). Neutral, not an
		// error; two leading spaces reserve a cell for the frontend's spinning
		// hourglass overlay (raw render without a frontend still reads fine).
		sb.WriteString(prefix)
		sb.WriteString(fgWrap("  推理中…", ColMuted))
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
func writeEditDiff(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int) {
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

	// Header. The "✏️ <tool>: <path>" shape is load-bearing: the frontend's
	// clickable diff affordance (parseEditHeaderLine) matches on it.
	dispPath := filePath
	if dispPath == "" { dispPath = "unknown" }
	headerText := fmt.Sprintf(" ✏️ %s: %s ", evt.ToolName, dispPath)
	writeBoxTop(p, sb, prefix, headerText, bWidth, borderColor)

	writeDiffLine := func(content string, fgColor, bgColor Color) {
		body := padRight(content, contentWidth)
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(p.BoxV, borderColor))
		if bgColor != ColNone {
			sb.WriteString(bgWrap(fgWrap(body, fgColor), bgColor))
		} else {
			sb.WriteString(fgWrap(body, fgColor))
		}
		sb.WriteString(fgWrap(p.BoxV, borderColor))
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

	writeBoxBottom(p, sb, prefix, "", bWidth, borderColor)
}

// splitLines splits s on "\n", returning an empty slice for an empty string.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
