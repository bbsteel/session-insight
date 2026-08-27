package reader

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/bbsteel/session-insight/internal/presentation"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/chrys"
	"github.com/bbsteel/session-insight/internal/reader/claude"
	"github.com/bbsteel/session-insight/internal/reader/codex"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
	"github.com/bbsteel/session-insight/internal/reader/grok"
	"github.com/bbsteel/session-insight/internal/reader/hermes"
	"github.com/bbsteel/session-insight/internal/reader/imported"
	"github.com/bbsteel/session-insight/internal/reader/opencode"
)

// RegisteredAgentDefinition aggregates adapter-owned capability and
// presentation declarations. The registry copies exports; it does not restate
// field values.
type RegisteredAgentDefinition struct {
	Capabilities   capability.AgentCapabilities
	Presentation   presentation.Declaration
	MigrationState presentation.MigrationState
}

// RegisteredAgentDefinitions returns the static capability+presentation
// catalog for every supported Agent. Order is stable: sorted by AgentType.
func RegisteredAgentDefinitions() []RegisteredAgentDefinition {
	defs := []RegisteredAgentDefinition{
		{Capabilities: claude.Capabilities(), Presentation: claude.Presentation(), MigrationState: claude.PresentationMigrationState()},
		{Capabilities: chrys.Capabilities(), Presentation: chrys.Presentation(), MigrationState: chrys.PresentationMigrationState()},
		{Capabilities: codex.Capabilities(), Presentation: codex.Presentation(), MigrationState: codex.PresentationMigrationState()},
		{Capabilities: copilot.Capabilities(), Presentation: copilot.Presentation(), MigrationState: copilot.PresentationMigrationState()},
		{Capabilities: grok.Capabilities(), Presentation: grok.Presentation(), MigrationState: grok.PresentationMigrationState()},
		{Capabilities: hermes.Capabilities(), Presentation: hermes.Presentation(), MigrationState: hermes.PresentationMigrationState()},
		{Capabilities: imported.Capabilities(), Presentation: imported.Presentation(), MigrationState: imported.PresentationMigrationState()},
		{Capabilities: opencode.Capabilities(), Presentation: opencode.Presentation(), MigrationState: opencode.PresentationMigrationState()},
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Capabilities.AgentType < defs[j].Capabilities.AgentType
	})
	return defs
}

// RegisteredAgentDefinitionByType returns the aggregated declaration for
// agentType, if known.
func RegisteredAgentDefinitionByType(agentType string) (RegisteredAgentDefinition, bool) {
	for _, d := range RegisteredAgentDefinitions() {
		if d.Capabilities.AgentType == agentType {
			return d, true
		}
	}
	return RegisteredAgentDefinition{}, false
}

// AgentDefinitions returns the capability-only catalog. It is implemented as
// a projection of RegisteredAgentDefinitions so callers that have not yet
// migrated keep a stable signature.
func AgentDefinitions() []capability.AgentCapabilities {
	registered := RegisteredAgentDefinitions()
	defs := make([]capability.AgentCapabilities, len(registered))
	for i, d := range registered {
		defs[i] = d.Capabilities
	}
	return defs
}

// AgentDefinition returns the static capability declaration for agentType, if known.
func AgentDefinition(agentType string) (capability.AgentCapabilities, bool) {
	d, ok := RegisteredAgentDefinitionByType(agentType)
	if !ok {
		return capability.AgentCapabilities{}, false
	}
	return d.Capabilities, true
}

// UsesDeclarationResolver reports whether production ANSI/positions should
// compile this Agent's presentation declaration. Slice B keeps every Agent
// on the current profileFor path; Slice F flips Agents one at a time after
// evidence, fixtures, and visual review land.
func UsesDeclarationResolver(agentType string) bool {
	switch agentType {
	// Slice F adds cases as each Agent leaves legacy_unverified.
	default:
		return false
	}
}

// Discover returns BaseSessionReader instances for Agents whose storage exists
// on the current machine. It is independent of AgentDefinitions: an Agent may
// appear in the catalog without a discovered reader, and a discovered reader
// always has a matching catalog entry.
func Discover() []BaseSessionReader {
	var readers []BaseSessionReader

	hermesDBPath, ok := hermes.ResolveDBPath()
	if ok {
		reader, err := hermes.New(hermesDBPath)
		if err != nil {
			log.Printf("hermes reader init failed: %v", err)
		} else {
			readers = append(readers, reader)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return readers
	}

	copilotDir := filepath.Join(homeDir, ".copilot", "session-state")
	if info, err := os.Stat(copilotDir); err == nil && info.IsDir() {
		readers = append(readers, copilot.New(copilotDir))
	}

	codexDir := filepath.Join(homeDir, ".codex", "sessions")
	if info, err := os.Stat(codexDir); err == nil && info.IsDir() {
		readers = append(readers, codex.New(codexDir))
	}

	claudeDir := filepath.Join(homeDir, ".claude", "projects")
	if info, err := os.Stat(claudeDir); err == nil && info.IsDir() {
		readers = append(readers, claude.New(claudeDir))
	}

	chrysDirs := []string{}
	if envRoot := os.Getenv("CHRYS_SESSION_ROOT_DIR"); envRoot != "" {
		chrysDirs = append(chrysDirs, filepath.Join(envRoot, "sessions"))
	}
	chrysDirs = append(chrysDirs, filepath.Join(homeDir, ".chrys", "sessions"))
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			chrysDirs = append(chrysDirs, filepath.Join(appData, "chrys", "sessions"))
		}
	}
	for _, chrysDir := range chrysDirs {
		if info, err := os.Stat(chrysDir); err == nil && info.IsDir() {
			readers = append(readers, chrys.New(chrysDir))
			break
		}
	}

	dbPath, ok := opencode.ResolveDBPath()
	if ok {
		reader, err := opencode.New(dbPath)
		if err != nil {
			log.Printf("openCode reader init failed: %v", err)
		} else {
			readers = append(readers, reader)
		}
	}

	grokDir := filepath.Join(homeDir, ".grok", "sessions")
	if info, err := os.Stat(grokDir); err == nil && info.IsDir() {
		readers = append(readers, grok.New(grokDir))
	}

	return readers
}
