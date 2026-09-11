package worktreereview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/bbsteel/session-insight/internal/render"
)

// AgentType is the stable identifier for Worktree Review journals.
const AgentType = "worktree-review"

// adapterRevision bumps whenever the mapping changes in a way the index
// must re-read.
const adapterRevision = 1

// Reader serves Worktree Review Session Journals from a journal root
// (one directory per attempt). A missing root is an empty inventory, never
// an error.
type Reader struct {
	root string
	// now is injectable for deterministic liveness tests.
	now func() time.Time
}

// New constructs a reader rooted at the session journal root
// ($XDG_STATE_HOME/worktree-review/sessions by default).
func New(root string) *Reader {
	return &Reader{root: root, now: time.Now}
}

func (r *Reader) AgentType() string   { return AgentType }
func (r *Reader) DisplayName() string { return "Worktree Review" }

// WatchRoots implements reader.WatchRootProvider.
func (r *Reader) WatchRoots() []string { return []string{r.root} }

// loadAttempt reads the three journal documents for one attempt with the
// contract tolerance rules: metadata may be missing (still listed from
// events), events tolerate corruption, result.json may be absent.
func (r *Reader) loadAttempt(attemptID string) (*attemptView, error) {
	dir := AttemptDir(r.root, attemptID)
	if dir == "" {
		return nil, readerr.New(readerr.SourceMissing, "invalid_attempt_id",
			fmt.Errorf("invalid attempt id %q", attemptID))
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, readerr.New(readerr.SourceMissing, "source_missing",
			fmt.Errorf("worktree-review session not found %q", attemptID))
	}

	view := &attemptView{sources: sourceInventory(r.root, attemptID)}

	if data, err := os.ReadFile(MetadataPath(r.root, attemptID)); err == nil {
		// Unknown metadata versions degrade to "no metadata" rather than
		// failing the whole session; the version is surfaced via warnings.
		// A document whose attempt_id does not match the directory is
		// discarded: it was written for a different attempt.
		if meta, parseErr := ParseSessionMetadata(data); parseErr == nil {
			if meta.AttemptID == attemptID {
				view.meta = meta
			} else {
				view.mismatchedDocs = append(view.mismatchedDocs, "metadata.json")
			}
		}
	}

	eventsPath := EventsPath(r.root, attemptID)
	if _, err := os.Stat(eventsPath); err == nil {
		events, err := ReadEventsFile(eventsPath)
		if err != nil {
			return nil, readerr.New(readerr.SourceUnreadable, "source_unreadable", err).
				WithSources(sourceInventory(r.root, attemptID))
		}
		view.events = events.Events
		view.skippedMalformed = events.SkippedMalformed
		view.skippedVersion = events.SkippedVersion
	}

	if data, err := os.ReadFile(ResultPath(r.root, attemptID)); err == nil {
		if doc, parseErr := ParseResultDocument(data); parseErr == nil {
			if doc.AttemptID == attemptID {
				view.result = doc
			} else {
				view.mismatchedDocs = append(view.mismatchedDocs, "result.json")
			}
		}
	}

	if view.meta == nil && len(view.events) == 0 && view.result == nil {
		return nil, readerr.New(readerr.MetadataOnly, "empty_journal",
			fmt.Errorf("worktree-review session %q has no readable journal documents", attemptID))
	}
	return view, nil
}

// sessionName derives the display name from the recorded request, never
// from the directory name alone.
func sessionName(view *attemptView, attemptID string) string {
	for i := range view.events {
		if view.events[i].EventType != "attempt.created" {
			continue
		}
		repository := payloadString(&view.events[i], "repository_display")
		source := payloadString(&view.events[i], "source")
		if repository != "" && source != "" {
			return fmt.Sprintf("%s · %s", repository, source)
		}
		if repository != "" {
			return repository
		}
	}
	return attemptID
}

