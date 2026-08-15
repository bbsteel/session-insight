package server

import (
	"container/list"
	"os"
	"slices"
	"strconv"
	"sync"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/render"
)

// replayCache is the server-level, in-memory cache for the replay open path.
//
// Without it, opening one session fires four endpoints (render, edits,
// tool-outputs, positions) that each re-parse the whole transcript file — a
// 58MB Codex rollout costs ~3s per parse, paid on every single open. The
// cache shares one parse across all consumers and keeps the rendered ANSI
// text per (revision, cols, options), so a repeat open of an unchanged
// session is served from memory.
//
// Validation is stat-only: entries key off reader.LiveRevisionProvider's
// revision (mtime+size), so a live-tailing session that grows on disk
// invalidates automatically. Readers without that capability (imported
// bundles) bypass the cache entirely.
//
// The cache is reader-agnostic and keyed by agent type + session id; parse
// errors are never cached so the handler reader-loop fallthrough semantics
// are preserved.
type replayCache struct {
	mu sync.Mutex

	events      map[string]*replayEventsEntry
	eventsLRU   *list.List // of string keys, front = most recently used
	eventsBytes int64

	ansi      map[string]*replayANSIEntry
	ansiLRU   *list.List // of string keys, front = most recently used
	ansiBytes int64

	// inflight is a hand-rolled singleflight: concurrent opens of the same
	// session share one parse instead of stampeding the reader.
	inflight map[string]*replayParseCall

	maxBytes int64 // per sub-cache (events and ANSI each)
}

type replayEventsEntry struct {
	liveRev int64
	events  []model.RenderEvent
	bytes   int64
	lruElem *list.Element
}

type replayANSIEntry struct {
	ansi    string
	bytes   int64
	lruElem *list.Element
}

type replayParseCall struct {
	done   chan struct{}
	events []model.RenderEvent
	err    error
}

const defaultReplayCacheMB = 256

func newReplayCache() *replayCache {
	maxMB := int64(defaultReplayCacheMB)
	if v := os.Getenv("SI_REPLAY_CACHE_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxMB = n
		}
	}
	return &replayCache{
		events:    make(map[string]*replayEventsEntry),
		eventsLRU: list.New(),
		ansi:      make(map[string]*replayANSIEntry),
		ansiLRU:   list.New(),
		inflight:  make(map[string]*replayParseCall),
		maxBytes:  maxMB << 20,
	}
}

// estimateEventsBytes approximates the retained size of an event slice:
// a per-event struct/header allowance plus the big string fields.
func estimateEventsBytes(events []model.RenderEvent) int64 {
	total := int64(len(events)) * 256
	for i := range events {
		e := &events[i]
		total += int64(len(e.Text) + len(e.Stdout) + len(e.Stderr))
	}
	return total
}

// renderEventsFor returns the session's render events, served from the cache
// when the on-disk source is unchanged. The returned slice is always a fresh
// clone: formatters mutate event fields in place (e.g. the grok profile's
// Preprocess), so callers must never receive the cached backing array.
func (s *Server) renderEventsFor(rd reader.BaseSessionReader, id string) ([]model.RenderEvent, error) {
	events, _, err := s.replay.eventsFor(rd, id)
	return events, err
}

