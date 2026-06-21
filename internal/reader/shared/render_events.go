package shared

import (
	"strings"

	"session-insight/internal/model"
)

// DropEmptyRenderTurns removes TurnBoundary+UserPrompt pairs for turns that carry
// no real content. AgentSpecific/turn_duration markers do not count as content.
func DropEmptyRenderTurns(events []model.RenderEvent) []model.RenderEvent {
	hasContent := make(map[int]bool)
	for _, e := range events {
		switch e.Type {
		case "TurnBoundary":
		case "UserPrompt":
			if strings.TrimSpace(e.Text) != "" {
				hasContent[e.TurnIndex] = true
			}
		case "AgentSpecific":
			if e.Subtype != "turn_duration" {
				hasContent[e.TurnIndex] = true
			}
		default:
			hasContent[e.TurnIndex] = true
		}
	}

	filtered := make([]model.RenderEvent, 0, len(events))
	for _, e := range events {
		if (e.Type == "TurnBoundary" || e.Type == "UserPrompt") && !hasContent[e.TurnIndex] {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}
