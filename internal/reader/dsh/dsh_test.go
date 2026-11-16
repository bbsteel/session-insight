package dsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/klauspost/compress/zstd"
)

const exampleID = "session-example"

func fixtureReader(t *testing.T, fixture string, compressed bool) *Reader {
	t.Helper()
	root := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	generation := "4"
	if fixture == "v3.jsonl" {
		generation = "3"
	}
	path := filepath.Join(root, "--workspace-example--", exampleID, "session.v"+generation+".jsonl")
	if compressed {
		path += ".zstd"
		body = compressFrame(t, body)
	}
	adaptertest.MustWrite(t, path, string(body))
	return New(root)
}

func compressFrame(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(plaintext, nil)
}

func TestConformance(t *testing.T) {
	for _, fixture := range []string{"v3.jsonl", "v4.jsonl"} {
		for _, compressed := range []bool{false, true} {
			name := fixture
			if compressed {
				name += ".zstd"
			}
			t.Run(name, func(t *testing.T) {
				newReader := func(t *testing.T) adaptertest.Reader { return fixtureReader(t, fixture, compressed) }
				adaptertest.RunFull(t, adaptertest.FullConfig{
					Config: adaptertest.Config{Capabilities: Capabilities(), NewReader: newReader, Expect: adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{exampleID}}},
					Evidence: []adaptertest.EvidenceCase{
						{Scenario: "tokens-tools-diff", Synthetic: true, Sanitized: true,
							Covers: []capability.CapabilityID{capability.CapabilityTokens, capability.CapabilityToolResults, capability.CapabilityDiff}, NewReader: newReader,
							Assert: func(t *testing.T, reader adaptertest.Reader) {
								adaptertest.AssertTokens(t, reader, adaptertest.TokenExpect{SessionID: exampleID, RequireNonNilBilling: true, RequireExactPrecision: true, ExactPrompt: adaptertest.Int64(70), ExactCompletion: adaptertest.Int64(24), ExactCacheRead: adaptertest.Int64(40), ExactReasoning: adaptertest.Int64(5), PresentInput: model.PresenceExact, PresentOutput: model.PresenceExact, PresentCacheRead: model.PresenceExact})
								adaptertest.AssertToolResults(t, reader, adaptertest.ToolResultsExpect{SessionID: exampleID, MinPairs: 2, RequireSuccess: true, RequireFailure: true})
								adaptertest.AssertDiff(t, reader, adaptertest.DiffExpect{SessionID: exampleID, FilePathSub: "a.go", OldSub: "foo", NewSub: "bar"})
								detail, err := reader.GetSession(exampleID)
								if err != nil {
									t.Fatal(err)
								}
								adaptertest.AssertProvenanceComplete(t, detail, Capabilities())
								if len(detail.Turns) != 2 || detail.Turns[0].DurationMs != 800 || detail.Turns[1].UserMessage != "Explain the result" {
									t.Fatalf("turns=%+v", detail.Turns)
								}
								ansi, err := reader.RenderANSI(exampleID, 100)
								if err != nil {
									t.Fatal(err)
								}
								for _, text := range []string{"Fix the example", "I will update the example.", "FAIL: example test", "foo", "bar"} {
									if !strings.Contains(ansi, text) {
										t.Fatalf("render missing %q", text)
									}
								}
							},
						},
						{Scenario: "append-frame", Synthetic: true, Sanitized: true, Covers: []capability.CapabilityID{capability.CapabilityRealtime}, NewReader: newReader,
							Assert: func(t *testing.T, reader adaptertest.Reader) {
								dshReader := reader.(*Reader)
								path, err := dshReader.sessionPath(exampleID)
								if err != nil {
									t.Fatal(err)
								}
								adaptertest.AssertRealtimeStableThenMutate(t, reader, exampleID, func(t *testing.T) {
									appendBody := []byte("{\"type\":\"user/message\",\"seq\":100,\"time\":1760000003000,\"data\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"realtime-marker\"}]}}\n")
									if compressed {
										appendBody = compressFrame(t, appendBody)
									}
									file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
									if err != nil {
										t.Fatal(err)
									}
									defer file.Close()
									if _, err = file.Write(appendBody); err != nil {
										t.Fatal(err)
									}
								}, adaptertest.RealtimeExpect{ContentMarker: "realtime-marker"})
							},
						},
					},
				})
			})
		}
	}
}

func TestMissingFieldsEmptyAndInterrupted(t *testing.T) {
	for _, fixture := range []string{"header-only.jsonl", "interrupted.jsonl"} {
		t.Run(fixture, func(t *testing.T) {
			reader := fixtureReader(t, fixture, true)
			detail, err := reader.GetSession(exampleID)
			if err != nil {
				t.Fatal(err)
			}
			if fixture == "header-only.jsonl" {
				adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
				if detail.Billing != nil || len(detail.Turns) != 0 {
					t.Fatalf("empty session fabricated data: %+v", detail)
				}
			} else if detail.Turns[0].DurationMs != 0 || detail.Turns[0].TokenUsage.Present.CacheWrite != model.PresenceMissing {
				t.Fatalf("interruption/missing field fabricated: %+v", detail.Turns)
			}
		})
	}
}

