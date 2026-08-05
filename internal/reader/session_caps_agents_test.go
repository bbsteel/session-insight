package reader

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/chrys"
	"github.com/bbsteel/session-insight/internal/reader/claude"
	"github.com/bbsteel/session-insight/internal/reader/codex"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
	"github.com/bbsteel/session-insight/internal/reader/grok"
	"github.com/bbsteel/session-insight/internal/reader/hermes"
	"github.com/bbsteel/session-insight/internal/reader/opencode"
)

// Representative per-Agent resolution using each adapter's real static
// Capabilities() declaration (Phase 1/3 catalog), without live storage.
func TestResolveSevenAgentsCatalogIntegration(t *testing.T) {
	type agentCase struct {
		name   string
		static capability.AgentCapabilities
		// unsupportedCaps must remain unsupported after resolve.
		unsupportedCaps map[capability.CapabilityID]string
	}
	cases := []agentCase{
		{name: "claude", static: claude.Capabilities()},
		{name: "codex", static: codex.Capabilities()},
		{
			name:   "copilot",
			static: copilot.Capabilities(),
			unsupportedCaps: map[capability.CapabilityID]string{
				capability.CapabilityResume: copilot.Capabilities().Capabilities[capability.CapabilityResume].ReasonCode,
			},
		},
		{
			name:   "chrys",
			static: chrys.Capabilities(),
			unsupportedCaps: map[capability.CapabilityID]string{
				capability.CapabilityTerminate: chrys.Capabilities().Capabilities[capability.CapabilityTerminate].ReasonCode,
			},
		},
		{
			name:   "opencode",
			static: opencode.Capabilities(),
			unsupportedCaps: map[capability.CapabilityID]string{
				capability.CapabilityTerminate: opencode.Capabilities().Capabilities[capability.CapabilityTerminate].ReasonCode,
			},
		},
		{
			name:   "grok",
			static: grok.Capabilities(),
			// subtasks is exact once collaboration recognition is implemented.
			unsupportedCaps: map[capability.CapabilityID]string{},
		},
		{
			name:   "hermes",
			static: hermes.Capabilities(),
			unsupportedCaps: map[capability.CapabilityID]string{
				capability.CapabilityTerminate: hermes.Capabilities().Capabilities[capability.CapabilityTerminate].ReasonCode,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/complete-exact-tokens", func(t *testing.T) {
			// Snapshot static map before resolve so mutation is detectable.
			before := snapshotCaps(tc.static.Capabilities)
			d := detailBase(tc.static.AgentType, "sess-complete")
			d.ResumeID = "native-resume-" + tc.name
			d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
			d.Turns = []model.TurnVM{{UserMessage: "hi", ToolCallCount: 0}} // zero tools still exact
			src := &capsFakeReader{agentType: tc.static.AgentType, live: boolPtr(false), hasRev: true}
			got, err := ResolveSessionCapabilities(src, d, tc.static)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Status) != 10 {
				t.Fatalf("status=%d", len(got.Status))
			}
			if got.Status[capability.CapabilityTokens].State != capability.CapabilityExact {
				t.Errorf("tokens=%+v", got.Status[capability.CapabilityTokens])
			}
			if got.Status[capability.CapabilityToolResults].State != capability.CapabilityExact {
				t.Errorf("zero tools must stay exact: %+v", got.Status[capability.CapabilityToolResults])
			}
			if got.Status[capability.CapabilityDiff].State != capability.CapabilityExact {
				t.Errorf("zero diffs must stay exact: %+v", got.Status[capability.CapabilityDiff])
			}
			// Resume: unsupported agents stay unsupported; others exact with ID.
			if _, uns := tc.unsupportedCaps[capability.CapabilityResume]; uns {
				if got.Status[capability.CapabilityResume].State != capability.CapabilityUnsupported {
					t.Errorf("resume should stay unsupported: %+v", got.Status[capability.CapabilityResume])
				}
			} else if got.Status[capability.CapabilityResume].State != capability.CapabilityExact {
				t.Errorf("resume=%+v", got.Status[capability.CapabilityResume])
			}
			for id, reason := range tc.unsupportedCaps {
				st := got.Status[id]
				if st.State != capability.CapabilityUnsupported {
					t.Errorf("%s state=%s want unsupported", id, st.State)
				}
				if reason != "" && st.ReasonCode != reason {
					t.Errorf("%s reason=%q want static %q", id, st.ReasonCode, reason)
				}
				if st.ReasonCode == "" {
					t.Errorf("%s missing reason", id)
				}
			}
			after := snapshotCaps(tc.static.Capabilities)
			if !capsEqual(before, after) {
				t.Fatalf("static Capabilities map mutated during resolve for %s", tc.name)
			}
		})

		t.Run(tc.name+"/missing-tokens", func(t *testing.T) {
			d := detailBase(tc.static.AgentType, "sess-missing-tokens")
			d.ResumeID = "r"
			d.Billing = &model.SessionBilling{Precision: model.PrecisionMissing}
			src := &capsFakeReader{agentType: tc.static.AgentType, live: boolPtr(false), hasRev: true}
			got, err := ResolveSessionCapabilities(src, d, tc.static)
			if err != nil {
				t.Fatal(err)
			}
			st := got.Status[capability.CapabilityTokens]
			// Only if static tokens is exact/estimated can we downgrade to missing.
			switch tc.static.Capabilities[capability.CapabilityTokens].State {
			case capability.CapabilityExact, capability.CapabilityEstimated:
				if st.State != capability.CapabilityMissing || st.ReasonCode != capability.ReasonSessionNotFinalized {
					t.Fatalf("tokens=%+v", st)
				}
			}
		})
	}
}

