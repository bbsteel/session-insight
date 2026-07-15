package reader

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bbsteel/session-insight/internal/reader/chrys"
	"github.com/bbsteel/session-insight/internal/reader/claude"
	"github.com/bbsteel/session-insight/internal/reader/codex"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
	"github.com/bbsteel/session-insight/internal/reader/grok"
	"github.com/bbsteel/session-insight/internal/reader/opencode"
)

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
