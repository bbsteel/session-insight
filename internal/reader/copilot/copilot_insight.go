package copilot

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// promptExcerptChars bounds how much delegation prompt text enters the bundle.
// The full prompt (thousands of chars per subagent) never leaves the machine;
// only prompt_chars (the true length) and a bounded excerpt are preserved.
const promptExcerptChars = 400

// GetInsightEvidence implements reader.InsightEvidenceProvider for Copilot.
// It recovers the subagent delegation facts the unified model drops: each
// subagent's task-tool arguments (description, model, mode, prompt length),
// its run window, the model responses attributed to it by temporal nesting,
// and whether its window overlapped another subagent. Returns the evidence and
// the revision it read (events.jsonl mtime, matching the session revision).
func (r *CopilotReader) GetInsightEvidence(id string, _ int64) (*model.InsightEvidence, int64, error) {
	if !validSessionID(id) {
		return nil, 0, errInvalidID(id)
	}
	eventsPath := filepath.Join(r.sessionDir, id, "events.jsonl")
	info, err := os.Stat(eventsPath)
	if err != nil {
		return nil, 0, err
	}
	rev := info.ModTime().UnixNano()
	ev, err := parseInsightEvidence(eventsPath)
	if err != nil {
		return nil, rev, err
	}
	return ev, rev, nil
}

func errInvalidID(id string) error {
	return &os.PathError{Op: "insight", Path: id, Err: os.ErrInvalid}
}

// taskCall holds one `task` tool delegation and its owning turn.
type taskCall struct {
	turnIndex   int
	description string
	name        string
	model       string
	mode        string
	prompt      string
	toolCalls   int // numberOfToolCallsMadeByAgent from execution_complete
}

// subWindow is a subagent's [started, completed] run window.
type subWindow struct {
	toolCallID  string
	displayName string
	turnIndex   int
	started     time.Time
	completed   time.Time
	hasStart    bool
	hasComplete bool
}

