package pi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/bbsteel/session-insight/internal/render"
)

const (
	agentType   = "pi"
	displayName = "Pi"
)

// PiReader reads Pi coding-agent sessions from a sessions directory tree.
type PiReader struct {
	sessionsDir string

	// pathsMu guards the id → canonical-file index: paths holds the current
	// transcript for an id, and idDirs holds the encoded-cwd directories known
	// to contain that id. Lookups resolve from this index only, so LiveRevision
	// and replay stay stat-level instead of walking the whole tree per call.
	pathsMu  sync.RWMutex
	paths    map[string]string
	lastSeen map[string]string
	idDirs   map[string]map[string]struct{}
	// lastIndexAt bounds how often an unknown id may trigger a full directory
	// scan, so repeated probes for a session Pi does not own stay cheap without
	// permanently hiding a newly created session.
	lastIndexAt time.Time

	// metaMu guards metaCache, the size+mtime keyed inventory of scanning
	// sessions. Listing is the indexer's reconciliation path, so an unchanged
	// transcript must cost one stat instead of a full decode.
	metaMu    sync.Mutex
	metaCache map[string]metaCacheEntry
}

// metaCacheEntry is one listing scan result plus the stat it was computed
// from. A size or mtime change invalidates it.
type metaCacheEntry struct {
	size    int64
	modTime time.Time
	session model.Session
}

// New constructs a reader rooted at sessionsDir (~/.pi/agent/sessions).
func New(sessionsDir string) *PiReader {
	return &PiReader{
		sessionsDir: sessionsDir,
		paths:       make(map[string]string),
		lastSeen:    make(map[string]string),
		idDirs:      make(map[string]map[string]struct{}),
		metaCache:   make(map[string]metaCacheEntry),
	}
}

// scanSessionMetaCached returns the metadata for path, reusing the previous
// scan while the file's size and mtime are unchanged.
func (r *PiReader) scanSessionMetaCached(path string) (model.Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return model.Session{}, err
	}
	r.metaMu.Lock()
	cached, ok := r.metaCache[path]
	r.metaMu.Unlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.session, nil
	}
	session, err := scanSessionMeta(path)
	if err != nil {
		return model.Session{}, err
	}
	r.metaMu.Lock()
	if r.metaCache == nil {
		r.metaCache = make(map[string]metaCacheEntry)
	}
	r.metaCache[path] = metaCacheEntry{size: info.Size(), modTime: info.ModTime(), session: session}
	r.metaMu.Unlock()
	return session, nil
}

// pruneMetaCache drops cache entries for files that are no longer listed, so a
// long-lived process does not accumulate deleted sessions.
func (r *PiReader) pruneMetaCache(seen map[string]struct{}) {
	r.metaMu.Lock()
	defer r.metaMu.Unlock()
	for path := range r.metaCache {
		if _, ok := seen[path]; !ok {
			delete(r.metaCache, path)
		}
	}
}

func (r *PiReader) WatchRoots() []string { return []string{r.sessionsDir} }

func (r *PiReader) AgentType() string   { return agentType }
func (r *PiReader) DisplayName() string { return displayName }

func validSessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

var errSessionNotFound = errors.New("pi session not found")

func (r *PiReader) rememberPath(id, path string) {
	if id == "" || path == "" {
		return
	}
	dir := filepath.Dir(path)
	r.pathsMu.Lock()
	r.paths[id] = path
	r.lastSeen[id] = path
	if r.idDirs == nil {
		r.idDirs = make(map[string]map[string]struct{})
	}
	if r.idDirs[id] == nil {
		r.idDirs[id] = make(map[string]struct{})
	}
	r.idDirs[id][dir] = struct{}{}
	r.pathsMu.Unlock()
}

func (r *PiReader) knownSessionFile(id string) string {
	r.pathsMu.RLock()
	defer r.pathsMu.RUnlock()
	return r.paths[id]
}

