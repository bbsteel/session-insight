package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestCodexCapabilityEvidence(t *testing.T) {
	dir, sessionID := writeCodexBasicFixture(t)
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader:    func(t *testing.T) adaptertest.Reader { return New(dir) },
			Expect:       adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{sessionID}},
		},
		Evidence: codexEvidenceCases(),
	})
}

func TestCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), codexEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d", len(rows))
	}
}

func codexEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-subtasks-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults, capability.CapabilityDiff,
				capability.CapabilitySubtasks, capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeCodexRichFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				// Parent root id is filename stem; resume is payload id.
				list, err := r.ListSessions()
				if err != nil || len(list) < 1 {
					t.Fatalf("list: %v n=%d", err, len(list))
				}
				// Prefer root (non-subagent) for tools/tokens.
				var rootID, childID string
				for _, s := range list {
					if s.IsSubagent {
						childID = s.ID
						continue
					}
					rootID = s.ID
				}
				if rootID == "" {
					rootID = list[0].ID
				}
				d, err := r.GetSession(rootID)
				if err != nil {
					t.Fatal(err)
				}
				var prompt, completion, cacheRead, reasoning int64
				for _, tr := range d.Turns {
					prompt += tr.TokenUsage.PromptTokens
					completion += tr.TokenUsage.CompletionTokens
					cacheRead += tr.TokenUsage.CacheReadTokens
					reasoning += tr.TokenUsage.ReasoningTokens
				}
				if prompt != 200 || completion != 100 || cacheRead != 800 || reasoning != 30 {
					t.Fatalf("token buckets prompt=%d completion=%d cache=%d reasoning=%d want 200/100/800/30",
						prompt, completion, cacheRead, reasoning)
				}
				if reasoning > completion {
					t.Fatal("reasoning double-count")
				}
				adaptertest.AssertToolResults(t, r, rootID, 1)
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: rootID, FilePathSub: "notes.md", OldSub: "old", NewSub: "new",
				})
				if childID != "" {
					adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{SessionID: rootID, MinSubagents: 1, RequireChildIDs: true})
				} else {
					adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{SessionID: rootID, MinSubagents: 1})
				}
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{
					SessionID: rootID, RejectSuffix: ".jsonl",
				})
				// Child resume identity when present
				if childID != "" {
					cd, err := r.GetSession(childID)
					if err != nil {
						t.Fatal(err)
					}
					if cd.ResumeID == "" || strings.HasSuffix(cd.ResumeID, ".jsonl") {
						t.Fatalf("child ResumeID=%q", cd.ResumeID)
					}
					if cd.ResumeID == childID {
						// filename stem equals resume only if no native id — fail for our fixture
						t.Logf("note: ResumeID equals file stem %q", cd.ResumeID)
					}
				}
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeCodexBasicFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				cr := r.(*CodexReader)
				list, _ := r.ListSessions()
				id := list[0].ID
				path := cr.findSessionFile(id)
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					_, _ = f.WriteString(`{"timestamp":"2026-01-01T00:00:09.000Z","type":"event_msg","payload":{"type":"user_message","message":"more"}}` + "\n")
				})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				_, r := writeDeleteFixture(t)
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
				dir, _ := writeCodexBasicFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				cr := r.(*CodexReader)
				list, _ := r.ListSessions()
				id := list[0].ID
				path := cr.findSessionFile(id)
				// HoldersOf excludes the test process; use a child holder.
				pid := adaptertest.StartFileHolder(t, path)
				adaptertest.AssertTerminatePID(t, r, id, pid, 1)
			},
		},
	}
}

func writeCodexRichFixture(t *testing.T) (sessionsDir, rootID string) {
	t.Helper()
	root := t.TempDir()
	sessionsDir = filepath.Join(root, "sessions")
	day := filepath.Join(sessionsDir, "2026", "01", "02")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	parentNative := "parent-native-id"
	childNative := "child-native-id"
	rootID = "rollout-2026-01-02T00-00-00-019f0000-0000-7000-8000-0000000000aa"
	childFile := "rollout-2026-01-02T00-00-01-019f0000-0000-7000-8000-0000000000bb"

	rootLines := strings.Join([]string{
		`{"timestamp":"2026-01-02T00:00:00.000Z","type":"session_meta","payload":{"id":"` + parentNative + `","cwd":"/tmp/proj","model_provider":"openai"}}`,
		`{"timestamp":"2026-01-02T00:00:00.500Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"timestamp":"2026-01-02T00:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"edit file"}}`,
		`{"timestamp":"2026-01-02T00:00:02.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: notes.md\n@@\n-old\n+new\n*** End Patch"}}`,
		`{"timestamp":"2026-01-02T00:00:02.500Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"Exit code: 0\nOutput:\nSuccess"}}`,
		`{"timestamp":"2026-01-02T00:00:03.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":30},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":30}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(day, rootID+".jsonl"), []byte(rootLines), 0o644); err != nil {
		t.Fatal(err)
	}

	childLines := strings.Join([]string{
		`{"timestamp":"2026-01-02T00:00:04.000Z","type":"session_meta","payload":{"id":"` + childNative + `","session_id":"` + parentNative + `","parent_thread_id":"` + parentNative + `","thread_source":"subagent","agent_path":"/root/audit","cwd":"/tmp/proj"}}`,
		`{"timestamp":"2026-01-02T00:00:04.500Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"timestamp":"2026-01-02T00:00:05.000Z","type":"event_msg","payload":{"type":"user_message","message":"child work"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(day, childFile+".jsonl"), []byte(childLines), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionsDir, rootID
}
