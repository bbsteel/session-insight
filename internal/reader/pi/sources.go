package pi

import (
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists Pi files this adapter cares about for one session.
// Layout (under ~/.pi/agent/sessions/<encoded-cwd>/):
//
//	<timestamp>_<session-id>.jsonl    primary transcript (one file per session)
//
// Forked or cloned sessions are separate JSONL files (separate session IDs),
// not nested sidecars. Global config under ~/.pi/agent/ (settings.json,
// auth.json, models-store.json, trust.json) is shared and is not part of a
// single session's inventory.
func sourceInventory(jsonlPath string) []model.SessionSourceFile {
	if jsonlPath == "" {
		return nil
	}
	return []model.SessionSourceFile{
		provenance.StatSource(model.SourceRolePrimaryTranscript, filepath.Clean(jsonlPath)),
	}
}