// knownSessionDirs returns the encoded-cwd directories already associated with
// this id, so a rescan never has to touch the whole sessions tree.
func (r *PiReader) knownSessionDirs(id string) []string {
	r.pathsMu.RLock()
	defer r.pathsMu.RUnlock()
	dirs := make([]string, 0, len(r.idDirs[id]))
	for dir := range r.idDirs[id] {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

// rememberSession records the canonical path plus every directory known to hold
// a copy of the id, so later duplicate lookups never rescan the sessions root.
func (r *PiReader) rememberSession(id, path string, dirs map[string]struct{}) {
	r.rememberPath(id, path)
	if len(dirs) == 0 {
		return
	}
	r.pathsMu.Lock()
	if r.idDirs[id] == nil {
		r.idDirs[id] = make(map[string]struct{})
	}
	for dir := range dirs {
		r.idDirs[id][dir] = struct{}{}
	}
	r.pathsMu.Unlock()
}

// lastKnownSessionFile is the most recently resolved path for id, retained
// after the live index forgets it so a not-found error can still report the
// source it was reading (provenance must not vanish on the failure path).
func (r *PiReader) lastKnownSessionFile(id string) string {
	r.pathsMu.RLock()
	defer r.pathsMu.RUnlock()
	return r.lastSeen[id]
}

// forgetPath invalidates the resolved path but keeps lastSeen and the known
// directories, so a delete or an unreadable rescan does not erase provenance.
func (r *PiReader) forgetPath(id string) {
	r.pathsMu.Lock()
	delete(r.paths, id)
	r.pathsMu.Unlock()
}

func matchSessionFilename(name, id string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".jsonl") {
		return false
	}
	return sessionIDFromFilename(name) == id
}

// findSessionFile resolves the canonical JSONL for a session id.
//
// The hot path is one stat. The server probes every reader's LiveRevision for
// every session it displays, and Pi is registered ahead of the home-directory
// agents, so Pi must never walk its whole tree for an id it does not own.
//
// Where duplicate resolution happens:
//   - Listing is the reconciliation path. It scans the tree, picks the newest
//     same-id file, and reregisters that path, so a copy, crash leftover, or
//     in-progress rewrite converges to the transcript Pi is actually writing.
//   - Per-request lookups stat the indexed path and only compare siblings in
//     the id's already-known directories, which is a bounded directory read
//     rather than a whole-tree walk.
//   - An unknown id triggers at most one full index pass per
//     unknownIDIndexInterval, so repeated probes stay cheap while a newly
//     created session is still discovered without waiting for a list.
func (r *PiReader) findSessionFile(id string) string {
	if !validSessionID(id) {
		return ""
	}
	cached := r.knownSessionFile(id)
	if cached != "" {
		if _, err := os.Stat(cached); err == nil && matchSessionFilename(filepath.Base(cached), id) {
			if newest := r.newestKnownSibling(id, cached); newest != cached {
				r.rememberPath(id, newest)
				return newest
			}
			return cached
		}
		r.forgetPath(id)
	}
	if !r.indexOnce() {
		return ""
	}
	return r.knownSessionFile(id)
}

// newestKnownSibling returns the newest file encoding id among the cached path
// and the id's known directories. It never enumerates the sessions root.
func (r *PiReader) newestKnownSibling(id, cached string) string {
	dirs := r.knownSessionDirs(id)
	if len(dirs) == 0 {
		return cached
	}
	best := cached
	for _, dir := range dirs {
		for _, path := range sessionFilesInDir(dir, id) {
			if newestFileIsLater(path, best) {
				best = path
			}
		}
	}
	return best
}

// unknownIDIndexInterval bounds how often a lookup for an id that is not in the
// path index may trigger a full directory scan. Without it, every reader probe
// for a session this reader does not own would walk the whole Pi tree; with it,
// a newly created session is still discovered within one interval.
const unknownIDIndexInterval = time.Second

// markIndexFresh records that a full listing just reconciled the index, which
// extends the window before another unknown id triggers a directory scan.
func (r *PiReader) markIndexFresh() {
	r.pathsMu.Lock()
	r.lastIndexAt = time.Now()
	r.pathsMu.Unlock()
}

// indexOnce performs at most one full directory scan per interval, so a miss
// is cheap to repeat but a new session still appears without a list.
func (r *PiReader) indexOnce() bool {
	r.pathsMu.Lock()
	if time.Since(r.lastIndexAt) < unknownIDIndexInterval {
		r.pathsMu.Unlock()
		return false
	}
	r.lastIndexAt = time.Now()
	r.pathsMu.Unlock()

	for _, path := range r.listSessionFilenames("") {
		id := sessionIDFromFilename(filepath.Base(path))
		if id == "" {
			continue
		}
		if previous := r.knownSessionFile(id); previous == "" || newestFileIsLater(path, previous) {
			r.rememberPath(id, path)
		}
	}
	return true
}

// sessionFilesInDir lists JSONL files in one directory whose filename encodes
// id, without opening transcript contents.
func sessionFilesInDir(dir, id string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !matchSessionFilename(entry.Name(), id) {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(out)
	return out
}

// listSessionFilenames returns every JSONL path under the sessions root and one
// encoded-cwd level. When onlyID is set, paths whose filename does not encode
// that id are skipped. It never opens transcript contents.
func (r *PiReader) listSessionFilenames(onlyID string) []string {
	var out []string
	seen := map[string]struct{}{}
	addDir := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				continue
			}
			if onlyID != "" && !matchSessionFilename(entry.Name(), onlyID) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			out = append(out, path)
		}
	}
	addDir(r.sessionsDir)
	entries, err := os.ReadDir(r.sessionsDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				addDir(filepath.Join(r.sessionsDir, entry.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// collectSessionFiles lists JSONL files whose filename encodes id, used by
// delete, which must reach every copy.
func (r *PiReader) collectSessionFiles(id string) []string {
	if !validSessionID(id) {
		return nil
	}
	return r.listSessionFilenames(id)
}

// newestFileIsLater reports whether candidatePath is the newer copy of a
// duplicated session id than currentPath: later mtime wins, and an equal mtime
// falls back to the lexically greater filename (Pi encodes the creation time
// in the name). An unreadable candidate is never preferred over a readable one.
func newestFileIsLater(candidatePath, currentPath string) bool {
	candidateInfo, candidateErr := os.Stat(candidatePath)
	currentInfo, currentErr := os.Stat(currentPath)
	switch {
	case candidateErr != nil:
		return false
	case currentErr != nil:
		return true
	}
	if !candidateInfo.ModTime().Equal(currentInfo.ModTime()) {
		return candidateInfo.ModTime().After(currentInfo.ModTime())
	}
	return candidatePath > currentPath
}

func sessionIDFromFilename(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if i := strings.LastIndex(name, "_"); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

func (r *PiReader) ListSessions() ([]model.Session, error) {
	sessions, _, err := r.ListSessionsDetailed()
	return sessions, err
}

// ListSessionsDetailed walks cwd-encoded directories for *.jsonl files and
// reports incompleteness when a directory or file fails for reasons other
// than absence. Listing uses a metadata-only scan; replay parsing stays on
// GetSession / GetRenderEvents.
func (r *PiReader) ListSessionsDetailed() (sessions []model.Session, complete bool, err error) {
	complete = true
	// Duplicate ids are collapsed onto the newest file. The winner is decided
	// after the whole walk, because directory order says nothing about which
	// copy Pi is appending to.
	type candidate struct {
		session model.Session
		path    string
		// dirs collects every directory that holds a copy of this id, so later
		// duplicate lookups stay bounded to known directories.
		dirs map[string]struct{}
	}
	winners := map[string]candidate{}
	var order []string
	listed := map[string]struct{}{}
	addDir := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				complete = false
			}
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			listed[path] = struct{}{}
			session, parseErr := r.scanSessionMetaCached(path)
			if parseErr != nil {
				if !os.IsNotExist(parseErr) {
					complete = false
				}
				continue
			}
			if session.ID == "" {
				complete = false
				continue
			}
			if previous, dup := winners[session.ID]; dup {
				previous.dirs[filepath.Dir(path)] = struct{}{}
				if !newestFileIsLater(path, previous.path) {
					continue
				}
				previous.session = session
				previous.path = path
				winners[session.ID] = previous
				continue
			}
			order = append(order, session.ID)
			winners[session.ID] = candidate{
				session: session,
				path:    path,
				dirs:    map[string]struct{}{filepath.Dir(path): {}},
			}
		}
	}
	addDir(r.sessionsDir)
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []model.Session{}, true, nil
		}
		return nil, false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			addDir(filepath.Join(r.sessionsDir, entry.Name()))
		}
	}
	for _, id := range order {
		winner := winners[id]
		sessions = append(sessions, winner.session)
		r.rememberSession(id, winner.path, winner.dirs)
	}
	r.markIndexFresh()
	r.pruneMetaCache(listed)
	if sessions == nil {
		sessions = []model.Session{}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
	return sessions, complete, nil
}

