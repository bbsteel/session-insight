package codex

import (
	"strings"
	"testing"
)

func TestQuotaMarkerDoesNotRequireTokenInfo(t *testing.T) {
	for _, test := range []struct {
		name, infoJSON string
		wantRequests   int
		wantPrompt     int64
	}{
		{name: "omitted info"},
		{name: "null info", infoJSON: `,"info":null`},
		{name: "token info", infoJSON: `,"info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}`, wantRequests: 1, wantPrompt: 60},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `{"timestamp":"2026-04-01T12:00:00Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-04-01T12:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Synthetic quota fixture"}}
{"timestamp":"2026-04-01T12:00:02Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"rate_limit_reached_type":"primary"}` + test.infoJSON + `}}
`
			parsed, _, _, _, err := parseCodexEventsReader(strings.NewReader(source))
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed.Active) != 1 {
				t.Fatalf("turns = %+v", parsed.Active)
			}
			turn := parsed.Active[0]
			quotaEvents := 0
			for _, event := range turn.Events {
				if event.Type != "quota_exhausted" {
					continue
				}
				quotaEvents++
				if event.Timestamp != "2026-04-01T12:00:02Z" || event.Data["reached_type"] != "primary" {
					t.Fatalf("quota event = %+v", event)
				}
			}
			if quotaEvents != 1 || turn.RequestCount != test.wantRequests || turn.TokenUsage.PromptTokens != test.wantPrompt {
				t.Fatalf("quota events = %d, requests = %d, usage = %+v", quotaEvents, turn.RequestCount, turn.TokenUsage)
			}
		})
	}
}

func TestQuotaMarkerRequiresActiveTurnAndReachedType(t *testing.T) {
	source := `{"timestamp":"2026-04-01T12:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"rate_limit_reached_type":"primary"}}}
{"timestamp":"2026-04-01T12:00:01Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-04-01T12:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"Synthetic normal fixture"}}
{"timestamp":"2026-04-01T12:00:03Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{}}}
{"timestamp":"2026-04-01T12:00:04Z","type":"event_msg","payload":{"type":"token_count"}}
`
	parsed, _, _, _, err := parseCodexEventsReader(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range parsed.Active {
		for _, event := range turn.Events {
			if event.Type == "quota_exhausted" {
				t.Fatalf("unexpected quota event: %+v", event)
			}
		}
	}
}
