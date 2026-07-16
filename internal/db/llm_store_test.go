package db

import "testing"

func TestLLMProviderModelIDUnique(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	id1, err := database.AddLLMProvider(LLMProvider{
		Name: "one", Kind: "acp", Agent: "grok", ModelID: "grok-4.5", ModelLabel: "Grok",
	})
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	if id1 == 0 {
		t.Fatal("expected id")
	}

	_, err = database.AddLLMProvider(LLMProvider{
		Name: "two", Kind: "acp", Agent: "grok", ModelID: "grok-4.5", ModelLabel: "Grok",
	})
	if err == nil {
		t.Fatal("expected unique model_id reject")
	}

	other, err := database.FindLLMProviderByModelID("grok-4.5", 0)
	if err != nil {
		t.Fatal(err)
	}
	if other == nil || other.ID != id1 {
		t.Fatalf("find: %+v", other)
	}
	// Updating self keeps the same model_id.
	self, err := database.FindLLMProviderByModelID("grok-4.5", id1)
	if err != nil {
		t.Fatal(err)
	}
	if self != nil {
		t.Fatalf("exclude self should return nil, got %+v", self)
	}

	// Different model_id is fine.
	if _, err := database.AddLLMProvider(LLMProvider{
		Name: "three", Kind: "acp", Agent: "grok", ModelID: "other-model",
	}); err != nil {
		t.Fatalf("add different: %v", err)
	}
}
