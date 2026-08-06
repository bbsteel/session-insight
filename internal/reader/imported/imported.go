// Package imported is a read-only reader for sessions that arrived via a
// session-migration bundle (".sibundle"). Sessions are static snapshots:
// no resume, no delete of any live source, no watchers. Imported copies are
// managed as whole bundles through DELETE /api/imports/{bundle}.
//
// On-disk layout (root = <SI_DATA_DIR>/imports):
//
//	<root>/<bundleID>/
//	  manifest.json        bundle manifest (format, origin, session entries)
//	  sessions/<file>      {"detail":…,"render_events":…} per session
//	  raw/<rawDir>/<name>  optional captured source files
package imported

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/bbsteel/session-insight/internal/render"
)

// AgentType is the stable identifier for imported sessions.
const AgentType = "imported"

// Reader serves imported bundle sessions from a root directory. A missing
// root means an empty inventory, never an error.
type Reader struct {
	root string

	mu   sync.RWMutex
	locs map[string]sessionLoc // imported session id → on-disk location
}

type sessionLoc struct {
	bundleDir string
	file      string // relative to <bundleDir>/sessions/
	manifest  bundle.Manifest
	entry     bundle.SessionEntry
}

// New constructs a reader rooted at root (<SI_DATA_DIR>/imports).
func New(root string) *Reader {
	return &Reader{root: root, locs: make(map[string]sessionLoc)}
}

func (r *Reader) AgentType() string   { return AgentType }
func (r *Reader) DisplayName() string { return "Imported" }

// SanitizeID maps path separators and control characters in an original
// session id to '_' so the composed imported id stays a single safe token
// (same guard style as the grok reader's validSessionID).
func SanitizeID(orig string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '_'
		case unicode.IsControl(r):
			return '_'
		default:
			return r
		}
	}, orig)
}

// JoinSessionID composes the imported session id shared by the import API
// (when it writes import records) and this reader: <bundleID>--<sanitized id>.
func JoinSessionID(bundleID, origID string) string {
	return bundleID + "--" + SanitizeID(origID)
}

// validSessionID mirrors the grok guard: a usable id is a single path
// element. Imported ids contain no separators by construction, so this
// mainly rejects obviously hostile probe ids before any map/disk touch.
func validSessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

func (r *Reader) ListSessions() ([]model.Session, error) {
	sessions, _, err := r.ListSessionsDetailed()
	return sessions, err
}

// ListSessionsDetailed rebuilds the id→location cache from the bundle tree.
// Inventory is always complete: every readable bundle dir contributes all of
// its manifest sessions, so the indexer may tombstone sessions whose bundle
// directory was removed.
func (r *Reader) ListSessionsDetailed() (sessions []model.Session, complete bool, err error) {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}

	locs := make(map[string]sessionLoc)
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		bundleDir := filepath.Join(r.root, ent.Name())
		manifest, err := readManifest(filepath.Join(bundleDir, "manifest.json"))
		if err != nil {
			continue
		}
		for _, se := range manifest.Sessions {
			if !bundlePathSafe(se.File) {
				continue
			}
			id := JoinSessionID(ent.Name(), se.ID)
			locs[id] = sessionLoc{
				bundleDir: bundleDir,
				file:      se.File,
				manifest:  *manifest,
				entry:     se,
			}
			name := strings.TrimSpace(se.Title)
			if name == "" {
				name = se.ID
			}
			sessions = append(sessions, model.Session{
				ID:        id,
				AgentType: AgentType,
				Name:      name,
				CreatedAt: se.CreatedAt,
				UpdatedAt: se.UpdatedAt,
				IsLive:    false,
			})
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	r.mu.Lock()
	r.locs = locs
	r.mu.Unlock()
	return sessions, true, nil
}

// bundlePathSafe rejects manifest file names that could escape the bundle
// directory when joined (defense in depth on top of bundle.Extract's checks).
func bundlePathSafe(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	return filepath.Clean(name) == name && !strings.Contains(name, "..")
}

