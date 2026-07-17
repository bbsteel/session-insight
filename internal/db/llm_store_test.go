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

func TestLLMProviderHeadersRoundTrip(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	headersJSON := `{"HTTP-Referer":"https://example.com","X-Title":"session-insight"}`
	id, err := database.AddLLMProvider(LLMProvider{
		Name: "api-src", Kind: "api", BaseURL: "https://openrouter.ai/api/v1",
		APIKey: "sk-test", Headers: headersJSON, ModelID: "some-model", ModelLabel: "Some",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := database.GetLLMProvider(id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected provider")
	}
	if got.Headers != headersJSON {
		t.Fatalf("headers: got %q want %q", got.Headers, headersJSON)
	}

	updated := *got
	updated.Headers = `{"X-Api-Key":"gateway-token"}`
	if err := database.UpdateLLMProvider(updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, err := database.GetLLMProvider(id)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Headers != updated.Headers {
		t.Fatalf("headers after update: got %q want %q", got2.Headers, updated.Headers)
	}

	list, err := database.ListLLMProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Headers != updated.Headers {
		t.Fatalf("list headers: %+v", list)
	}
}
