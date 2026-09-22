package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/home"
	"github.com/bbsteel/session-insight/internal/llm"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
)

type homeFactCache struct {
	mu    sync.Mutex
	items map[string]homeFactEntry
}

type homeFactEntry struct {
	updatedAt time.Time
	fact      home.SessionFact
}

func newHomeFactCache() *homeFactCache {
	return &homeFactCache{items: map[string]homeFactEntry{}}
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	query, err := parseHomeQuery(r.URL.Query(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessions, err := s.DB.ListSessionSummaries("")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	notes, err := s.bookmarkNotes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	titles := s.titleOverrides()
	var roots, children []model.Session
	for _, session := range sessions {
		if llm.IsScratchCWD(session.CWD) {
			continue
		}
		if session.IsSubagent {
			children = append(children, session)
			continue
		}
		roots = append(roots, session)
	}
	facts := make([]home.SessionFact, 0, len(roots))
	indexByKey := map[string]int{}
	for _, session := range roots {
		fact := s.cachedHomeFact(session)
		applyHomeIdentity(&fact, session, notes, titles)
		indexByKey[db.BookmarkKey(session.AgentType, session.ID)] = len(facts)
		facts = append(facts, fact)
	}
	unattributed := 0
	for _, session := range children {
		parentKey := db.BookmarkKey(session.AgentType, session.ParentSessionID)
		index, ok := indexByKey[parentKey]
		if !ok || session.ParentSessionID == "" {
			unattributed++
			continue
		}
		child := s.cachedHomeFact(session)
		home.AbsorbChild(&facts[index], child)
	}
	report := home.Aggregate(facts, query, time.Now())
	report.Coverage.UnattributedChildSessions += unattributed
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(report) //nolint:errcheck
}

func applyHomeIdentity(fact *home.SessionFact, session model.Session, notes, titles map[string]string) {
	key := db.BookmarkKey(session.AgentType, session.ID)
	if titles != nil {
		if title, ok := titles[key]; ok && title != "" {
			fact.Name = title
		}
	}
	if notes == nil {
		return
	}
	if note, ok := notes[key]; ok {
		fact.Bookmarked = true
		_ = note
	}
}

func (s *Server) cachedHomeFact(session model.Session) home.SessionFact {
	if s.homeFacts == nil {
		s.homeFacts = newHomeFactCache()
	}
	key := db.BookmarkKey(session.AgentType, session.ID)
	freshEnough := time.Since(session.UpdatedAt) >= model.LiveWindow
	s.homeFacts.mu.Lock()
	entry, ok := s.homeFacts.items[key]
	s.homeFacts.mu.Unlock()
	if ok && entry.updatedAt.Equal(session.UpdatedAt) && freshEnough {
		cloned := entry.fact.Clone()
		cloned.Live = false
		cloned.Signals.Live = false
		return cloned
	}
	fact := s.loadHomeFact(session)
	s.homeFacts.mu.Lock()
	s.homeFacts.items[key] = homeFactEntry{updatedAt: session.UpdatedAt, fact: fact}
	s.homeFacts.mu.Unlock()
	return fact.Clone()
}

func (s *Server) loadHomeFact(session model.Session) home.SessionFact {
	fact := home.SessionFact{
		ID: session.ID, AgentType: session.AgentType, Project: session.Project,
		Name: session.Name, UpdatedAt: session.UpdatedAt, DetailMissing: true,
	}
	rd := readerByAgent(s.Readers, session.AgentType)
	if rd == nil {
		return fact
	}
	detail, err := rd.GetSession(session.ID)
	if err != nil || detail == nil {
		log.Printf("GET /api/home: session %s/%s: %v", session.AgentType, session.ID, err)
		return fact
	}
	if static, ok := reader.AgentDefinition(session.AgentType); ok {
		resolved, err := reader.ResolveSessionCapabilities(rd, detail, static)
		if err == nil {
			detail.IsLive = resolved.Liveness.IsLive
		} else {
			detail.IsLive = model.IsSessionLive(detail.UpdatedAt)
		}
	} else {
		detail.IsLive = model.IsSessionLive(detail.UpdatedAt)
	}
	events, err := rd.GetRenderEvents(session.ID)
	if err != nil {
		events = nil
	}
	fact = home.BuildFact(detail, events)
	if s.DB != nil {
		envelope, ok, err := s.DB.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
		if err != nil {
			log.Printf("GET /api/home: git evidence %s/%s: %v", session.AgentType, session.ID, err)
		} else if ok {
			changes, untimed := home.CodeFromEnvelope(envelope)
			fact.Code = append(fact.Code, changes...)
			fact.UntimedCode += untimed
		}
	}
	return fact
}

func readerByAgent(readers []reader.BaseSessionReader, agentType string) reader.BaseSessionReader {
	for _, rd := range readers {
		if rd.AgentType() == agentType {
			return rd
		}
	}
	return nil
}

func parseHomeQuery(values url.Values, now time.Time) (home.Query, error) {
	query := home.Query{WindowDays: 7, Now: now, Location: time.Local}
	if raw := values.Get("days"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || (days != 7 && days != 30) {
			return home.Query{}, errHomeQuery("days must be 7 or 30")
		}
		query.WindowDays = days
	}
	if day := values.Get("day"); day != "" {
		if _, err := time.ParseInLocation("2006-01-02", day, time.Local); err != nil {
			return home.Query{}, errHomeQuery("day must be YYYY-MM-DD")
		}
		query.Day = day
	}
	query.Project = values.Get("project")
	query.Agent = values.Get("agent")
	query.FocusKind = values.Get("focus_kind")
	query.FocusValue = values.Get("focus_value")
	switch query.FocusKind {
	case "", home.FocusHealth, home.FocusTool, home.FocusSkill:
	default:
		return home.Query{}, errHomeQuery("focus_kind must be health, tool, or skill")
	}
	if query.FocusKind == "" {
		query.FocusValue = ""
	}
	return query, nil
}

type homeQueryError string

func (e homeQueryError) Error() string { return string(e) }

func errHomeQuery(message string) error { return homeQueryError(message) }
