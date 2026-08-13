package copilot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

// RenderANSI implements reader.BaseSessionReader.
func (r *CopilotReader) copilotEvents(id string) ([]model.RenderEvent, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid copilot session id: %q", id)
	}
	eventsPath := filepath.Join(r.sessionDir, id, "events.jsonl")
	if _, err := os.Stat(eventsPath); err != nil {
		return nil, fmt.Errorf("copilot session not found %q: %w", id, err)
	}
	events, err := parseCopilotRenderEventsForSession(eventsPath, id)
	if err != nil {
		return nil, err
	}
	return shared.InterleaveToolResults(events), nil
}

func (r *CopilotReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return r.copilotEvents(id)
}

func (r *CopilotReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.copilotEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

// parseCopilotRenderEvents parses a Copilot events.jsonl file into a flat
// []model.RenderEvent stream suitable for render.FormatEvents.
func parseCopilotRenderEvents(path string) ([]model.RenderEvent, error) {
	return parseCopilotRenderEventsForSession(path, "")
}

// parseCopilotRenderEventsForSession additionally associates subagent
// lifecycle events with their collaboration invocation when the session ID
// is known. Parent-stream content stays root-associated: a reconstructed
// time window is never exposed as exact child content.
func parseCopilotRenderEventsForSession(path, sessionID string) ([]model.RenderEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		events       []model.RenderEvent
		eventCtr     int
		turnIndex    int
		pendingTools = make(map[string]string) // toolCallId -> ToolInvocation EventID

		// Copilot events carry the most explicit turn brackets of any agent:
		// assistant.turn_start / assistant.turn_end pairs, plus a
		// session.shutdown on any orderly exit. An open bracket at EOF means
		// the CLI is still working (or was killed mid-turn — the LiveWindow
		// guard in shared.TrailingInProgress bounds that case).
		turnOpen bool
	)

	currentTurnIndex := func() int {
		if turnIndex == 0 {
			return 0
		}
		return turnIndex - 1
	}

	emit := func(evt model.RenderEvent) string {
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("cop-%04d-%s", eventCtr, evt.Type)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "copilot"
		}
		events = append(events, evt)
		return evt.EventID
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var jev jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &jev); err != nil {
			continue
		}

		ts := parseCopilotTimestamp(jev.Timestamp)

		switch jev.Type {
		case "assistant.turn_start":
			turnOpen = true

		case "assistant.turn_end", "session.shutdown", "session.error":
			turnOpen = false

		case "user.message":
			content, _ := extractString(jev.Data, "content")
			if strings.TrimSpace(content) == "" {
				continue
			}
			turnIndex++
			emit(model.RenderEvent{
				EventID:   fmt.Sprintf("cop-%04d-boundary", eventCtr),
				Type:      "TurnBoundary",
				Timestamp: ts,
				TurnIndex: turnIndex - 1,
			})
			eventCtr++

			emit(model.RenderEvent{
				Type:      "UserPrompt",
				Timestamp: ts,
				TurnIndex: turnIndex - 1,
				Text:      content,
			})

		case "assistant.message":
			// encryptedContent is opaque ciphertext, not displayable text.
			content, _ := extractString(jev.Data, "content")
			if content != "" {
				emit(model.RenderEvent{
					Type:      "TextChunk",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      content,
				})
			}

		case "tool.execution_start":
			toolName, _ := extractString(jev.Data, "toolName")
			toolCallID, _ := extractString(jev.Data, "toolCallId")

			var toolInput map[string]any
			if raw, ok := jev.Data["arguments"]; ok {
				if m, ok := raw.(map[string]any); ok {
					toolInput = m
				}
			} else if raw, ok := jev.Data["parameters"]; ok {
				// Retain compatibility with older event producers.
				toolInput, _ = raw.(map[string]any)
			}

			invID := emit(model.RenderEvent{
				Type:       "ToolInvocation",
				Timestamp:  ts,
				TurnIndex:  currentTurnIndex(),
				ToolName:   toolName,
				ToolCallID: toolCallID,
				ToolInput:  toolInput,
			})
			if toolCallID != "" {
				pendingTools[toolCallID] = invID
			}

		case "tool.execution_complete":
			toolCallID, _ := extractString(jev.Data, "toolCallId")
			exitCode, stdout, stderr, durationMs := copilotToolResult(jev.Data)

			parentEventID := ""
			if toolCallID != "" {
				parentEventID = pendingTools[toolCallID]
				delete(pendingTools, toolCallID)
			}

			emit(model.RenderEvent{
				Type:          "ToolResult",
				Timestamp:     ts,
				TurnIndex:     currentTurnIndex(),
				ToolCallID:    toolCallID,
				Stdout:        stdout,
				Stderr:        stderr,
				ExitCode:      exitCode,
				DurationMs:    durationMs,
				ParentEventID: parentEventID,
			})

		case "skill.invoked":
			name, _ := extractString(jev.Data, "skill_name")
			if name == "" {
				name, _ = extractString(jev.Data, "name")
			}
			if name != "" {
				emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "skill_invoked",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      name,
				})
			}

		case "subagent.started":
			name, _ := extractString(jev.Data, "agentDisplayName")
			if name == "" {
				name, _ = extractString(jev.Data, "subagent_id")
			}
			if name != "" {
				evt := model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "subagent_started",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      name,
				}
				// The lifecycle marker belongs to the child invocation keyed
				// by toolCallId; everything else in the parent stream keeps
				// the root default (empty InvocationID).
				if tc, _ := extractString(jev.Data, "toolCallId"); tc != "" && sessionID != "" {
					evt.InvocationID = collaboration.ChildInvocationID("copilot", sessionID, tc)
				}
				emit(evt)
			}

		case "session.model_change":
			if newModel, ok := extractString(jev.Data, "newModel"); ok && newModel != "" {
				emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "model_change",
					Timestamp: ts,
					TurnIndex: currentTurnIndex(),
					Text:      newModel,
				})
			}
		}
	}

	// Trailing "推理中…" row for a turn still bracket-open at EOF.
	if turnOpen && turnIndex > 0 {
		if fi, statErr := f.Stat(); statErr == nil {
			if evt, ok := shared.TrailingInProgress(true, fi.ModTime(), turnIndex-1); ok {
				emit(evt)
			}
		}
	}

	return events, scanner.Err()
}

func copilotToolResult(data map[string]any) (exitCode int, stdout, stderr string, durationMs int64) {
	exitCode = int(extractFloat(data, "exit_code"))
	if exitCode == 0 {
		exitCode = int(extractFloat(data, "exitCode"))
	}
	stdout, _ = extractString(data, "stdout")
	stderr, _ = extractString(data, "stderr")
	durationMs = int64(extractFloat(data, "duration_ms"))
	if durationMs == 0 {
		durationMs = int64(extractFloat(data, "durationMs"))
	}

	if result, ok := data["result"].(map[string]any); ok {
		if stdout == "" {
			stdout, _ = extractString(result, "content")
		}
		if stdout == "" {
			stdout, _ = extractString(result, "detailedContent")
		}
	}
	if failure, ok := data["error"].(map[string]any); ok && stderr == "" {
		stderr, _ = extractString(failure, "message")
	}
	if success, ok := data["success"].(bool); ok && !success && exitCode == 0 {
		exitCode = 1
	}
	return exitCode, stdout, stderr, durationMs
}
