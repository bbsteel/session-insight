package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/render"
)

// countingReader is a fake BaseSessionReader + LiveRevisionProvider whose
// parse count and on-disk revision tests control directly.
type countingReader struct {
	agentType string

	rev      atomic.Int64
	parses   atomic.Int32
	parseErr error

	// bumpRevDuringParse simulates a live-tailing session that grows while
	// GetRenderEvents is running; the parsed result must be served but not
	// cached.
	bumpRevDuringParse atomic.Bool
}

func (r *countingReader) AgentType() string { return r.agentType }
func (r *countingReader) DisplayName() string {
	return r.agentType
}
func (r *countingReader) ListSessions() ([]model.Session, error) { return nil, nil }
func (r *countingReader) GetSession(id string) (*model.SessionDetail, error) {
	return nil, errors.New("not implemented")
}
func (r *countingReader) RenderANSI(id string, cols int) (string, error) {
	return "", errors.New("not implemented")
}
func (r *countingReader) LiveRevision(id string) (int64, error) {
	return r.rev.Load(), nil
}
func (r *countingReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	r.parses.Add(1)
	if r.parseErr != nil {
		return nil, r.parseErr
	}
	if r.bumpRevDuringParse.Load() {
		r.rev.Add(1)
	}
	return []model.RenderEvent{
		{Type: "UserMessage", Text: fmt.Sprintf("rev-%d", r.rev.Load())},
		{Type: "AssistantMessage", Text: "reply"},
	}, nil
}

// plainReader lacks LiveRevisionProvider and must never be cached. (It does
// not embed countingReader: embedding would promote LiveRevision into its
// method set.)
type plainReader struct {
	parses atomic.Int32
}

func (r *plainReader) AgentType() string                    { return "plain" }
func (r *plainReader) DisplayName() string                  { return "plain" }
func (r *plainReader) ListSessions() ([]model.Session, error) { return nil, nil }
func (r *plainReader) GetSession(id string) (*model.SessionDetail, error) {
	return nil, errors.New("not implemented")
}
func (r *plainReader) RenderANSI(id string, cols int) (string, error) {
	return "", errors.New("not implemented")
}
func (r *plainReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	r.parses.Add(1)
	return []model.RenderEvent{{Type: "UserMessage", Text: "hi"}}, nil
}

