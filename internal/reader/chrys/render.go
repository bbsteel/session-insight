package chrys

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/render"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// RenderANSI implements reader.BaseSessionReader.
func (r *ChrysReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.toRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

func (r *ChrysReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return r.toRenderEvents(id)
}

// subagentIndex maps a parent function_call's call_id to the sub-agent's
// transcript file under <session-dir>/sub_agents/sessions/. The join key is
// the sidecar's meta.parent_provider_call_id — tool-kind annotations on the
// call itself (_chrys_tool_kind) only exist in newer chrys versions, so the
// directory scan is the reliable source.
func buildSubagentIndex(sessionDir string) map[string]string {
	dir := filepath.Join(sessionDir, "sub_agents", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	index := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		sf, err := readSessionFile(path)
		if err != nil {
			continue
		}
		if sf.Meta.ParentProviderCallID != "" {
			index[sf.Meta.ParentProviderCallID] = path
		}
	}
	return index
}

type renderState struct {
	events   []model.RenderEvent
	eventCtr int
	subIndex map[string]string
}

func (rs *renderState) emit(e model.RenderEvent) string {
	if e.EventID == "" {
		e.EventID = fmt.Sprintf("evt-chrys-%04d", rs.eventCtr)
		rs.eventCtr++
	}
	if e.AgentType == "" {
		e.AgentType = "chrys"
	}
	rs.events = append(rs.events, e)
	return e.EventID
}

func (r *ChrysReader) toRenderEvents(id string) ([]model.RenderEvent, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid chrys session id: %q", id)
	}
	sessionDir := filepath.Join(r.sessionsDir, id)
	sf, err := readEffectiveSession(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("chrys session not found %q: %w", id, err)
	}

	rs := &renderState{subIndex: buildSubagentIndex(sessionDir)}

	if len(sf.State.CompressedMsgs) > 0 {
		rs.emit(model.RenderEvent{Type: "CompactionBoundary", TurnIndex: 0})
	}

	turnIdx := -1
	for _, m := range sf.State.Messages {
		kind := m.markerKind()
		ts := m.createdAt()

		switch {
		case kind == "interrupted":
			if turnIdx < 0 {
				turnIdx = 0
			}
			if m.isInFlightCheckpoint() {
				// Turn still in progress (chrys's recovery checkpoint). Render a
				// neutral placeholder, not a red interruption; the frontend
				// overlays a spinning hourglass on this row.
				rs.emit(model.RenderEvent{
					Type:      "AgentSpecific",
					Subtype:   "in_progress",
					Timestamp: ts,
					TurnIndex: turnIdx,
				})
				break
			}
			text := m.userText()
			if text == "" {
				text = "会话被中断"
			}
			rs.emit(model.RenderEvent{
				Type:      "AgentSpecific",
				Subtype:   "interrupted",
				Timestamp: ts,
				TurnIndex: turnIdx,
				Text:      text,
			})
		case kind != "":
			// turn markers etc. carry no displayable content

		case m.Role == "user":
			turnIdx++
			var meta map[string]any
			if sf.Meta.AgentDisplayName != "" {
				meta = map[string]any{"agent_label": sf.Meta.AgentDisplayName}
			}
			boundaryID := rs.emit(model.RenderEvent{
				Type:      "TurnBoundary",
				Timestamp: ts,
				TurnIndex: turnIdx,
				Model:     sf.Meta.ModelID,
				Metadata:  meta,
			})
			rs.emit(model.RenderEvent{
				ParentEventID: boundaryID,
				Type:          "UserPrompt",
				Timestamp:     ts,
				TurnIndex:     turnIdx,
				Text:          m.userText(),
			})

		case m.Role == "assistant":
			if turnIdx < 0 {
				turnIdx = 0
			}
			rs.appendAssistantMessage(m, turnIdx, 0)

		case m.Role == "tool":
			if turnIdx < 0 {
				turnIdx = 0
			}
			rs.appendToolMessage(m, turnIdx, 0, true)
		}
	}

	return shared.DropEmptyRenderTurns(rs.events), nil
}