// parseInsightEvidence scans events.jsonl once and reconstructs subagent
// evidence. Response attribution uses temporal nesting: an assistant.message
// belongs to the innermost subagent window (latest start) that contains its
// timestamp — sync subagents block the parent, so nesting is well-defined and
// the per-subagent counts partition the in-window responses exactly.
func parseInsightEvidence(path string) (*model.InsightEvidence, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tasks := map[string]*taskCall{}
	windows := map[string]*subWindow{}
	var windowOrder []string
	type resp struct {
		ts     time.Time
		tokens int64
	}
	var responses []resp
	turnIndex := -1

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var evt jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "user.message":
			turnIndex++
		case "tool.execution_start":
			if name, _ := extractString(evt.Data, "toolName"); name == "task" {
				if tc, _ := extractString(evt.Data, "toolCallId"); tc != "" {
					tasks[tc] = parseTaskArgs(evt.Data, turnIndex)
				}
			}
		case "tool.execution_complete":
			if tc, _ := extractString(evt.Data, "toolCallId"); tc != "" {
				if t, ok := tasks[tc]; ok {
					t.toolCalls = subagentToolCalls(evt.Data)
					if t.model == "" {
						t.model, _ = extractString(evt.Data, "model")
					}
				}
			}
		case "subagent.started":
			tc, _ := extractString(evt.Data, "toolCallId")
			if tc == "" {
				continue
			}
			w := windows[tc]
			if w == nil {
				w = &subWindow{toolCallID: tc, turnIndex: turnIndex}
				windows[tc] = w
				windowOrder = append(windowOrder, tc)
			}
			w.displayName, _ = extractString(evt.Data, "agentDisplayName")
			if ts, ok := parseTS(evt.Timestamp); ok {
				w.started = ts
				w.hasStart = true
			}
		case "subagent.completed":
			tc, _ := extractString(evt.Data, "toolCallId")
			if w := windows[tc]; w != nil {
				if ts, ok := parseTS(evt.Timestamp); ok {
					w.completed = ts
					w.hasComplete = true
				}
			}
		case "assistant.message":
			if ts, ok := parseTS(evt.Timestamp); ok {
				responses = append(responses, resp{ts: ts, tokens: extractInt64(evt.Data, "outputTokens")})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Attribute each response to the innermost containing window.
	reqCount := map[string]int{}
	outTokens := map[string]int64{}
	for _, rsp := range responses {
		best := ""
		var bestStart time.Time
		for _, tc := range windowOrder {
			w := windows[tc]
			if !w.hasStart || !w.hasComplete {
				continue
			}
			if (rsp.ts.Equal(w.started) || rsp.ts.After(w.started)) &&
				(rsp.ts.Equal(w.completed) || rsp.ts.Before(w.completed)) {
				if best == "" || w.started.After(bestStart) {
					best = tc
					bestStart = w.started
				}
			}
		}
		if best != "" {
			reqCount[best]++
			outTokens[best] += rsp.tokens
		}
	}

	// Overlap detection: does a window intersect any other window?
	overlaps := map[string]bool{}
	for _, a := range windowOrder {
		wa := windows[a]
		if !wa.hasStart || !wa.hasComplete {
			continue
		}
		for _, b := range windowOrder {
			if a == b {
				continue
			}
			wb := windows[b]
			if !wb.hasStart || !wb.hasComplete {
				continue
			}
			if wa.started.Before(wb.completed) && wb.started.Before(wa.completed) {
				overlaps[a] = true
				break
			}
		}
	}

	ev := &model.InsightEvidence{}
	for _, tc := range windowOrder {
		w := windows[tc]
		s := model.SubagentEvidence{
			ToolCallID:    tc,
			TurnIndex:     w.turnIndex,
			Name:          w.displayName,
			RequestCount:  reqCount[tc],
			OutputTokens:  outTokens[tc],
			OverlapsOther: overlaps[tc],
		}
		if t := tasks[tc]; t != nil {
			s.Description = t.description
			if t.name != "" {
				s.Name = t.name
			}
			s.Model = t.model
			s.Mode = t.mode
			s.PromptChars = len([]rune(t.prompt))
			s.Prompt = truncateRunes(t.prompt, promptExcerptChars)
			s.TurnIndex = t.turnIndex
		}
		if w.hasStart {
			s.StartedAt = w.started.UTC().Format(time.RFC3339)
		}
		if w.hasComplete {
			s.CompletedAt = w.completed.UTC().Format(time.RFC3339)
			if w.hasStart {
				s.DurationMs = w.completed.Sub(w.started).Milliseconds()
			}
		}
		ev.Subagents = append(ev.Subagents, s)
	}
	// Stable order: by turn then start time, so fixtures compare deterministically.
	sort.SliceStable(ev.Subagents, func(i, j int) bool {
		if ev.Subagents[i].TurnIndex != ev.Subagents[j].TurnIndex {
			return ev.Subagents[i].TurnIndex < ev.Subagents[j].TurnIndex
		}
		return ev.Subagents[i].StartedAt < ev.Subagents[j].StartedAt
	})
	return ev, nil
}

func parseTaskArgs(data map[string]any, turnIndex int) *taskCall {
	t := &taskCall{turnIndex: turnIndex}
	args := nestedMap(data, "arguments")
	if args == nil {
		return t
	}
	t.description, _ = extractString(args, "description")
	t.name, _ = extractString(args, "name")
	t.model, _ = extractString(args, "model")
	t.mode, _ = extractString(args, "mode")
	t.prompt, _ = extractString(args, "prompt")
	return t
}

// subagentToolCalls reads numberOfToolCallsMadeByAgent from the task tool's
// telemetry, which records how much work the subagent did.
func subagentToolCalls(data map[string]any) int {
	tel := nestedMap(data, "toolTelemetry")
	if tel == nil {
		return 0
	}
	metrics := nestedMap(tel, "metrics")
	if metrics == nil {
		return 0
	}
	return int(extractFloat(metrics, "numberOfToolCallsMadeByAgent"))
}

func parseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// truncateRunes bounds a string to max runes, marking truncation.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…(截断)"
}
