package chrys

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// chrysSourceInventory is Chrys-owned provenance: only files this adapter
// understands. Layout under ~/.chrys/sessions/<id>/:
//
//	session.json                 durable primary store
//	session.recovery.json        crash / in-flight sidecar
//	session.json.bak             backup of primary
//	sub_agents/sessions/*.json   collaboration child transcripts
//	snapshots/*                  turn checkpoints
//	mutations/*                  file-edit mutation cache (role edit_cache)
//
// Roles are stable cross-adapter values from model.SourceRole*; Chrys maps
// its layout onto them and does not dump unknowns as "other".
// The session directory itself is never listed (open-in-editor needs a file).
func chrysSourceInventory(sessionDir, effectivePath string) []model.SessionSourceFile {
	var sources []model.SessionSourceFile
	seen := map[string]struct{}{}
	add := func(role, path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || path == filepath.Clean(sessionDir) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		seen[path] = struct{}{}
		sources = append(sources, provenance.StatSource(role, path))
	}

	primaryPath := filepath.Join(sessionDir, "session.json")
	recoveryPath := filepath.Join(sessionDir, "session.recovery.json")
	bakPath := filepath.Join(sessionDir, "session.json.bak")
	effective := filepath.Clean(effectivePath)

	// Exactly one primary_transcript: the file SI actually read.
	if effective != "" {
		add(model.SourceRolePrimaryTranscript, effective)
	}

	// Non-winning durable primary stays listed with a precise role (not a
	// second primary_transcript).
	if primaryPath != effective {
		// Stale committed store when recovery won.
		add(model.SourceRoleSnapshot, primaryPath)
	}
	// Recovery sidecar when it did not win (when it won it is already primary).
	if recoveryPath != effective {
		add(model.SourceRoleRecovery, recoveryPath)
	}
	// Backup of primary.
	add(model.SourceRoleSnapshot, bakPath)

	// Collaboration child transcripts.
	addDirFiles(filepath.Join(sessionDir, "sub_agents", "sessions"), model.SourceRoleCollaboration, add, func(name string) bool {
		return strings.HasSuffix(name, ".json")
	})

	// Turn checkpoints.
	addDirFiles(filepath.Join(sessionDir, "snapshots"), model.SourceRoleSnapshot, add, nil)

	// File-edit mutation cache — UI collapses edit_cache by role.
	addDirFiles(filepath.Join(sessionDir, "mutations"), model.SourceRoleEditCache, add, nil)

	return sources
}

// addDirFiles lists regular files in dir (non-recursive), sorted by name.
// accept may filter by name; nil accepts every regular file.
func addDirFiles(dir, role string, add func(role, path string), accept func(name string) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if accept != nil && !accept(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		add(role, filepath.Join(dir, name))
	}
}
