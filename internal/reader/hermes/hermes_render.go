package hermes

import (
	"fmt"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

// GetRenderEvents returns a deterministic structured replay of the Hermes
// message order. Tool results are paired to invocations by Hermes' native
// tool_call_id rather than by text position.
func (r *HermesReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	row, err := r.readSessionRow(id)
	if err != nil {
		return nil, err
	}
	messages, err := r.readMessages(id)
	if err != nil {
		return nil, err
	}
	return r.renderMessages(row, messages), nil
}

func (r *HermesReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

func (r *HermesReader) renderMessages(row sessionRow, messages []hermesMessage) []model.RenderEvent {
	var events []model.RenderEvent
	ids := map[string]string{}
	eventCounter := 0
	turnIndex := -1
	lastAssistantOpen := false

	emit := func(event model.RenderEvent) string {
		if event.EventID == "" {
			event.EventID = fmt.Sprintf("evt-hermes-%04d", eventCounter)
			eventCounter++
		}
		if event.AgentType == "" {
			event.AgentType = agentType
		}
		events = append(events, event)
		return event.EventID
	}

	for _, message := range messages {
		ts := message.Timestamp
		if ts.IsZero() {
			ts = row.UpdatedAt
		}
		switch message.Role {
		case "user":
			turnIndex++
			emit(model.RenderEvent{Type: "TurnBoundary", Timestamp: ts, TurnIndex: turnIndex})
			if text := strings.TrimSpace(contentText(message.Content)); text != "" {
				emit(model.RenderEvent{Type: "UserPrompt", Timestamp: ts, TurnIndex: turnIndex, Text: text})
			}
			lastAssistantOpen = false

		case "assistant":
			if turnIndex < 0 {
				turnIndex = 0
				emit(model.RenderEvent{Type: "TurnBoundary", Timestamp: ts, TurnIndex: turnIndex})
			}
			lastAssistantOpen = strings.TrimSpace(message.FinishReason) == ""
			if reasoning := strings.TrimSpace(message.Reasoning + "\n" + message.ReasoningContent); reasoning != "" {
				emit(model.RenderEvent{Type: "ThinkingStart", Timestamp: ts, TurnIndex: turnIndex, Text: strings.TrimSpace(reasoning)})
			}
			if text := strings.TrimSpace(contentText(message.Content)); text != "" {
				emit(model.RenderEvent{Type: "TextChunk", Timestamp: ts, TurnIndex: turnIndex, Text: text})
			}
			for _, invocation := range parseToolInvocations(message) {
				callID := invocation.ID
				eventID := emit(model.RenderEvent{
					Type: "ToolInvocation", Timestamp: ts, TurnIndex: turnIndex,
					ToolName: invocation.Name, ToolInput: invocation.Input, ToolCallID: callID,
				})
				ids[callID] = eventID
			}

		case "tool", "tool_result", "function":
			if turnIndex < 0 {
				continue
			}
			result := parseToolResult(message)
			parent := ids[result.CallID]
			event := model.RenderEvent{
				Type: "ToolResult", Timestamp: ts, TurnIndex: turnIndex,
				ParentEventID: parent, ToolCallID: result.CallID, ToolName: result.Name,
				Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode,
				DurationMs: result.DurationMs, ErrorKind: result.ErrorKind,
				TimedOut: result.TimedOut, TimeoutSeconds: result.TimeoutSeconds,
				Rejected: result.Rejected, ToolKind: result.ToolKind,
			}
			emit(event)
		}
	}

	// ended_at is nullable while Hermes is actively serving a run. The final
	// assistant finish_reason is the transcript-level close marker. As with
	// other adapters, the store mtime bounds stale interrupted sessions.
	if lastAssistantOpen && turnIndex >= 0 && !row.EndedAtSet {
		if lastWrite, err := r.lastStoreWrite(); err == nil {
			if event, ok := shared.TrailingInProgress(true, lastWrite, turnIndex); ok {
				emit(event)
			}
		}
	}
	return shared.DropEmptyRenderTurns(interleaveToolResults(events))
}

// interleaveToolResults moves each ToolResult directly after its parent
// ToolInvocation. Hermes stores parallel tool calls as one assistant message
// carrying N tool_calls followed by N separate tool messages, so raw stream
// order renders call1, call2, result1, result2 — the result boxes detach
// from the invocation they belong to. Pairing by tool_call_id restores
// call1, result1, call2, result2. Results whose parent invocation is unknown
// or in another turn keep their stream position.
func interleaveToolResults(events []model.RenderEvent) []model.RenderEvent {
	invocations := map[string]int{}
	for i, event := range events {
		if event.Type == "ToolInvocation" {
			invocations[event.EventID] = i
		}
	}
	if len(invocations) == 0 {
		return events
	}
	// A result may only move within its own turn: never reorder across a
	// TurnBoundary.
	turnOf := make([]int, len(events))
	turn := -1
	for i, event := range events {
		if event.Type == "TurnBoundary" {
			turn++
		}
		turnOf[i] = turn
	}
	resultsByParent := map[string][]int{}
	moved := make([]bool, len(events))
	for i, event := range events {
		if event.Type != "ToolResult" || event.ParentEventID == "" {
			continue
		}
		parent, ok := invocations[event.ParentEventID]
		if !ok || turnOf[parent] != turnOf[i] {
			continue
		}
		resultsByParent[event.ParentEventID] = append(resultsByParent[event.ParentEventID], i)
		moved[i] = true
	}
	if len(resultsByParent) == 0 {
		return events
	}
	out := make([]model.RenderEvent, 0, len(events))
	for i, event := range events {
		if moved[i] {
			continue
		}
		out = append(out, event)
		for _, resultIdx := range resultsByParent[event.EventID] {
			out = append(out, events[resultIdx])
		}
	}
	return out
}
