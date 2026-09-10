package worktreereview

import (
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory lists the journal files this adapter cares about for one
// attempt. Layout (under $XDG_STATE_HOME/worktree-review/sessions/<attempt>/):
//
//	metadata.json   session metadata + heartbeat (atomic replace)
//	events.jsonl    append-only ReviewEvent stream (primary transcript)
//	result.json     immutable terminal result (written once)
//
// The attempt directory itself is never listed as a source: open-in-editor
// and path copy target real files.
func sourceInventory(root, attemptID string) []model.SessionSourceFile {
	dir := AttemptDir(root, attemptID)
	if dir == "" {
		return nil
	}
	return []model.SessionSourceFile{
		provenance.StatSource(model.SourceRolePrimaryTranscript, EventsPath(root, attemptID)),
		provenance.StatSource(model.SourceRoleMetadata, MetadataPath(root, attemptID)),
		provenance.StatSource(model.SourceRoleSnapshot, ResultPath(root, attemptID)),
	}
}
