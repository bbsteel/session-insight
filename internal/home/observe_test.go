package home

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestRenderTokensUseExclusiveBuckets(t *testing.T) {
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, agent                                 string
		input, cacheRead, cacheWrite, prompt, total int64
	}{
		{"grok cached input", "grok", 100, 40, 0, 60, 110},
		{"grok uncached input", "grok", 100, 0, 0, 100, 110},
		{"grok fully cached input", "grok", 100, 100, 0, 0, 110},
		{"claude exclusive input", "claude", 100, 40, 5, 100, 155},
	} {
		t.Run(test.name, func(t *testing.T) {
			detail := &model.SessionDetail{Session: model.Session{AgentType: test.agent, UpdatedAt: at}}
			fact := BuildFact(detail, []model.RenderEvent{{Timestamp: at, TokenUsage: &model.RenderTokenUsage{
				InputTokens: test.input, CacheReadTokens: test.cacheRead, CacheCreationTokens: test.cacheWrite, OutputTokens: 10,
			}}})
			if len(fact.Tokens) != 1 || fact.Tokens[0].Prompt != test.prompt {
				t.Fatalf("exclusive tokens = %+v", fact.Tokens)
			}
			report := Aggregate([]SessionFact{fact}, Query{Day: "2026-09-22", Now: at, Location: time.UTC}, at)
			if report.Summary.Tokens != test.total || report.Days[0].Tokens != test.total {
				t.Fatalf("summary tokens = %d, daily = %+v, want %d", report.Summary.Tokens, report.Days, test.total)
			}
		})
	}
}

func TestRootTurnTimesIgnoreEmbeddedChildEvents(t *testing.T) {
	rootTime := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	childTime := rootTime.AddDate(0, 0, -1)
	for _, renderRootTime := range []bool{true, false} {
		detail := &model.SessionDetail{Session: model.Session{AgentType: "claude", UpdatedAt: rootTime}, Turns: []model.TurnVM{{
			TurnIndex: 1, UserMessage: "root question", AssistantMessage: "root answer",
			ToolNames: []string{"Read"}, ErrorCount: 1,
			Events: []model.EventVM{{Timestamp: rootTime.Format(time.RFC3339)}},
		}}}
		events := []model.RenderEvent{{TurnIndex: 1, Timestamp: childTime, InvocationID: "child"}}
		if renderRootTime {
			events = append(events, model.RenderEvent{TurnIndex: 1, Timestamp: rootTime})
		}
		fact := BuildFact(detail, events)
		report := Aggregate([]SessionFact{fact}, Query{Day: "2026-09-22", Now: rootTime, Location: time.UTC}, rootTime)
		if report.Summary.Turns != 1 || report.Summary.Messages != 2 || len(report.Tools) != 1 || report.Health.ToolFailures != 1 {
			t.Fatalf("render root = %v: root evidence moved to child day: %+v", renderRootTime, report)
		}
		previousDay := Aggregate([]SessionFact{fact}, Query{Day: "2026-09-21", Now: rootTime, Location: time.UTC}, rootTime)
		if previousDay.Summary.Turns != 0 || previousDay.Summary.Messages != 0 || len(previousDay.Tools) != 0 || previousDay.Health.ToolFailures != 0 {
			t.Fatalf("child day contains root evidence: %+v", previousDay)
		}
	}
}

func TestGrokTurnFallbackAlreadyHasExclusiveInput(t *testing.T) {
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	detail := &model.SessionDetail{Session: model.Session{AgentType: "grok", UpdatedAt: at}, Turns: []model.TurnVM{{
		TurnIndex: 1, TokenUsage: model.TokenUsage{PromptTokens: 60, CacheReadTokens: 40, CompletionTokens: 10},
		Events: []model.EventVM{{Timestamp: at.Format(time.RFC3339)}},
	}}}
	fact := BuildFact(detail, nil)
	report := Aggregate([]SessionFact{fact}, Query{Day: "2026-09-22", Now: at, Location: time.UTC}, at)
	if report.Summary.Tokens != 110 || report.Days[0].Tokens != 110 {
		t.Fatalf("fallback input normalized twice: %+v", report)
	}
}
