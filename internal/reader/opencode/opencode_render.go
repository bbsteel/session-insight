package opencode

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"session-insight/internal/model"
	"session-insight/internal/render"
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

	return events, nil
}

type renderPartTool struct {
	name     string
	title    string
	output   string
	errText  string
	exitCode int
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
