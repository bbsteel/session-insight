package grok

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// toRenderEvents prefers updates.jsonl; falls back to chat_history.jsonl.
func (r *GrokReader) toRenderEvents(loc sessionLoc) ([]model.RenderEvent, error) {
	updatesPath := filepath.Join(loc.Dir, "updates.jsonl")
	if _, err := os.Stat(updatesPath); err == nil {
		events, turnOpen, err := parseUpdatesRender(updatesPath)
		if err != nil {
			return nil, err
		}
		// Prefer events.jsonl turn brackets when present (more precise).
		if open, ok := turnOpenFromEvents(filepath.Join(loc.Dir, "events.jsonl")); ok {
			turnOpen = open
		}
		events = shared.DropEmptyRenderTurns(events)
		lastWrite := r.lastContentWrite(loc.Dir)
		turnIdx := 0
		if len(events) > 0 {
			turnIdx = events[len(events)-1].TurnIndex
		}
		if evt, ok := shared.TrailingInProgress(turnOpen, lastWrite, turnIdx); ok {
			// Only append when trailing turn survived DropEmpty.
			if len(events) == 0 || events[len(events)-1].TurnIndex == turnIdx {
				evt.EventID = fmt.Sprintf("evt-grok-inprog-%04d", len(events))
				evt.AgentType = "grok"
				events = append(events, evt)
			}
		}
		return events, nil
	}

	chatPath := filepath.Join(loc.Dir, "chat_history.jsonl")
	if _, err := os.Stat(chatPath); err == nil {
		events, err := parseChatRender(chatPath)
		if err != nil {
			return nil, err
		}
		events = shared.DropEmptyRenderTurns(events)
		return events, nil
	}
	return nil, fmt.Errorf("grok session has no updates.jsonl or chat_history.jsonl: %s", loc.ID)
}

// turnOpenFromEvents returns (open, ok). ok=false means events.jsonl missing/unreadable.
func turnOpenFromEvents(path string) (bool, bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	open := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "turn_started":
			open = true
		case "turn_ended":
			open = false
		}
	}
	return open, true
}

type rawUpdateLine struct {
	Timestamp int64  `json:"timestamp"`
	Method    string `json:"method"`
	Params    struct {
		Update json.RawMessage `json:"update"`
	} `json:"params"`
}

type rawUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	RawOutput     json.RawMessage `json:"rawOutput"`
	StopReason    string          `json:"stop_reason"`
	Usage         *turnUsage      `json:"usage"`
	Meta          map[string]any  `json:"_meta"`
}