func (r *Reader) sessionFromView(attemptID string, view *attemptView) model.Session {
	session := model.Session{
		ID:        SessionIDForAttempt(attemptID),
		AgentType: AgentType,
		Name:      sessionName(view, attemptID),
		IsLive:    view.isLive(r.now()),
	}
	if first := view.firstEvent(); first != nil {
		session.CreatedAt = first.OccurredAt
		session.Repository = payloadString(first, "repository_display")
	}
	switch {
	case view.meta != nil && !view.meta.HeartbeatAt.IsZero():
		session.UpdatedAt = view.meta.HeartbeatAt
	case len(view.events) > 0:
		session.UpdatedAt = view.events[len(view.events)-1].OccurredAt
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	return session
}

// ListSessions implements reader.BaseSessionReader.
func (r *Reader) ListSessions() ([]model.Session, error) {
	sessions, _, err := r.ListSessionsDetailed()
	return sessions, err
}

// ListSessionsDetailed implements reader.DetailedSessionLister. Journal
// directories with unreadable or unsupported-version metadata are skipped
// and mark the inventory incomplete, so the indexer never tombstones
// sessions that may simply be unreadable.
func (r *Reader) ListSessionsDetailed() (sessions []model.Session, complete bool, err error) {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}

	complete = true
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		attemptID := entry.Name()
		if !validAttemptID(attemptID) {
			continue
		}
		view, loadErr := r.loadAttempt(attemptID)
		if loadErr != nil {
			complete = false
			continue
		}
		sessions = append(sessions, r.sessionFromView(attemptID, view))
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, complete, nil
}

// GetSession implements reader.BaseSessionReader.
func (r *Reader) GetSession(id string) (*model.SessionDetail, error) {
	view, err := r.loadAttempt(id)
	if err != nil {
		return nil, err
	}
	session := r.sessionFromView(id, view)
	return buildDetail(session, view), nil
}

// GetSessionMeta implements reader.SessionMetaProvider using the same view
// as GetSession so revisions stay identical.
func (r *Reader) GetSessionMeta(id string) (*model.Session, error) {
	view, err := r.loadAttempt(id)
	if err != nil {
		return nil, err
	}
	session := r.sessionFromView(id, view)
	return &session, nil
}

// GetRenderEvents implements reader.BaseSessionReader.
func (r *Reader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	view, err := r.loadAttempt(id)
	if err != nil {
		return nil, err
	}
	return eventsToRenderEvents(view.events), nil
}

// RenderANSI implements reader.BaseSessionReader.
func (r *Reader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEventsOpts(events, cols, render.Options{}), nil
}

// ReadIndexSnapshot implements reader.IndexSnapshotReader: one journal read
// produces detail and render stream together.
func (r *Reader) ReadIndexSnapshot(_ context.Context, session model.Session) (*model.SessionDetail, []model.RenderEvent, error) {
	view, err := r.loadAttempt(session.ID)
	if err != nil {
		return nil, nil, err
	}
	detail := buildDetail(r.sessionFromView(session.ID, view), view)
	return detail, eventsToRenderEvents(view.events), nil
}

// SessionLive implements reader.SessionLivenessProvider from the journal
// heartbeat and terminal state; no process scanning.
func (r *Reader) SessionLive(id string) (bool, error) {
	view, err := r.loadAttempt(id)
	if err != nil {
		return false, err
	}
	return view.isLive(r.now()), nil
}

// LiveRevision implements reader.LiveRevisionProvider: a stat-level revision
// over the journal files (size and mtime), never a content parse.
func (r *Reader) LiveRevision(id string) (int64, error) {
	dir := AttemptDir(r.root, id)
	if dir == "" {
		return 0, fmt.Errorf("invalid attempt id %q", id)
	}
	var revision int64
	for _, name := range []string{"events.jsonl", "metadata.json", "result.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		revision = revision ^ info.ModTime().UnixNano() ^ info.Size()
	}
	return revision, nil
}

// Compile-time guards against drift from the reader contracts.
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
		WatchRoots() []string
	} = (*Reader)(nil)
	_ interface {
		ReadIndexSnapshot(context.Context, model.Session) (*model.SessionDetail, []model.RenderEvent, error)
	} = (*Reader)(nil)
	_ interface {
		SessionLive(string) (bool, error)
	} = (*Reader)(nil)
	_ interface {
		LiveRevision(string) (int64, error)
	} = (*Reader)(nil)
	_ interface {
		GetSessionMeta(string) (*model.Session, error)
	} = (*Reader)(nil)
)
