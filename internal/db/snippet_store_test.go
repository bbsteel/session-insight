package db

import "testing"

func TestSnippetStorePersistsSourceAndContent(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	turnIndex := 3
	created, err := database.AddSnippet(Snippet{
		Content: "Keep this useful conclusion.", AgentType: "codex", SessionID: "session-1",
		SessionName: "Snippet design", SourceKind: "assistant", TurnIndex: &turnIndex,
	})
	if err != nil {
		t.Fatalf("AddSnippet: %v", err)
	}
	if created.ID == 0 || created.Content != "Keep this useful conclusion." || created.TurnIndex == nil || *created.TurnIndex != 3 {
		t.Fatalf("unexpected created snippet: %+v", created)
	}

	list, err := database.ListSnippets()
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}
	if len(list) != 1 || list[0].SessionName != "Snippet design" {
		t.Fatalf("unexpected snippets: %+v", list)
	}
	deleted, err := database.DeleteSnippet(created.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteSnippet: deleted=%v err=%v", deleted, err)
	}
	deleted, err = database.DeleteSnippet(created.ID)
	if err != nil || deleted {
		t.Fatalf("second DeleteSnippet: deleted=%v err=%v", deleted, err)
	}
}