func (c *replayCache) eventsFor(rd reader.BaseSessionReader, id string) ([]model.RenderEvent, int64, error) {
	lp, ok := rd.(reader.LiveRevisionProvider)
	if !ok {
		// No stat-level revision → no safe invalidation; parse every time.
		events, err := rd.GetRenderEvents(id)
		return events, 0, err
	}
	rev, err := lp.LiveRevision(id)
	if err != nil {
		// Preserve the handler reader-loop semantics: an error here means
		// "not this reader's session" (or an unreadable source) and the
		// caller moves on to the next reader.
		return nil, 0, err
	}
	key := rd.AgentType() + "\x00" + id

	c.mu.Lock()
	if entry, hit := c.events[key]; hit && entry.liveRev == rev {
		c.eventsLRU.MoveToFront(entry.lruElem)
		events := slices.Clone(entry.events)
		c.mu.Unlock()
		return events, rev, nil
	}
	if call, flying := c.inflight[key]; flying {
		c.mu.Unlock()
		<-call.done
		if call.err != nil {
			return nil, 0, call.err
		}
		return slices.Clone(call.events), rev, nil
	}
	call := &replayParseCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	events, parseErr := rd.GetRenderEvents(id)

	c.mu.Lock()
	delete(c.inflight, key)
	if parseErr == nil {
		// Re-stat after the parse: a session that grew mid-parse is served
		// but not cached, so the next request re-parses the full source.
		if revAfter, statErr := lp.LiveRevision(id); statErr == nil && revAfter == rev {
			c.storeEventsLocked(key, rev, events)
		}
	}
	call.events = events
	call.err = parseErr
	close(call.done)
	c.mu.Unlock()

	if parseErr != nil {
		return nil, 0, parseErr
	}
	return slices.Clone(events), rev, nil
}

func (c *replayCache) storeEventsLocked(key string, rev int64, events []model.RenderEvent) {
	if old, exists := c.events[key]; exists {
		c.eventsLRU.Remove(old.lruElem)
		c.eventsBytes -= old.bytes
		delete(c.events, key)
	}
	bytes := estimateEventsBytes(events)
	if bytes > c.maxBytes/2 {
		// Single oversized entry: serve it, but never let one session
		// monopolize the cache.
		return
	}
	entry := &replayEventsEntry{liveRev: rev, events: events, bytes: bytes}
	entry.lruElem = c.eventsLRU.PushFront(key)
	c.events[key] = entry
	c.eventsBytes += bytes
	for c.eventsBytes > c.maxBytes {
		tail := c.eventsLRU.Back()
		if tail == nil {
			break
		}
		victimKey := tail.Value.(string)
		victim := c.events[victimKey]
		c.eventsBytes -= victim.bytes
		delete(c.events, victimKey)
		c.eventsLRU.Remove(tail)
	}
}

// renderANSIFor returns the formatted ANSI render for (session, cols, opts),
// cached per content revision so a repeat open at the same width skips both
// the parse and the format pass.
func (s *Server) renderANSIFor(rd reader.BaseSessionReader, id string, cols int, opts render.Options) (string, error) {
	c := s.replay
	events, rev, err := c.eventsFor(rd, id)
	if err != nil {
		return "", err
	}
	if rev == 0 {
		// Uncacheable reader (no LiveRevisionProvider): render directly.
		return render.FormatEventsOpts(events, cols, opts), nil
	}

	key := rd.AgentType() + "\x00" + id + "\x00" + strconv.FormatInt(rev, 10) +
		"\x00" + strconv.Itoa(cols) + "\x00" + strconv.FormatInt(opts.Mask(), 10)

	c.mu.Lock()
	if entry, hit := c.ansi[key]; hit {
		c.ansiLRU.MoveToFront(entry.lruElem)
		ansi := entry.ansi
		c.mu.Unlock()
		return ansi, nil
	}
	c.mu.Unlock()

	ansi := render.FormatEventsOpts(events, cols, opts)

	c.mu.Lock()
	bytes := int64(len(ansi))
	if bytes <= c.maxBytes/2 {
		entry := &replayANSIEntry{ansi: ansi, bytes: bytes}
		entry.lruElem = c.ansiLRU.PushFront(key)
		c.ansi[key] = entry
		c.ansiBytes += bytes
		for c.ansiBytes > c.maxBytes {
			tail := c.ansiLRU.Back()
			if tail == nil {
				break
			}
			victimKey := tail.Value.(string)
			victim := c.ansi[victimKey]
			c.ansiBytes -= victim.bytes
			delete(c.ansi, victimKey)
			c.ansiLRU.Remove(tail)
		}
	}
	c.mu.Unlock()
	return ansi, nil
}
