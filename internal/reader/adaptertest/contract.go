package adaptertest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// RunContract validates the static capability declaration and its agreement
// with optional Go interfaces implemented by r. It performs no fixture I/O.
func RunContract(t *testing.T, caps capability.AgentCapabilities, r Reader) {
	t.Helper()
	t.Run("contract/validate_static", func(t *testing.T) {
		if errs := capability.ValidateStatic(caps); len(errs) != 0 {
			t.Fatalf("ValidateStatic failed: %v", errs)
		}
	})
	t.Run("contract/identity", func(t *testing.T) {
		if r.AgentType() != caps.AgentType {
			t.Fatalf("reader AgentType %q != declaration %q", r.AgentType(), caps.AgentType)
		}
		if r.DisplayName() != caps.DisplayName {
			t.Fatalf("reader DisplayName %q != declaration %q", r.DisplayName(), caps.DisplayName)
		}
		if strings.TrimSpace(r.AgentType()) == "" {
			t.Fatal("AgentType is empty")
		}
		if strings.TrimSpace(r.DisplayName()) == "" {
			t.Fatal("DisplayName is empty")
		}
	})
	t.Run("contract/discovery_replay_surface", func(t *testing.T) {
		// discovery and replay require the shared Reader surface; presence of
		// the methods is structural. Require they are declared exact or at
		// least not unsupported without reason (ValidateStatic already covers
		// reason codes). Soft check: exact/estimated implies list/detail work
		// is expected — exercised fully by RunBasicBehavior.
		for _, id := range []capability.CapabilityID{
			capability.CapabilityDiscovery,
			capability.CapabilityReplay,
		} {
			decl, ok := caps.Capabilities[id]
			if !ok {
				t.Fatalf("missing capability %s", id)
			}
			if decl.State == capability.CapabilityUnsupported {
				t.Fatalf("%s is unsupported: every registered Agent must support discovery and replay at the reader surface", id)
			}
		}
	})
	t.Run("contract/operation_interfaces", func(t *testing.T) {
		if err := CheckOperationInterfaces(caps, r); err != nil {
			t.Fatal(err)
		}
	})
}

// CheckOperationInterfaces returns a deterministic error when an exact/estimated
// operation declaration lacks the matching optional Go interface. Exported for
// package self-tests that assert negative paths without failing the parent test.
func CheckOperationInterfaces(caps capability.AgentCapabilities, r Reader) error {
	rt := caps.Capabilities[capability.CapabilityRealtime]
	switch rt.State {
	case capability.CapabilityExact, capability.CapabilityEstimated:
		if _, ok := r.(LiveRevisionProvider); !ok {
			return fmt.Errorf("realtime=%s requires LiveRevision(id) (int64, error); reader %T does not implement it",
				rt.State, r)
		}
	}

	del := caps.Capabilities[capability.CapabilityDelete]
	if del.State == capability.CapabilityExact {
		if _, ok := r.(SessionDeleter); !ok {
			return fmt.Errorf("delete=exact requires DeleteSession(id) error; reader %T does not implement it", r)
		}
	}

	term := caps.Capabilities[capability.CapabilityTerminate]
	if term.State == capability.CapabilityExact {
		if _, ok := r.(SessionProcessFinder); !ok {
			return fmt.Errorf("terminate=exact requires SessionProcesses(id) ([]int, error); reader %T does not implement it", r)
		}
	}
	if term.State == capability.CapabilityEstimated {
		return fmt.Errorf("terminate=estimated is forbidden (got reason %q)", term.ReasonCode)
	}
	return nil
}

// Config is the Phase 2 entry for shared contract + basic behavior checks.
//
// NewReader must construct a Reader against a test-owned fixture (TempDir or
// test SQLite). It must not open the developer's real home agent roots.
//
// Layer 3 capability suites are intentionally not part of Config yet; add
// optional fields later without breaking existing adapter tests.
type Config struct {
	// Capabilities is the adapter-owned static declaration (e.g. Capabilities()).
	Capabilities capability.AgentCapabilities
	// NewReader builds a reader bound to a sanitized, test-owned basic fixture.
	NewReader func(t *testing.T) Reader
	// Expect describes list identity for the fixture NewReader provides.
	Expect Expectations
}

// Run executes Layer 1 contract checks and Layer 2 basic behavior checks.
// It does not claim full capability evidence (Layer 3).
func Run(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.NewReader == nil {
		t.Fatal("adaptertest.Config.NewReader is required")
	}
	r := cfg.NewReader(t)
	if r == nil {
		t.Fatal("NewReader returned nil Reader")
	}
	RunContract(t, cfg.Capabilities, r)
	RunBasicBehavior(t, r, cfg.Expect)
}

// FormatContractError is used by self-tests to assert deterministic messages.
func FormatContractError(field, message string) string {
	return fmt.Sprintf("%s: %s", field, message)
}
