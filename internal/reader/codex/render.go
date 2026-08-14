package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

// RenderANSI implements reader.BaseSessionReader.
func (r *CodexReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("codex session not found: %s", id)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		return nil, err
	}
	events = shared.InterleaveToolResults(events)
	// A subagent rollout rendered through its backing Session carries the
	// child invocation ID on every event; root events stay unmarked (absent
	// InvocationID means the root invocation).
	if invID := r.childInvocationID(id); invID != "" {
		for i := range events {
			events[i].InvocationID = invID
		}
	}
	return events, nil
}

func (r *CodexReader) RenderANSI(id string, cols int) (string, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return "", fmt.Errorf("codex session not found: %s", id)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

// ---- JSONL shapes ----

func cellIDFromText(text string) string {
	m := cellIDRe.FindStringSubmatch(text)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// summarizeExecInput removes Codex's JavaScript bridge from the replay while
// retaining the actual command the agent asked to run.
func summarizeExecInput(name, input string) (string, map[string]any) {
	if name != "exec" {
		return name, parseArguments(input)
	}
	m := execCommandRe.FindStringSubmatch(input)
	if len(m) != 2 {
		return name, parseArguments(input)
	}
	var command string
	if json.Unmarshal([]byte(m[1]), &command) != nil || command == "" {
		return name, parseArguments(input)
	}
	return name, map[string]any{"command": command}
}

type codexRenderAttempt struct {
	events          []model.RenderEvent
	groups          []*codexRenderRollback
	originalIndex   int
	goalPromptEvent int
}

type codexRenderRollback struct {
	timestamp time.Time
	removed   []*codexRenderAttempt
}

type codexCellTask struct {
	toolCallID    string
	parentEventID string
}

func codexRenderAttemptVisible(a *codexRenderAttempt) bool {
	if a == nil {
		return false
	}
	for _, evt := range a.events {
		switch evt.Type {
		case "TurnBoundary":
		case "UserPrompt":
			if strings.TrimSpace(evt.Text) != "" {
				return true
			}
		case "AgentSpecific":
			if evt.Subtype != "turn_duration" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// renderAttemptHasText reports whether the attempt already carries assistant
// text, so the task_complete last_agent_message fallback only fires when no
// AgentMessage item was seen.
func renderAttemptHasText(a *codexRenderAttempt) bool {
	for _, evt := range a.events {
		if evt.Type == "TextChunk" && evt.Text != "" {
			return true
		}
	}
	return false
}

// codexToRenderEvents parses a Codex JSONL session file into a flat
// []model.RenderEvent stream suitable for render.FormatEvents.
func codexToRenderEvents(path string) ([]model.RenderEvent, error) {
	events, _, err := codexToRenderEventsDetailed(path)
	return events, err
}

func codexToRenderEventsDetailed(path string) ([]model.RenderEvent, int, error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer source.Close()
	var modTime *time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		value := info.ModTime()
		modTime = &value
	}
	return codexToRenderEventsDetailedReader(path, source, modTime)
}

func codexToRenderEventsSnapshot(path, snapshotPath string) ([]model.RenderEvent, int, error) {
	snapshot, err := os.Open(snapshotPath)
	if err != nil {
		return nil, 0, err
	}
	defer snapshot.Close()
	return codexToRenderEventsDetailedReader(path, snapshot, nil)
}

func codexToRenderEventsDetailedReader(path string, source io.Reader, sourceModTime *time.Time) ([]model.RenderEvent, int, error) {

	var (
		attempts     []*codexRenderAttempt
		active       []*codexRenderAttempt
		rootGroups   []*codexRenderRollback
		current      *codexRenderAttempt
		fileTag      = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		eventCtr     int
		pendingTools = make(map[string]string) // callID -> ToolInvocation EventID
		completed    = make(map[string]bool)   // patch_apply_end already emitted the result
		cellTasks    = make(map[string]codexCellTask)
		waitTasks    = make(map[string]codexCellTask)
		skipped      int

		// Codex rollouts carry explicit turn brackets: task_started opens a
		// turn, task_complete / turn_aborted closes it. An open bracket at
		// EOF means the CLI is still working (or died mid-turn — the
		// LiveWindow guard in shared.TrailingInProgress bounds that case).
		turnOpen      bool
		pendingGoal   string
		pendingGoalTS time.Time
		lastGoal      string
	)

	emit := func(evt model.RenderEvent) string {
		if current == nil {
			return ""
		}
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("evt-%s-%04d", fileTag, eventCtr)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "codex"
		}
		current.events = append(current.events, evt)
		return evt.EventID
	}

	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			skipped++
			continue
		}

		ts := parseTimestamp(evt.Timestamp)

		switch evt.Type {
		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				skipped++
				continue
			}
			switch p.Type {
			case "thread_goal_updated":
				if p.Goal != nil {
					if objective := strings.TrimSpace(p.Goal.Objective); objective != "" && objective != lastGoal {
						pendingGoal = objective
						pendingGoalTS = ts
						lastGoal = objective
					}
				}
			case "task_started":
				turnOpen = true
				current = &codexRenderAttempt{goalPromptEvent: -1}
				attempts = append(attempts, current)
				active = append(active, current)
				emit(model.RenderEvent{
					Type:      "TurnBoundary",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
				})
				if pendingGoal != "" {
					current.goalPromptEvent = len(current.events)
					emit(model.RenderEvent{
						Type:      "UserPrompt",
						Timestamp: pendingGoalTS,
						TurnIndex: len(attempts) - 1,
						Text:      pendingGoal,
					})
					pendingGoal = ""
					pendingGoalTS = time.Time{}
				}

			case "task_complete":
				turnOpen = false
				// Defensive fallback: paginated history mode carries the final
				// assistant text on task_complete. When no AgentMessage item
				// arrived (aborted stream, partial flush), keep the text
				// visible instead of dropping the turn.
				if current != nil && p.LastAgentMessage != "" && !renderAttemptHasText(current) {
					emit(model.RenderEvent{
						Type:      "TextChunk",
						Timestamp: ts,
						TurnIndex: len(attempts) - 1,
						Text:      p.LastAgentMessage,
					})
				}

			case "turn_aborted":
				turnOpen = false

			case "thread_rolled_back":
				n := p.NumTurns
				if n < 0 {
					n = 0
				}
				if n > len(active) {
					n = len(active)
				}
				if n > 0 {
					group := &codexRenderRollback{
						timestamp: ts,
						removed:   append([]*codexRenderAttempt(nil), active[len(active)-n:]...),
					}
					active = active[:len(active)-n]
					if len(active) == 0 {
						rootGroups = append(rootGroups, group)
					} else {
						target := active[len(active)-1]
						target.groups = append(target.groups, group)
					}
				}
				current = nil
				turnOpen = false

			case "user_message":
				if current == nil || p.Message == "" {
					continue
				}
				// A direct user message in the same turn supersedes a goal
				// update that was queued just before task_started.
				if current.goalPromptEvent >= 0 {
					current.events = append(current.events[:current.goalPromptEvent], current.events[current.goalPromptEvent+1:]...)
					current.goalPromptEvent = -1
				}
				emit(model.RenderEvent{
					Type:      "UserPrompt",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
					Text:      p.Message,
				})

			case "agent_message":
				if p.Message != "" {
					emit(model.RenderEvent{
						Type:      "TextChunk",
						Timestamp: ts,
						TurnIndex: len(attempts) - 1,
						Text:      p.Message,
					})
				}

			case "item_completed":
				// Paginated history mode (Codex CLI ~0.147+): text arrives as
				// application-layer items instead of user_message/agent_message
				// events. Only message items carry text; CommandExecution /
				// FileChange items duplicate response_item tool records and are
				// ignored here.
				if current == nil || p.Item == nil {
					continue
				}
				switch p.Item.Type {
				case "UserMessage":
					text := codexItemText(p.Item)
					if text == "" {
						continue
					}
					// Same goal-prompt supersession as the user_message branch.
					if current.goalPromptEvent >= 0 {
						current.events = append(current.events[:current.goalPromptEvent], current.events[current.goalPromptEvent+1:]...)
						current.goalPromptEvent = -1
					}
					emit(model.RenderEvent{
						Type:      "UserPrompt",
						Timestamp: ts,
						TurnIndex: len(attempts) - 1,
						Text:      text,
					})
				case "AgentMessage":
					if text := codexItemText(p.Item); text != "" {
						emit(model.RenderEvent{
							Type:      "TextChunk",
							Timestamp: ts,
							TurnIndex: len(attempts) - 1,
							Text:      text,
						})
					}
				}

			case "patch_apply_end":
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
					completed[p.CallID] = true
				}
				exitCode := 0
				if !p.Success {
					exitCode = 1
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        p.Stdout,
					Stderr:        p.Stderr,
					ExitCode:      exitCode,
					ParentEventID: parentEventID,
				})
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "message":
				// agent_message already covers assistant text; response_item
				// assistant blocks would duplicate it.
			case "function_call":
				input := parseArguments(p.Arguments)
				if p.Name == "wait" {
					if cellID, _ := input["cell_id"].(string); cellID != "" {
						if task, ok := cellTasks[cellID]; ok {
							waitTasks[p.CallID] = task
							continue
						}
					}
				}
				invID := emit(model.RenderEvent{
					Type:       "ToolInvocation",
					Timestamp:  ts,
					TurnIndex:  len(attempts) - 1,
					ToolName:   p.Name,
					ToolCallID: p.CallID,
					ToolInput:  input,
				})
				if p.CallID != "" {
					pendingTools[p.CallID] = invID
				}

			case "function_call_output":
				output := outputText(p.Output)
				if task, ok := waitTasks[p.CallID]; ok {
					delete(waitTasks, p.CallID)
					if cellIDFromText(output) != "" {
						continue
					}
					emit(model.RenderEvent{
						Type:          "ToolResult",
						Timestamp:     ts,
						TurnIndex:     len(attempts) - 1,
						ToolCallID:    task.toolCallID,
						Stdout:        output,
						ExitCode:      extractExitCode(output),
						ParentEventID: task.parentEventID,
					})
					for cellID, active := range cellTasks {
						if active == task {
							delete(cellTasks, cellID)
						}
					}
					continue
				}
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        output,
					ExitCode:      extractExitCode(output),
					ParentEventID: parentEventID,
				})

			case "custom_tool_call":
				name := p.Name
				if name == "" {
					name = p.CustomToolName
				}
				input := p.Input
				if input == "" {
					input = p.Arguments
				}
				name, input = unwrapApplyPatchExec(name, input)
				name, toolInput := summarizeExecInput(name, input)
				invID := emit(model.RenderEvent{
					Type:       "ToolInvocation",
					Timestamp:  ts,
					TurnIndex:  len(attempts) - 1,
					ToolName:   name,
					ToolCallID: p.CallID,
					ToolInput:  toolInput,
				})
				if p.CallID != "" {
					pendingTools[p.CallID] = invID
				}

			case "custom_tool_call_output":
				output := outputText(p.Output)
				if cellID := cellIDFromText(output); cellID != "" {
					if parentEventID := pendingTools[p.CallID]; parentEventID != "" {
						cellTasks[cellID] = codexCellTask{toolCallID: p.CallID, parentEventID: parentEventID}
						delete(pendingTools, p.CallID)
						continue
					}
				}
				if p.CallID != "" && completed[p.CallID] {
					delete(completed, p.CallID)
					continue
				}
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        output,
					ExitCode:      extractExitCode(output),
					ParentEventID: parentEventID,
				})
			}

		case "compacted":
			// Emit a CompactionBoundary so the formatter records a
			// minimap compaction marker (blue "C") and the terminal
			// view can show a context-compression indicator.
			if current != nil {
				emit(model.RenderEvent{
					Type:      "CompactionBoundary",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
				})
			}
		}
	}

	// Trailing "推理中…" row for a turn still bracket-open at EOF. Runs
	// only when the trailing turn already has visible content: a bare
	// task_started must not be resurrected by the progress marker.
	if turnOpen && current != nil && codexRenderAttemptVisible(current) {
		if sourceModTime != nil {
			if evt, ok := shared.TrailingInProgress(true, *sourceModTime, len(attempts)-1); ok {
				emit(evt)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, err
	}

	visible := make(map[*codexRenderAttempt]bool, len(attempts))
	original := 0
	for _, attempt := range attempts {
		if !codexRenderAttemptVisible(attempt) {
			continue
		}
		attempt.originalIndex = original
		original++
		visible[attempt] = true
	}
	activeIndex := make(map[*codexRenderAttempt]int, len(active))
	for _, attempt := range active {
		if visible[attempt] {
			activeIndex[attempt] = len(activeIndex)
		}
	}

	var events []model.RenderEvent
	var appendAttempt func(*codexRenderAttempt, bool)
	appendGroup := func(group *codexRenderRollback, target *codexRenderAttempt, rolledBackTarget bool) {}
	appendAttempt = func(attempt *codexRenderAttempt, rolledBack bool) {
		if !visible[attempt] {
			return
		}
		idx := activeIndex[attempt]
		if rolledBack {
			idx = -(attempt.originalIndex + 1)
		}
		for i, evt := range attempt.events {
			evt.TurnIndex = idx
			if i == 0 && evt.Type == "TurnBoundary" {
				if evt.Metadata == nil {
					evt.Metadata = map[string]any{}
				}
				evt.Metadata["original_turn_index"] = attempt.originalIndex
				if rolledBack {
					evt.Metadata["rolled_back"] = true
				}
			}
			events = append(events, evt)
		}
		for _, group := range attempt.groups {
			appendGroup(group, attempt, rolledBack)
		}
	}
	appendGroup = func(group *codexRenderRollback, target *codexRenderAttempt, rolledBackTarget bool) {
		visibleRemoved := make([]*codexRenderAttempt, 0, len(group.removed))
		for _, attempt := range group.removed {
			if visible[attempt] {
				visibleRemoved = append(visibleRemoved, attempt)
			}
		}
		if len(visibleRemoved) == 0 {
			return
		}
		targetIndex := -1
		resumeTurn := 0
		if target != nil {
			resumeTurn = target.originalIndex + 1
			if rolledBackTarget {
				targetIndex = -(target.originalIndex + 1)
			} else {
				targetIndex = activeIndex[target]
			}
		}
		meta := map[string]any{
			"count":       len(visibleRemoved),
			"resume_turn": resumeTurn,
		}
		events = append(events, model.RenderEvent{
			Type:      "RollbackStart",
			Timestamp: group.timestamp,
			TurnIndex: targetIndex,
			AgentType: "codex",
			Metadata:  meta,
		})
		for _, attempt := range visibleRemoved {
			appendAttempt(attempt, true)
		}
		events = append(events, model.RenderEvent{
			Type:      "RollbackEnd",
			Timestamp: group.timestamp,
			TurnIndex: targetIndex,
			AgentType: "codex",
			Metadata:  meta,
		})
	}

	for _, group := range rootGroups {
		appendGroup(group, nil, false)
	}
	for _, attempt := range active {
		appendAttempt(attempt, false)
	}

	return events, skipped, nil
}

// parseArguments attempts to unmarshal a JSON string into map[string]any.
// On failure, it wraps the raw string as {"args": args}.
func parseArguments(args string) map[string]any {
	if args == "" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(args), &result); err != nil {
		return map[string]any{"args": args}
	}
	return result
}
