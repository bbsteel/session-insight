//go:build sqlite_fts5

package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/dsh"
	"github.com/klauspost/compress/zstd"
)

func dshFixtureBytes(t *testing.T, version string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "reader", "dsh", "testdata", "v"+version+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func dshCompressedFrame(t *testing.T, body []byte) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(body, nil)
}

func TestDSHIndexerPersistsDegradationWithoutNewEventTime(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		for _, scenario := range []string{"malformed-line", "torn-line", "malformed-assistant-data", "torn-frame"} {
			if scenario == "torn-frame" && !compressed {
				continue
			}
			name := scenario + "/plain"
			if compressed {
				name = scenario + "/zstd"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "project", "session-example", "session.v4.jsonl")
				body := dshFixtureBytes(t, "4")
				if compressed {
					path += ".zstd"
					body = dshCompressedFrame(t, body)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				source := dsh.New(root)
				database := openIndexerDB(t)
				indexer := New(database, []reader.BaseSessionReader{source})
				for cycle := 0; cycle < 2; cycle++ {
					if err := indexer.RunOnce(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				originalRevision, exists, err := database.GetWatermark("dsh", "session-example")
				if err != nil || !exists {
					t.Fatalf("watermark exists=%v err=%v", exists, err)
				}
				originalSourceRevision, _, _, err := database.SessionChangeRequestCreationIndexState("dsh", "session-example")
				if err != nil {
					t.Fatal(err)
				}
				suffix := []byte("not-json\n")
				switch scenario {
				case "torn-line":
					suffix = []byte(`{"type":"assistant/message","seq":100,"data":`)
				case "malformed-assistant-data":
					suffix = []byte("{\"type\":\"assistant/message\",\"seq\":100,\"time\":1760000001400,\"data\":{\"turn\":2,\"usage\":{\"inputTokens\":\"invalid\"}}}\n")
				case "torn-frame":
					suffix = []byte("{\"type\":\"assistant/message\",\"seq\":100}\n")
				}
				if compressed {
					suffix = dshCompressedFrame(t, suffix)
				}
				if scenario == "torn-frame" {
					suffix = suffix[:len(suffix)-3]
				}
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.Write(suffix)
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
				// A fresh indexer must also detect it; no in-memory revision history is required.
				indexer = New(database, []reader.BaseSessionReader{source})
				for cycle := 0; cycle < 2; cycle++ {
					if err := indexer.RunOnce(context.Background()); err != nil {
						t.Fatal(err)
					}
					persisted, exists, err := database.GetProvenance("dsh", "session-example")
					if err != nil || !exists || persisted.State != model.RecordDegraded {
						t.Fatalf("persisted provenance=%+v exists=%v err=%v", persisted, exists, err)
					}
				}
				currentRevision, _, err := database.GetWatermark("dsh", "session-example")
				if err != nil || currentRevision != originalRevision {
					t.Fatalf("event watermark changed: %d -> %d err=%v", originalRevision, currentRevision, err)
				}
				currentSourceRevision, _, _, err := database.SessionChangeRequestCreationIndexState("dsh", "session-example")
				if err != nil || currentSourceRevision == originalSourceRevision {
					t.Fatalf("source revision unchanged: %q err=%v", currentSourceRevision, err)
				}
				detail, err := source.GetSession("session-example")
				if err != nil {
					t.Fatal(err)
				}
				if detail.Billing == nil || detail.Billing.Precision != model.PrecisionMissing || detail.Billing.Totals.PromptTokens != 70 {
					t.Fatalf("partial billing=%+v", detail.Billing)
				}
				resolved, err := reader.ResolveSessionCapabilities(source, detail, dsh.Capabilities())
				if err != nil || resolved.Status[capability.CapabilityTokens].State != capability.CapabilityMissing {
					t.Fatalf("token state=%+v err=%v", resolved.Status[capability.CapabilityTokens], err)
				}
			})
		}
	}
}

func TestDSHIndexerDetectsGenerationAndSameStatReplacements(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "project", "session-example", "session.v3.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, dshFixtureBytes(t, "3"), 0600); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	source := dsh.New(root)
	database := openIndexerDB(t)
	indexer := New(database, []reader.BaseSessionReader{source})
	if err := indexer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	originalRevision, _, err := database.GetWatermark("dsh", "session-example")
	if err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(filepath.Dir(path), "session.v4.jsonl")
	for _, title := range []string{"Example update", "Example revise"} {
		replacement := strings.Replace(string(dshFixtureBytes(t, "4")), "Example repair", title, 1)
		if err := os.WriteFile(currentPath, []byte(replacement), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(currentPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
			t.Fatal(err)
		}
		if err := indexer.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		persisted, exists, err := database.GetSessionRow("dsh", "session-example")
		if err != nil || !exists || persisted.Name != title {
			t.Fatalf("replacement not persisted: %+v exists=%v err=%v", persisted, exists, err)
		}
		revision, _, err := database.GetWatermark("dsh", "session-example")
		if err != nil || revision != originalRevision {
			t.Fatalf("event time changed: %d -> %d err=%v", originalRevision, revision, err)
		}
	}
}

func TestDSHChildTurnRefreshesCollaborationWithoutChangingParentWatermark(t *testing.T) {
	root := t.TempDir()
	parentPath := filepath.Join(root, "project", "session-example", "session.v4.jsonl")
	childPath := filepath.Join(root, "project", "session-child", "session.v4.jsonl")
	for _, path := range []string{parentPath, childPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(parentPath, dshFixtureBytes(t, "4"), 0600); err != nil {
		t.Fatal(err)
	}
	writeChild := func(completion string) {
		t.Helper()
		body := `{"type":"session","version":4,"id":"session-child","parentSession":"session-example","createdAt":1760000000000}
{"type":"turn/start","seq":1,"time":1760000000010,"data":{"turn":1}}
` + completion
		if err := os.WriteFile(childPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	database := openIndexerDB(t)
	index := New(database, []reader.BaseSessionReader{dsh.New(root)})
	readGraph := func() (int64, int64, int) {
		t.Helper()
		if err := index.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		graph, err := database.GetCollaboration("dsh", "session-example")
		if err != nil || graph == nil || len(graph.Graph.Invocations) != 2 {
			t.Fatalf("collaboration graph=%+v err=%v", graph, err)
		}
		parentWatermark, exists, err := database.GetWatermark("dsh", "session-example")
		if err != nil || !exists {
			t.Fatalf("parent watermark exists=%v err=%v", exists, err)
		}
		return parentWatermark, graph.Graph.Revision, len(graph.Graph.Invocations[1].Activity)
	}
	writeChild("")
	originalWatermark, originalGraphRevision, originalSpans := readGraph()
	writeChild("{\"type\":\"turn/end\",\"seq\":2,\"time\":1760000000040,\"data\":{\"turn\":1,\"reason\":{\"kind\":\"completed\"}}}\n")
	watermark, graphRevision, spans := readGraph()
	if watermark != originalWatermark || graphRevision == originalGraphRevision || originalSpans != 0 || spans != 1 {
		t.Fatalf("child change: watermark %d -> %d, graph %d -> %d, spans %d -> %d",
			originalWatermark, watermark, originalGraphRevision, graphRevision, originalSpans, spans)
	}
}
