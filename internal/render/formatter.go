package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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

// FormatEventsOpts is FormatEvents with per-request render options.
func FormatEventsOpts(events []model.RenderEvent, cols int, opts Options) string {
	ansi, _ := FormatEventsWithPositionsOpts(events, cols, opts)
	return ansi
}

// FormatEventsWithPositions renders events as ANSI text and simultaneously
// records terminal line positions for each significant event kind.
// Returns the ANSI string and a slice of positions ordered by line_start.
func FormatEventsWithPositions(events []model.RenderEvent, cols int) (string, []RenderPosition) {
	return FormatEventsWithPositionsOpts(events, cols, Options{})
}

// FormatEventsWithPositionsOpts is FormatEventsWithPositions with per-request
// render options. Options change line layout, so callers caching either
// return value must include opts.Mask() in their cache key.
func FormatEventsWithPositionsOpts(events []model.RenderEvent, cols int, opts Options) (string, []RenderPosition) {
	if cols <= 0 {
		cols = TermWidth
	}
	bWidth := cols - 4
	if bWidth < 40 {
		bWidth = 40
	}

	p := profileFor(events)
	if p.Name == "grok" {
		computeGrokThoughtSummaries(events)
	}
	// chrys emits a turn's parallel invocations first and all results after;
	// pair each invocation with its result so a per-tool fold covers input+output.
	if p.ToolBullet {
		events = pairToolRuns(events)
	}
	groupStarts := computeToolRuns(p, events)
	outcomes := computeToolOutcomes(events)

	// tsFor formats an event's timestamp for inline display when the
	// corresponding Options flag is on; "" means "don't render one".
	tsFor := func(evt model.RenderEvent, enabled bool) string {
		if !enabled || evt.Timestamp.IsZero() {
			return ""
		}
		return evt.Timestamp.Local().Format("15:04:05")
	}

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
		run           toolRun
		turnIndex     int
		key           string
		headerDisplay int
		headerLogical int
		bodyDisplay   int
		bodyLogical   int
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

	// Rollback folds surround turns that remain in Codex's append-only log but
	// were removed from the resumable conversation. Unlike tool folds these
	// may contain whole turns (and, after repeated rewinds, nested rollback
	// folds), so keep a stack and let the frontend de-nest collapsed ranges.
	type rollbackFoldState struct {
		turnIndex     int
		key           string
		headerDisplay int
		headerLogical int
		bodyDisplay   int
		bodyLogical   int
		count         int
	}
	var rollbackFolds []rollbackFoldState
	closeRollbackFold := func() {
		if len(rollbackFolds) == 0 {
			return
		}
		f := rollbackFolds[len(rollbackFolds)-1]
		rollbackFolds = rollbackFolds[:len(rollbackFolds)-1]
		if tb.CurrentLine() <= f.bodyDisplay {
			return
		}
		endIncl := tb.CurrentLine() - 1
		positions = append(positions, RenderPosition{
			PositionKey: f.key,
			Kind:        "fold",
			TurnIndex:   f.turnIndex,
			LineStart:   f.headerDisplay,
			LineEnd:     &endIncl,
			Label:       fmt.Sprintf("已回滚 %d turns", f.count),
			Payload: map[string]any{
				"level":          "rollback",
				"display_start":  float64(f.bodyDisplay),
				"display_end":    float64(tb.CurrentLine()),
				"logical_start":  float64(f.bodyLogical),
				"logical_end":    float64(tb.CurrentLogicalLine()),
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
	var openGrokThoughtFold *toolFoldState
	grokSidebar := ColNone
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
		if p.Name == "grok" {
			grokSidebar = ColNone // reset after tool block
		}
	}

	closeGrokThoughtFold := func() {
		if openGrokThoughtFold == nil {
			return
		}
		f := openGrokThoughtFold
		openGrokThoughtFold = nil
		if tb.CurrentLine() <= f.bodyDisplay {
			return
		}
		endIncl := tb.CurrentLine() - 1
		positions = append(positions, RenderPosition{
			PositionKey: f.key,
			Kind:        "fold",
			TurnIndex:   f.turnIndex,
			LineStart:   f.headerDisplay,
			LineEnd:     &endIncl,
			Label:       "thought",
			Payload: map[string]any{
				"level":          "thought",
				"group_key":      f.groupKey,
				"display_start":  float64(f.bodyDisplay),
				"display_end":    float64(tb.CurrentLine()),
				"logical_start":  float64(f.bodyLogical),
				"logical_end":    float64(tb.CurrentLogicalLine()),
				"header_logical": float64(f.headerLogical),
			},
		})
		grokSidebar = ColNone // reset after thought block to prevent sidebar leak to following text
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
				Payload:     map[string]any{"output_index": float64(truncSeq), "logical_start": float64(tb.CurrentLogicalLine())},
			})
			truncSeq++
		}
	}

	emit := func(kind, label, severity string, turnIndex int, payload map[string]any) {
		lineStart := tb.CurrentLine()
		key := fmt.Sprintf("%s:%d:%d", kind, turnIndex, lineStart)
		// logical_start lets the client resolve the position to a buffer row
		// via xterm's own wrap state (non-wrapped row count) instead of
		// predicting soft wraps — display rows drift once fold badges change
		// header widths, logical lines never do.
		if payload == nil {
			payload = map[string]any{}
		}
		payload["logical_start"] = float64(tb.CurrentLogicalLine())
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
			closeGrokThoughtFold()
			closeFold()
		}

		closeGrokThoughtFold()

		if evt.Type == "RollbackStart" {
			closeToolFold()
			closeGrokThoughtFold()
			closeFold()
			count := metadataInt(evt.Metadata, "count")
			resumeTurn := metadataInt(evt.Metadata, "resume_turn")
			headerDisplay, headerLogical := writeRollbackHeader(tb, count, resumeTurn)
			rollbackFolds = append(rollbackFolds, rollbackFoldState{
				turnIndex:     evt.TurnIndex,
				key:           fmt.Sprintf("rollback:%d:%d", evt.TurnIndex, headerDisplay),
				headerDisplay: headerDisplay,
				headerLogical: headerLogical,
				bodyDisplay:   tb.CurrentLine(),
				bodyLogical:   tb.CurrentLogicalLine(),
				count:         count,
			})
			prevDepth0Type = ""
			continue
		}
		if evt.Type == "RollbackEnd" {
			closeToolFold()
			closeGrokThoughtFold()
			closeFold()
			closeRollbackFold()
			prevTurnIndex = evt.TurnIndex
			prevDepth0Type = ""
			continue
		}

		if evt.TurnIndex != prevTurnIndex {
			closeToolFold() // never let a tool fold span a turn boundary
			closeGrokThoughtFold()
			rolledBack := evt.Type == "TurnBoundary" && metadataBool(evt.Metadata, "rolled_back")
			if rolledBack {
				original := metadataInt(evt.Metadata, "original_turn_index")
				emit("turn", fmt.Sprintf("已回滚 · 原 Turn %d", original+1), "", evt.TurnIndex, map[string]any{"rolled_back": true, "original_turn_index": original})
				writeRollbackSeparator(tb, original, cols)
			} else {
				emit("turn", fmt.Sprintf("Turn %d", evt.TurnIndex), "", evt.TurnIndex, nil)
				writeSeparator(tb, evt.TurnIndex, cols)
			}
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
			userPayload := map[string]any{}
			if !evt.Timestamp.IsZero() {
				userPayload["ts_ms"] = float64(evt.Timestamp.UnixMilli())
			}
			if evt.Text != "" {
				userPayload["text"] = evt.Text
			}
			emit("user", "用户输入", "", evt.TurnIndex, userPayload)
			userPrefix := prefix
			if p.Name == "grok" && grokSidebar != ColNone {
				userPrefix = prefix + fgWrap(p.BoxV, grokSidebar) + " "
			}
			writeUserPrompt(p, tb, evt, userPrefix, tsFor(evt, opts.TimestampUser))
		case "ThinkingStart":
			if p.Name == "grok" && evt.Text != "" {
				// compact ◆ header
				tb.WriteString(prefix)
				if t := tsFor(evt, opts.TimestampAssistant); t != "" {
					tb.WriteString(fgWrap(t+" ", ColMuted))
				}
				tb.WriteString(fgWrap("◆", ColMuted))
				tb.WriteString(styled(" "+evt.Text, ColFg, ColNone, true, false))
				tb.WriteString("\n")

				// setup fold so body (following chunks with | ) can be collapsed by default
				// like tool tfolds
				hdrDisp := tb.CurrentLine() - 1
				hdrLog := tb.CurrentLogicalLine() - 1
				bodyDisp := tb.CurrentLine()
				bodyLog := tb.CurrentLogicalLine()
				openGrokThoughtFold = &toolFoldState{
					turnIndex:     evt.TurnIndex,
					key:           fmt.Sprintf("tfold:%d:%d", evt.TurnIndex, hdrDisp),
					groupKey:      "",
					headerDisplay: hdrDisp,
					headerLogical: hdrLog,
					bodyDisplay:   bodyDisp,
					bodyLogical:   bodyLog,
					badgeOffset:   0,
				}
			} else {
				writeThinking(tb, evt, prefix)
			}
		case "ThinkingChunk":
			if p.Name == "grok" {
				// write content so it's available when thought fold is expanded
				// prefix with green │ to match native expanded sidebar
				tb.WriteString(prefix)
				tb.WriteString(fgWrap(p.BoxV, ColMuted))
				tb.WriteString(" ")
				tb.WriteString(italicWrap(fgWrap(sanitizeControlChars(evt.Text), ColMuted)))
				tb.WriteString("\n")
			} else {
				writeThinking(tb, evt, prefix)
			}
		case "ThinkingEnd":
			if p.Name == "grok" {
				closeGrokThoughtFold()
			}
		case "TextChunk":
			if p.Name == "grok" {
				grokSidebar = ColNone // reset to prevent sidebar leak to plain text after blocks
			}
			textPrefix := prefix
			if p.Name == "grok" && grokSidebar != ColNone {
				textPrefix = prefix + fgWrap(p.BoxV, grokSidebar) + " "
			}
			if p.AssistantHeader && evt.Depth == 0 && prevDepth0Type != "TextChunk" {
				if prevDepth0Type != "" {
					tb.WriteString("\n")
				}
				if p.Name == "grok" && grokSidebar != ColNone {
					tb.WriteString(prefix + fgWrap(p.BoxV, grokSidebar) + " ")
				}
				writeAssistantHeader(tb, agentLabel, tsFor(evt, opts.TimestampAssistant))
			} else if evt.Depth == 0 && prevDepth0Type != "TextChunk" {
				// Profiles without an assistant header line get the timestamp
				// as its own muted line: prefixing the first markdown line
				// would overflow the wrap width computed without the prefix.
				if ts := tsFor(evt, opts.TimestampAssistant); ts != "" {
					tb.WriteString(textPrefix)
					tb.WriteString(fgWrap(ts, ColMuted))
					tb.WriteString("\n")
				}
			}
			writeTextChunk(tb, evt, textPrefix, cols)
		case "ToolInvocation":
			onEdit := func(filePath string) {
				seq := editSeqByTurn[evt.TurnIndex]
				editSeqByTurn[evt.TurnIndex]++
				emit("edit", filePath, "", evt.TurnIndex, map[string]any{"edit_seq": float64(seq)})
			}
			outcome := outcomes[evt.ToolCallID]
			toolTS := tsFor(evt, opts.TimestampTool)
			failed := outcome.status != "" && outcome.status != "ok"
			if p.Name == "grok" {
				grokSidebar = ColNone // reset for new tool bullet
			}
			// Every depth-0 tool call gets a "tool" position anchored to its
			// header line — the tool-call panel's data source. Nested
			// (depth>0) subagent tools are skipped: their TurnIndex is local
			// to the subagent transcript, not the parent session.
			if evt.Depth == 0 {
				emit("tool", evt.ToolName, "", evt.TurnIndex, toolPositionPayload(evt, outcome))
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
			if evt.Depth == 0 && p.ToolBullet && !model.IsEditTool(evt.ToolName) {
				headerDisplay := tb.CurrentLine()
				headerLogical := tb.CurrentLogicalLine()
				bodyD, bodyL, badgeOff := writeToolInvocation(p, tb, evt, prefix, bWidth, onEdit, true, outcome.durationMs, toolTS, failed)
				gk := ""
				if openFold != nil {
					gk = openFold.key
				}
				openToolFold = &toolFoldState{
					turnIndex:     evt.TurnIndex,
					key:           fmt.Sprintf("tfold:%d:%d", evt.TurnIndex, headerDisplay),
					groupKey:      gk,
					headerDisplay: headerDisplay,
					headerLogical: headerLogical,
					bodyDisplay:   bodyD,
					bodyLogical:   bodyL,
					badgeOffset:   badgeOff,
				}
			} else {
				writeToolInvocation(p, tb, evt, prefix, bWidth, onEdit, false, outcome.durationMs, toolTS, failed)
			}
		case "ToolResult":
			if evt.Rejected {
				emit("error", "工具被拒绝", "warning", evt.TurnIndex, map[string]any{"tool": evt.ToolName, "kind": evt.ErrorKind})
			} else if evt.TimedOut {
				emit("error", "工具超时", "error", evt.TurnIndex, map[string]any{"tool": evt.ToolName, "timeout_seconds": evt.TimeoutSeconds})
			} else if evt.ExitCode != 0 || evt.Stderr != "" {
				emit("error", "工具错误", "error", evt.TurnIndex, map[string]any{"tool": evt.ToolName, "exit_code": evt.ExitCode, "kind": evt.ErrorKind})
			}
			resultPrefix := prefix
			if p.Name == "grok" && evt.Depth == 0 {
				resultPrefix = prefix + "  " // match the indent of input box under ◆ header
			}
			writeToolResult(p, tb, evt, resultPrefix, bWidth, makeOnTrunc(evt.TurnIndex))
			closeToolFold() // finalize the per-tool fold immediately after its result, so following assistant text is not swallowed into the fold (fixes folding assistant messages)
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
	closeGrokThoughtFold()
	closeFold()
	for len(rollbackFolds) > 0 {
		closeRollbackFold()
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

// FormatVersion increments whenever the ANSI layout changes in a way that
// shifts line numbers, so cached line positions keyed on it are invalidated.
const FormatVersion int64 = 26

// toolOutcome aggregates a tool call's result(s): merged status and best
// available duration. status "" means no result was seen (still running or
// transcript cut short).
type toolOutcome struct {
	durationMs int64
	status     string // "" | "ok" | "error" | "timeout" | "rejected"
}

// computeToolOutcomes indexes results by ToolCallID. A call can have several
// result events (e.g. embedded bash emits stdout and stderr separately), so
// statuses merge worst-wins. Duration prefers the result's own DurationMs;
// most readers don't populate it, so the invocation→result timestamp delta is
// the fallback.
func computeToolOutcomes(events []model.RenderEvent) map[string]toolOutcome {
	rank := map[string]int{"": 0, "ok": 1, "error": 2, "timeout": 3, "rejected": 4}
	invTS := make(map[string]time.Time)
	out := make(map[string]toolOutcome)
	for _, e := range events {
		if e.ToolCallID == "" {
			continue
		}
		switch e.Type {
		case "ToolInvocation":
			if _, seen := invTS[e.ToolCallID]; !seen && !e.Timestamp.IsZero() {
				invTS[e.ToolCallID] = e.Timestamp
			}
		case "ToolResult":
			o := out[e.ToolCallID]
			status := "ok"
			switch {
			case e.Rejected:
				status = "rejected"
			case e.TimedOut:
				status = "timeout"
			case e.ExitCode != 0 || e.Stderr != "":
				status = "error"
			}
			if rank[status] > rank[o.status] {
				o.status = status
			}
			if e.DurationMs > o.durationMs {
				o.durationMs = e.DurationMs
			}
			if o.durationMs == 0 && !e.Timestamp.IsZero() {
				if ts, ok := invTS[e.ToolCallID]; ok {
					if d := e.Timestamp.Sub(ts).Milliseconds(); d > 0 {
						o.durationMs = d
					}
				}
			}
			out[e.ToolCallID] = o
		}
	}
	return out
}

// toolPositionPayload builds the "tool" position's payload: everything the
// tool-call panel shows without refetching the event stream.
func toolPositionPayload(evt model.RenderEvent, outcome toolOutcome) map[string]any {
	purpose, _ := promoteBoxHeader(evt.ToolInput)
	payload := map[string]any{
		"tool_name": evt.ToolName,
		"category":  toolCategory(evt.ToolName),
		"status":    outcome.status,
	}
	if evt.ToolCallID != "" {
		payload["tool_call_id"] = evt.ToolCallID
	}
	if s := toolSummary(purpose, evt.ToolInput); s != "" {
		payload["summary"] = truncateToWidth(s, 200)
	}
	if !evt.Timestamp.IsZero() {
		payload["ts_ms"] = float64(evt.Timestamp.UnixMilli())
	}
	if outcome.durationMs > 0 {
		payload["duration_ms"] = float64(outcome.durationMs)
	}
	if preview := toolInputPreview(evt.ToolInput); len(preview) > 0 {
		payload["input_preview"] = preview
	}
	return payload
}

// toolInputPreview caps the "key: value" input lines for the tool-call
// panel's expanded view — full values (whole file contents in a Write call)
// stay in the terminal; the panel is for orientation, then jumping there.
func toolInputPreview(input map[string]any) []string {
	lines := formatToolInput(input)
	if len(lines) == 0 {
		return nil
	}
	const maxLines = 10
	extra := 0
	if len(lines) > maxLines {
		extra = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		out = append(out, truncateToWidth(l, 200))
	}
	if extra > 0 {
		out = append(out, fmt.Sprintf("… 另有 %d 个参数", extra))
	}
	return out
}

// fmtDurationShort renders a tool-call duration for inline display.
func fmtDurationShort(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	case ms < 3_600_000:
		return fmt.Sprintf("%dm%02ds", ms/60_000, (ms%60_000)/1000)
	default:
		return fmt.Sprintf("%dh%02dm", ms/3_600_000, (ms%3_600_000)/60_000)
	}
}

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

func writeAssistantHeader(sb *trackingBuilder, label string, ts string) {
	if label == "" {
		label = "Agent"
	}
	if ts != "" {
		sb.WriteString(fgWrap(ts+" ", ColMuted))
	}
	sb.WriteString(styled("◇ "+sanitizeControlChars(label), ColSuccessBright, ColNone, false, false))
	sb.WriteString("\n")
}

// computeGrokThoughtSummaries post-processes the event list for grok to set
// a compact "Thought for Xs" summary on ThinkingStart events (duration from
// start to corresponding ThinkingEnd in same turn). This lets the terminal
// view show only the ◆ header line (matching native Grok TUI), while the
// detailed thought chunks can be suppressed in writing for the list view.
func computeGrokThoughtSummaries(events []model.RenderEvent) {
	for i := range events {
		e := &events[i]
		if e.Type != "ThinkingStart" || e.AgentType != "grok" {
			continue
		}
		startTs := e.Timestamp
		turn := e.TurnIndex
		for j := i + 1; j < len(events); j++ {
			if events[j].TurnIndex != turn {
				break
			}
			if events[j].Type == "ThinkingEnd" {
				if !events[j].Timestamp.IsZero() && !startTs.IsZero() {
					d := events[j].Timestamp.Sub(startTs).Seconds()
					if d < 0 {
						d = 0
					}
					e.Text = fmt.Sprintf("Thought for %.1fs", d)
				}
				break
			}
			if events[j].Type == "ThinkingStart" {
				break
			}
		}
		if e.Text == "" {
			e.Text = "Thought"
		}
	}
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

func writeRollbackHeader(sb *trackingBuilder, count, resumeTurn int) (headerDisplay, headerLogical int) {
	label := fmt.Sprintf("▼ ↩ 已回滚 %d 个 turn", count)
	if resumeTurn > 0 {
		label += fmt.Sprintf(" · CLI 从第 %d 轮恢复", resumeTurn)
	}
	sb.WriteString("\n")
	headerDisplay = sb.CurrentLine()
	headerLogical = sb.CurrentLogicalLine()
	sb.WriteString(styled(label, ColWarning, ColNone, true, false))
	sb.WriteString("\n")
	return headerDisplay, headerLogical
}

func writeRollbackSeparator(sb *trackingBuilder, originalTurnIdx int, termWidth int) {
	label := fmt.Sprintf(" 已回滚 · 原第 %d 轮 ", originalTurnIdx+1)
	rest := termWidth - displayWidth(label)
	if rest < 1 {
		rest = 1
	}
	sb.WriteString("\n")
	sb.WriteString(styled(label, ColMuted, ColNone, false, false))
	sb.WriteString(fgWrap(strings.Repeat("╌", rest), ColMuted))
	sb.WriteString("\n")
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	switch v := metadata[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	v, _ := metadata[key].(bool)
	return v
}

func writeUserPrompt(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, ts string) {
	if p.UserHeader != "" {
		sb.WriteString(prefix)
		if ts != "" {
			sb.WriteString(fgWrap(ts+" ", ColMuted))
		}
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
	for i, line := range strings.Split(prompt, "\n") {
		sb.WriteString(prefix)
		if i == 0 && ts != "" {
			sb.WriteString(fgWrap(ts+" ", ColMuted))
		}
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
	if footer != "" {
		maxFooterWidth := inner - 4
		if displayWidth(footer) > maxFooterWidth {
			footer = truncateToWidth(footer, maxFooterWidth)
		}
		fillLen := inner - 2 - displayWidth(footer)
		if fillLen < 0 {
			fillLen = 0
		}
		left := strings.Repeat(p.BoxH, fillLen)
		right := strings.Repeat(p.BoxH, 2)
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(p.BoxBL+left, borderColor))
		sb.WriteString(styled(footer, borderColor, ColNone, true, false))
		sb.WriteString(fgWrap(right+p.BoxBR, borderColor))
		sb.WriteString("\n")
	} else {
		body := strings.Repeat(p.BoxH, inner)
		sb.WriteString(prefix)
		sb.WriteString(fgWrap(p.BoxBL+body+p.BoxBR, borderColor))
		sb.WriteString("\n")
	}
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
// durationMs (0 = unknown) and ts ("" = disabled) enrich the header line so
// the invocation's cost and wall-clock moment are readable without expanding
// anything. Edit tools render as diffs and currently skip both.
func writeToolInvocation(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, onEditStart func(filePath string), asFoldHeader bool, durationMs int64, ts string, failed bool) (bodyDisplay, bodyLogical, headerBadgeOffset int) {
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
			if p.Name == "grok" {
				// write ◆ for grok edits too, for consistent bullet style
				// full name "SearchReplace", with timestamp like other bullets
				editLabel := "SearchReplace"
				if ts != "" {
					sb.WriteString(prefix)
					sb.WriteString(fgWrap(ts+" ", ColMuted))
				}
				sb.WriteString(prefix)
				sb.WriteString(styled("◆ "+editLabel, ColFg, ColNone, true, false))
				sb.WriteString("\n")
			}
			writeEditDiff(p, sb, evt, prefix, bWidth)
			return 0, 0, 0
		}
	}

	toolName := sanitizeControlChars(evt.ToolName)
	input := evt.ToolInput

	borderColor := ColTool
	if evt.ToolName == "Agent" || evt.ToolName == "Task" {
		borderColor = ColSubagent
	}
	if evt.ToolName == "Skill" {
		borderColor = ColSkill // magenta/violet — skill accent (native Grok accent_skill)
	}
	if p.Name == "grok" && toolName == "Run" {
		borderColor = ColSuccess // green for success run tools
		if failed {
			borderColor = ColErrorBright // red for failed
		}
	}
	var header string

	if p.ToolBullet {
		var purpose string
		if purpose, input = promoteBoxHeader(evt.ToolInput); purpose != "" {
			header = " " + sanitizeControlChars(purpose) + " "
		} else {
			header = ""
		}
		if p.Name == "grok" {
			header = "" // do not repeat description in the inner tool box top; the ◆ header already shows it. Box will show the command in content.
		}
		sb.WriteString(prefix)
		tsRun := ""
		if ts != "" {
			tsRun = fgWrap(ts+" ", ColMuted)
			sb.WriteString(tsRun)
		}
		char := p.ToolBulletChar
		if char == "" {
			char = "•"
		}
		bulletColor := ColFg
		if p.Name == "grok" {
			bulletColor = ColSuccess
		}
		if asFoldHeader {
			// Emit the name and the summary as two independent SGR runs so the
			// client can splice the fold's "(N 行)" badge between them without any
			// knowledge of this profile's byte shape: headerBadgeOffset marks the
			// UTF-16 index just past the name run (right after its trailing reset),
			// which is where the badge goes. Keeping name/summary in separate runs
			// means the badge needs no style reopen — the summary run restyles
			// itself. This is the ONLY place that encodes "where chrys's tool
			// header ends"; the client stays profile-agnostic.
			bullet := "▼ " + char + " "
			if p.Name == "grok" || char == "◆" {
				bullet = char + " "
			}
			if p.Name == "grok" && toolName == "Run" {
				// ◆ color based on success/fail (green/red), text default (matches native)
				c := ColSuccess
				if failed {
					c = ColErrorBright
				}
				diamondStr := fgWrap(char, c)
				sb.WriteString(diamondStr)
				nameRun := styled(" "+toolName, ColFg, ColNone, true, false)
				sb.WriteString(nameRun)
				headerBadgeOffset = utf16Len(prefix) + utf16Len(tsRun) + utf16Len(diamondStr) + utf16Len(nameRun)
				sep := " "
				if s := toolSummary(purpose, evt.ToolInput); s != "" {
					sb.WriteString(styled(sep+s, ColFg, ColNone, true, false))
				}
			} else if p.Name == "grok" && toolName == "Skill" {
				// Native: "Skill <name>" with skill name on accent_skill (ColSkill).
				diamondStr := fgWrap(char, ColSkill)
				sb.WriteString(diamondStr)
				nameRun := styled(" "+toolName, ColFg, ColNone, true, false)
				sb.WriteString(nameRun)
				headerBadgeOffset = utf16Len(prefix) + utf16Len(tsRun) + utf16Len(diamondStr) + utf16Len(nameRun)
				if s := toolSummary(purpose, evt.ToolInput); s != "" {
					sb.WriteString(styled(" "+s, ColSkill, ColNone, true, false))
				}
			} else {
				nameRun := styled(bullet+toolName, bulletColor, ColNone, true, false)
				sb.WriteString(nameRun)
				headerBadgeOffset = utf16Len(prefix) + utf16Len(tsRun) + utf16Len(nameRun)
				sep := "  "
				if s := toolSummary(purpose, evt.ToolInput); s != "" {
					sb.WriteString(styled(sep+s, bulletColor, ColNone, true, false))
				}
			}
		} else {
			if p.Name == "grok" && toolName == "Run" {
				c := ColSuccess
				if failed {
					c = ColErrorBright
				}
				sb.WriteString(fgWrap(char, c))
				sb.WriteString(styled(" "+toolName, ColFg, ColNone, true, false))
			} else if p.Name == "grok" && toolName == "Skill" {
				sb.WriteString(fgWrap(char, ColSkill))
				sb.WriteString(styled(" "+toolName, ColFg, ColNone, true, false))
				if s := toolSummary(purpose, evt.ToolInput); s != "" {
					sb.WriteString(styled(" "+s, ColSkill, ColNone, true, false))
				}
			} else {
				sb.WriteString(styled(char+" "+toolName, bulletColor, ColNone, true, false))
			}
		}
		if durationMs > 0 {
			sb.WriteString(fgWrap(" · "+fmtDurationShort(durationMs), ColMuted))
		}
		sb.WriteString("\n")
	} else {
		// Box profiles carry the salient input arg, duration, and optional
		// timestamp inside the top border's header: a prefix outside the box
		// would misalign the border with the content rows beneath it.
		parts := []string{fmt.Sprintf("Tool: %s", toolName)}
		if s := toolSummary("", evt.ToolInput); s != "" {
			parts = append(parts, truncateToWidth(s, 48))
		}
		if durationMs > 0 {
			parts = append(parts, fmtDurationShort(durationMs))
		}
		if ts != "" {
			parts = append(parts, ts)
		}
		header = " " + strings.Join(parts, " · ") + " "
	}

	// Body starts after the (possibly wrapped) header line: everything from
	// here to the tool's end is what a collapsed fold hides.
	bodyDisplay = sb.CurrentLine()
	bodyLogical = sb.CurrentLogicalLine()

	inputLines := formatToolInput(input)

	boxPrefix := prefix
	if p.Name == "grok" {
		boxPrefix = prefix + "  " // indent box so not tight against left border
	}

	writeBoxTop(p, sb, boxPrefix, header, bWidth, borderColor)
	for _, il := range inputLines {
		writeBoxRow(p, sb, boxPrefix, il, bWidth, borderColor, ColFg)
	}
	writeBoxBottom(p, sb, boxPrefix, "", bWidth, borderColor)
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
	for _, k := range []string{"command", "skill", "file_path", "path", "pattern", "query", "url", "prompt", "description"} {
		if v, ok := input[k].(string); ok {
			if s := oneLine(v); s != "" {
				return sanitizeControlChars(s)
			}
		}
	}
	// No purpose and no well-known key: synthesize a compact "k: v · k: v"
	// from all params so simple tools (load_skill、自定义 MCP 工具等) read
	// fully on the collapsed row instead of forcing an expand.
	parts := make([]string, 0, len(input))
	for _, l := range formatToolInput(input) {
		if s := oneLine(l); s != "" {
			parts = append(parts, truncateToWidth(s, 60))
		}
	}
	return strings.Join(parts, " · ")
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
		rw := runeCellWidth(r)
		if w+rw > budget {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	sb.WriteRune('…')
	return sb.String()
}

func writeToolResult(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, onTrunc func()) {
	ok := evt.ExitCode == 0 && evt.Stderr == "" && !evt.TimedOut && !evt.Rejected

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
	label, color := toolResultStatus(evt, ok)
	sb.WriteString(fgWrap(label, color))
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

// toolResultStatus returns a display label and color for a tool result,
// leveraging structured metadata (timeout, rejection, exit code) when
// available, with a graceful fallback to the legacy ok/failed binary.
func toolResultStatus(evt model.RenderEvent, ok bool) (string, Color) {
	if evt.Rejected {
		if evt.ErrorKind == "hook_denied" {
			return "✗ Rejected (hook)", ColWarning
		}
		return "✗ Rejected", ColWarning
	}
	if evt.TimedOut {
		if evt.TimeoutSeconds > 0 {
			return fmt.Sprintf("✗ Timeout (%gs)", evt.TimeoutSeconds), ColErrorBright
		}
		return "✗ Timeout", ColErrorBright
	}
	if !ok {
		if evt.ExitCode != 0 {
			return fmt.Sprintf("✗ Failed (exit %d)", evt.ExitCode), ColErrorBright
		}
		return "✗ Failed", ColErrorBright
	}
	return "✓", ColSuccessBright
}

// writeToolResultBox renders a tool result as a bordered "Output" box with a
// Completed/Failed footer (chrys-native layout). Output-less results collapse
// to a single status line so the transcript doesn't fill with empty boxes.
func writeToolResultBox(p *Profile, sb *trackingBuilder, evt model.RenderEvent, prefix string, bWidth int, ok bool, onTrunc func()) {
	borderColor := ColSuccessBright
	footer := " Completed "
	if !ok {
		borderColor = ColErrorBright
		footer = " Failed "
		switch {
		case evt.Rejected:
			borderColor = ColWarning
			if evt.ErrorKind == "hook_denied" {
				footer = " Rejected (hook) "
			} else {
				footer = " Rejected "
			}
		case evt.TimedOut:
			if evt.TimeoutSeconds > 0 {
				footer = fmt.Sprintf(" Timeout (%gs) ", evt.TimeoutSeconds)
			} else {
				footer = " Timeout "
			}
		case evt.ExitCode != 0:
			footer = fmt.Sprintf(" Failed (exit %d) ", evt.ExitCode)
		}
	}

	if evt.Stdout == "" && evt.Stderr == "" {
		sb.WriteString(prefix)
		if ok {
			sb.WriteString(styled("✓ Completed", ColSuccessBright, ColNone, true, false))
		} else {
			label, color := toolResultStatus(evt, ok)
			sb.WriteString(styled(label, color, ColNone, true, false))
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
		writeLines(evt.Stderr, ColErrorBright)
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
		sb.WriteString(fgWrap(fmt.Sprintf("⚠ 中断: %s", sanitizeControlChars(evt.Text)), ColErrorBright))
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
		w += runeCellWidth(r)
	}
	return w
}

// runeCellWidth mirrors xterm.js's default (wcwidth-style) cell accounting:
// zero-width marks occupy no cell even when they change the glyph. Without
// this, "✏️" (U+270F + VS16) counts as 2 here but renders in 1 cell, so box
// top borders drawn around it end one column short of the body rows.
func runeCellWidth(r rune) int {
	if isZeroWidthRune(r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

func isZeroWidthRune(r rune) bool {
	switch {
	case r >= 0xFE00 && r <= 0xFE0F, // variation selectors (VS16 emoji style)
		r >= 0x200B && r <= 0x200F, // zero-width space/joiners, directional marks
		r >= 0x0300 && r <= 0x036F, // combining diacritical marks
		r == 0xFEFF:                // BOM / zero-width no-break space
		return true
	}
	return false
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
		rw := runeCellWidth(r)
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
func lcsLineDiff(old, new []string) []struct {
	kind int
	text string
} {
	type op = struct {
		kind int
		text string
	}
	const opEqual, opRemove, opAdd = 0, 1, 2
	m, n := len(old), len(new)
	if m*n > 60000 {
		ops := make([]op, 0, m+n)
		for _, l := range old {
			ops = append(ops, op{opRemove, l})
		}
		for _, l := range new {
			ops = append(ops, op{opAdd, l})
		}
		return ops
	}
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
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
			ops = append(ops, op{opEqual, old[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, op{opAdd, new[j-1]})
			j--
		} else {
			ops = append(ops, op{opRemove, old[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
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
	oldStr := str("old_string")
	newStr := str("new_string")

	oldLines := splitLines(oldStr)
	newLines := splitLines(newStr)
	ops := lcsLineDiff(oldLines, newLines)

	// Count actual changes for summary
	nDel, nAdd := 0, 0
	for _, op := range ops {
		switch op.kind {
		case 1:
			nDel++
		case 2:
			nAdd++
		}
	}

	// Header. The "✏️ <tool>: <path>" shape is load-bearing: the frontend's
	// clickable diff affordance (parseEditHeaderLine) matches on it.
	dispPath := filePath
	if dispPath == "" {
		dispPath = "unknown"
	}
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
			sigil = "  "
			fgColor = ColFg
			bgColor = ColNone
		case 1: // remove
			sigil = "- "
			fgColor = ColFg
			bgColor = ColDiffDel
		case 2: // add
			sigil = "+ "
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
