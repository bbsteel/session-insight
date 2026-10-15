package home

import (
	"testing"
	"time"
)

func TestAggregateWindowKeepsLiveAndUnfinished(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 22, 15, 0, 0, 0, loc)
	inside := time.Date(2026, 9, 22, 9, 0, 0, 0, loc)
	outside := time.Date(2026, 8, 1, 9, 0, 0, 0, loc)
	facts := []SessionFact{
		{
			ID: "live", AgentType: "codex", Project: "si", Name: "running", UpdatedAt: inside, Live: true,
			Signals: Signals{Live: true, TurnOpen: true}, Turns: []time.Time{inside},
		},
		{
			ID: "old-exit", AgentType: "claude", Project: "si", Name: "interrupted", UpdatedAt: outside,
			Signals: Signals{UserInterrupt: true}, Turns: []time.Time{outside},
		},
		{
			ID: "today", AgentType: "claude", Project: "si", Name: "review", UpdatedAt: inside, Bookmarked: true,
			UserUtterances: []time.Time{inside}, AssistantUtterances: []time.Time{inside}, Turns: []time.Time{inside},
			Tokens:     []TokenEvent{{At: inside, Prompt: 10, CacheRead: 4, CacheWrite: 1, Completion: 3, InputPresence: "exact", OutputPresence: "exact"}},
			Tools:      []TimedName{{Name: "Bash", At: inside}},
			TurnHealth: []TurnHealth{{At: inside, ToolFailure: true}},
			HasBill:    true,
			Costs:      []CostItem{{Unit: "usd", Amount: 1.5, Precision: "exact"}},
			Code:       []CodeChange{{Path: "a.go", Additions: 2, Deletions: 1, HasLines: true, RecordedAt: inside}},
		},
		{
			ID: "untimed-code", AgentType: "claude", Project: "other", Name: "gap", UpdatedAt: inside,
			Turns: []time.Time{inside}, UntimedCode: 2,
		},
	}
	report := Aggregate(facts, Query{WindowDays: 7, Now: now, Location: loc}, now)
	if report.Summary.Sessions != 3 {
		t.Fatalf("sessions = %d, want 3 windowed roots", report.Summary.Sessions)
	}
	if len(report.Live) != 1 || report.Live[0].ID != "live" {
		t.Fatalf("live = %+v", report.Live)
	}
	if len(report.Unfinished) != 1 || report.Unfinished[0].ID != "old-exit" || report.Unfinished[0].Reasons[0] != ReasonTemporaryExit {
		t.Fatalf("unfinished = %+v", report.Unfinished)
	}
	if report.Summary.Messages != 2 || report.Summary.Turns != 3 || report.Summary.Tokens != 18 {
		t.Fatalf("summary messages/turns/tokens = %d/%d/%d", report.Summary.Messages, report.Summary.Turns, report.Summary.Tokens)
	}
	if report.Summary.CodeFiles != 1 || report.Summary.Additions != 2 || report.Coverage.UntimedCodeChanges != 2 {
		t.Fatalf("code = %+v coverage %+v", report.Summary, report.Coverage)
	}
	if len(report.Summary.Costs) != 1 || report.Summary.Costs[0].Unit != "usd" || report.Summary.Costs[0].Amount != 1.5 {
		t.Fatalf("costs = %+v", report.Summary.Costs)
	}
	if report.Health.ToolFailures != 1 || len(report.Tools) != 1 || report.Tools[0].Name != "Bash" {
		t.Fatalf("health %+v tools %+v", report.Health, report.Tools)
	}
	if len(report.Starred) != 1 || report.Starred[0].ID != "today" {
		t.Fatalf("starred = %+v", report.Starred)
	}
}

func TestAggregateDayAndProjectFilter(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 22, 15, 0, 0, 0, loc)
	today := time.Date(2026, 9, 22, 9, 0, 0, 0, loc)
	yesterday := time.Date(2026, 9, 21, 9, 0, 0, 0, loc)
	facts := []SessionFact{
		{ID: "today", AgentType: "codex", Project: "si", UpdatedAt: today, Turns: []time.Time{today}, Live: true, Signals: Signals{Live: true}},
		{ID: "yesterday", AgentType: "codex", Project: "other", UpdatedAt: yesterday, Turns: []time.Time{yesterday}, Signals: Signals{TurnOpen: true}},
	}
	day := Aggregate(facts, Query{WindowDays: 7, Day: "2026-09-22", Now: now, Location: loc}, now)
	if day.Summary.Sessions != 1 || day.Summary.Turns != 1 {
		t.Fatalf("day summary = %+v", day.Summary)
	}
	if len(day.Unfinished) != 1 || day.Unfinished[0].ID != "yesterday" {
		t.Fatalf("day filter hid unfinished: %+v", day.Unfinished)
	}
	project := Aggregate(facts, Query{WindowDays: 7, Project: "other", Now: now, Location: loc}, now)
	if len(project.Live) != 0 || len(project.Unfinished) != 1 {
		t.Fatalf("project filter live %+v unfinished %+v", project.Live, project.Unfinished)
	}
}

func TestAggregateDoesNotAddCostUnitsOrReasoningTokens(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, loc)
	at := now.Add(-time.Hour)
	facts := []SessionFact{{
		ID: "mixed", AgentType: "copilot", Project: "si", UpdatedAt: at, Turns: []time.Time{at},
		Tokens:  []TokenEvent{{At: at, Prompt: 1, Completion: 2, InputPresence: "exact", OutputPresence: "exact"}},
		HasBill: true,
		Costs:   []CostItem{{Unit: "aiu", Amount: 3, Precision: "exact"}, {Unit: "usd", Amount: 0.2, Precision: "estimated"}},
	}}
	report := Aggregate(facts, Query{WindowDays: 7, Now: now, Location: loc}, now)
	if report.Summary.Tokens != 3 {
		t.Fatalf("tokens = %d", report.Summary.Tokens)
	}
	if len(report.Summary.Costs) != 2 {
		t.Fatalf("costs = %+v", report.Summary.Costs)
	}
}

func TestAbsorbChildKeepsRootPopulation(t *testing.T) {
	parent := SessionFact{ID: "root", Turns: []time.Time{time.Unix(10, 0)}}
	child := SessionFact{
		ID: "child", Tokens: []TokenEvent{{At: time.Unix(10, 0), Prompt: 5, InputPresence: "exact", OutputPresence: "exact"}},
		Tools: []TimedName{{Name: "Read", At: time.Unix(10, 0)}}, Turns: []time.Time{time.Unix(10, 0)},
	}
	AbsorbChild(&parent, child)
	if len(parent.Turns) != 1 || len(parent.Tokens) != 1 || parent.Tokens[0].Prompt != 5 {
		t.Fatalf("absorb = %+v", parent)
	}
}