func readManifest(path string) (*bundle.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m bundle.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// sessionFile mirrors the bundle payload document shape.
type sessionFile struct {
	Detail       *model.SessionDetail `json:"detail"`
	RenderEvents []model.RenderEvent  `json:"render_events"`
}

// findSession resolves an imported id to its on-disk location, refreshing
// the cache once on a miss (a bundle may have been imported after the last
// list pass).
func (r *Reader) findSession(id string) (sessionLoc, error) {
	if !validSessionID(id) {
		return sessionLoc{}, fmt.Errorf("invalid imported session id: %q", id)
	}
	r.mu.RLock()
	loc, ok := r.locs[id]
	r.mu.RUnlock()
	if !ok {
		if _, _, err := r.ListSessionsDetailed(); err != nil {
			return sessionLoc{}, err
		}
		r.mu.RLock()
		loc, ok = r.locs[id]
		r.mu.RUnlock()
	}
	if !ok {
		return sessionLoc{}, readerr.New(readerr.SourceMissing, "source_missing",
			fmt.Errorf("imported session not found %q", id))
	}
	return loc, nil
}

// loadSession reads the stored payload document and shapes the detail for
// the imported surface: imported identity, never live, import provenance.
func (r *Reader) loadSession(loc sessionLoc, id string) (*model.SessionDetail, []model.RenderEvent, error) {
	data, err := os.ReadFile(filepath.Join(loc.bundleDir, "sessions", loc.file))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, readerr.New(readerr.SourceMissing, "source_missing",
				fmt.Errorf("imported session payload missing %q: %w", id, err))
		}
		return nil, nil, readerr.New(readerr.SourceUnreadable, "source_unreadable",
			fmt.Errorf("imported session payload unreadable %q: %w", id, err))
	}
	var payload sessionFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, nil, readerr.New(readerr.ParseFailed, "parse_failed",
			fmt.Errorf("imported session payload invalid %q: %w", id, err))
	}
	detail := payload.Detail
	if detail == nil {
		return nil, nil, readerr.New(readerr.MetadataOnly, "metadata_only",
			fmt.Errorf("imported session payload has no detail %q", id))
	}
	detail.AgentType = AgentType
	detail.ID = id
	detail.IsLive = false
	bundleID := filepath.Base(loc.bundleDir)
	detail.ImportInfo = &model.ImportInfo{
		OriginalAgentType: loc.entry.AgentType,
		OriginalSessionID: loc.entry.ID,
		OriginHost:        loc.manifest.OriginHost,
		BundleID:          bundleID,
		CaseLabel:         loc.manifest.CaseLabel,
		Redacted:          loc.entry.Redacted,
		ImportedAt:        bundleIDTimestamp(bundleID),
	}
	if events := payload.RenderEvents; events == nil {
		payload.RenderEvents = []model.RenderEvent{}
	}
	return detail, payload.RenderEvents, nil
}

func (r *Reader) GetSession(id string) (*model.SessionDetail, error) {
	loc, err := r.findSession(id)
	if err != nil {
		return nil, err
	}
	detail, _, err := r.loadSession(loc, id)
	return detail, err
}

// GetRenderEvents returns the stored render stream. Absent events are an
// empty slice, never an error.
func (r *Reader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	loc, err := r.findSession(id)
	if err != nil {
		return nil, err
	}
	_, events, err := r.loadSession(loc, id)
	if err != nil {
		return nil, err
	}
	return events, nil
}

// RenderANSI formats the stored render stream the same way the live render
// endpoint does.
func (r *Reader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEventsOpts(events, cols, render.Options{}), nil
}

// ReadIndexSnapshot satisfies reader.IndexSnapshotReader: the stored payload
// document already carries detail and render stream in one read.
func (r *Reader) ReadIndexSnapshot(ctx context.Context, sess model.Session) (*model.SessionDetail, []model.RenderEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	loc, err := r.findSession(sess.ID)
	if err != nil {
		return nil, nil, err
	}
	return r.loadSession(loc, sess.ID)
}

// SessionLive satisfies reader.SessionLivenessProvider. Imported sessions
// are static snapshots: never live, so a freshly imported session can never
// flash a live badge while the timestamp window would otherwise fire.
func (r *Reader) SessionLive(id string) (bool, error) {
	return false, nil
}

// bundleIDTimestamp parses the import-time timestamp prefix of a bundle id
// ("20060102-150405-<hex>"); unparseable ids yield the zero time.
func bundleIDTimestamp(bundleID string) time.Time {
	const prefixLen = len("20060102-150405")
	if len(bundleID) < prefixLen {
		return time.Time{}
	}
	t, err := time.ParseInLocation("20060102-150405", bundleID[:prefixLen], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Compile-time guards against accidental drift from the reader contracts.
var (
	_ interface {
		AgentType() string
		DisplayName() string
		ListSessions() ([]model.Session, error)
		GetSession(string) (*model.SessionDetail, error)
		RenderANSI(string, int) (string, error)
		GetRenderEvents(string) ([]model.RenderEvent, error)
	} = (*Reader)(nil)
	_ interface {
		ListSessionsDetailed() ([]model.Session, bool, error)
	} = (*Reader)(nil)
	_ interface {
		ReadIndexSnapshot(context.Context, model.Session) (*model.SessionDetail, []model.RenderEvent, error)
	} = (*Reader)(nil)
	_ interface {
		SessionLive(string) (bool, error)
	} = (*Reader)(nil)
)