func TestRenderEventsCacheHit(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(7)
	c := newReplayCache()

	first, rev, err := c.eventsFor(rd, "s1")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := c.eventsFor(rd, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if rev != 7 {
		t.Errorf("rev = %d, want 7", rev)
	}
	if got := rd.parses.Load(); got != 1 {
		t.Errorf("parses = %d, want 1", got)
	}
	if len(first) != 2 || len(second) != 2 || first[0].Text != second[0].Text {
		t.Errorf("cache hit returned different events: %v vs %v", first, second)
	}
}

func TestRenderEventsInvalidationOnGrowth(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(1)
	c := newReplayCache()

	if _, _, err := c.eventsFor(rd, "s1"); err != nil {
		t.Fatal(err)
	}
	rd.rev.Store(2) // simulate live-tail append
	events, _, err := c.eventsFor(rd, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got := rd.parses.Load(); got != 2 {
		t.Errorf("parses = %d, want 2 after growth", got)
	}
	if events[0].Text != "rev-2" {
		t.Errorf("stale events after growth: %q", events[0].Text)
	}
}

func TestRenderEventsSingleflight(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(3)
	c := newReplayCache()

	const callers = 8
	var wg sync.WaitGroup
	results := make([][]model.RenderEvent, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			events, _, err := c.eventsFor(rd, "s1")
			results[i] = events
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if len(results[i]) != 2 {
			t.Fatalf("caller %d got %d events", i, len(results[i]))
		}
	}
	if got := rd.parses.Load(); got != 1 {
		t.Errorf("parses = %d, want 1 for %d concurrent callers", got, callers)
	}
}

func TestRenderEventsCloneIsolation(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(5)
	c := newReplayCache()

	events, _, err := c.eventsFor(rd, "s1")
	if err != nil {
		t.Fatal(err)
	}
	// Formatters mutate event fields in place (grok Preprocess); the cached
	// copy must be unaffected by caller writes.
	events[0].Text = "mutated"

	again, _, err := c.eventsFor(rd, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Text == "mutated" {
		t.Error("cache returned the same backing array the caller mutated")
	}
	if got := rd.parses.Load(); got != 1 {
		t.Errorf("parses = %d, want 1", got)
	}
}

func TestRenderEventsErrorNotCached(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(1)
	rd.parseErr = errors.New("boom")
	c := newReplayCache()

	if _, _, err := c.eventsFor(rd, "s1"); err == nil {
		t.Fatal("expected error")
	}
	rd.parseErr = nil
	if _, _, err := c.eventsFor(rd, "s1"); err != nil {
		t.Fatalf("second call should succeed after transient error: %v", err)
	}
	if got := rd.parses.Load(); got != 2 {
		t.Errorf("parses = %d, want 2 (error not cached)", got)
	}
}

func TestRenderEventsNoLiveRevisionProvider(t *testing.T) {
	rd := &plainReader{}
	c := newReplayCache()

	for i := 0; i < 2; i++ {
		events, rev, err := c.eventsFor(rd, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if rev != 0 {
			t.Errorf("rev = %d, want 0 for uncacheable reader", rev)
		}
		if len(events) != 1 {
			t.Fatalf("got %d events", len(events))
		}
	}
	if got := rd.parses.Load(); got != 2 {
		t.Errorf("parses = %d, want 2 (no caching without LiveRevision)", got)
	}
}

func TestRenderEventsMidParseGrowthNotCached(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(1)
	rd.bumpRevDuringParse.Store(true)
	c := newReplayCache()

	if _, _, err := c.eventsFor(rd, "s1"); err != nil {
		t.Fatal(err)
	}
	rd.bumpRevDuringParse.Store(false)
	if _, _, err := c.eventsFor(rd, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := rd.parses.Load(); got != 2 {
		t.Errorf("parses = %d, want 2 (mid-parse growth must not be cached)", got)
	}
}

func TestRenderANSICacheColsVariants(t *testing.T) {
	rd := &countingReader{agentType: "fake"}
	rd.rev.Store(9)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	srv := New(database, nil)

	first, err := srv.renderANSIFor(rd, "s1", 120, render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := srv.renderANSIFor(rd, "s1", 120, render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.renderANSIFor(rd, "s1", 80, render.Options{}); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("same cols returned different ANSI")
	}
	if got := rd.parses.Load(); got != 1 {
		t.Errorf("parses = %d, want 1 across cols variants", got)
	}
	if len(srv.replay.ansi) != 2 {
		t.Errorf("cached ANSI variants = %d, want 2 (cols 120 and 80)", len(srv.replay.ansi))
	}
}

// metaCountingReader adds the SessionMetaProvider capability and records
// whether the expensive GetSession path was used.
type metaCountingReader struct {
	countingReader
	session         model.Session
	metaCalls       atomic.Int32
	getSessionCalls atomic.Int32
}

func (r *metaCountingReader) GetSessionMeta(id string) (*model.Session, error) {
	r.metaCalls.Add(1)
	s := r.session
	return &s, nil
}

func (r *metaCountingReader) GetSession(id string) (*model.SessionDetail, error) {
	r.getSessionCalls.Add(1)
	return &model.SessionDetail{Session: r.session}, nil
}

func newMetaReader() *metaCountingReader {
	rd := &metaCountingReader{
		session: model.Session{
			ID:        "s1",
			AgentType: "fake",
			UpdatedAt: time.Unix(1700000000, 123).UTC(),
		},
	}
	rd.agentType = "fake"
	rd.rev.Store(11)
	return rd
}

// TestSessionOpenSharesParseAcrossEndpoints drives the four endpoints the
// frontend fires on session open and asserts the transcript is parsed once.
func TestSessionOpenSharesParseAcrossEndpoints(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rd := newMetaReader()
	srv := New(database, []reader.BaseSessionReader{rd})

	paths := []string{
		"/api/sessions/s1/render?cols=120",
		"/api/sessions/s1/edits",
		"/api/sessions/s1/tool-outputs",
		"/api/sessions/s1/positions?cols=120",
	}
	for _, p := range paths {
		req := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s -> %d, want 200: %s", p, w.Code, w.Body.String())
		}
	}
	if got := rd.parses.Load(); got != 1 {
		t.Errorf("parses = %d, want 1 shared across the open path", got)
	}
}

// TestHandleSessionPositionsUsesCheapMeta locks the position-cache contract:
// the revision comes from the cheap meta scan (GetSession is never called on
// a cache hit) and equals render.PositionsRevision of that metadata.
func TestHandleSessionPositionsUsesCheapMeta(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rd := newMetaReader()
	srv := New(database, []reader.BaseSessionReader{rd})

	opts := render.Options{}
	wantRevision := render.PositionsRevision(rd.session, opts)
	if err := database.SavePositionCache("fake", "s1", wantRevision, 120, 42, []db.PositionEntry{
		{PositionKey: "k1", Kind: "user", LineStart: 3},
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/sessions/s1/positions?cols=120", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET positions -> %d: %s", w.Code, w.Body.String())
	}
	var resp positionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Revision != wantRevision {
		t.Errorf("revision = %d, want %d", resp.Revision, wantRevision)
	}
	if resp.TotalLines != 42 {
		t.Errorf("total_lines = %d, want 42 (DB cache)", resp.TotalLines)
	}
	if got := rd.metaCalls.Load(); got == 0 {
		t.Error("GetSessionMeta was not used")
	}
	if got := rd.getSessionCalls.Load(); got != 0 {
		t.Errorf("GetSession called %d times on a position-cache hit, want 0", got)
	}
	if got := rd.parses.Load(); got != 0 {
		t.Errorf("parses = %d on a position-cache hit, want 0", got)
	}
}
