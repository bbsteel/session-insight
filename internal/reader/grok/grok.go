// Package grok reads Grok Build TUI sessions from ~/.grok/sessions.
//
// Layout:
//
//	~/.grok/sessions/<url-encoded-cwd>/<uuid>/
//	  summary.json          metadata (title, cwd, model, timestamps)
//	  updates.jsonl         primary ACP stream for terminal replay
//	  chat_history.jsonl    compact transcript fallback
//	  events.jsonl          turn_started / turn_ended brackets
//
// Global sidecars (delete targets):
//
//	~/.grok/active_sessions.json
//	~/.grok/sessions/session_search.sqlite
//	~/.grok/sessions/<cwd>/prompt_history.jsonl
package grok

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

// GrokReader implements reader.BaseSessionReader for Grok Build sessions.
type GrokReader struct {
	sessionsDir string // ~/.grok/sessions
	grokHome    string // ~/.grok
	locMu       sync.RWMutex
	locs        map[string]sessionLoc
	// lineageMu guards childParent: child session UUID → parent session UUID
	// from structured subagents/*/meta.json (O(sessions+sidecars) build).
	lineageMu   sync.RWMutex
	childParent map[string]string
}

// New constructs a reader rooted at sessionsDir (~/.grok/sessions).
func New(sessionsDir string) *GrokReader {
	return &GrokReader{
		sessionsDir: sessionsDir,
		grokHome:    filepath.Dir(sessionsDir),
		locs:        make(map[string]sessionLoc),
	}
}

func (r *GrokReader) WatchRoots() []string { return []string{r.sessionsDir} }

func (r *GrokReader) AgentType() string   { return "grok" }
func (r *GrokReader) DisplayName() string { return "Grok" }

func validSessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

