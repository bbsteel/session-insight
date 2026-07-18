//go:build sqlite_fts5

package indexer

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

func TestBuildTurnTexts_ExpandsRoles(t *testing.T) {
	sess := model.Session{
		ID:         "sess-1",
		Name:       "my task",
		Repository: "/repo",
		Branch:     "feat/x",
		ModelName:  "gpt-test",
	}
	detail := &model.SessionDetail{
		Session: sess,
		Turns: []model.TurnVM{
			{
				TurnIndex:        0,
				UserMessage:      "please fix auth",
				AssistantMessage: "Looking at session-insight/pull/10 next",
				Skills:           []string{"git-commit-with-context", "git-commit-with-context"},
				ToolNames:        []string{"Bash"},
				ToolDetails: []model.ToolCallVM{
					{
						Name:         "Bash",
						ExitCode:     127,
						ErrorKind:    "shell_error",
						ErrorMessage: "command not found",
					},
				},
				Anomalies: []string{"tool_failure"},
			},
		},
	}
	render := []model.RenderEvent{
		{
			Type:      "ToolInvocation",
			TurnIndex: 0,
			ToolName:  "Bash",
			ToolInput: map[string]any{
				"command": "ls /tmp/session-insight-ui",
			},
		},
		{
			Type:      "ToolInvocation",
			TurnIndex: 0,
			ToolName:  "Read",
			ToolInput: map[string]any{
				"file_path": "/home/deck/projects/session-insight/AGENTS.md",
			},
		},
	}

	texts := buildTurnTexts(sess, detail, render)
	byRole := map[string]string{}
	for _, row := range texts {
		key := row.Role
		if row.TurnIndex >= 0 {
			key = row.Role + "@0"
			if row.TurnIndex != 0 {
				t.Fatalf("unexpected turn_index %d", row.TurnIndex)
			}
		}
		byRole[key] = row.Content
	}

	if !strings.Contains(byRole["meta"], "my task") ||
		!strings.Contains(byRole["meta"], "feat/x") ||
		!strings.Contains(byRole["meta"], "gpt-test") {
		t.Fatalf("meta incomplete: %q", byRole["meta"])
	}
	if byRole["user@0"] != "please fix auth" {
		t.Fatalf("user: %q", byRole["user@0"])
	}
	if !strings.Contains(byRole["assistant@0"], "session-insight/pull/10") {
		t.Fatalf("assistant missing PR path: %q", byRole["assistant@0"])
	}
	if byRole["skill@0"] != "git-commit-with-context" {
		t.Fatalf("skill: %q", byRole["skill@0"])
	}
	tool := byRole["tool@0"]
	if !strings.Contains(tool, "Bash") || !strings.Contains(tool, "command:ls /tmp/session-insight-ui") {
		t.Fatalf("tool missing command summary: %q", tool)
	}
	if !strings.Contains(tool, "file_path:/home/deck/projects/session-insight/AGENTS.md") {
		t.Fatalf("tool missing path summary: %q", tool)
	}
	errText := byRole["error@0"]
	if !strings.Contains(errText, "tool_failure") ||
		!strings.Contains(errText, "shell_error") ||
		!strings.Contains(errText, "command not found") {
		t.Fatalf("error incomplete: %q", errText)
	}
}

func TestBuildTurnTexts_CapsAssistant(t *testing.T) {
	long := strings.Repeat("中", maxAssistantRunes+50)
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{{
			TurnIndex:        0,
			AssistantMessage: long,
		}},
	}
	texts := buildTurnTexts(model.Session{ID: "x"}, detail, nil)
	var got string
	for _, row := range texts {
		if row.Role == "assistant" {
			got = row.Content
		}
	}
	if got == "" {
		t.Fatal("missing assistant row")
	}
	if utf8.RuneCountInString(got) != maxAssistantRunes {
		t.Fatalf("assistant runes = %d, want %d", utf8.RuneCountInString(got), maxAssistantRunes)
	}
}

func TestSummarizeToolInput_SkipsUnknownKeys(t *testing.T) {
	sum := summarizeToolInput(map[string]any{
		"command":    "echo hi",
		"old_string": strings.Repeat("x", 2000),
		"path":       "a.go",
	})
	if !strings.Contains(sum, "command:echo hi") || !strings.Contains(sum, "path:a.go") {
		t.Fatalf("sum=%q", sum)
	}
	if strings.Contains(sum, "old_string") {
		t.Fatalf("should skip old_string: %q", sum)
	}
}

func TestIndexer_SearchHitsNewRoles(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	// Use buildTurnTexts + UpsertTurns directly to avoid mock reader surface.
	sess := model.Session{ID: "s-new", Name: "n"}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{{
			TurnIndex:        0,
			UserMessage:      "user only unique_zzz",
			AssistantMessage: "assistant mentions pull_ten_xyz",
			Skills:           []string{"skill_abc_unique"},
		}},
	}
	render := []model.RenderEvent{{
		Type: "ToolInvocation", TurnIndex: 0, ToolName: "Bash",
		ToolInput: map[string]any{"command": "unique_cmd_qwerty"},
	}}
	texts := buildTurnTexts(sess, detail, render)
	if err := database.UpsertTurns("test", "s-new", texts, 1); err != nil {
		t.Fatalf("UpsertTurns: %v", err)
	}

	for _, q := range []string{"pull_ten_xyz", "skill_abc_unique", "unique_cmd_qwerty"} {
		hits, err := database.SearchTurns(q, 30)
		if err != nil {
			t.Fatalf("SearchTurns(%q): %v", q, err)
		}
		if len(hits) == 0 {
			t.Fatalf("expected hit for %q", q)
		}
	}
}

func TestToolSummariesPreferRenderInputsUnderCap(t *testing.T) {
	// Many bare tool names first would push a late render command past the cap
	// if names were appended before render inputs.
	names := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		names = append(names, "ToolNamePad"+strings.Repeat("x", 20))
	}
	detail := &model.SessionDetail{
		Turns: []model.TurnVM{{
			TurnIndex: 0,
			ToolNames: names,
		}},
	}
	render := []model.RenderEvent{{
		Type: "ToolInvocation", TurnIndex: 0, ToolName: "Bash",
		ToolInput: map[string]any{"command": "unique_acceptance_cmd_pull13"},
	}}
	byTurn := toolSummariesByTurn(detail, render)
	got := byTurn[0]
	if !strings.Contains(got, "unique_acceptance_cmd_pull13") {
		t.Fatalf("render command lost under name flood: %q", got[:min(200, len(got))])
	}
	// After truncate in buildTurnTexts path:
	texts := buildTurnTexts(model.Session{ID: "s"}, detail, render)
	var tool string
	for _, row := range texts {
		if row.Role == "tool" {
			tool = row.Content
		}
	}
	if !strings.Contains(tool, "unique_acceptance_cmd_pull13") {
		t.Fatalf("capped tool row missing render command: len=%d head=%q", len(tool), tool[:min(120, len(tool))])
	}
}
