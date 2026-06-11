package reader

import (
	"os"
	"path/filepath"

	"session-insight/internal/reader/copilot"
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

	return readers
}
