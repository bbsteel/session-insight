package insight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/analytics"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
)

// TestGoldenBundleEndToEnd is the deterministic half of the golden-sample
// acceptance: the real Copilot session 27e7dd07 must flow through the reader ->
// findings -> evidence bundle pipeline with its structured subagent evidence
// intact, so a model could reach the review-fix-cascade conclusion. It skips
// when the session is not present (it is not committed). The model-output half
// is a semantic rubric, evaluated separately, not a brittle snapshot here.
func TestGoldenBundleEndToEnd(t *testing.T) {
	home, _ := os.UserHomeDir()
	sessionDir := filepath.Join(home, ".copilot", "session-state")
	golden := "27e7dd07-40e7-4728-9bc8-c70ee77f4d04"
	if _, err := os.Stat(filepath.Join(sessionDir, golden, "events.jsonl")); err != nil {
		t.Skip("golden session not present on this machine")
	}

	rd := copilot.New(sessionDir)
	detail, err := rd.GetSession(golden)
	if err != nil {
		t.Fatal(err)
	}
	res := analytics.Compute(detail)
	ev, _, err := rd.GetInsightEvidence(golden, 0)
	if err != nil {
		t.Fatal(err)
	}
	bundle := BuildBundle(detail, res, ev)

	// The subagent fan-out finding must fire with the true count.
	var fanout *FindingBrief
	for i := range bundle.Findings {
		if bundle.Findings[i].Code == analytics.CodeSubagentFanout {
			fanout = &bundle.Findings[i]
		}
	}
	if fanout == nil {
		t.Fatal("subagent_fanout finding missing on golden session")
	}
	if got := fanout.Metrics["subagent_count"]; got != 27 {
		t.Errorf("subagent_count = %v, want 27", got)
	}

	// The bundle must carry all 27 subagent facts with their delegation
	// descriptions preserved (the raw signal the model classifies into
	// implement/review/fix roles — the reader must not pre-classify).
	subFacts := 0
	var reqs, sync int
	var tokens int64
	roles := map[string]int{}
	for _, f := range bundle.Facts {
		if f.Kind != "subagent" {
			continue
		}
		subFacts++
		if v, ok := f.Values["request_count"].(int); ok {
			reqs += v
		}
		if v, ok := f.Values["output_tokens"].(int64); ok {
			tokens += v
		}
		if f.Values["mode"] == "sync" {
			sync++
		}
		switch {
		case strings.Contains(f.Statement, "Implement"):
			roles["implement"]++
		case strings.Contains(f.Statement, "Fix"):
			roles["fix"]++
		case strings.Contains(f.Statement, "Review") || strings.Contains(f.Statement, "Approve") || strings.Contains(f.Statement, "review"):
			roles["review"]++
		}
	}
	if subFacts != 27 {
		t.Errorf("bundle subagent facts = %d, want 27", subFacts)
	}
	if reqs != 277 {
		t.Errorf("attributed responses in bundle = %d, want 277", reqs)
	}
	if tokens != 177449 {
		t.Errorf("subagent output tokens in bundle = %d, want 177449", tokens)
	}
	if sync != 22 {
		t.Errorf("sync delegations in bundle = %d, want 22", sync)
	}
	// The review-fix cascade must be legible from descriptions: implements are
	// few, reviews dominate. Exact role counts are the model's semantic call;
	// here we only assert the signal survived and is skewed toward review.
	if roles["implement"] == 0 || roles["review"] <= roles["implement"] {
		t.Errorf("delegation role signal not preserved: %v", roles)
	}

	// Referential integrity end to end: every session/turn finding ref resolves.
	factIDs := map[string]bool{}
	for _, f := range bundle.Facts {
		factIDs[f.ID] = true
	}
	for _, fb := range bundle.Findings {
		for _, ref := range fb.EvidenceRefs {
			if (ref.Kind == "session" || ref.Kind == "turn") && !factIDs[ref.ID] {
				t.Errorf("finding %s references unresolved %s", fb.Code, ref.ID)
			}
		}
	}
}
