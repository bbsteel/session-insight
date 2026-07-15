package opencode

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

// RenderANSI implements reader.BaseSessionReader.
func (r *OpenCodeReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	return r.toRenderEvents(id)
}

func (r *OpenCodeReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.toRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

func (r *OpenCodeReader) toRenderEvents(sessionID string) ([]model.RenderEvent, error) {
	// Must error when the session is absent so server handler fall-through
	// does not steal UUIDs belonging to other agents (empty success short-
	// circuits GetRenderEvents / RenderANSI for everyone registered later).
	var n int
	if err := r.db.QueryRow(`SELECT count(*) FROM session WHERE id = ?`, sessionID).Scan(&n); err != nil {
		return nil, fmt.Errorf("opencode render: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("opencode session not found: %s", sessionID)
	}

	rows, err := r.db.Query(
		"SELECT id, time_created, data FROM message WHERE session_id = ? ORDER BY time_created ASC",
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("opencode render: %w", err)
	}
	defer rows.Close()

	type msgRow struct {
		id          string
		timeCreated int64
		data        string
	}
	var msgs []msgRow
	for rows.Next() {
		var m msgRow
		if rows.Scan(&m.id, &m.timeCreated, &m.data) == nil {
			msgs = append(msgs, m)
		}
	}

	// Separate user / assistant, preserving arrival order for user turns.
	var userIDs []string
	userMap := make(map[string]msgRow)
	assistantMap := make(map[string][]msgRow) // parentID → []assistant

	// OpenCode's on-disk in-progress marker: an assistant message gets
	// time.completed only when its run finishes (abort/error also close
	// it). The messages are scanned in time order, so after the loop this
	// holds the state of the newest assistant message.
	lastAssistantOpen := false

	for _, m := range msgs {
		var base msgBase
		if json.Unmarshal([]byte(m.data), &base) != nil {
			continue
		}
		switch base.Role {
		case "user":
			userIDs = append(userIDs, m.id)
			userMap[m.id] = m
		case "assistant":
			var a assistantMsgData
			if json.Unmarshal([]byte(m.data), &a) == nil {
				assistantMap[a.ParentID] = append(assistantMap[a.ParentID], m)
				lastAssistantOpen = a.Time != nil && a.Time.Completed == nil && a.Error == nil
			}
		}
	}

	var (
		events   []model.RenderEvent
		eventCtr int
		turnIdx  int
	)

	emit := func(e model.RenderEvent) string {
		if e.EventID == "" {
			e.EventID = fmt.Sprintf("evt-oc-%04d", eventCtr)
			eventCtr++
		}
		if e.AgentType == "" {
			e.AgentType = "opencode"
		}
		events = append(events, e)
		return e.EventID
	}

	for _, uid := range userIDs {
		uMsg := userMap[uid]
		aMsgs := assistantMap[uid]
		if len(aMsgs) == 0 {
			continue
		}

		ts := time.UnixMilli(uMsg.timeCreated)
		emit(model.RenderEvent{
			Type:      "TurnBoundary",
			Timestamp: ts,
			TurnIndex: turnIdx,
		})

		uParts := r.readParts(uid)
		if text := strings.TrimSpace(strings.Join(uParts.Texts, "\n")); text != "" {
			emit(model.RenderEvent{
				Type:      "UserPrompt",
				Timestamp: ts,
				TurnIndex: turnIdx,
				Text:      text,
			})
		}

		for _, aMsg := range aMsgs {
			aTs := time.UnixMilli(aMsg.timeCreated)
			rp := r.readRenderParts(aMsg.id)

			if len(rp.reasoning) > 0 {
				emit(model.RenderEvent{
					Type:      "ThinkingStart",
					Timestamp: aTs,
					TurnIndex: turnIdx,
					Text:      strings.Join(rp.reasoning, "\n"),
				})
			}

			for _, txt := range rp.texts {
				if txt != "" {
					emit(model.RenderEvent{
						Type:      "TextChunk",
						Timestamp: aTs,
						TurnIndex: turnIdx,
						Text:      txt,
					})
				}
			}

			for _, t := range rp.tools {
				toolInput := map[string]any{}
				if t.title != "" {
					toolInput["title"] = t.title
				}
				// Normalise OpenCode camelCase edit inputs → snake_case
				if t.input != nil {
					if v, ok := t.input["filePath"].(string); ok {
						toolInput["file_path"] = v
					}
					if v, ok := t.input["oldString"].(string); ok {
						toolInput["old_string"] = v
					}
					if v, ok := t.input["newString"].(string); ok {
						toolInput["new_string"] = v
					}
				}
				invID := emit(model.RenderEvent{
					Type:      "ToolInvocation",
					Timestamp: aTs,
					TurnIndex: turnIdx,
					ToolName:  t.name,
					ToolInput: toolInput,
				})
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     aTs,
					TurnIndex:     turnIdx,
					ParentEventID: invID,
					Stdout:        t.output,
					Stderr:        t.errText,
					ExitCode:      t.exitCode,
				})
			}
		}

		turnIdx++
	}

	// Trailing "推理中…" row while the newest assistant message is still
	// open. The store-wide write guard bounds the case where the whole
	// OpenCode server died before writing time.completed.
	if lastAssistantOpen && turnIdx > 0 {
		if lastWrite, err := r.lastStoreWrite(); err == nil {
			if evt, ok := shared.TrailingInProgress(true, lastWrite, turnIdx-1); ok {
				emit(evt)
			}
		}
	}

	return events, nil
}

type renderPartTool struct {
	name     string
	title    string
	output   string
	errText  string
	exitCode int
	input    map[string]any
}

type renderParts struct {
	texts     []string
	reasoning []string
	tools     []renderPartTool
}

// readRenderParts is a richer variant of readParts that also captures tool
// output and error text needed for terminal rendering.
func (r *OpenCodeReader) readRenderParts(messageID string) renderParts {
	rows, err := r.db.Query(
		"SELECT data FROM part WHERE message_id = ? ORDER BY id ASC",
		messageID,
	)
	if err != nil {
		return renderParts{}
	}
	defer rows.Close()

	var out renderParts
	for rows.Next() {
		var data string
		if rows.Scan(&data) != nil {
			continue
		}
		var p partData
		if json.Unmarshal([]byte(data), &p) != nil {
			continue
		}
		switch p.Type {
		case "text":
			if p.Text != "" {
				out.texts = append(out.texts, p.Text)
			}
		case "reasoning":
			if p.Text != "" {
				out.reasoning = append(out.reasoning, p.Text)
			}
		case "tool":
			t := renderPartTool{name: p.Tool}
			if p.State != nil {
				t.title = p.State.Title
				t.output = p.State.Output
				t.errText = p.State.Error
				t.input = p.State.Input
				if p.State.Status == "error" {
					t.exitCode = 1
				}
			}
			if t.title == "" && p.Description != "" {
				t.title = p.Description
			}
			out.tools = append(out.tools, t)
		}
	}
	return out
}