// appendAssistantMessage emits the render events for one assistant message:
// reasoning, visible text (including _intermediate_text, which chrys shows
// before the message's tool calls), and tool invocations, in content order.
func (rs *renderState) appendAssistantMessage(m chrysMessage, turnIdx, depth int) {
	ts := m.createdAt()
	intermediate := m.intermediateText()

	for _, c := range m.Contents {
		switch c.Type {
		case "text_reasoning":
			if strings.TrimSpace(c.Text) != "" {
				rs.emit(model.RenderEvent{
					Type:      "ThinkingStart",
					Timestamp: ts,
					TurnIndex: turnIdx,
					Depth:     depth,
					Text:      c.Text,
				})
			}
		case "text":
			if strings.TrimSpace(c.Text) != "" {
				rs.emit(model.RenderEvent{
					Type:      "TextChunk",
					Timestamp: ts,
					TurnIndex: turnIdx,
					Depth:     depth,
					Text:      c.Text,
				})
			}
		case "function_call":
			if intermediate != "" {
				rs.emit(model.RenderEvent{
					Type:      "TextChunk",
					Timestamp: ts,
					TurnIndex: turnIdx,
					Depth:     depth,
					Text:      intermediate,
				})
				intermediate = ""
			}
			rs.emit(model.RenderEvent{
				EventID:    "call-" + c.CallID,
				Type:       "ToolInvocation",
				Timestamp:  ts,
				TurnIndex:  turnIdx,
				Depth:      depth,
				ToolName:   c.Name,
				ToolCallID: c.CallID,
				ToolInput:  normalizeToolInput(c.Name, c.argsMap()),
			})
		}
	}

	if intermediate != "" {
		rs.emit(model.RenderEvent{
			Type:      "TextChunk",
			Timestamp: ts,
			TurnIndex: turnIdx,
			Depth:     depth,
			Text:      intermediate,
		})
	}
}

// appendToolMessage emits ToolResult events; when a result's call_id matches
// a sub-agent transcript (spliceSubagents), that transcript is rendered as a
// nested Depth+1 branch before the summary result, mirroring how chrys's own
// TUI shows the sub-agent's tool calls inside the parent tool block.
func (rs *renderState) appendToolMessage(m chrysMessage, turnIdx, depth int, spliceSubagents bool) {
	ts := m.createdAt()
	for _, c := range m.Contents {
		if c.Type != "function_result" {
			continue
		}

		if spliceSubagents {
			if subPath, ok := rs.subIndex[c.CallID]; ok {
				rs.spliceSubagent(subPath, turnIdx, depth+1)
			}
		}

		exitCode := 0
		stderr := ""
		if c.failed() {
			exitCode = 1
			stderr = c.errorMessage()
		}
		rs.emit(model.RenderEvent{
			ParentEventID: "call-" + c.CallID,
			Type:          "ToolResult",
			Timestamp:     ts,
			TurnIndex:     turnIdx,
			Depth:         depth,
			ToolCallID:    c.CallID,
			Stdout:        c.resultText(),
			Stderr:        stderr,
			ExitCode:      exitCode,
		})
	}
}

// spliceSubagent renders one sub-agent transcript nested at depth. The
// sub-agent's own user prompt is skipped — it duplicates the parent
// function_call's prompt argument, already visible in the invocation box.
// Sub-agent events keep the parent's TurnIndex so the shared formatter does
// not print a spurious turn banner mid-splice. Only one nesting level is
// followed: chrys stores every sub-agent flat under the parent session's
// sub_agents/sessions/, so a deeper transcript has nowhere to live.
func (rs *renderState) spliceSubagent(path string, turnIdx, depth int) {
	sf, err := readSessionFile(path)
	if err != nil {
		rs.emit(model.RenderEvent{
			Type:      "AgentSpecific",
			Subtype:   "subagent_load_error",
			TurnIndex: turnIdx,
			Depth:     depth - 1,
			Payload:   map[string]any{"reason": err.Error()},
		})
		return
	}

	label := sf.Meta.AgentDisplayName
	if label == "" {
		label = sf.Meta.ToolName
	}
	rs.emit(model.RenderEvent{
		Type:      "AgentSpecific",
		Subtype:   "subagent_started",
		TurnIndex: turnIdx,
		Depth:     depth,
		Text:      label,
	})

	toolCalls := 0
	var tokens int64
	for _, m := range sf.State.Messages {
		for _, c := range m.Contents {
			if c.Type == "function_call" {
				toolCalls++
			}
		}
		tokens += m.groupTokenCount()
		if m.markerKind() != "" || m.Role == "user" {
			continue
		}
		switch m.Role {
		case "assistant":
			rs.appendAssistantMessage(m, turnIdx, depth)
		case "tool":
			rs.appendToolMessage(m, turnIdx, depth, false)
		}
	}

	summary := fmt.Sprintf("Tool calls: %d", toolCalls)
	if tokens > 0 {
		summary += fmt.Sprintf(" · Total: %s tokens", formatTokens(tokens))
	}
	rs.emit(model.RenderEvent{
		Type:      "AgentSpecific",
		Subtype:   "subagent_summary",
		TurnIndex: turnIdx,
		Depth:     depth,
		Text:      summary,
	})
}

// formatTokens renders a token count the way chrys's own TUI does (106.9k).
func formatTokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// normalizeToolInput adapts chrys-specific argument key names so shared
// render paths (edit diff boxes, edit extraction) recognise them:
// edit_file/write_file/read_file use "path" where the shared vocabulary
// expects "file_path".
func normalizeToolInput(toolName string, input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	if _, hasFilePath := input["file_path"]; !hasFilePath {
		if p, ok := input["path"].(string); ok {
			switch toolName {
			case "edit_file", "write_file", "read_file":
				input["file_path"] = p
				delete(input, "path")
			}
		}
	}
	return input
}