func TestResolveCopilotResumeStaysUnsupportedEvenWithEmptyResumeID(t *testing.T) {
	static := copilot.Capabilities()
	d := detailBase("copilot", "c1")
	d.ResumeID = ""
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "copilot", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilityResume].State != capability.CapabilityUnsupported {
		t.Fatalf("%+v", got.Status[capability.CapabilityResume])
	}
	// Must not become missing/resume_id_missing.
	if got.Status[capability.CapabilityResume].ReasonCode == capability.ReasonResumeIDMissing {
		t.Fatal("unsupported must not become resume_id_missing")
	}
}

func TestResolveGrokSubtasksExact(t *testing.T) {
	static := grok.Capabilities()
	d := detailBase("grok", "g1")
	d.ResumeID = "g1"
	d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
	src := &capsFakeReader{agentType: "grok", live: boolPtr(false), hasRev: true}
	got, err := ResolveSessionCapabilities(src, d, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status[capability.CapabilitySubtasks].State != capability.CapabilityExact {
		t.Fatalf("%+v", got.Status[capability.CapabilitySubtasks])
	}
	if static.Capabilities[capability.CapabilitySubtasks].State != capability.CapabilityExact {
		t.Fatalf("static declaration must be exact: %+v", static.Capabilities[capability.CapabilitySubtasks])
	}
}

func TestResolveChrysOpenCodeTerminateUnsupported(t *testing.T) {
	for _, static := range []capability.AgentCapabilities{chrys.Capabilities(), opencode.Capabilities()} {
		d := detailBase(static.AgentType, "t1")
		d.ResumeID = "t1"
		d.Billing = &model.SessionBilling{Precision: model.PrecisionExact}
		d.UpdatedAt = time.Now() // appears live
		src := &capsFakeReader{agentType: static.AgentType, live: boolPtr(true), hasRev: true}
		got, err := ResolveSessionCapabilities(src, d, static)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status[capability.CapabilityTerminate].State != capability.CapabilityUnsupported {
			t.Errorf("%s terminate=%+v", static.AgentType, got.Status[capability.CapabilityTerminate])
		}
		act := got.Actions[capability.CapabilityTerminate]
		if act.Availability != capability.ActionUnavailable {
			t.Errorf("%s terminate action=%+v", static.AgentType, act)
		}
	}
}

func snapshotCaps(in map[capability.CapabilityID]capability.CapabilityDeclaration) map[capability.CapabilityID]capability.CapabilityDeclaration {
	out := make(map[capability.CapabilityID]capability.CapabilityDeclaration, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func capsEqual(a, b map[capability.CapabilityID]capability.CapabilityDeclaration) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va != vb {
			return false
		}
	}
	return true
}
