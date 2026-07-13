package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// eventHub fans a "sessions changed" signal out to all connected SSE clients.
// Events carry no payload — the sidebar refetches /api/sessions, which scans
// disk live, so a bare ping is always sufficient and never stale.
type eventHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[chan struct{}]struct{})}
}

func (h *eventHub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// broadcast is non-blocking: each subscriber channel has capacity 1, so a
// slow client coalesces pending pings instead of stalling the watcher.
func (h *eventHub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// NotifySessionsChanged bumps the list revision (invalidating /api/sessions
// ETags) and pushes a sessions_changed event to every connected client.
// Called after an index round that actually changed data (wired in main).
func (s *Server) NotifySessionsChanged() {
	s.listRev.Add(1)
	s.events.broadcast()
}

// bumpListRev invalidates /api/sessions ETags without broadcasting — for
// serve-path mutations (bookmark/title) where the acting client already
// updates its own state.
func (s *Server) bumpListRev() {
	s.listRev.Add(1)
}

// handleEvents streams sessions_changed pings over SSE. The browser-side
// EventSource auto-reconnects, so a server restart self-heals without any
// frontend retry logic.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 先发一条注释确认连接建立（EventSource 的 open 依赖首个字节到达）
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	// 心跳防止代理/浏览器判定连接空闲（vite dev 代理链路上尤其需要）
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: sessions_changed\ndata: {}\n\n")
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
