package llm

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

func TestACPModelSelectionRejectsModelNoLongerAdvertised(t *testing.T) {
	var sess acpSession
	err := json.Unmarshal([]byte(`{
		"sessionId":"session-1",
		"configOptions":[{"id":"model","category":"model","options":[
			{"value":"gpt-5.5","name":"GPT-5.5"},
			{"value":"gpt-5.6-luna","name":"GPT-5.6-Luna"}
		]}]
	}`), &sess)
	if err != nil {
		t.Fatal(err)
	}

	client := &acpClient{cfg: Config{Agent: "codex", ModelID: "gpt-5.4-mini"}}
	method, params, err := client.modelSelectionRequest(&sess)
	if err == nil {
		t.Fatal("expected unavailable model error")
	}
	if method != "" || params != nil {
		t.Fatalf("method = %q, params = %#v; want no selection request", method, params)
	}
	var unavailable *ModelUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error type = %T, want *ModelUnavailableError", err)
	}
	for _, want := range []string{"模型「gpt-5.4-mini」已无法", "Codex CLI", "gpt-5.5", "gpt-5.6-luna"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestACPModelSelectionRejectsUnavailableLegacyModel(t *testing.T) {
	var sess acpSession
	if err := json.Unmarshal([]byte(`{
		"sessionId":"session-1",
		"models":{"currentModelId":"claude-sonnet","availableModels":[
			{"modelId":"claude-sonnet","name":"Claude Sonnet"}
		]}
	}`), &sess); err != nil {
		t.Fatal(err)
	}

	client := &acpClient{cfg: Config{Agent: "claude", ModelID: "claude-opus-old"}}
	method, params, err := client.modelSelectionRequest(&sess)
	if err == nil || !strings.Contains(err.Error(), "claude-sonnet") {
		t.Fatalf("error = %v, want advertised legacy model", err)
	}
	if method != "" || params != nil {
		t.Fatalf("method = %q, params = %#v; want no selection request", method, params)
	}
}

func TestACPModelSelectionRejectsEmptyAdvertisement(t *testing.T) {
	var sess acpSession
	if err := json.Unmarshal([]byte(`{
		"sessionId":"session-1",
		"configOptions":[{"id":"model","category":"model","options":[]}]
	}`), &sess); err != nil {
		t.Fatal(err)
	}

	client := &acpClient{cfg: Config{Agent: "codex", ModelID: "gpt-5.4-mini"}}
	method, params, err := client.modelSelectionRequest(&sess)
	if err == nil || !strings.Contains(err.Error(), "当前未公布任何可选型号") {
		t.Fatalf("error = %v, want empty-advertisement guidance", err)
	}
	if method != "" || params != nil {
		t.Fatalf("method = %q, params = %#v; want no selection request", method, params)
	}
}
