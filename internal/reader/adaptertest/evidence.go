package adaptertest

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// EvidenceCase is one executable proof that one or more capabilities work on a
// test-owned fixture. Assert must call the real reader; empty Assert is rejected.
type EvidenceCase struct {
	// Scenario is a stable short name (e.g. "tokens-basic", "delete-sandbox").
	Scenario string
	// Synthetic is true when the fixture is fully synthetic.
	Synthetic bool
	// Sanitized must be true. Unsanitized cases fail the coverage gate.
	Sanitized bool
	// Platforms lists platforms where this evidence is valid. Empty = portable.
	// Linux-only process tests should set []string{"linux"}.
	Platforms []string
	// Covers lists capability IDs this case proves when Assert passes.
	Covers []capability.CapabilityID
	// NewReader builds a reader bound to a disposable fixture for this case.
	NewReader func(t *testing.T) Reader
	// Assert runs semantic checks against the live reader.
	Assert func(t *testing.T, r Reader)
}

// CoverageOptions controls which capabilities Phase 2 basic conformance may
// satisfy without a Layer-3 evidence case.
type CoverageOptions struct {
	// BasicSatisfied is typically discovery + replay after adaptertest.Run.
	BasicSatisfied []capability.CapabilityID
}

// EvidenceResult is one cell of the generated evidence matrix.
type EvidenceResult struct {
	AgentType   string
	Capability  capability.CapabilityID
	Declaration capability.CapabilityState
	ReasonCode  string
	Scenario    string
	Result      string // "pass", "fail", "n/a"
	Platforms   string
	Synthetic   bool
	Sanitized   bool
}

// CheckCoverage returns a deterministic error when any exact/estimated
// capability lacks registered executable evidence (except opts.BasicSatisfied).
func CheckCoverage(decl capability.AgentCapabilities, cases []EvidenceCase, opts CoverageOptions) error {
	if errs := capability.ValidateStatic(decl); len(errs) != 0 {
		return fmt.Errorf("declaration invalid: %v", errs)
	}

	basic := map[capability.CapabilityID]bool{}
	for _, id := range opts.BasicSatisfied {
		basic[id] = true
	}

	covered := map[capability.CapabilityID][]string{}
	seenScenarioCap := map[string]bool{}
	for i, c := range cases {
		if strings.TrimSpace(c.Scenario) == "" {
			return fmt.Errorf("evidence case %d: empty Scenario", i)
		}
		if !c.Sanitized {
			return fmt.Errorf("evidence case %q: Sanitized must be true", c.Scenario)
		}
		if c.NewReader == nil {
			return fmt.Errorf("evidence case %q: NewReader is required", c.Scenario)
		}
		if c.Assert == nil {
			return fmt.Errorf("evidence case %q: Assert is required (no empty evidence)", c.Scenario)
		}
		if len(c.Covers) == 0 {
			return fmt.Errorf("evidence case %q: Covers is empty", c.Scenario)
		}
		for _, id := range c.Covers {
			if !isBaseline(id) {
				return fmt.Errorf("evidence case %q: unknown capability %q", c.Scenario, id)
			}
			key := c.Scenario + "\x00" + string(id)
			if seenScenarioCap[key] {
				return fmt.Errorf("duplicate evidence for scenario %q capability %s", c.Scenario, id)
			}
			seenScenarioCap[key] = true
			covered[id] = append(covered[id], c.Scenario)
		}
	}

	var missing []string
	for _, id := range capability.BaselineIDs() {
		declCap, ok := decl.Capabilities[id]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s: not in declaration", id))
			continue
		}
		switch declCap.State {
		case capability.CapabilityExact, capability.CapabilityEstimated:
			if basic[id] {
				continue
			}
			if len(covered[id]) == 0 {
				missing = append(missing, fmt.Sprintf("%s=%s has no registered evidence", id, declCap.State))
			}
		case capability.CapabilityMissing:
			missing = append(missing, fmt.Sprintf("%s: static missing forbidden", id))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("coverage gate failed for agent %q:\n  - %s", decl.AgentType, strings.Join(missing, "\n  - "))
	}
	return nil
}

func isBaseline(id capability.CapabilityID) bool {
	for _, b := range capability.BaselineIDs() {
		if b == id {
			return true
		}
	}
	return false
}

// RunCapabilityEvidence runs the coverage gate then every evidence case.
// Results are logged as a TSV matrix (visible with -v).
func RunCapabilityEvidence(t *testing.T, decl capability.AgentCapabilities, cases []EvidenceCase, opts CoverageOptions) []EvidenceResult {
	t.Helper()
	return runEvidenceCases(t, decl, cases, opts)
}

