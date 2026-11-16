package dsh

import (
	"os"
	"path/filepath"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// sourceInventory describes the selected generation and preserved older
// generations in the same session directory. session.lock is a kernel lease
// rendezvous, not transcript data. Global storages/session_projcache, workspace,
// settings, credentials and attachments are never exposed as session sources.
func sourceInventory(primaryPath string) []model.SessionSourceFile {
	sources := []model.SessionSourceFile{provenance.StatSource(model.SourceRolePrimaryTranscript, primaryPath)}
	entries, _ := os.ReadDir(filepath.Dir(primaryPath))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || generationVersion(entry.Name()) == 0 {
			continue
		}
		path := filepath.Join(filepath.Dir(primaryPath), entry.Name())
		if path != primaryPath {
			sources = append(sources, provenance.StatSource(model.SourceRoleSnapshot, path))
		}
	}
	return sources
}