func parseUpdatesRender(path string) ([]model.RenderEvent, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	var (
		events       []model.RenderEvent
		eventCtr     int
		turnIndex    = -1
		turnOpen     bool
		pendingTools = map[string]string{} // toolCallId -> ToolInvocation EventID
		inThought    bool
	)

	emit := func(e model.RenderEvent) string {
		if e.EventID == "" {
			e.EventID = fmt.Sprintf("evt-grok-%04d", eventCtr)
			eventCtr++
		}
		if e.AgentType == "" {
			e.AgentType = "grok"
		}
		events = append(events, e)
		return e.EventID
	}

	currentTurn := func() int {
		if turnIndex < 0 {
			return 0
		}
		return turnIndex
	}

	closeThought := func(ts time.Time) {
		if inThought {
			emit(model.RenderEvent{
				Type:      "ThinkingEnd",
				Timestamp: ts,
				TurnIndex: currentTurn(),
			})
			inThought = false
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var line rawUpdateLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		var u rawUpdate
		if json.Unmarshal(line.Params.Update, &u) != nil {
			continue
		}
		ts := tsFromUnix(line.Timestamp)

		switch u.SessionUpdate {
		case "user_message_chunk":
			text := strings.TrimSpace(textFromContent(u.Content))
			if text == "" {
				continue
			}
			closeThought(ts)
			turnIndex++
			turnOpen = true
			emit(model.RenderEvent{
				Type:      "TurnBoundary",
				Timestamp: ts,
				TurnIndex: turnIndex,
			})
			emit(model.RenderEvent{
				Type:      "UserPrompt",
				Timestamp: ts,
				TurnIndex: turnIndex,
				Text:      text,
			})

		case "agent_thought_chunk":
			text := textFromContent(u.Content)
			if text == "" {
				continue
			}
			if !inThought {
				emit(model.RenderEvent{
					Type:      "ThinkingStart",
					Timestamp: ts,
					TurnIndex: currentTurn(),
				})
				inThought = true
			}
			emit(model.RenderEvent{
				Type:      "ThinkingChunk",
				Timestamp: ts,
				TurnIndex: currentTurn(),
				Text:      text,
			})

		case "agent_message_chunk":
			text := textFromContent(u.Content)
			if text == "" {
				continue
			}
			closeThought(ts)
			emit(model.RenderEvent{
				Type:      "TextChunk",
				Timestamp: ts,
				TurnIndex: currentTurn(),
				Text:      text,
			})

		case "tool_call":
			closeThought(ts)
			name := toolNameFromRaw(u)
			input := rawToMap(u.RawInput)
			invID := emit(model.RenderEvent{
				Type:       "ToolInvocation",
				Timestamp:  ts,
				TurnIndex:  currentTurn(),
				ToolName:   name,
				ToolCallID: u.ToolCallID,
				ToolInput:  input,
			})
			if u.ToolCallID != "" {
				pendingTools[u.ToolCallID] = invID
			}

		case "tool_call_update":
			if u.Status != "completed" && u.Status != "failed" && u.Status != "error" {
				// Intermediate status / richer rawInput — ignore for render.
				continue
			}
			closeThought(ts)
			stdout := toolResultText(u)
			exit := 0
			if u.Status == "failed" || u.Status == "error" {
				exit = 1
			}
			parent := ""
			if u.ToolCallID != "" {
				parent = pendingTools[u.ToolCallID]
				delete(pendingTools, u.ToolCallID)
			}
			emit(model.RenderEvent{
				Type:          "ToolResult",
				Timestamp:     ts,
				TurnIndex:     currentTurn(),
				ToolCallID:    u.ToolCallID,
				Stdout:        stdout,
				ExitCode:      exit,
				ParentEventID: parent,
			})

		case "turn_completed":
			closeThought(ts)
			turnOpen = false
			if u.Usage != nil && turnIndex >= 0 {
				// Attach token metadata as AgentSpecific for analytics-friendly trails.
				emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "turn_usage",
					Timestamp: ts,
					TurnIndex: turnIndex,
					TokenUsage: &model.RenderTokenUsage{
						InputTokens:     u.Usage.InputTokens,
						OutputTokens:    u.Usage.OutputTokens,
						CacheReadTokens: u.Usage.CachedReadTokens,
					},
					Metadata: map[string]any{
						"stop_reason": u.StopReason,
					},
				})
			}

		case "plan":
			// Optional plan snapshot — surface as agent-specific note.
			closeThought(ts)
			emit(model.RenderEvent{
				Type:      "AgentSpecific",
				Subtype:   "plan",
				Timestamp: ts,
				TurnIndex: currentTurn(),
				Text:      "plan updated",
				Payload:   map[string]any{"raw": string(line.Params.Update)},
			})

		case "session_recap":
			// Skip auto recap noise in terminal replay.
		case "hook_execution", "task_backgrounded", "task_completed":
			// Deferred: background task / hooks not expanded in v1.
		}
	}
	closeThought(time.Now())

	if err := sc.Err(); err != nil {
		return events, turnOpen, err
	}
	return events, turnOpen, nil
}

