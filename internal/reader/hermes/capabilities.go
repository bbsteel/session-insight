package hermes

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// Capabilities declares only facts backed by the Hermes state store and the
// adapter's executable conformance fixtures. Subtasks remain estimated: the
// parent_session_id/_delegate_from lineage is readable, but Hermes does not
// persist a complete invocation graph with per-child timing/result semantics.
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       agentType,
		DisplayName:     displayName,
		AdapterRevision: 1,
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.Estimated("partial_lineage"),
			capability.CapabilityResume:      capability.Exact(),
			capability.CapabilityDelete:      capability.Exact(),
			capability.CapabilityTerminate:   capability.Unsupported("exact_pid_unavailable"),
		},
	}
}

// ResolveDBPath follows Hermes' supported HERMES_HOME override and its
// platform default. HERMES_STATE_DB/HERMES_DB are accepted as explicit test
// and packaging overrides without changing the normal discovery path.
func ResolveDBPath() (string, bool) {
	for _, key := range []string{"HERMES_STATE_DB", "HERMES_DB"} {
		if path := os.Getenv(key); path != "" {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, true
			}
		}
	}

	home := hermesHome()
	path := filepath.Join(home, "state.db")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path, true
	}
	return "", false
}

func hermesHome() string {
	if path := os.Getenv("HERMES_HOME"); path != "" {
		return path
	}
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "hermes")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hermes"
	}
	return filepath.Join(home, ".hermes")
}