func runEvidenceCases(t *testing.T, decl capability.AgentCapabilities, cases []EvidenceCase, opts CoverageOptions) []EvidenceResult {
	t.Helper()

	t.Run("coverage_gate", func(t *testing.T) {
		if err := CheckCoverage(decl, cases, opts); err != nil {
			t.Fatal(err)
		}
	})

	var results []EvidenceResult
	for _, id := range capability.BaselineIDs() {
		d := decl.Capabilities[id]
		if d.State == capability.CapabilityUnsupported || d.State == capability.CapabilityNotApplicable {
			results = append(results, EvidenceResult{
				AgentType: decl.AgentType, Capability: id, Declaration: d.State,
				ReasonCode: d.ReasonCode, Scenario: d.ReasonCode, Result: "n/a",
			})
		}
	}
	for _, id := range opts.BasicSatisfied {
		d := decl.Capabilities[id]
		if d.State == capability.CapabilityExact || d.State == capability.CapabilityEstimated {
			results = append(results, EvidenceResult{
				AgentType: decl.AgentType, Capability: id, Declaration: d.State,
				ReasonCode: d.ReasonCode, Scenario: "basic-conformance", Result: "pass",
				Sanitized: true, Synthetic: true,
			})
		}
	}

	for _, c := range cases {
		c := c
		ok := t.Run("evidence/"+c.Scenario, func(t *testing.T) {
			r := c.NewReader(t)
			if r == nil {
				t.Fatal("NewReader returned nil")
			}
			if r.AgentType() != decl.AgentType {
				t.Fatalf("reader AgentType %q != declaration %q", r.AgentType(), decl.AgentType)
			}
			c.Assert(t, r)
		})
		result := "pass"
		if !ok {
			result = "fail"
		}
		plats := strings.Join(c.Platforms, ",")
		for _, id := range c.Covers {
			d := decl.Capabilities[id]
			results = append(results, EvidenceResult{
				AgentType: decl.AgentType, Capability: id, Declaration: d.State,
				ReasonCode: d.ReasonCode, Scenario: c.Scenario, Result: result,
				Platforms: plats, Synthetic: c.Synthetic, Sanitized: c.Sanitized,
			})
		}
	}

	hasPass := map[capability.CapabilityID]bool{}
	for _, row := range results {
		if row.Result == "pass" {
			hasPass[row.Capability] = true
		}
	}
	for _, id := range capability.BaselineIDs() {
		d := decl.Capabilities[id]
		if d.State != capability.CapabilityExact && d.State != capability.CapabilityEstimated {
			continue
		}
		if !hasPass[id] {
			t.Errorf("capability %s=%s ended without a passing evidence row", id, d.State)
		}
	}

	LogMatrix(t, results)
	return results
}

// LogMatrix prints a TSV evidence matrix for -v inspection.
func LogMatrix(t *testing.T, rows []EvidenceResult) {
	t.Helper()
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].AgentType != rows[j].AgentType {
			return rows[i].AgentType < rows[j].AgentType
		}
		if rows[i].Capability != rows[j].Capability {
			return rows[i].Capability < rows[j].Capability
		}
		return rows[i].Scenario < rows[j].Scenario
	})
	var b strings.Builder
	b.WriteString("agent\tcapability\tdeclaration\treason\tscenario\tresult\tplatforms\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.AgentType, r.Capability, r.Declaration, r.ReasonCode, r.Scenario, r.Result, r.Platforms)
	}
	t.Log("\n" + b.String())
}

// DefaultBasicSatisfied is discovery + replay (covered by Phase 2 Run).
func DefaultBasicSatisfied() []capability.CapabilityID {
	return []capability.CapabilityID{
		capability.CapabilityDiscovery,
		capability.CapabilityReplay,
	}
}

// FullConfig extends Phase 2 Config with Layer-3 evidence cases.
type FullConfig struct {
	Config
	Evidence []EvidenceCase
}

// RunFull runs Phase 2 contract+basic behavior then Layer-3 evidence.
// Existing Run call sites remain unchanged.
func RunFull(t *testing.T, cfg FullConfig) []EvidenceResult {
	t.Helper()
	Run(t, cfg.Config)
	return runEvidenceCases(t, cfg.Capabilities, cfg.Evidence, CoverageOptions{
		BasicSatisfied: DefaultBasicSatisfied(),
	})
}
