package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestCopilotCapabilityEvidence(t *testing.T) {
	dir, id := writeCopilotBasicFixture(t)
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader:    func(t *testing.T) adaptertest.Reader { return New(dir) },
			Expect:       adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{id}},
		},
		Evidence: copilotEvidenceCases(),
	})
}

func TestCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), copilotEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d", len(rows))
	}
}

func copilotEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-subtasks", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults,
				capability.CapabilityDiff, capability.CapabilitySubtasks,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeCopilotRichFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "copilot-rich-1"
				// tokenDetails: input 100, output 50, cache_read 10 (exclusive buckets).
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID:             id,
					RequireNonNilBilling:  true,
					RequireExactPrecision: true,
					ExactPrompt:           adaptertest.Int64(100),
					ExactCompletion:       adaptertest.Int64(50),
					ExactCacheRead:        adaptertest.Int64(10),
					PresentInput:          model.PresenceExact,
					PresentOutput:         model.PresenceExact,
					PresentCacheRead:      model.PresenceExact,
				})
				adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
					SessionID:      id,
					MinPairs:       2,
					RequireSuccess: true,
					RequireFailure: true, // exitCode:1 shell in fixture
				})
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: id, FilePathSub: "a.go",
				})
				// If patch path extraction fails filters, still require min edit
				events, _ := r.GetRenderEvents(id)
				var edits int
				for _, e := range events {
					if e.Type == "ToolInvocation" && (e.ToolName == "apply_patch" || e.ToolName == "edit") {
						edits++
					}
				}
				if edits == 0 {
					adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{SessionID: id, MinEditCalls: 1})
				}
				adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{SessionID: id, MinSubagents: 1})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeCopilotBasicFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "conformance-copilot-1"
				path := filepath.Join(r.(*CopilotReader).sessionDir, id, "events.jsonl")
				marker := "realtime-marker-copilot-more"
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					_, _ = f.WriteString(`{"type":"user.message","timestamp":"2026-01-01T00:00:10Z","data":{"content":"` + marker + `"}}` + "\n")
				}, adaptertest.RealtimeExpect{ContentMarker: marker})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				r, _ := newTestCopilotHome(t)
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, delID, keepID)
			},
		},
		{
			Scenario: "terminate-fd-holder", Synthetic: true, Sanitized: true,
			Platforms: []string{"linux"},
			Covers:    []capability.CapabilityID{capability.CapabilityTerminate},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeCopilotBasicFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "conformance-copilot-1"
				path := filepath.Join(r.(*CopilotReader).sessionDir, id, "events.jsonl")
				pid := adaptertest.StartFileHolder(t, path)
				adaptertest.AssertTerminatePID(t, r, id, pid, 1)
			},
		},
	}
}

func writeCopilotRichFixture(t *testing.T) (stateDir, sessionID string) {
	t.Helper()
	stateDir = t.TempDir()
	sessionID = "copilot-rich-1"
	dir := filepath.Join(stateDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := `id: copilot-rich-1
cwd: /tmp/proj
name: Rich
user_named: true
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:05:00Z
`
	adaptertest.MustWrite(t, filepath.Join(dir, "workspace.yaml"), ws)
	// Billing via session.shutdown; tools (success+failure); subagent; apply_patch
	patch := strings.ReplaceAll(`*** Begin Patch
*** Update File: a.go
@@
-old
+new
*** End Patch`, "\n", "\\n")
	events := strings.Join([]string{
		`{"type":"user.message","timestamp":"2026-01-01T00:00:00Z","data":{"content":"hi"}}`,
		`{"type":"tool.execution_start","timestamp":"2026-01-01T00:00:01Z","data":{"toolCallId":"c1","toolName":"apply_patch","arguments":{"input":"` + patch + `"}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-01-01T00:00:02Z","data":{"toolCallId":"c1","exitCode":0,"stdout":"ok"}}`,
		`{"type":"tool.execution_start","timestamp":"2026-01-01T00:00:03Z","data":{"toolCallId":"c2","toolName":"shell","arguments":{"command":"false"}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-01-01T00:00:04Z","data":{"toolCallId":"c2","exitCode":1,"stderr":"fail"}}`,
		`{"type":"subagent.started","timestamp":"2026-01-01T00:00:05Z","data":{"agentDisplayName":"explore","subagent_id":"sub-1"}}`,
		`{"type":"subagent.completed","timestamp":"2026-01-01T00:00:06Z","data":{"subagent_id":"sub-1"}}`,
		`{"type":"session.shutdown","timestamp":"2026-01-01T00:00:07Z","data":{"totalNanoAiu":1500000000,"tokenDetails":{"input":{"tokenCount":100},"output":{"tokenCount":50},"cache_read":{"tokenCount":10}}}}`,
	}, "\n") + "\n"
	adaptertest.MustWrite(t, filepath.Join(dir, "events.jsonl"), events)
	return stateDir, sessionID
}
