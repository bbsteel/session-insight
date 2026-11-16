// Package dsh reads DeepSeek Harness generation-based JSONL storage read-only.
package dsh

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/bbsteel/session-insight/internal/render"
)

const agentType = "dsh"
const displayName = "DeepSeek Harness"

var generationPattern = regexp.MustCompile(`^session\.v([1-9][0-9]*)\.jsonl(\.zstd)?$`)

func generationVersion(name string) int {
	match := generationPattern.FindStringSubmatch(name)
	if match == nil {
		return 0
	}
	version, _ := strconv.Atoi(match[1])
	return version
}

type metadataSnapshot struct {
	path     string
	size     int64
	modified time.Time
	session  model.Session
}

// Reader caches listing metadata only; detail and render always read fresh bytes.
type Reader struct {
	sessionsRoot   string
	mutex          sync.RWMutex
	paths          map[string]string
	metadata       map[string]metadataSnapshot
	inventoryBuilt bool
}

func New(sessionsRoot string) *Reader {
	return &Reader{sessionsRoot: sessionsRoot, paths: map[string]string{}, metadata: map[string]metadataSnapshot{}}
}
func (*Reader) AgentType() string           { return agentType }
func (*Reader) DisplayName() string         { return displayName }
func (reader *Reader) WatchRoots() []string { return []string{reader.sessionsRoot} }

func (reader *Reader) ListSessions() ([]model.Session, error) {
	sessions, _, err := reader.ListSessionsDetailed()
	return sessions, err
}

func (reader *Reader) ListSessionsDetailed() ([]model.Session, bool, error) {
	selectedPaths := map[string]string{}
	ambiguousIDs := map[string]bool{}
	complete := true
	err := filepath.WalkDir(reader.sessionsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			complete = false
			if path == reader.sessionsRoot {
				return walkErr
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relative, _ := filepath.Rel(reader.sessionsRoot, path)
		depth := len(strings.Split(relative, string(filepath.Separator)))
		if entry.IsDir() {
			if depth > 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth != 3 || generationVersion(entry.Name()) == 0 {
			return nil
		}
		sessionID := filepath.Base(filepath.Dir(path))
		if ambiguousIDs[sessionID] {
			return nil
		}
		previous := selectedPaths[sessionID]
		if previous != "" && filepath.Dir(previous) != filepath.Dir(path) {
			complete = false
			ambiguousIDs[sessionID] = true
			delete(selectedPaths, sessionID)
			return nil
		}
		if previous == "" || generationVersion(entry.Name()) > generationVersion(filepath.Base(previous)) {
			selectedPaths[sessionID] = path
		} else if generationVersion(entry.Name()) == generationVersion(filepath.Base(previous)) {
			// dsh refuses mixed encodings for one generation; do not choose silently.
			complete = false
			ambiguousIDs[sessionID] = true
			delete(selectedPaths, sessionID)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	reader.mutex.Lock()
	reader.inventoryBuilt = true
	for id := range ambiguousIDs {
		delete(reader.paths, id)
		delete(reader.metadata, id)
	}
	for id := range reader.metadata {
		if _, exists := selectedPaths[id]; !exists {
			delete(reader.metadata, id)
		}
	}
	for id, path := range selectedPaths {
		reader.paths[id] = path
	}
	reader.mutex.Unlock()
	sessions := make([]model.Session, 0, len(selectedPaths))
	for id, path := range selectedPaths {
		info, err := os.Stat(path)
		if err != nil {
			complete = false
			continue
		}
		reader.mutex.RLock()
		cached, exists := reader.metadata[id]
		reader.mutex.RUnlock()
		if exists && cached.path == path && cached.size == info.Size() && cached.modified.Equal(info.ModTime()) {
			sessions = append(sessions, cached.session)
			continue
		}
		detail, _, err := readSession(path, id)
		if err != nil {
			complete = false
			continue
		}
		sessions = append(sessions, detail.Session)
		reader.mutex.Lock()
		reader.metadata[id] = metadataSnapshot{path: path, size: info.Size(), modified: info.ModTime(), session: detail.Session}
		reader.mutex.Unlock()
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, complete, nil
}

func (reader *Reader) sessionPath(id string) (string, error) {
	reader.mutex.RLock()
	path := reader.paths[id]
	initialized := reader.inventoryBuilt
	reader.mutex.RUnlock()
	if path == "" && !initialized {
		if _, _, err := reader.ListSessionsDetailed(); err != nil {
			return "", err
		}
		reader.mutex.RLock()
		path = reader.paths[id]
		reader.mutex.RUnlock()
	}
	if path == "" {
		return "", readerr.New(readerr.SourceMissing, "source_missing", fmt.Errorf("dsh session not found"))
	}
	return path, nil
}

func (reader *Reader) GetSession(id string) (*model.SessionDetail, error) {
	path, err := reader.sessionPath(id)
	if err != nil {
		return nil, err
	}
	detail, _, err := readSession(path, id)
	return detail, err
}
func (reader *Reader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	path, err := reader.sessionPath(id)
	if err != nil {
		return nil, err
	}
	detail, events, err := readSession(path, id)
	if err == nil && detail.Provenance.State == model.RecordParserUnsupported {
		return nil, readerr.New(readerr.FormatUnsupported, model.WarnUnsupportedSchema, fmt.Errorf("unsupported dsh generation")).WithSources(detail.Provenance.Sources)
	}
	return events, err
}
func (reader *Reader) RenderANSI(id string, cols int) (string, error) {
	events, err := reader.GetRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}
func (reader *Reader) LiveRevision(id string) (int64, error) {
	path, err := reader.sessionPath(id)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano() + info.Size(), nil
}
func (reader *Reader) ReadIndexSnapshot(ctx context.Context, session model.Session) (*model.SessionDetail, []model.RenderEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	path, err := reader.sessionPath(session.ID)
	if err != nil {
		return nil, nil, err
	}
	return readSession(path, session.ID)
}
