package dsh

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

func TestNativeBashCommandOutcomes(t *testing.T) {
	cases := []struct {
		name          string
		output        string
		deliveryError bool
		toolName      string
		exitCode      int
		errorCount    int
	}{
		{name: "command-failure", output: "FAIL: native command\n[exit code: 7]", toolName: "bash", exitCode: 7, errorCount: 1},
		{name: "command-success", output: "ok", toolName: "bash"},
		{name: "delivery-failure", output: "Could not spawn process", toolName: "bash", deliveryError: true, exitCode: 1, errorCount: 1},
		{name: "embedded-marker", output: "example [exit code: 7]", toolName: "bash"},
		{name: "nonterminal-marker", output: "[exit code: 7]\nmore output", toolName: "bash"},
		{name: "other-tool", output: "[exit code: 7]", toolName: "read"},
	}
	for _, fixture := range []string{"v3.jsonl", "v4.jsonl"} {
		for _, testCase := range cases {
			t.Run(fixture+"/"+testCase.name, func(t *testing.T) {
				source := fixtureReader(t, fixture, false)
				path, err := source.sessionPath(exampleID)
				if err != nil {
					t.Fatal(err)
				}
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var modifiedBody strings.Builder
				for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
					var record map[string]any
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatal(err)
					}
					data, _ := record["data"].(map[string]any)
					if record["type"] == "tool/call" && data["callId"] == "call-bash" {
						data["name"] = testCase.toolName
					}
					if record["type"] == "tool/result" {
						message := data["message"].(map[string]any)
						if message["id"] == "result-bash" {
							result := message
							if fixture == "v3.jsonl" {
								result = message["content"].([]any)[0].(map[string]any)
							}
							result["isError"] = testCase.deliveryError
							result["content"] = []any{map[string]any{"type": "text", "text": testCase.output}}
						}
					}
					encoded, err := json.Marshal(record)
					if err != nil {
						t.Fatal(err)
					}
					modifiedBody.Write(encoded)
					modifiedBody.WriteByte('\n')
				}
				adaptertest.MustWrite(t, path, modifiedBody.String())
				events, err := source.GetRenderEvents(exampleID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, event := range events {
					if event.Type == "ToolResult" && event.ToolCallID == "call-bash" {
						found = true
						if event.ExitCode != testCase.exitCode || (event.ErrorKind != "") != (testCase.errorCount > 0) {
							t.Fatalf("outcome=%+v", event)
						}
					}
				}
				if !found {
					t.Fatal("missing bash result")
				}
				detail, err := source.GetSession(exampleID)
				if err != nil {
					t.Fatal(err)
				}
				turn := detail.Turns[0]
				if turn.ErrorCount != testCase.errorCount || turn.ToolDetails[1].ExitCode != testCase.exitCode {
					t.Fatalf("turn outcomes=%+v", turn)
				}
				if testCase.exitCode != 0 {
					ansi, err := source.RenderANSI(exampleID, 100)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(ansi, fmt.Sprintf("Failed (exit %d)", testCase.exitCode)) {
						t.Fatal("rendered failure missing recorded exit code")
					}
				}
			})
		}
	}
}

func TestAuthoritativeSnapshotConformance(t *testing.T) {
	for _, fixture := range []string{"v3.jsonl", "v4.jsonl"} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/compressed=%v", fixture, compressed), func(t *testing.T) {
				source := fixtureReader(t, fixture, compressed)
				first := adaptertest.AssertIndexSnapshotEnvelope(t, source, adaptertest.IndexSnapshotEnvelopeExpect{SessionID: exampleID})
				second := adaptertest.AssertIndexSnapshotEnvelope(t, source, adaptertest.IndexSnapshotEnvelopeExpect{SessionID: exampleID})
				path, err := source.sessionPath(exampleID)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(filepath.Clean(path))
				if err != nil {
					t.Fatal(err)
				}
				if first.SourceRevision != second.SourceRevision || first.SourceFingerprint.SizeBytes != info.Size() {
					t.Fatalf("unstable or wrong fingerprint: %+v / %+v", first.SourceFingerprint, second.SourceFingerprint)
				}
			})
		}
	}
}
