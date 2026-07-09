package shared

import "github.com/bbsteel/session-insight/internal/model"

// FilterEmptyTurns removes turns that have no user message, no assistant message,
// and no tool calls (e.g. trailing empty user messages).
func FilterEmptyTurns(turns []model.TurnVM) []model.TurnVM {
	filtered := turns[:0]
	for _, t := range turns {
		if t.UserMessage == "" && t.AssistantMessage == "" && t.ToolCallCount == 0 {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
