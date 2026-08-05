package opencode

import (
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists OpenCode files this adapter cares about.
//
// OpenCode keeps every session’s messages/parts/todos in one shared SQLite DB:
//
//	~/.local/share/opencode/opencode.db
//
// There is no per-session transcript file. The db is the primary *store* SI
// reads for body (role primary_transcript = main source for this snapshot).
//
// We deliberately do NOT list opencode.db-wal / -shm: they are SQLite
// operational sidecars, not tool results, and opening them in a text editor
// is useless. Users can copy the .db path; “open in editor” is suppressed
// in the UI for binary/db paths.
func sourceInventory(dbPath string) []model.SessionSourceFile {
	if dbPath == "" {
		return nil
	}
	return []model.SessionSourceFile{
		provenance.StatSource(model.SourceRolePrimaryTranscript, filepath.Clean(dbPath)),
	}
}
