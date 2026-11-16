package dsh

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/presentation"
	"github.com/bbsteel/session-insight/internal/reader/capability"
)

// Capabilities declares only behavior exercised against persisted v3/v4 logs.
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType: agentType, DisplayName: displayName, AdapterRevision: 2,
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.Unsupported("adapter_not_implemented"),
			capability.CapabilityResume:      capability.Unsupported("adapter_not_implemented"),
			capability.CapabilityDelete:      capability.Unsupported("adapter_not_implemented"),
			capability.CapabilityTerminate:   capability.Unsupported("exact_pid_unavailable"),
		},
	}
}

func Presentation() presentation.Declaration                  { return presentation.NativeNeutralDeclaration(agentType) }
func PresentationMigrationState() presentation.MigrationState { return "" }

// ResolveSessionsDir follows the cross-platform dsh-home-paths contract.
func ResolveSessionsDir() (string, bool) {
	homeRoot := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if homeRoot == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		homeRoot = filepath.Join(userHome, ".dsh")
	} else if homeRoot == "~" || strings.HasPrefix(homeRoot, "~/") || strings.HasPrefix(homeRoot, "~\\") {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		homeRoot = filepath.Join(userHome, strings.TrimLeft(homeRoot[1:], "/\\"))
	}
	sessionsRoot := filepath.Join(homeRoot, "sessions")
	info, err := os.Stat(sessionsRoot)
	return sessionsRoot, err == nil && info.IsDir()
}
