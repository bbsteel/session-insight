package llm

import "testing"

func TestParseGrokModelsOutput(t *testing.T) {
	out := `You are logged in with grok.com.

Default model: grok-4.5

Available models:
  * grok-4.5 (default)
  - grok-composer-2.5-fast
  - grok-build
`
	models := parseGrokModelsOutput(out)
	if len(models) != 3 {
		t.Fatalf("got %d models: %+v", len(models), models)
	}
	if models[0].ID != "grok-4.5" {
		t.Errorf("default should be first, got %q", models[0].ID)
	}
	if models[0].Label != "grok-4.5 (default)" {
		t.Errorf("label=%q", models[0].Label)
	}
	if models[1].ID != "grok-composer-2.5-fast" {
		t.Errorf("second=%q", models[1].ID)
	}
	if models[2].ID != "grok-build" {
		t.Errorf("third=%q", models[2].ID)
	}
}

func TestParseGrokModelsOutput_DefaultOnlyLine(t *testing.T) {
	out := "Default model: solo-model\nAvailable models:\n"
	models := parseGrokModelsOutput(out)
	if len(models) != 1 || models[0].ID != "solo-model" {
		t.Fatalf("got %+v", models)
	}
}

func TestParseGrokModelsOutput_Empty(t *testing.T) {
	if models := parseGrokModelsOutput("not logged in\n"); len(models) != 0 {
		t.Fatalf("expected empty, got %+v", models)
	}
}

func TestParseGrokModelsOutput_Dedup(t *testing.T) {
	out := `Available models:
  * a (default)
  - a
  - b
`
	models := parseGrokModelsOutput(out)
	if len(models) != 2 {
		t.Fatalf("got %d: %+v", len(models), models)
	}
}
