package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodexACPCommandUsesPinnedRegistryPackage(t *testing.T) {
	got, err := acpCommand("codex")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"npx", "-y", "@agentclientprotocol/codex-acp@1.1.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("acpCommand(codex) = %q, want %q", got, want)
	}
}

func TestACPSessionModelListPrefersConfigOptions(t *testing.T) {
	var sess acpSession
	err := json.Unmarshal([]byte(`{
		"models": {
			"availableModels": [
				{"modelId":"gpt-5.6-terra[medium]","name":"GPT-5.6-Terra (medium)"},
				{"modelId":"gpt-5.5[medium]","name":"GPT-5.5 (medium)"}
			]
		},
		"configOptions": [{
			"id":"model",
			"category":"model",
			"options": [
				{"value":"gpt-5.6-sol","name":"GPT-5.6-Sol"},
				{"value":"gpt-5.6-terra","name":"GPT-5.6-Terra"},
				{"value":"gpt-5.6-luna","name":"GPT-5.6-Luna"},
				{"value":"gpt-5.5","name":"GPT-5.5"}
			]
		}]
	}`), &sess)
	if err != nil {
		t.Fatal(err)
	}

	models := sess.modelList()
	got := make([]string, 0, len(models))
	for _, model := range models {
		got = append(got, model.ID)
	}
	want := []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model IDs = %q, want config option IDs %q", got, want)
	}
}

func TestACPSessionModelListFallsBackToLegacyModels(t *testing.T) {
	var sess acpSession
	err := json.Unmarshal([]byte(`{
		"models": {
			"availableModels": [
				{"modelId":"claude-sonnet","name":"Claude Sonnet","description":"Fast"}
			]
		}
	}`), &sess)
	if err != nil {
		t.Fatal(err)
	}

	models := sess.modelList()
	if len(models) != 1 || models[0].ID != "claude-sonnet" || models[0].Description != "Fast" {
		t.Fatalf("legacy models = %+v", models)
	}
}

func TestACPModelSelectionPrefersConfigOption(t *testing.T) {
	var sess acpSession
	err := json.Unmarshal([]byte(`{
		"sessionId":"session-1",
		"models":{"currentModelId":"gpt-5.5[medium]","availableModels":[{"modelId":"gpt-5.5[medium]"}]},
		"configOptions":[{"id":"model","category":"model","options":[{"value":"gpt-5.6-terra"}]}]
	}`), &sess)
	if err != nil {
		t.Fatal(err)
	}

	client := &acpClient{cfg: Config{Agent: "codex", ModelID: "gpt-5.6-terra"}}
	method, params, err := client.modelSelectionRequest(&sess)
	if err != nil {
		t.Fatal(err)
	}
	if method != "session/set_config_option" {
		t.Fatalf("method = %q, want session/set_config_option", method)
	}
	want := map[string]any{"sessionId": "session-1", "configId": "model", "value": "gpt-5.6-terra"}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("params = %#v, want %#v", params, want)
	}
}