func parseChatRender(path string) ([]model.RenderEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		events    []model.RenderEvent
		eventCtr  int
		turnIndex = -1
	)
	emit := func(e model.RenderEvent) {
		if e.EventID == "" {
			e.EventID = fmt.Sprintf("evt-grok-chat-%04d", eventCtr)
			eventCtr++
		}
		e.AgentType = "grok"
		events = append(events, e)
	}

	// chat_history has tool_result but not tool invocations — emit results only.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var msg chatMsg
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch msg.Type {
		case "user":
			text := extractUserQuery(msg.contentText())
			if text == "" {
				continue
			}
			turnIndex++
			emit(model.RenderEvent{
				Type:      "TurnBoundary",
				TurnIndex: turnIndex,
			})
			emit(model.RenderEvent{
				Type:      "UserPrompt",
				TurnIndex: turnIndex,
				Text:      text,
			})
		case "reasoning":
			if turnIndex < 0 {
				turnIndex = 0
			}
			text := reasoningSummaryText(msg.Summary)
			if text == "" {
				continue
			}
			emit(model.RenderEvent{Type: "ThinkingStart", TurnIndex: turnIndex})
			emit(model.RenderEvent{Type: "ThinkingChunk", TurnIndex: turnIndex, Text: text})
			emit(model.RenderEvent{Type: "ThinkingEnd", TurnIndex: turnIndex})
		case "assistant":
			if turnIndex < 0 {
				turnIndex = 0
			}
			text := msg.contentText()
			if text == "" {
				continue
			}
			emit(model.RenderEvent{
				Type:      "TextChunk",
				TurnIndex: turnIndex,
				Text:      text,
				Model:     msg.ModelID,
			})
		case "tool_result":
			if turnIndex < 0 {
				turnIndex = 0
			}
			emit(model.RenderEvent{
				Type:       "ToolResult",
				TurnIndex:  turnIndex,
				ToolCallID: msg.ToolCallID,
				Stdout:     msg.contentText(),
			})
		}
	}
	return events, sc.Err()
}

func reasoningSummaryText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func tsFromUnix(ts int64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	// Heuristic: values past year 2001 in seconds vs milliseconds.
	if ts > 1e12 {
		return time.UnixMilli(ts)
	}
	return time.Unix(ts, 0)
}

func textFromContent(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	// {"type":"text","text":"..."}
	var obj struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Text != "" {
		return obj.Text
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func rawToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	// Non-object input — wrap as string for display.
	return map[string]any{"value": string(raw)}
}

func toolNameFromRaw(u rawUpdate) string {
	if u.Meta != nil {
		if tool, ok := u.Meta["x.ai/tool"].(map[string]any); ok {
			if name, _ := tool["name"].(string); name != "" {
				return name
			}
		}
	}
	if u.Title != "" {
		return u.Title
	}
	return "tool"
}

func toolResultText(u rawUpdate) string {
	// Prefer structured content array on completed tool_call_update.
	if len(u.Content) > 0 {
		// Try array form: [{"type":"content","content":{"type":"text","text":"..."}}]
		var arr []toolContent
		if json.Unmarshal(u.Content, &arr) == nil && len(arr) > 0 {
			var b strings.Builder
			for _, c := range arr {
				if c.Content.Text != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(c.Content.Text)
				}
			}
			if b.Len() > 0 {
				return b.String()
			}
		}
		// Object form with text
		if t := textFromContent(u.Content); t != "" {
			return t
		}
	}
	// rawOutput.Bash.output is often a byte array — decode if possible.
	if len(u.RawOutput) > 0 {
		var ro struct {
			Type            string `json:"type"`
			OutputForPrompt string `json:"output_for_prompt"`
			Output          any    `json:"output"`
		}
		if json.Unmarshal(u.RawOutput, &ro) == nil {
			if ro.OutputForPrompt != "" {
				return ro.OutputForPrompt
			}
			switch o := ro.Output.(type) {
			case string:
				return o
			case []any:
				buf := make([]byte, 0, len(o))
				for _, v := range o {
					switch n := v.(type) {
					case float64:
						buf = append(buf, byte(n))
					}
				}
				if len(buf) > 0 {
					return string(buf)
				}
			}
		}
	}
	return ""
}
