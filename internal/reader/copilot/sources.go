package copilot

import (
	"os"
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists GitHub Copilot CLI files this adapter cares about.
// Layout (under session-state/<sessionID>/):
//
//	workspace.yaml     metadata (cwd, title, timestamps)
//	events.jsonl       primary transcript / event stream
//	session.db         local sqlite (todos and related) when present
//	session.db-wal/shm sqlite sidecars when present
//
// Collaboration is embedded in events.jsonl (no separate child files).
// Global session-store.db is shared and not listed per session.
func sourceInventory(sessionDir, sessionID string) []model.SessionSourceFile {
	dir := filepath.Join(sessionDir, sessionID)
	var sources []model.SessionSourceFile
	seen := map[string]struct{}{}
	add := func(role, path string, required bool) {
		path = filepath.Clean(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) && !required {
				return
			}
			seen[path] = struct{}{}
			sources = append(sources, provenance.StatSource(role, path))
			return
		}
		if !info.Mode().IsRegular() {
			return
		}
		seen[path] = struct{}{}
		sources = append(sources, provenance.StatSource(role, path))
	}

	add(model.SourceRoleMetadata, filepath.Join(dir, "workspace.yaml"), true)
	add(model.SourceRolePrimaryTranscript, filepath.Join(dir, "events.jsonl"), true)
	add(model.SourceRoleToolResults, filepath.Join(dir, "session.db"), false)
	// WAL/SHM are operational sidecars of the same sqlite store.
	for _, name := range []string{"session.db-wal", "session.db-shm"} {
		add(model.SourceRoleToolResults, filepath.Join(dir, name), false)
	}

	return sources
}
