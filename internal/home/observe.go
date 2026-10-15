package home

import (
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// SignalsFromDetail reads the latest ending from one session body.
// Render events supply utterance and token times; turn events supply the
// ending markers adapters record.
func SignalsFromDetail(detail *model.SessionDetail) Signals {
	if detail == nil {
		return Signals{}
	}
	signals := Signals{
		Live:            detail.IsLive,
		OpenTodos:       hasOpenTodo(detail.Todos),
		MissingShutdown: detail.AnomalySummary.MissingShutdown,
	}
	if len(detail.Turns) == 0 {
		return signals
	}
	quota, interrupt, normalClose, turnOpen := endingOf(detail.Turns[len(detail.Turns)-1])
	signals.Quota = quota
	signals.UserInterrupt = interrupt
	signals.TurnOpen = turnOpen && !normalClose && !quota && !interrupt
	return signals
}

func endingOf(turn model.TurnVM) (quota, interrupt, normalClose, turnOpen bool) {
	for index := len(turn.Events) - 1; index >= 0; index-- {
		event := turn.Events[index]
		switch event.Type {
		case "quota_exhausted":
			return true, false, false, false
		case "turn_aborted":
			reason, _ := event.Data["reason"].(string)
			if reason == "interrupted" {
				return false, true, false, false
			}
			return false, false, false, true
		case "interrupted":
			by, _ := event.Data["by"].(string)
			if by == "user" || by == "" {
				return false, true, false, false
			}
			return false, false, false, true
		case "task_complete":
			return false, false, true, false
		case "assistant.message":
			reason, _ := event.Data["stop_reason"].(string)
			switch reason {
			case "end_turn", "stop_sequence":
				return textEnding(turn)
			case "tool_use":
				return false, false, false, true
			}
		}
	}
	return textEnding(turn)
}

func textEnding(turn model.TurnVM) (quota, interrupt, normalClose, turnOpen bool) {
	if isUserInterrupt(turn.UserMessage) {
		return false, true, false, false
	}
	if isQuotaText(turn.UserMessage) || isQuotaText(turn.AssistantMessage) {
		return true, false, false, false
	}
	for _, anomaly := range turn.Anomalies {
		if anomaly == "interrupted" {
			return false, true, false, false
		}
	}
	if strings.TrimSpace(turn.AssistantMessage) == "" && strings.TrimSpace(turn.UserMessage) != "" {
		return false, false, false, true
	}
	if strings.TrimSpace(turn.AssistantMessage) != "" {
		return false, false, true, false
	}
	return false, false, false, false
}

func isUserInterrupt(text string) bool {
	switch strings.TrimSpace(text) {
	case "[Request interrupted by user]", "[Request interrupted by user for tool use]":
		return true
	default:
		return false
	}
}

func isQuotaText(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{
		"you've reached your usage limit",
		"you’ve reached your usage limit",
		"you've hit your limit",
		"usage limit reached",
		"rate limit reached",
		"rate_limit_reached",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func hasOpenTodo(todos []model.Todo) bool {
	for _, todo := range todos {
		switch strings.ToLower(strings.TrimSpace(todo.Status)) {
		case "done", "completed", "complete", "canceled", "cancelled":
			continue
		default:
			return true
		}
	}
	return false
}

// BuildFact projects one session body into home evidence. Child absorption is
// separate so a child transcript does not become its own row.
func BuildFact(detail *model.SessionDetail, events []model.RenderEvent) SessionFact {
	fact := SessionFact{}
	if detail == nil {
		fact.DetailMissing = true
		return fact
	}
	fact.ID = detail.ID
	fact.AgentType = detail.AgentType
	fact.Project = detail.Project
	fact.Name = detail.Name
	fact.UpdatedAt = detail.UpdatedAt
	fact.Live = detail.IsLive
	fact.Bookmarked = detail.Bookmarked
	fact.Signals = SignalsFromDetail(detail)
	fact.MissingShutdown = detail.AnomalySummary.MissingShutdown
	turnTimes := turnTimesFrom(events, detail.Turns)
	for _, turn := range detail.Turns {
		at := turnTimes[turn.TurnIndex]
		if strings.TrimSpace(turn.UserMessage) != "" {
			if at.IsZero() {
				fact.UntimedUtterances++
			} else {
				fact.UserUtterances = append(fact.UserUtterances, at)
			}
		}
		if strings.TrimSpace(turn.AssistantMessage) != "" {
			if at.IsZero() {
				fact.UntimedUtterances++
			} else {
				fact.AssistantUtterances = append(fact.AssistantUtterances, at)
			}
		}
		if !at.IsZero() {
			fact.Turns = append(fact.Turns, at)
		}
		if contains(turn.Anomalies, "tool_failure") || turn.ErrorCount > 0 ||
			contains(turn.Anomalies, "duration_spike") || contains(turn.Anomalies, "continuation_nudge") {
			fact.TurnHealth = append(fact.TurnHealth, TurnHealth{
				At:                at,
				ToolFailure:       contains(turn.Anomalies, "tool_failure") || turn.ErrorCount > 0,
				DurationSpike:     contains(turn.Anomalies, "duration_spike"),
				ContinuationNudge: contains(turn.Anomalies, "continuation_nudge"),
			})
		}
		for _, name := range turn.ToolNames {
			if at.IsZero() {
				continue
			}
			fact.Tools = append(fact.Tools, TimedName{Name: name, At: at})
		}
		for _, name := range turn.Skills {
			if at.IsZero() {
				fact.UntimedSkills++
				continue
			}
			fact.Skills = append(fact.Skills, TimedName{Name: name, At: at})
		}
	}
	applyEventTokens(&fact, detail, events, turnTimes)
	applyPresence(&fact, detail)
	if detail.Billing != nil && detail.Billing.BillingUnit != "" && detail.Billing.Precision != model.PrecisionMissing {
		fact.HasBill = true
		fact.Costs = append(fact.Costs, CostItem{
			Unit: detail.Billing.BillingUnit, Amount: detail.Billing.BillingAmount, Precision: detail.Billing.Precision,
		})
	}
	return fact
}

func turnTimesFrom(events []model.RenderEvent, turns []model.TurnVM) map[int]time.Time {
	times := map[int]time.Time{}
	for _, event := range events {
		if event.Timestamp.IsZero() {
			continue
		}
		current, ok := times[event.TurnIndex]
		if !ok || event.Timestamp.Before(current) {
			times[event.TurnIndex] = event.Timestamp
		}
	}
	for _, turn := range turns {
		if _, ok := times[turn.TurnIndex]; ok {
			continue
		}
		for _, event := range turn.Events {
			parsed := parseTime(event.Timestamp)
			if parsed.IsZero() {
				continue
			}
			times[turn.TurnIndex] = parsed
			break
		}
	}
	return times
}

func applyEventTokens(fact *SessionFact, detail *model.SessionDetail, events []model.RenderEvent, turnTimes map[int]time.Time) {
	usedEvent := false
	for _, event := range events {
		if event.TokenUsage == nil || event.Timestamp.IsZero() {
			continue
		}
		usedEvent = true
		usage := event.TokenUsage
		fact.Tokens = append(fact.Tokens, TokenEvent{
			At: usageTime(event.Timestamp), Prompt: usage.InputTokens, CacheRead: usage.CacheReadTokens,
			CacheWrite: usage.CacheCreationTokens, Completion: usage.OutputTokens,
		})
	}
	if usedEvent {
		return
	}
	for _, turn := range detail.Turns {
		usage := turn.TokenUsage
		if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 {
			continue
		}
		at := turnTimes[turn.TurnIndex]
		if at.IsZero() {
			fact.UntimedTokens = true
			continue
		}
		fact.Tokens = append(fact.Tokens, TokenEvent{
			At: at, Prompt: usage.PromptTokens, CacheRead: usage.CacheReadTokens,
			CacheWrite: usage.CacheWriteTokens, Completion: usage.CompletionTokens,
			InputPresence: string(usage.Present.Input), OutputPresence: string(usage.Present.Output),
			CacheReadPresence: string(usage.Present.CacheRead), CacheWritePresence: string(usage.Present.CacheWrite),
		})
	}
}

func applyPresence(fact *SessionFact, detail *model.SessionDetail) {
	readMissing, writeMissing := false, false
	seen := false
	consider := func(present model.TokenPresence) {
		seen = true
		if present.CacheRead == model.PresenceMissing {
			readMissing = true
		}
		if present.CacheWrite == model.PresenceMissing {
			writeMissing = true
		}
	}
	for _, turn := range detail.Turns {
		if turn.TokenUsage.Present != (model.TokenPresence{}) {
			consider(turn.TokenUsage.Present)
		}
	}
	if detail.Billing != nil && detail.Billing.Totals.Present != (model.TokenPresence{}) {
		consider(detail.Billing.Totals.Present)
	}
	if !seen {
		return
	}
	fact.MissingCacheRead = readMissing
	fact.MissingCacheWrite = writeMissing
}

func usageTime(at time.Time) time.Time { return at }

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
