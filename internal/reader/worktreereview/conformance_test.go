package worktreereview

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/adaptertest"
)

// TestConformance runs the shared Layer 1 + Layer 2 suite against the
// passed fixture: stable listing, list/detail agreement, reread stability,
// unknown-session handling and no panics.
func TestConformance(t *testing.T) {
	adaptertest.Run(t, adaptertest.Config{
		Capabilities: Capabilities(),
		NewReader: func(t *testing.T) adaptertest.Reader {
			return New("testdata")
		},
		Expect: adaptertest.Expectations{
			// 7 readable fixtures (the v99 one is skipped, inventory incomplete).
			SessionCount: 7,
			SessionIDs: []string{
				fixturePassed,
				fixtureBlocked,
				fixtureError,
				fixtureRunning,
				fixtureInterrupted,
				fixtureCorrupt,
				fixtureChild,
			},
		},
	})
}

// TestRealtimeEvidence covers the realtime declaration: stat-level revision
// is stable, changes after an append, and the new content becomes readable.
func TestRealtimeEvidence(t *testing.T) {
	dir := t.TempDir()
	attemptID := "attempt_realtime_evidence"
	meta := `{"schema":"worktree-review.session-metadata/v1","attempt_id":"` + attemptID + `","heartbeat_at":"2999-01-01T00:00:00.000Z","last_persisted_sequence":1}` + "\n"
	event1 := `{"schema":"worktree-review.event/v1","sequence":1,"occurred_at":"2026-09-09T16:00:00.000Z","attempt_id":"` + attemptID + `","surface":"web","event_type":"attempt.created","payload":{"source":"local-worktree","repository_id":"r","repository_display":"acme/realtime"},"reconstructed":false}` + "\n"
	adaptertest.MustWrite(t, dir+"/"+attemptID+"/metadata.json", meta)
	adaptertest.MustWrite(t, dir+"/"+attemptID+"/events.jsonl", event1)

	r := New(dir)
	adaptertest.AssertRealtimeStableThenMutate(t, r, attemptID, func(t *testing.T) {
		event2 := `{"schema":"worktree-review.event/v1","sequence":2,"occurred_at":"2026-09-09T16:00:01.000Z","attempt_id":"` + attemptID + `","surface":"web","event_type":"gate.evaluated","payload":{"gate_state":"Passed"},"reconstructed":false}` + "\n"
		f, err := osOpenAppend(dir + "/" + attemptID + "/events.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(event2); err != nil {
			t.Fatal(err)
		}
	}, adaptertest.RealtimeExpect{ContentMarker: "Gate: Passed"})
}

// TestTokenEvidence covers the tokens declaration with exact recorded usage.
func TestTokenEvidence(t *testing.T) {
	r := New("testdata")
	adaptertest.AssertTokens(t, r, adaptertest.TokenExpect{
		SessionID:             fixtureBlocked,
		ExactPrompt:           adaptertest.Int64(12400),
		ExactCompletion:       adaptertest.Int64(1600),
		PresentInput:          model.PresenceExact,
		PresentOutput:         model.PresenceExact,
		RequireExactPrecision: true,
		RequireNonNilBilling:  true,
	})
}

// TestToolResultEvidence covers the tool_results declaration: invocation and
// result pair by ToolCallID with both success and failure evidence.
func TestToolResultEvidence(t *testing.T) {
	r := New("testdata")
	adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
		SessionID:      fixtureBlocked,
		MinPairs:       5,
		RequireSuccess: true,
	})
	// Failure evidence comes from the error fixture's failed stage.
	adaptertest.AssertToolResults(t, r, adaptertest.ToolResultsExpect{
		SessionID:      fixtureError,
		MinPairs:       2,
		RequireFailure: true,
	})
}

// TestProvenanceComplete covers the provenance contract for a full journal.
func TestProvenanceComplete(t *testing.T) {
	detail, err := New("testdata").GetSession(fixturePassed)
	if err != nil {
		t.Fatal(err)
	}
	adaptertest.AssertProvenanceComplete(t, detail, Capabilities())
}

// TestProvenanceDegraded covers the corrupt journal: body exists but events
// were skipped, so the record is degraded with explicit warnings.
func TestProvenanceDegraded(t *testing.T) {
	detail, err := New("testdata").GetSession(fixtureCorrupt)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Provenance == nil {
		t.Fatal("provenance missing")
	}
	if detail.Provenance.State != "degraded" {
		t.Fatalf("state=%q want degraded", detail.Provenance.State)
	}
}