// summaryFile is the on-disk shape of summary.json.
type summaryFile struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	SessionSummary  string `json:"session_summary"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	LastActiveAt    string `json:"last_active_at"`
	NumMessages     int    `json:"num_messages"`
	NumChatMessages int    `json:"num_chat_messages"`
	CurrentModelID  string `json:"current_model_id"`
	GeneratedTitle  string `json:"generated_title"`
	GitRootDir      string `json:"git_root_dir"`
	HeadBranch      string `json:"head_branch"`
	AgentName       string `json:"agent_name"`
}

type sessionLoc struct {
	ID          string
	Dir         string // full path to session directory
	ProjectDir  string // parent cwd-encoded directory
	SummaryPath string
}

func (r *GrokReader) findSession(id string) (sessionLoc, error) {
	if !validSessionID(id) {
		return sessionLoc{}, fmt.Errorf("invalid grok session id: %q", id)
	}
	r.locMu.RLock()
	cached, ok := r.locs[id]
	r.locMu.RUnlock()
	if ok {
		if _, err := os.Stat(cached.SummaryPath); err == nil {
			return cached, nil
		}
	}
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		return sessionLoc{}, fmt.Errorf("grok session not found %q: %w", id, err)
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(r.sessionsDir, ent.Name(), id)
		sumPath := filepath.Join(dir, "summary.json")
		if _, err := os.Stat(sumPath); err == nil {
			loc := sessionLoc{
				ID:          id,
				Dir:         dir,
				ProjectDir:  filepath.Join(r.sessionsDir, ent.Name()),
				SummaryPath: sumPath,
			}
			r.locMu.Lock()
			r.locs[id] = loc
			r.locMu.Unlock()
			return loc, nil
		}
	}
	return sessionLoc{}, fmt.Errorf("grok session not found %q", id)
}

func readSummary(path string) (*summaryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s summaryFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

func (r *GrokReader) ListSessions() ([]model.Session, error) {
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// One O(sessions+sidecars) lineage pass for the whole tree so each child
	// gets IsSubagent / ParentSessionID without rescanning parents per child.
	lineage := r.scanChildParentIndex(nil)

	var sessions []model.Session
	locs := make(map[string]sessionLoc)
	for _, proj := range entries {
		if !proj.IsDir() {
			continue
		}
		projPath := filepath.Join(r.sessionsDir, proj.Name())
		subs, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			if !sub.IsDir() {
				continue
			}
			id := sub.Name()
			if !validSessionID(id) {
				continue
			}
			sumPath := filepath.Join(projPath, id, "summary.json")
			sum, err := readSummary(sumPath)
			if err != nil {
				continue
			}
			loc := sessionLoc{
				ID:          id,
				Dir:         filepath.Join(projPath, id),
				ProjectDir:  projPath,
				SummaryPath: sumPath,
			}
			locs[id] = loc
			sessions = append(sessions, r.buildSession(loc, sum, lineage))
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	r.locMu.Lock()
	r.locs = locs
	r.locMu.Unlock()
	r.storeLineage(lineage)
	return sessions, nil
}

func (r *GrokReader) buildSession(loc sessionLoc, sum *summaryFile, lineage map[string]string) model.Session {
	cwd := sum.Info.CWD
	if cwd == "" {
		// Decode url-encoded project dir name as a last resort.
		if dec, err := url.PathUnescape(filepath.Base(loc.ProjectDir)); err == nil && strings.HasPrefix(dec, "/") {
			cwd = dec
		}
	}
	created := parseTS(sum.CreatedAt)
	updated := parseTS(sum.UpdatedAt)
	if la := parseTS(sum.LastActiveAt); !la.IsZero() {
		// updated_at also advances when Grok writes a background session recap,
		// sometimes hours after the interactive turn completed. last_active_at is
		// the authoritative activity time when available; content mtimes below
		// still capture writes that race slightly after it.
		updated = la
	}
	// Prefer content-file mtime for LIVE badge / revision semantics.
	if mt := r.lastContentWrite(loc.Dir); mt.After(updated) {
		updated = mt
	}

	name := strings.TrimSpace(sum.GeneratedTitle)
	if name == "" {
		name = strings.TrimSpace(sum.SessionSummary)
	}
	if name == "" {
		name = "Session"
	}
	name = shared.TruncateRunes(strings.ReplaceAll(name, "\n", " "), 50)

	preview := r.scanPreview(loc.Dir)
	if preview == "" && sum.SessionSummary != "" {
		preview = shared.TruncateRunes(sum.SessionSummary, 200)
	}

	sess := model.Session{
		ID:           loc.ID,
		AgentType:    "grok",
		CWD:          cwd,
		Branch:       sum.HeadBranch,
		Project:      shared.ResolveProject(cwd, sum.GitRootDir),
		Name:         name,
		ModelName:    sum.CurrentModelID,
		ResumeID:     loc.ID, // CLI accepts the session UUID
		PreviewText:  preview,
		MessageCount: sum.NumMessages,
		CreatedAt:    created,
		UpdatedAt:    updated,
	}
	// Lineage is exact only when a structured subagent meta records this
	// session as child_session_id (or subagent_id). Never inferred from CWD,
	// timestamps, model, title, or UUID order. AgentPath has no honest Grok
	// source representation, so it stays empty.
	if parent, ok := lineage[loc.ID]; ok && parent != "" && parent != loc.ID {
		sess.IsSubagent = true
		sess.ParentSessionID = parent
	}
	return sess
}

// subagentMetaLineage is the minimal on-disk shape used for Session lineage.
// Full collaboration mapping lives in collaboration.go; this keeps list/get
// free of large update scans.
type subagentMetaLineage struct {
	SubagentID      string `json:"subagent_id"`
	ParentSessionID string `json:"parent_session_id"`
	ChildSessionID  string `json:"child_session_id"`
}

// scanChildParentIndex walks every session's subagents/*/meta.json once and
// returns child session UUID → parent session UUID. ctx may be nil. Conflicting
// parents for one child keep the first deterministic (sorted path) claim.
// Complexity: O(number of sessions + number of sidecars), not O(S²).
func (r *GrokReader) scanChildParentIndex(ctx interface{ Err() error }) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		return out
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, proj := range entries {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return out
			}
		}
		if !proj.IsDir() {
			continue
		}
		projPath := filepath.Join(r.sessionsDir, proj.Name())
		subs, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
		for _, sub := range subs {
			if ctx != nil {
				if err := ctx.Err(); err != nil {
					return out
				}
			}
			if !sub.IsDir() || !validSessionID(sub.Name()) {
				continue
			}
			sessionDir := filepath.Join(projPath, sub.Name())
			// Collect lineage from subagent sidecars even when this directory
			// lacks summary.json (incomplete write); children may still need
			// parent_session_id from these metas.
			r.collectSubagentLineage(sessionDir, out, ctx)
		}
	}
	return out
}

func (r *GrokReader) collectSubagentLineage(sessionDir string, out map[string]string, ctx interface{ Err() error }) {
	subRoot := filepath.Join(sessionDir, "subagents")
	entries, err := os.ReadDir(subRoot)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, ent := range entries {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return
			}
		}
		if !ent.IsDir() {
			continue
		}
		metaPath := filepath.Join(subRoot, ent.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta subagentMetaLineage
		if json.Unmarshal(data, &meta) != nil {
			continue
		}
		parent := strings.TrimSpace(meta.ParentSessionID)
		child := strings.TrimSpace(meta.ChildSessionID)
		if child == "" {
			child = strings.TrimSpace(meta.SubagentID)
		}
		if parent == "" || child == "" || parent == child {
			continue
		}
		if !validSessionID(parent) || !validSessionID(child) {
			continue
		}
		if _, exists := out[child]; exists {
			// Deterministic: first sorted path wins; do not thrash on conflicts.
			continue
		}
		out[child] = parent
	}
}

func (r *GrokReader) storeLineage(lineage map[string]string) {
	r.lineageMu.Lock()
	r.childParent = lineage
	r.lineageMu.Unlock()
}

// ensureLineage returns child→parent, building the index when GetSession runs
// cold (before ListSessions). Safe for concurrent readers.
func (r *GrokReader) ensureLineage() map[string]string {
	r.lineageMu.RLock()
	if r.childParent != nil {
		cp := r.childParent
		r.lineageMu.RUnlock()
		return cp
	}
	r.lineageMu.RUnlock()

	r.lineageMu.Lock()
	defer r.lineageMu.Unlock()
	if r.childParent != nil {
		return r.childParent
	}
	r.childParent = r.scanChildParentIndex(nil)
	return r.childParent
}

func (r *GrokReader) scanPreview(dir string) string {
	// Prefer clean user prompts from updates.jsonl.
	updatesPath := filepath.Join(dir, "updates.jsonl")
	if msgs := scanUpdateUserPrompts(updatesPath, 5); len(msgs) > 0 {
		return shared.BuildPreviewText(msgs)
	}
	chatPath := filepath.Join(dir, "chat_history.jsonl")
	if msgs := scanChatUserPrompts(chatPath, 5); len(msgs) > 0 {
		return shared.BuildPreviewText(msgs)
	}
	return ""
}

func scanUpdateUserPrompts(path string, limit int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var line rawUpdateLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		var u rawUpdate
		if json.Unmarshal(line.Params.Update, &u) != nil {
			continue
		}
		if u.SessionUpdate != "user_message_chunk" {
			continue
		}
		text := strings.TrimSpace(textFromContent(u.Content))
		if text == "" {
			continue
		}
		out = append(out, text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scanChatUserPrompts(path string, limit int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var msg chatMsg
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		if msg.Type != "user" {
			continue
		}
		text := extractUserQuery(msg.contentText())
		if text == "" {
			continue
		}
		out = append(out, text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// extractUserQuery pulls <user_query>…</user_query> when present, otherwise
// returns non-meta user text (skips system-reminder / user_info blobs).
func extractUserQuery(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	const open, close = "<user_query>", "</user_query>"
	if i := strings.Index(text, open); i >= 0 {
		rest := text[i+len(open):]
		if j := strings.Index(rest, close); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	// Skip injected context blocks.
	if strings.Contains(text, "<system-reminder>") ||
		strings.Contains(text, "<user_info>") ||
		strings.Contains(text, "<git_status>") ||
		strings.Contains(text, "<agent_skills>") {
		return ""
	}
	return text
}

func (r *GrokReader) GetSession(id string) (*model.SessionDetail, error) {
	loc, err := r.findSession(id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, readerr.New(readerr.SourceMissing, "source_missing", err)
		}
		// findSession wraps "not found" without IsNotExist when the id is absent.
		if strings.Contains(err.Error(), "not found") {
			return nil, readerr.New(readerr.SourceMissing, "source_missing", err)
		}
		return nil, readerr.New(readerr.SourceUnreadable, "source_unreadable", err)
	}
	sum, err := readSummary(loc.SummaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, readerr.New(readerr.SourceMissing, "source_missing",
				fmt.Errorf("grok session not found %q: %w", id, err))
		}
		return nil, readerr.New(readerr.SourceUnreadable, "source_unreadable",
			fmt.Errorf("grok session unreadable %q: %w", id, err))
	}
	// Cold GetSession must agree with ListSessions on lineage; build the
	// index when list has not run yet.
	session := r.buildSession(loc, sum, r.ensureLineage())
	turns, billing := buildTurnsAndBilling(loc.Dir, sum.CurrentModelID)
	session.TurnCount = len(turns)
	msgCount := 0
	for _, t := range turns {
		msgCount += len(t.Events)
	}
	if msgCount > 0 {
		session.MessageCount = msgCount
	}
	filtered := shared.FilterEmptyTurns(turns)
	if filtered == nil {
		filtered = []model.TurnVM{}
	}
	detail := &model.SessionDetail{
		Session: session,
		Turns:   filtered,
		Billing: billing,
	}
	detail.AnomalySummary = shared.RunAnomalyDetection(detail.Turns)
	p := provenance.Build(provenance.Input{
		CapturedAt:        time.Now().UTC(),
		AdapterRevision:   Capabilities().AdapterRevision,
		Sources:           sourceInventory(loc.Dir, loc.SummaryPath),
		HasReplayableBody: len(filtered) > 0,
	})
	detail.Provenance = &p
	return detail, nil
}

func (r *GrokReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	loc, err := r.findSession(id)
	if err != nil {
		return nil, err
	}
	return r.toRenderEvents(loc)
}

func (r *GrokReader) RenderANSI(id string, cols int) (string, error) {
	events, err := r.GetRenderEvents(id)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

// LiveRevision is a cheap mtime+size fingerprint of the content files that
// grow while a turn is live. Must not parse file contents.
func (r *GrokReader) LiveRevision(id string) (int64, error) {
	loc, err := r.findSession(id)
	if err != nil {
		return 0, err
	}
	var rev int64
	for _, name := range []string{"updates.jsonl", "chat_history.jsonl", "events.jsonl"} {
		info, err := os.Stat(filepath.Join(loc.Dir, name))
		if err != nil {
			continue
		}
		v := info.ModTime().UnixNano() + info.Size()
		if v > rev {
			rev = v
		}
	}
	if rev == 0 {
		return 0, fmt.Errorf("grok session has no content files: %s", id)
	}
	return rev, nil
}

func (r *GrokReader) lastContentWrite(dir string) time.Time {
	var latest time.Time
	for _, name := range []string{"updates.jsonl", "chat_history.jsonl", "events.jsonl"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
	}
	return latest
}

// ---- turn / billing from updates (or chat fallback) ----

func buildTurnsAndBilling(dir, modelName string) ([]model.TurnVM, *model.SessionBilling) {
	updatesPath := filepath.Join(dir, "updates.jsonl")
	if _, err := os.Stat(updatesPath); err == nil {
		return turnsFromUpdates(updatesPath, modelName)
	}
	return turnsFromChat(filepath.Join(dir, "chat_history.jsonl"), modelName), nil
}

func turnsFromUpdates(path, modelName string) ([]model.TurnVM, *model.SessionBilling) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var (
		turns      []model.TurnVM
		current    *model.TurnVM
		billing    = &model.SessionBilling{Precision: model.PrecisionMissing}
		modelUsage = map[string]*model.ModelUsage{}
		hasUsage   bool
	)

	flush := func() {
		if current != nil {
			turns = append(turns, *current)
			current = nil
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var line rawUpdateLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		var u rawUpdate
		if json.Unmarshal(line.Params.Update, &u) != nil {
			continue
		}
		ts := tsFromUnix(line.Timestamp)

		switch u.SessionUpdate {
		case "user_message_chunk":
			text := strings.TrimSpace(textFromContent(u.Content))
			if text == "" {
				continue
			}
			flush()
			current = &model.TurnVM{
				TurnIndex:   len(turns),
				UserMessage: text,
				Events: []model.EventVM{{
					Type:      "user_message",
					Timestamp: ts.Format(time.RFC3339Nano),
					Data:      map[string]any{"content": text},
				}},
			}

		case "agent_message_chunk":
			if current == nil {
				continue
			}
			text := textFromContent(u.Content)
			if text == "" {
				continue
			}
			current.AssistantMessage += text
			current.Events = append(current.Events, model.EventVM{
				Type:      "assistant_message",
				Timestamp: ts.Format(time.RFC3339Nano),
				Data:      map[string]any{"content": text},
			})

		case "agent_thought_chunk":
			if current == nil {
				continue
			}
			text := textFromContent(u.Content)
			if text == "" {
				continue
			}
			current.Events = append(current.Events, model.EventVM{
				Type:      "reasoning",
				Timestamp: ts.Format(time.RFC3339Nano),
				Data:      map[string]any{"content": text},
			})

		case "tool_call":
			if current == nil {
				continue
			}
			name := toolNameFromRaw(u)
			input := rawToMap(u.RawInput)
			// Same skill rewrite as render path: SKILL.md read → Skill + turn.Skills.
			if skill := skillNameFromRead(name, input); skill != "" {
				current.Skills = append(current.Skills, skill)
				name = "Skill"
			}
			current.ToolCallCount++
			current.ToolNames = append(current.ToolNames, name)
			current.ToolDetails = append(current.ToolDetails, model.ToolCallVM{Name: name})
			current.Events = append(current.Events, model.EventVM{
				Type:      "tool_call",
				Timestamp: ts.Format(time.RFC3339Nano),
				Data: map[string]any{
					"toolCallId": u.ToolCallID,
					"name":       name,
				},
			})

		case "turn_completed":
			if current == nil {
				continue
			}
			if u.Usage != nil {
				hasUsage = true
				tu := usageToTokenUsage(u.Usage)
				current.TokenUsage.AddUsage(tu)
				current.RequestCount += maxInt(1, u.Usage.ModelCalls)
				if u.Usage.APIDurationMs > 0 {
					current.DurationMs = u.Usage.APIDurationMs
				}
				billing.Totals.AddUsage(tu)
				for m, mu := range u.Usage.ModelUsage {
					slot := modelUsage[m]
					if slot == nil {
						slot = &model.ModelUsage{Model: m}
						modelUsage[m] = slot
					}
					slot.Usage.AddUsage(usageEntryToTokenUsage(mu))
					slot.Requests += maxInt(1, mu.ModelCalls)
				}
			}
			_ = modelName
		}
	}
	flush()

	if !hasUsage {
		return turns, &model.SessionBilling{Precision: model.PrecisionMissing}
	}
	billing.Precision = model.PrecisionExact
	for _, mu := range modelUsage {
		billing.ByModel = append(billing.ByModel, *mu)
	}
	sort.Slice(billing.ByModel, func(i, j int) bool {
		return billing.ByModel[i].Model < billing.ByModel[j].Model
	})
	return turns, billing
}

func turnsFromChat(path, modelName string) []model.TurnVM {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var turns []model.TurnVM
	var current *model.TurnVM
	flush := func() {
		if current != nil {
			turns = append(turns, *current)
			current = nil
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var msg chatMsg
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch msg.Type {
		case "user":
			text := extractUserQuery(msg.contentText())
			if text == "" {
				continue
			}
			flush()
			current = &model.TurnVM{
				TurnIndex:   len(turns),
				UserMessage: text,
				Events: []model.EventVM{{
					Type: "user_message",
					Data: map[string]any{"content": text},
				}},
			}
		case "assistant":
			if current == nil {
				continue
			}
			text := msg.contentText()
			current.AssistantMessage += text
			if msg.ModelID != "" {
				modelName = msg.ModelID
			}
			current.RequestCount++
			current.Events = append(current.Events, model.EventVM{
				Type: "assistant_message",
				Data: map[string]any{"content": text, "model": modelName},
			})
		case "tool_result":
			if current == nil {
				continue
			}
			current.ToolCallCount++
			current.Events = append(current.Events, model.EventVM{
				Type: "tool_result",
				Data: map[string]any{"tool_call_id": msg.ToolCallID, "content": msg.contentText()},
			})
		case "reasoning":
			if current == nil {
				continue
			}
			current.Events = append(current.Events, model.EventVM{
				Type: "reasoning",
				Data: map[string]any{"summary": msg.Summary},
			})
		}
	}
	flush()
	return turns
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func usageToTokenUsage(u *turnUsage) model.TokenUsage {
	if u == nil {
		return model.TokenUsage{}
	}
	// Grok reports inputTokens including cache hits (DeepSeek-style inclusive).
	// Canonical buckets are exclusive: prompt = input - cache_read.
	prompt := u.InputTokens - u.CachedReadTokens
	if prompt < 0 {
		prompt = u.InputTokens
	}
	tu := model.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: u.OutputTokens,
		ReasoningTokens:  u.ReasoningTokens,
		CacheReadTokens:  u.CachedReadTokens,
		Present: model.TokenPresence{
			Input:     model.PresenceExact,
			Output:    model.PresenceExact,
			CacheRead: model.PresenceExact,
			// Grok does not surface cache write as a billed bucket.
			CacheWrite: model.PresenceNA,
		},
	}
	if u.ReasoningTokens > 0 {
		tu.Present.Reasoning = model.PresenceExact
	}
	return tu
}

func usageEntryToTokenUsage(u modelUsageEntry) model.TokenUsage {
	return usageToTokenUsage(&turnUsage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CachedReadTokens: u.CachedReadTokens,
		ReasoningTokens:  u.ReasoningTokens,
		ModelCalls:       u.ModelCalls,
		APIDurationMs:    u.APIDurationMs,
	})
}

// ---- shared wire types ----

// toolContent is a tool result content block in tool_call_update.
type toolContent struct {
	Type    string `json:"type"`
	Content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type turnUsage struct {
	InputTokens      int64                      `json:"inputTokens"`
	OutputTokens     int64                      `json:"outputTokens"`
	TotalTokens      int64                      `json:"totalTokens"`
	CachedReadTokens int64                      `json:"cachedReadTokens"`
	ReasoningTokens  int64                      `json:"reasoningTokens"`
	ModelCalls       int                        `json:"modelCalls"`
	APIDurationMs    int64                      `json:"apiDurationMs"`
	ModelUsage       map[string]modelUsageEntry `json:"modelUsage"`
	NumTurns         int                        `json:"numTurns"`
}

type modelUsageEntry struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	CachedReadTokens int64 `json:"cachedReadTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
	ModelCalls       int   `json:"modelCalls"`
	APIDurationMs    int64 `json:"apiDurationMs"`
}

type chatMsg struct {
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content"`
	ModelID    string          `json:"model_id"`
	ToolCallID string          `json:"tool_call_id"`
	Summary    json.RawMessage `json:"summary"`
	ID         string          `json:"id"`
	Status     string          `json:"status"`
}

func (m chatMsg) contentText() string {
	if len(m.Content) == 0 {
		return ""
	}
	// string form
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	// array of {type,text}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return string(m.Content)
}
