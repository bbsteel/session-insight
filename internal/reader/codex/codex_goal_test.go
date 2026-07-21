package codex

import (
	"strings"
	"testing"
)

func TestGoalModeObjectiveIsRenderedAsUserPrompt(t *testing.T) {
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-20T12:40:23Z","type":"event_msg","payload":{"type":"thread_goal_updated","goal":{"objective":"Ship language selection"}}}`,
		`{"timestamp":"2026-07-20T12:40:24Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-07-20T12:40:25Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"goal\">\n<objective>Ship language selection</objective>\nBudget: unlimited\n</codex_internal_context>"}]}}`,
		`{"timestamp":"2026-07-20T12:40:26Z","type":"event_msg","payload":{"type":"agent_message","message":"Working on it."}}`,
		`{"timestamp":"2026-07-20T12:40:27Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	}, "\n") + "\n"

	path := writeCodexFixture(t, fixture)
	parsed, _, _ := parseCodexEvents(path)
	if len(parsed.Active) != 1 || parsed.Active[0].UserMessage != "Ship language selection" {
		t.Fatalf("goal turn user message = %#v, want extracted objective", parsed.Active)
	}

	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, event := range events {
		if event.Type == "UserPrompt" {
			prompts = append(prompts, event.Text)
		}
	}
	if len(prompts) != 1 || prompts[0] != "Ship language selection" {
		t.Fatalf("rendered goal prompts = %#v, want one extracted objective", prompts)
	}
}

func TestGoalModeDoesNotReplaceAnActualUserPrompt(t *testing.T) {
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-20T12:40:23Z","type":"event_msg","payload":{"type":"thread_goal_updated","goal":{"objective":"Ship language selection"}}}`,
		`{"timestamp":"2026-07-20T12:40:24Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-07-20T12:40:25Z","type":"event_msg","payload":{"type":"user_message","message":"Show status"}}`,
		`{"timestamp":"2026-07-20T12:40:26Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"goal\"><objective>Ship language selection</objective></codex_internal_context>"}]}}`,
		`{"timestamp":"2026-07-20T12:40:27Z","type":"event_msg","payload":{"type":"agent_message","message":"Working on it."}}`,
	}, "\n") + "\n"

	path := writeCodexFixture(t, fixture)
	parsed, _, _ := parseCodexEvents(path)
	if len(parsed.Active) != 1 || parsed.Active[0].UserMessage != "Show status" {
		t.Fatalf("goal turn user message = %#v, want actual user prompt", parsed.Active)
	}

	events, err := codexToRenderEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, event := range events {
		if event.Type == "UserPrompt" {
			prompts = append(prompts, event.Text)
		}
	}
	if len(prompts) != 1 || prompts[0] != "Show status" {
		t.Fatalf("rendered goal prompts = %#v, want only actual user prompt", prompts)
	}
}

func TestGoalModeObjectiveRendersOnlyOnGoalUpdate(t *testing.T) {
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-20T12:40:23Z","type":"event_msg","payload":{"type":"thread_goal_updated","goal":{"objective":"Ship language selection"}}}`,
		`{"timestamp":"2026-07-20T12:40:24Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-07-20T12:40:25Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"goal\"><objective>Ship language selection</objective></codex_internal_context>"}]}}`,
		`{"timestamp":"2026-07-20T12:40:26Z","type":"event_msg","payload":{"type":"agent_message","message":"Working on it."}}`,
		`{"timestamp":"2026-07-20T12:40:27Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-07-20T12:41:23Z","type":"event_msg","payload":{"type":"thread_goal_updated","goal":{"objective":"Ship language selection"}}}`,
		`{"timestamp":"2026-07-20T12:41:24Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-07-20T12:41:25Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"goal\"><objective>Ship language selection</objective></codex_internal_context>"}]}}`,
		`{"timestamp":"2026-07-20T12:41:26Z","type":"event_msg","payload":{"type":"agent_message","message":"Still working."}}`,
	}, "\n") + "\n"

	events, err := codexToRenderEvents(writeCodexFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, event := range events {
		if event.Type == "UserPrompt" {
			prompts = append(prompts, event.Text)
		}
	}
	if len(prompts) != 1 || prompts[0] != "Ship language selection" {
		t.Fatalf("rendered goal prompts = %#v, want one goal-update prompt", prompts)
	}
}
