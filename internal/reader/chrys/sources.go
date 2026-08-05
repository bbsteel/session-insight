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
//
// The effective primary is always inventoried (even when missing/unreadable).
// Optional paths are omitted when absent, but retained when Stat fails for
// another reason (e.g. permission) so SourceUnreadable is visible.
func chrysSourceInventory(sessionDir, effectivePath string) []model.SessionSourceFile {
	var sources []model.SessionSourceFile
	seen := map[string]struct{}{}
	add := func(role, path string, required bool) {
		path = filepath.Clean(path)
		if path == "" || path == "." || path == filepath.Clean(sessionDir) {
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

	primaryPath := filepath.Join(sessionDir, "session.json")
	recoveryPath := filepath.Join(sessionDir, "session.recovery.json")
	bakPath := filepath.Join(sessionDir, "session.json.bak")
	effective := filepath.Clean(effectivePath)

	// Exactly one primary_transcript: the file SI actually read (always list).
	if effective != "" {
		add(model.SourceRolePrimaryTranscript, effective, true)
	}

	// Non-winning durable primary stays listed with a precise role (not a
	// second primary_transcript). Optional when simply absent.
	if primaryPath != effective {
		// Stale committed store when recovery won.
		add(model.SourceRoleSnapshot, primaryPath, false)
	}
	// Recovery sidecar when it did not win (when it won it is already primary).
	if recoveryPath != effective {
		add(model.SourceRoleRecovery, recoveryPath, false)
	}
	// Backup of primary.
	add(model.SourceRoleSnapshot, bakPath, false)

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
// Sidecar directories are optional: missing dirs produce no entries.
func addDirFiles(dir, role string, add func(role, path string, required bool), accept func(name string) bool) {
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
		// Files discovered by ReadDir should still surface Stat failures.
		add(role, filepath.Join(dir, name), true)
	}
}
