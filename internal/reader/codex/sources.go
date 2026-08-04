package codex

import (
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists Codex files this adapter cares about for one session.
// Layout (under ~/.codex/sessions/YYYY/MM/DD/):
//
//	rollout-<timestamp>-<uuid>.jsonl    primary transcript (one file per session)
//
// Subagent threads are separate rollout files (separate session IDs), not
// nested sidecars of the parent. Global ~/.codex/history.jsonl is shared
// across sessions and is not part of a single session's inventory.
func sourceInventory(jsonlPath string) []model.SessionSourceFile {
	if jsonlPath == "" {
		return nil
	}
	return []model.SessionSourceFile{
		provenance.StatSource(model.SourceRolePrimaryTranscript, filepath.Clean(jsonlPath)),
	}
}
