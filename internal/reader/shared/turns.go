package shared

import "github.com/bbsteel/session-insight/internal/model"

// FilterEmptyTurns removes turns that have no user message, no assistant message,
// and no tool calls (e.g. trailing empty user messages).
// Always returns a non-nil slice so JSON encodes as [] (never null): the
// frontend treats null turns as a crash (session.turns.length).
func FilterEmptyTurns(turns []model.TurnVM) []model.TurnVM {
	filtered := make([]model.TurnVM, 0, len(turns))
	for _, t := range turns {
		if t.UserMessage == "" && t.AssistantMessage == "" && t.ToolCallCount == 0 {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
