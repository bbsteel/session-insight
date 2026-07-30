package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestGrokCapabilityEvidence(t *testing.T) {
	root := t.TempDir()
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeSession(t, root, "%2Ftmp%2Fdemo", id, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader:    func(t *testing.T) adaptertest.Reader { return New(root) },
			Expect:       adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{id}},
		},
		Evidence: grokEvidenceCases(),
	})
}

func TestCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), grokEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d", len(rows))
	}
}

func grokEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults,
				capability.CapabilityDiff, capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				root := t.TempDir()
				id := "cccccccc-dddd-eeee-ffff-000000000001"
				// Include success edit + failed tool for pair evidence
				updates := sampleUpdatesWithEditAndFail()
				writeSession(t, root, "%2Ftmp%2Fdemo", id, summaryFile{}, updates, sampleEventsClosed())
				return New(root)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "cccccccc-dddd-eeee-ffff-000000000001"
				// inclusive input 100 − cache 40 → exclusive prompt 60
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID:             id,
					RequireNonNilBilling:  true,
					RequireExactPrecision: true,
					ExactPrompt:           adaptertest.Int64(60),
					ExactCompletion:       adaptertest.Int64(20),
					ExactCacheRead:        adaptertest.Int64(40),
					ExactReasoning:        adaptertest.Int64(5),
					PresentInput:          model.PresenceExact,
					PresentOutput:         model.PresenceExact,
					PresentCacheRead:      model.PresenceExact,
					PresentReasoning:      model.PresenceExact,
				})
				adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
					SessionID:      id,
					MinPairs:       2,
					RequireSuccess: true,
					RequireFailure: true,
				})
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: id, FilePathSub: "a.go", OldSub: "foo", NewSub: "bar",
				})
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{SessionID: id, ExactID: id})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				root := t.TempDir()
				writeSession(t, root, "%2Ftmp%2Fdemo", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
				return New(root)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
				gr := r.(*GrokReader)
				loc, err := gr.findSession(id)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(loc.Dir, "updates.jsonl")
				marker := "realtime-marker-grok-more"
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					_, _ = f.WriteString(`{"timestamp":1700000099,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + marker + `"}}}}` + "\n")
				}, adaptertest.RealtimeExpect{ContentMarker: marker})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				// Reuse delete test layout with two sessions under one home.
				root := t.TempDir()
				// Grok New expects sessionsDir; home is parent for active_sessions.
				// writeSession uses root as sessionsDir.
				writeSession(t, root, "%2Ftmp%2Fa", "del-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee", summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
				writeSession(t, root, "%2Ftmp%2Fb", "keep-bbbbbbbb-cccc-dddd-eeee-ffffffff", summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
				return New(root)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, "del-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee", "keep-bbbbbbbb-cccc-dddd-eeee-ffffffff")
			},
		},
		{
			Scenario: "terminate-active-pid", Synthetic: true, Sanitized: true,
			Platforms: []string{"linux"},
			Covers:    []capability.CapabilityID{capability.CapabilityTerminate},
			NewReader: func(t *testing.T) adaptertest.Reader {
				// sessions under root; active_sessions at parent = need New(sessions)
				// GrokReader: sessionsDir, grokHome = Dir(sessionsDir)
				home := t.TempDir()
				sessions := filepath.Join(home, "sessions")
				id := "term-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee"
				writeSession(t, sessions, "%2Ftmp%2Fdemo", id, summaryFile{}, sampleUpdatesClosed(), sampleEventsClosed())
				// Register this test process as owning the session.
				active := []map[string]any{{
					"session_id": id,
					"pid":        os.Getpid(),
				}}
				b, _ := json.Marshal(active)
				if err := os.WriteFile(filepath.Join(home, "active_sessions.json"), b, 0o644); err != nil {
					t.Fatal(err)
				}
				return New(sessions)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertTerminatePID(t, r, "term-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee", os.Getpid(), 1)
			},
		},
		{
			Scenario: "subtasks-standalone-child", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilitySubtasks},
			NewReader: func(t *testing.T) adaptertest.Reader {
				return New(fixtureStandaloneChild(t))
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.RunCollaboration(t, r, adaptertest.CollaborationExpect{
					RootSession: model.Session{
						ID: gRootID, AgentType: "grok",
					},
					MinChildren:           1,
					RequireBackingSession: true,
				})
				// Lineage must mark the known child; root must not be a second collab root.
				list, err := r.ListSessions()
				if err != nil {
					t.Fatal(err)
				}
				var child, root model.Session
				var foundChild, foundRoot bool
				for _, s := range list {
					switch s.ID {
					case gChildID:
						child = s
						foundChild = true
					case gRootID:
						root = s
						foundRoot = true
					}
				}
				if !foundChild || !foundRoot {
					t.Fatalf("expected child and root in ListSessions: child=%v root=%v", foundChild, foundRoot)
				}
				if !child.IsSubagent || child.ParentSessionID != gRootID {
					t.Fatalf("child lineage: IsSubagent=%v Parent=%q", child.IsSubagent, child.ParentSessionID)
				}
				if root.IsSubagent || root.ParentSessionID != "" {
					t.Fatalf("root must not carry child lineage: %+v", root)
				}
			},
		},
	}
}

func sampleUpdatesWithEditAndFail() string {
	return `{"timestamp":1700000000,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello"}}}}
{"timestamp":1700000001,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call-edit","title":"search_replace","rawInput":{"file_path":"a.go","old_string":"foo","new_string":"bar"},"_meta":{"x.ai/tool":{"name":"search_replace"}}}}}
{"timestamp":1700000002,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-edit","status":"completed","content":[{"type":"content","content":{"type":"text","text":"ok"}}]}}}
{"timestamp":1700000002,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call-fail","title":"bash","rawInput":{"command":"false"},"_meta":{"x.ai/tool":{"name":"bash"}}}}}
{"timestamp":1700000003,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-fail","status":"failed","content":[{"type":"content","content":{"type":"text","text":"Tool failed: blocked"}}]}}}
{"timestamp":1700000003,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}}}
{"timestamp":1700000004,"method":"_x.ai/session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn","usage":{"inputTokens":100,"outputTokens":20,"cachedReadTokens":40,"reasoningTokens":5,"modelCalls":1,"apiDurationMs":500,"modelUsage":{"grok-4.5":{"inputTokens":100,"outputTokens":20,"cachedReadTokens":40,"reasoningTokens":5,"modelCalls":1,"apiDurationMs":500}}}}}}
`
}
