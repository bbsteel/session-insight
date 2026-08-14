package codex

import (
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
)

// Paginated history mode (Codex CLI ~0.147+) carries user/assistant text as
// event_msg/item_completed items instead of legacy user_message/agent_message
// events. These tests pin the mapping against the sanitized fixtures in
// testdata/items-v2 and guard the legacy equivalence: the same logical
// session in both event styles must produce the same turns and text.

const (
	itemsV2LegacySessionID    = "rollout-2026-08-11T00-00-00-019f0000-0000-7000-8000-000000000001"
	itemsV2PaginatedSessionID = "rollout-2026-08-11T00-00-00-019f0000-0000-7000-8000-000000000002"
)

func itemsV2Reader(t *testing.T) *CodexReader {
	t.Helper()
	return New(filepath.Join("testdata", "items-v2", "sessions"))
}

func TestPaginatedItemsDetailTextAndTurns(t *testing.T) {
	r := itemsV2Reader(t)
	detail, err := r.GetSession(itemsV2PaginatedSessionID)
	if err != nil {
		t.Fatal(err)
	}

	// 3 turns: tool turn, pure message turn (legacy-only parsing dropped
	// these via empty-turn filtering), and the last_agent_message fallback
	// turn.
	if len(detail.Turns) != 3 {
		t.Fatalf("turns=%d, want 3 (pure message turn must survive)", len(detail.Turns))
	}

	turn1 := detail.Turns[0]
	if turn1.UserMessage != "add a greeting flag to the CLI" {
		t.Errorf("turn1 user = %q", turn1.UserMessage)
	}
	if turn1.AssistantMessage != "Added the greeting flag." {
		t.Errorf("turn1 assistant = %q", turn1.AssistantMessage)
	}
	// The CommandExecution item duplicates the response_item function_call;
	// tool counting must come from response_item only.
	if turn1.ToolCallCount != 1 {
		t.Errorf("turn1 tool calls = %d, want 1 (CommandExecution item must not double-count)", turn1.ToolCallCount)
	}

	turn2 := detail.Turns[1]
	if turn2.UserMessage != "what does the flag default to?" || turn2.AssistantMessage != "It defaults to false." {
		t.Errorf("turn2 = %q / %q", turn2.UserMessage, turn2.AssistantMessage)
	}

	turn3 := detail.Turns[2]
	if turn3.UserMessage != "rename it to salutation" {
		t.Errorf("turn3 user = %q", turn3.UserMessage)
	}
	// No AgentMessage item in this turn: text survives via the
	// task_complete last_agent_message fallback.
	if turn3.AssistantMessage != "Renamed the flag to salutation." {
		t.Errorf("turn3 assistant = %q, want last_agent_message fallback", turn3.AssistantMessage)
	}
	// The FileChange item is not a text source and not a response_item tool
	// record: zero counted tool calls.
	if turn3.ToolCallCount != 0 {
		t.Errorf("turn3 tool calls = %d, want 0 (FileChange item ignored)", turn3.ToolCallCount)
	}
}

func TestPaginatedItemsListMetadata(t *testing.T) {
	r := itemsV2Reader(t)
	sessions, err := r.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions=%d, want 2", len(sessions))
	}
	for _, sess := range sessions {
		// Both event styles name the session from the first user text rather
		// than falling back to the timestamp name.
		if sess.Name != "add a greeting flag to the CLI" {
			t.Errorf("session %s name = %q, want first user message", sess.ID, sess.Name)
		}
	}
}

func TestPaginatedItemsMatchLegacyEquivalence(t *testing.T) {
	r := itemsV2Reader(t)
	legacy, err := r.GetSession(itemsV2LegacySessionID)
	if err != nil {
		t.Fatal(err)
	}
	paginated, err := r.GetSession(itemsV2PaginatedSessionID)
	if err != nil {
		t.Fatal(err)
	}

	// Turns 0-1 are the same logical conversation in both event styles.
	for i := 0; i < 2; i++ {
		l, p := legacy.Turns[i], paginated.Turns[i]
		if l.UserMessage != p.UserMessage || l.AssistantMessage != p.AssistantMessage {
			t.Errorf("turn %d differs: legacy %q/%q vs paginated %q/%q",
				i, l.UserMessage, l.AssistantMessage, p.UserMessage, p.AssistantMessage)
		}
		if l.ToolCallCount != p.ToolCallCount {
			t.Errorf("turn %d tool calls: legacy %d vs paginated %d", i, l.ToolCallCount, p.ToolCallCount)
		}
	}
}

func TestPaginatedItemsRenderEvents(t *testing.T) {
	r := itemsV2Reader(t)

	legacyEvents, err := r.GetRenderEvents(itemsV2LegacySessionID)
	if err != nil {
		t.Fatal(err)
	}
	paginatedEvents, err := r.GetRenderEvents(itemsV2PaginatedSessionID)
	if err != nil {
		t.Fatal(err)
	}

	legacyText := renderTextPairs(legacyEvents)
	paginatedText := renderTextPairs(paginatedEvents)

	// Turns 0-1 (user prompt + assistant text) must render identically.
	if len(legacyText) < 4 || len(paginatedText) < 4 {
		t.Fatalf("rendered text events: legacy %d, paginated %d", len(legacyText), len(paginatedText))
	}
	for i := 0; i < 4; i++ {
		if legacyText[i] != paginatedText[i] {
			t.Errorf("render text %d differs: legacy %q vs paginated %q", i, legacyText[i], paginatedText[i])
		}
	}

	// The fallback turn contributes its last_agent_message as a TextChunk.
	last := paginatedText[len(paginatedText)-1]
	if last != "TextChunk:Renamed the flag to salutation." {
		t.Errorf("last paginated text = %q, want fallback assistant text", last)
	}
}

// renderTextPairs flattens UserPrompt/TextChunk events into comparable
// "Type:text" strings, in event order.
func renderTextPairs(events []model.RenderEvent) []string {
	var out []string
	for _, evt := range events {
		switch evt.Type {
		case "UserPrompt", "TextChunk":
			out = append(out, evt.Type+":"+evt.Text)
		}
	}
	return out
}