func TestTruncatedFrameAndMalformedRecord(t *testing.T) {
	reader := fixtureReader(t, "v4.jsonl", true)
	path, err := reader.sessionPath(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(compressFrame(t, []byte("invalid-json\n"))); err != nil {
		t.Fatal(err)
	}
	frame := compressFrame(t, []byte("{\"type\":\"unfinished\"}\n"))
	if _, err = file.Write(frame[:len(frame)-3]); err != nil {
		t.Fatal(err)
	}
	file.Close()
	detail, err := reader.GetSession(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
	if len(detail.Turns) != 2 || len(detail.Provenance.Warnings) < 2 {
		t.Fatalf("lost prefix/warnings: %+v", detail.Provenance)
	}
}

func TestGenerationSelectionInventoryAndUnsupported(t *testing.T) {
	reader := fixtureReader(t, "v3.jsonl", false)
	path, err := reader.sessionPath(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", "v4.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(filepath.Dir(path), "session.v4.jsonl")
	adaptertest.MustWrite(t, currentPath, string(body))
	adaptertest.MustWrite(t, filepath.Join(filepath.Dir(path), "session.lock"), "")
	adaptertest.MustWrite(t, filepath.Join(filepath.Dir(path), "unrelated-secret"), "excluded")
	sessions, complete, err := reader.ListSessionsDetailed()
	if err != nil || !complete || len(sessions) != 1 {
		t.Fatalf("listing: %v %v %v", sessions, complete, err)
	}
	detail, err := reader.GetSession(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Provenance.Sources) != 2 || detail.Provenance.Sources[0].Path != currentPath || detail.Provenance.Sources[1].Role != model.SourceRoleSnapshot {
		t.Fatalf("sources=%+v", detail.Provenance.Sources)
	}
	futureBody := strings.Replace(string(body), "\"version\":4", "\"version\":99", 1)
	adaptertest.MustWrite(t, filepath.Join(filepath.Dir(path), "session.v99.jsonl"), futureBody)
	if _, _, err := reader.ListSessionsDetailed(); err != nil {
		t.Fatal(err)
	}
	detail, err = reader.GetSession(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
	if detail.Provenance.State != model.RecordParserUnsupported {
		t.Fatalf("state=%s", detail.Provenance.State)
	}
	if _, err := reader.GetRenderEvents(exampleID); err == nil {
		t.Fatal("future generation replay should refuse")
	}
}

func TestMissingAndInvalidIdentity(t *testing.T) {
	reader := fixtureReader(t, "v4.jsonl", false)
	for _, id := range []string{"../session-example", "unknown"} {
		if _, err := reader.GetSession(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	path, err := reader.sessionPath(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = reader.GetSession(exampleID)
	failure, ok := readerr.As(err)
	if !ok || failure.Kind != readerr.SourceMissing || len(failure.Sources) != 1 || failure.Sources[0].State != model.SourceMissing {
		t.Fatalf("missing provenance=%+v err=%v", failure, err)
	}
}

func TestResolveSessionsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".dsh", "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_HOME", "  ")
	if path, ok := ResolveSessionsDir(); !ok || path != root {
		t.Fatalf("default=%s %v", path, ok)
	}
	custom := filepath.Join(home, "custom")
	if err := os.MkdirAll(filepath.Join(custom, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_HOME", custom)
	if path, ok := ResolveSessionsDir(); !ok || path != filepath.Join(custom, "sessions") {
		t.Fatalf("override=%s %v", path, ok)
	}
	t.Setenv("DSH_HOME", filepath.Join(home, "absent"))
	if _, ok := ResolveSessionsDir(); ok {
		t.Fatal("must not fall back from explicit absent override")
	}
}

func TestAmbiguousSessionSourcesAreNotChosen(t *testing.T) {
	for _, scenario := range []string{"duplicate-directory", "mixed-encoding"} {
		t.Run(scenario, func(t *testing.T) {
			reader := fixtureReader(t, "v4.jsonl", false)
			path, err := reader.sessionPath(exampleID)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "duplicate-directory" {
				adaptertest.MustWrite(t, filepath.Join(reader.sessionsRoot, "--other-project--", exampleID, "session.v4.jsonl"), string(body))
			} else {
				adaptertest.MustWrite(t, path+".zstd", string(compressFrame(t, body)))
			}
			sessions, complete, err := reader.ListSessionsDetailed()
			if err != nil || complete || len(sessions) != 0 {
				t.Fatalf("ambiguous inventory: %v complete=%v err=%v", sessions, complete, err)
			}
			if _, err := reader.GetSession(exampleID); err == nil {
				t.Fatal("must not replay an arbitrary ambiguous source")
			}
		})
	}
}

func TestPartialUsageAndPendingToolRemainMissing(t *testing.T) {
	reader := fixtureReader(t, "v4.jsonl", false)
	path, err := reader.sessionPath(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("{\"type\":\"assistant/message\",\"seq\":100,\"time\":1760000003000,\"data\":{\"turn\":2,\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"An incomplete response\"}]}}}\n" +
		"{\"type\":\"tool/call\",\"seq\":101,\"time\":1760000003100,\"data\":{\"turn\":2,\"callId\":\"pending\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"sleep 30\\\"}\"}}\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reader.GetSession(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing == nil || detail.Billing.Precision != model.PrecisionMissing || detail.Billing.Totals.PromptTokens != 70 {
		t.Fatalf("partial usage=%+v", detail.Billing)
	}
	adaptertest.AssertProvenanceDegradedOrUnsupported(t, detail, Capabilities())
}

func TestHeaderIdentityMismatchIsTyped(t *testing.T) {
	reader := fixtureReader(t, "header-only.jsonl", false)
	path, err := reader.sessionPath(exampleID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.MustWrite(t, path, strings.Replace(string(body), exampleID, "session-other", 1))
	_, err = reader.GetSession(exampleID)
	failure, ok := readerr.As(err)
	if !ok || failure.Kind != readerr.ParseFailed || len(failure.Sources) != 1 {
		t.Fatalf("identity failure=%+v err=%v", failure, err)
	}
}
