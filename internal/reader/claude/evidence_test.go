package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/procfind"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// TestClaudeCapabilityEvidence registers Layer-3 evidence for every exact
// capability and runs the strict coverage gate.
func TestClaudeCapabilityEvidence(t *testing.T) {
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeClaudeBasicFixture(t)
				return New(dir)
			},
			Expect: adaptertest.Expectations{
				SessionCount: 1,
				SessionIDs:   []string{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
			},
		},
		Evidence: claudeEvidenceCases(),
	})
}

// TestCapabilityEvidenceMatrix is the inspectable matrix entry for this package.
func TestCapabilityEvidenceMatrix(t *testing.T) {
	// RunFull already logs the matrix; re-run evidence path for -run filter.
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), claudeEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d want >= 10", len(rows))
	}
}

func claudeEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-subtasks-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens,
				capability.CapabilityToolResults,
				capability.CapabilityDiff,
				capability.CapabilitySubtasks,
				capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeClaudeRichFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
				// Fixture usage: input=100, output=40, cache_read=10, cache_write=5
				// on the assistant message → turn TokenUsage exclusive buckets.
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID:         id,
					AllowTurnFallback: true,
					ExactPrompt:       adaptertest.Int64(100),
					ExactCompletion:   adaptertest.Int64(40),
					ExactCacheRead:    adaptertest.Int64(10),
					ExactCacheWrite:   adaptertest.Int64(5),
					PresentInput:      model.PresenceExact,
					PresentOutput:     model.PresenceExact,
					PresentCacheRead:  model.PresenceExact,
					PresentCacheWrite: model.PresenceExact,
					PresentReasoning:  model.PresenceNA,
				})
				adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
					SessionID:      id,
					MinPairs:       2,
					RequireSuccess: true,
					RequireFailure: true,
				})
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: id, FilePathSub: "a.go", OldSub: "old", NewSub: "new",
				})
				adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{SessionID: id, MinSubagents: 1})
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{SessionID: id, ExactID: id, RejectSuffix: ".jsonl"})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeClaudeBasicFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
				path := findClaudeJSONL(t, r, id)
				marker := "realtime-marker-claude-more"
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					line := `{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:10.000Z","message":{"role":"user","content":"` + marker + `"}}` + "\n"
					if _, err := f.WriteString(line); err != nil {
						t.Fatal(err)
					}
				}, adaptertest.RealtimeExpect{ContentMarker: marker})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				r, _ := newTestClaudeHome(t)
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, delSessionID, otherSessionID)
			},
		},
		{
			Scenario: "terminate-heartbeat-pid", Synthetic: true, Sanitized: true,
			Platforms: []string{"linux"},
			Covers:    []capability.CapabilityID{capability.CapabilityTerminate},
			NewReader: func(t *testing.T) adaptertest.Reader {
				r, root := newTestClaudeHome(t)
				self := os.Getpid()
				token, _ := procfind.StartToken(self)
				writeHeartbeat(t, filepath.Join(root, "sessions"), self, delSessionID, token)
				return r
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertTerminatePID(t, r, delSessionID, os.Getpid(), 1)
			},
		},
	}
}

func findClaudeJSONL(t *testing.T, r adaptertest.Reader, id string) string {
	t.Helper()
	// ClaudeReader stores projectsDir unexported; re-derive from ListSessions by
	// using type assertion to *ClaudeReader.
	cr, ok := r.(*ClaudeReader)
	if !ok {
		t.Fatalf("want *ClaudeReader, got %T", r)
	}
	path := cr.findSessionFile(id)
	if path == "" {
		t.Fatalf("session file not found for %s", id)
	}
	return path
}

func writeClaudeRichFixture(t *testing.T) (projectsDir, sessionID string) {
	t.Helper()
	root := t.TempDir()
	projectsDir = filepath.Join(root, "projects")
	sessionID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	proj := filepath.Join(projectsDir, "-tmp-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Subagent transcript for Agent tool splicing.
	subDir := filepath.Join(proj, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentID := "agent1111-2222-3333-4444-555555555555"
	subBody := `{"type":"user","uuid":"su1","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"user","content":"sub task"}}
{"type":"assistant","uuid":"sa1","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"sub done"}]}}
`
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".jsonl"), []byte(subBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// meta.json linking toolUseId
	meta := map[string]string{"toolUseId": "toolu_agent_1", "agentId": agentID}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".meta.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}

	// Success Edit + Agent, plus a failing Bash for success+failure pair evidence.
	main := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/proj","sessionId":"` + sessionID + `","message":{"role":"user","content":"edit and delegate"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","model":"claude-sonnet-4","usage":{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":10,"cache_creation_input_tokens":5},"content":[{"type":"tool_use","id":"toolu_edit_1","name":"Edit","input":{"file_path":"/tmp/proj/a.go","old_string":"old","new_string":"new"}},{"type":"tool_use","id":"toolu_agent_1","name":"Agent","input":{"description":"explore","prompt":"look around"}},{"type":"tool_use","id":"toolu_bash_fail","name":"Bash","input":{"command":"false"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:00:04.000Z","toolUseResult":{"stdout":"ok"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_edit_1","content":"ok"}]}}
{"type":"user","uuid":"u3","timestamp":"2026-01-01T00:00:05.000Z","toolUseResult":{"stdout":"done","agentId":"` + agentID + `"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent_1","content":"done"}]}}
{"type":"user","uuid":"u4","timestamp":"2026-01-01T00:00:06.000Z","toolUseResult":{"stderr":"command failed","is_error":true},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash_fail","is_error":true,"content":"command failed"}]}}
`
	if err := os.WriteFile(filepath.Join(proj, sessionID+".jsonl"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectsDir, sessionID
}