func (r *PiReader) GetSession(id string) (*model.SessionDetail, error) {
	path := r.findSessionFile(id)
	if path == "" {
		readErr := readerr.New(readerr.SourceMissing, "source_missing",
			fmt.Errorf("%w: %s", errSessionNotFound, id))
		if known := r.lastKnownSessionFile(id); known != "" {
			readErr.WithSources(sourceInventory(known))
		}
		return nil, readErr
	}
	parsed, err := parseSessionFile(path)
	if err != nil {
		sources := sourceInventory(path)
		if os.IsNotExist(err) {
			return nil, readerr.New(readerr.SourceMissing, "source_missing", err).WithSources(sources)
		}
		return nil, readerr.New(readerr.SourceUnreadable, "source_unreadable", err).
			WithSources(sources).
			WithWarnings([]model.ParseWarning{readerr.SourceUnreadableWarning(model.SourceRolePrimaryTranscript)})
	}
	return parsed.Detail, nil
}

func (r *PiReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	parsed, err := parseSessionFile(path)
	if err != nil {
		return nil, err
	}
	return parsed.Events, nil
}

func (r *PiReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

// LiveRevision is a cheap mtime+size fingerprint of the session JSONL.
func (r *PiReader) LiveRevision(id string) (int64, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return 0, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano() + info.Size(), nil
}

func (r *PiReader) GetSessionMeta(id string) (*model.Session, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	session, err := r.scanSessionMetaCached(path)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func attachProvenance(detail *model.SessionDetail, path string, skipped int) {
	if detail == nil || path == "" {
		return
	}
	var warnings []model.ParseWarning
	if skipped > 0 {
		warnings = append(warnings, provenance.Warning(
			model.WarnMalformedRecordSkipped, model.WarningSeverityWarning, true,
			[]string{model.ImpactReplay}, model.SourceRolePrimaryTranscript, nil, skipped,
		))
	}
	info, _ := os.Stat(path)
	var updated time.Time
	var size int64
	if info != nil {
		updated = info.ModTime()
		size = info.Size()
	}
	p := provenance.Build(provenance.Input{
		CapturedAt:      time.Now().UTC(),
		AdapterRevision: Capabilities().AdapterRevision,
		Sources: []model.SessionSourceFile{
			provenance.PresentSource(model.SourceRolePrimaryTranscript, filepath.Clean(path), updated, size),
		},
		Warnings:          warnings,
		HasReplayableBody: len(detail.Turns) > 0,
	})
	detail.Provenance = &p
}

func parseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	return time.Time{}
}
