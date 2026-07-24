package chrys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestChrysCapabilityEvidence(t *testing.T) {
	dir, id := writeChrysBasicFixture(t)
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader:    func(t *testing.T) adaptertest.Reader { return New(dir) },
			Expect:       adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{id}},
		},
		Evidence: chrysEvidenceCases(),
	})
}

func TestCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), chrysEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d", len(rows))
	}
}

func chrysEvidenceCases() []adaptertest.EvidenceCase {
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-subtasks-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults, capability.CapabilityDiff,
				capability.CapabilitySubtasks, capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				return New(writeFixture(t)) // existing rich fixture with edit + subagent + failed tool
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "28491d6d491e"
				// writeFixture totals: inclusive input 555593, cache hit 488192 → exclusive prompt 67401;
				// completion 10759. Fixture includes a failed tool result.
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID:             id,
					RequireNonNilBilling:  true,
					RequireExactPrecision: true,
					ExactPrompt:           adaptertest.Int64(555593 - 488192),
					ExactCompletion:       adaptertest.Int64(10759),
					ExactCacheRead:        adaptertest.Int64(488192),
					PresentInput:          model.PresenceExact,
					PresentOutput:         model.PresenceExact,
					PresentCacheRead:      model.PresenceExact,
				})
				adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
					SessionID:      id,
					MinPairs:       1,
					RequireSuccess: true,
					RequireFailure: true,
				})
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: id, FilePathSub: "a.css", OldSub: "gap", NewSub: "gap",
				})
				adaptertest.AssertSubtasks(t, r, adaptertest.SubtaskExpect{SessionID: id, MinSubagents: 1})
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{SessionID: id, ExactID: id})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				dir, _ := writeChrysBasicFixture(t)
				return New(dir)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "conformance01"
				path := filepath.Join(r.(*ChrysReader).sessionsDir, id, "session.json")
				marker := "realtime-marker-chrys-more"
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					// Rewrite session.json with an extra user message so content is observable.
					body := `{
  "meta": {
    "schema_version": 1,
    "session_id": "conformance01-full",
    "agent_profile": "Code",
    "model_id": "test-model",
    "created_at": "2026-01-01T00:00:00+00:00",
    "updated_at": "2026-01-01T00:02:00+00:00",
    "message_count": 3,
    "primary_cwd": "/tmp/proj",
    "title": "Conformance"
  },
  "state": {
    "messages": [
      {
        "type": "message", "role": "user",
        "contents": [{"type": "text", "text": "hello conformance", "additional_properties": {}}],
        "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:00+00:00"}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [{"type": "text", "text": "hi", "additional_properties": {}}],
        "additional_properties": {"_chrys_created_at": "2026-01-01T00:00:30+00:00"}
      },
      {
        "type": "message", "role": "user",
        "contents": [{"type": "text", "text": "` + marker + `", "additional_properties": {}}],
        "additional_properties": {"_chrys_created_at": "2026-01-01T00:01:30+00:00"}
      },
      {
        "type": "message", "role": "assistant",
        "contents": [{"type": "text", "text": "", "additional_properties": {}}],
        "additional_properties": {"_chrys_kind": "turn", "_turn_id": "turn_1", "_turn": 1}
      }
    ],
    "compressed_msgs": [],
    "turn_counter": 1,
    "total_session_input_tokens": 12,
    "total_session_output_tokens": 5,
    "total_session_cache_hit_tokens": 0
  }
}`
					if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}, adaptertest.RealtimeExpect{ContentMarker: marker})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				root := t.TempDir()
				// Two sessions
				for _, id := range []string{"delme00000001", "keepme00000002"} {
					dir := filepath.Join(root, id)
					_ = os.MkdirAll(dir, 0o755)
					body := `{"meta":{"schema_version":1,"session_id":"` + id + `","created_at":"2026-01-01T00:00:00+00:00","updated_at":"2026-01-01T00:01:00+00:00","primary_cwd":"/tmp","title":"t","message_count":1},"state":{"messages":[{"type":"message","role":"user","contents":[{"type":"text","text":"hi","additional_properties":{}}],"additional_properties":{}}],"compressed_msgs":[],"turn_counter":0}}`
					_ = os.WriteFile(filepath.Join(dir, "session.json"), []byte(body), 0o644)
				}
				return New(root)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, "delme00000001", "keepme00000002")
			},
		},
		// terminate is unsupported — no evidence case required
	}
}
