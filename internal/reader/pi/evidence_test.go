package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

func TestPiCapabilityEvidence(t *testing.T) {
	root := installTestdata(t, basicID, "basic.jsonl")
	adaptertest.RunFull(t, adaptertest.FullConfig{
		Config: adaptertest.Config{
			Capabilities: Capabilities(),
			NewReader:    func(t *testing.T) adaptertest.Reader { return New(root) },
			Expect:       adaptertest.Expectations{SessionCount: 1, SessionIDs: []string{basicID}},
		},
		Evidence: piEvidenceCases(),
	})
}

func TestCapabilityEvidenceMatrix(t *testing.T) {
	rows := adaptertest.RunCapabilityEvidence(t, Capabilities(), piEvidenceCases(), adaptertest.CoverageOptions{
		BasicSatisfied: adaptertest.DefaultBasicSatisfied(),
	})
	if len(rows) < 10 {
		t.Fatalf("matrix rows=%d", len(rows))
	}
}

func piEvidenceCases() []adaptertest.EvidenceCase {
	const richID = "cccccccc-dddd-eeee-ffff-000000000001"
	return []adaptertest.EvidenceCase{
		{
			Scenario: "data-tokens-tools-diff-resume", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{
				capability.CapabilityTokens, capability.CapabilityToolResults,
				capability.CapabilityDiff, capability.CapabilityResume,
			},
			NewReader: func(t *testing.T) adaptertest.Reader {
				return New(installTestdata(t, richID, "rich.jsonl"))
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				// exclusive prompt 60+10+8+5=83, completion 20+2+2+4=28,
				// cacheRead 40, reasoning 5 (subset of first assistant output).
				adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
					SessionID:             richID,
					RequireNonNilBilling:  true,
					RequireExactPrecision: true,
					ExactPrompt:           adaptertest.Int64(83),
					ExactCompletion:       adaptertest.Int64(28),
					ExactCacheRead:        adaptertest.Int64(40),
					ExactReasoning:        adaptertest.Int64(5),
					PresentInput:          model.PresenceExact,
					PresentOutput:         model.PresenceExact,
					PresentCacheRead:      model.PresenceExact,
					PresentReasoning:      model.PresenceExact,
				})
				adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
					SessionID:      richID,
					MinPairs:       3,
					RequireSuccess: true,
					RequireFailure: true,
				})
				adaptertest.AssertDiff(t, r, adaptertest.DiffExpect{
					SessionID: richID, FilePathSub: "a.go", OldSub: "foo", NewSub: "bar",
				})
				adaptertest.AssertResume(t, r, adaptertest.ResumeExpect{SessionID: richID, ExactID: richID})
			},
		},
		{
			Scenario: "realtime-mutation", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityRealtime},
			NewReader: func(t *testing.T) adaptertest.Reader {
				return New(installTestdata(t, basicID, "basic.jsonl"))
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				pr := r.(*PiReader)
				path := pr.findSessionFile(basicID)
				if path == "" {
					t.Fatal("missing session file")
				}
				marker := "realtime-marker-pi-more"
				adaptertest.AssertRealtimeStableThenMutate(t, r, basicID, func(t *testing.T) {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					_, _ = f.WriteString(`{"type":"message","id":"u9","parentId":"a1","timestamp":"2026-01-01T00:00:10.000Z","message":{"role":"user","content":[{"type":"text","text":"` + marker + `"}],"timestamp":1767225610000}}` + "\n")
				}, adaptertest.RealtimeExpect{ContentMarker: marker})
			},
		},
		{
			Scenario: "delete-sandbox", Synthetic: true, Sanitized: true,
			Covers: []capability.CapabilityID{capability.CapabilityDelete},
			NewReader: func(t *testing.T) adaptertest.Reader {
				root := t.TempDir()
				body, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				writeSessionFile(t, root, "del-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee",
					strings.ReplaceAll(string(body), basicID, "del-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee"))
				writeSessionFile(t, root, "keep-bbbbbbbb-cccc-dddd-eeee-ffffffff",
					strings.ReplaceAll(string(body), basicID, "keep-bbbbbbbb-cccc-dddd-eeee-ffffffff"))
				return New(root)
			},
			Assert: func(t *testing.T, r adaptertest.Reader) {
				adaptertest.AssertDeleteSandbox(t, r, "del-aaaaaaaa-bbbb-cccc-dddd-eeeeeeee", "keep-bbbbbbbb-cccc-dddd-eeee-ffffffff")
			},
		},
	}
}
