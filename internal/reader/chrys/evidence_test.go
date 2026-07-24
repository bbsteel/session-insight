package chrys

import (
	"os"
	"path/filepath"
	"testing"

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
				return New(writeFixture(t)) // existing rich fixture with edit + subagent
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				id := "28491d6d491e"
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID: id, RequireNonNilBilling: true,
				})
				adaptertest.AssertToolResults(t, r, id, 1)
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
				adaptertest.AssertRealtimeStableThenMutate(t, r, id, func(t *testing.T) {
					// Append whitespace to change size+mtime.
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					_, _ = f.WriteString("\n")
				})
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
