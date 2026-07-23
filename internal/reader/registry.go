package reader

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/chrys"
	"github.com/bbsteel/session-insight/internal/reader/claude"
	"github.com/bbsteel/session-insight/internal/reader/codex"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
	"github.com/bbsteel/session-insight/internal/reader/grok"
	"github.com/bbsteel/session-insight/internal/reader/opencode"
)

// AgentDefinitions returns the static capability catalog for every supported
// Agent. The catalog is always available, even when an Agent's local storage
// is not installed or discovered.
//
// Declarations are owned by each adapter package; this function only
// aggregates exports and never re-states capability values.
// Order is stable: sorted by AgentType ascending.
func AgentDefinitions() []capability.AgentCapabilities {
	defs := []capability.AgentCapabilities{
		claude.Capabilities(),
		chrys.Capabilities(),
		codex.Capabilities(),
		copilot.Capabilities(),
		grok.Capabilities(),
		opencode.Capabilities(),
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].AgentType < defs[j].AgentType
	})
	return defs
}

// AgentDefinition returns the static declaration for agentType, if known.
func AgentDefinition(agentType string) (capability.AgentCapabilities, bool) {
	for _, d := range AgentDefinitions() {
		if d.AgentType == agentType {
			return d, true
		}
	}
	return capability.AgentCapabilities{}, false
}

// Discover returns BaseSessionReader instances for Agents whose storage exists
// on the current machine. It is independent of AgentDefinitions: an Agent may
// appear in the catalog without a discovered reader, and a discovered reader
// always has a matching catalog entry.
func Discover() []BaseSessionReader {
	var readers []BaseSessionReader

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
