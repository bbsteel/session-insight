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
	pathsMu     sync.RWMutex
	paths       map[string]string
}

// New constructs a reader rooted at sessionsDir (~/.pi/agent/sessions).
func New(sessionsDir string) *PiReader {
	return &PiReader{sessionsDir: sessionsDir, paths: make(map[string]string)}
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
	r.pathsMu.Lock()
	r.paths[id] = path
	r.pathsMu.Unlock()
}

func (r *PiReader) knownSessionFile(id string) string {
	r.pathsMu.RLock()
	defer r.pathsMu.RUnlock()
	return r.paths[id]
}

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

// collectSessionFiles lists JSONL files whose filename encodes id. It reads
// directory entries only (sessions root and one encoded-cwd level) and never
// opens transcript contents.
func (r *PiReader) collectSessionFiles(id string) []string {
	if !validSessionID(id) {
		return nil
	}
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
			if !matchSessionFilename(entry.Name(), id) {
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

func newestSessionFile(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	best := paths[0]
	var bestTime time.Time
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if bestTime.IsZero() || info.ModTime().After(bestTime) {
			best = path
			bestTime = info.ModTime()
		}
	}
	return best
}

func (r *PiReader) findSessionFile(id string) string {
	if !validSessionID(id) {
		return ""
	}
	if known := r.knownSessionFile(id); known != "" {
		if _, err := os.Stat(known); err == nil {
			return known
		}
	}
	found := newestSessionFile(r.collectSessionFiles(id))
	if found != "" {
		r.rememberPath(id, found)
	}
	return found
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
	seen := map[string]struct{}{}
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
			session, parseErr := scanSessionMeta(path)
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
			if _, dup := seen[session.ID]; dup {
				continue
			}
			seen[session.ID] = struct{}{}
			r.rememberPath(session.ID, path)
			sessions = append(sessions, session)
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
		if known := r.knownSessionFile(id); known != "" {
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
	session, err := scanSessionMeta(path)
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
