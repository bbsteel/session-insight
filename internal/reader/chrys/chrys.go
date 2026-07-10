package chrys

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

// ChrysReader reads Chrys sessions from ~/.chrys/sessions/<id>/session.json.
// Each session directory also holds sub_agents/sessions/*.json (complete
// sub-agent transcripts, joined back to the parent via the function_call's
// call_id) and mutations/ (file-edit snapshots, not consumed here).
type ChrysReader struct {
	sessionsDir string
}

func New(sessionsDir string) *ChrysReader {
	return &ChrysReader{sessionsDir: sessionsDir}
}

func (r *ChrysReader) AgentType() string   { return "chrys" }
func (r *ChrysReader) DisplayName() string { return "Chrys" }

// ---- session.json shapes ----

type sessionFile struct {
	Meta  sessionMeta  `json:"meta"`
	State sessionState `json:"state"`
}

type sessionMeta struct {
	SessionID        string   `json:"session_id"`
	AgentProfile     string   `json:"agent_profile"`
	AgentDisplayName string   `json:"agent_display_name"`
	ModelID          string   `json:"model_id"`
	ModelProvider    string   `json:"model_provider"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	MessageCount     int      `json:"message_count"`
	PrimaryCWD       string   `json:"primary_cwd"`
	WorkingDirs      []string `json:"working_dirs"`
	Title            string   `json:"title"`
	CustomTitle      string   `json:"custom_title"`
	GeneratedTitle   string   `json:"generated_title"`

	// Sub-agent sidecar fields (record_type == "sub_agent_session").
	RecordType           string `json:"record_type"`
	ParentProviderCallID string `json:"parent_provider_call_id"`
	ToolName             string `json:"tool_name"`
	Status               string `json:"status"`
}

type sessionState struct {
	Messages       []chrysMessage    `json:"messages"`
	CompressedMsgs []json.RawMessage `json:"compressed_msgs"`
	TurnCounter    int               `json:"turn_counter"`
	// DeepSeek-style inclusive semantics: input includes cache hits.
	TotalInput    int64 `json:"total_session_input_tokens"`
	TotalOutput   int64 `json:"total_session_output_tokens"`
	TotalCacheHit int64 `json:"total_session_cache_hit_tokens"`
}

type chrysMessage struct {
	Type     string         `json:"type"`
	Role     string         `json:"role"`
	Contents []chrysContent `json:"contents"`
	Props    map[string]any `json:"additional_properties"`
}

type chrysContent struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// function_call. Arguments is a JSON-encoded string in current chrys
	// versions but a plain JSON object in older session files — keep the raw
	// bytes and let argsMap decode either form.
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`

	// function_result
	Result    string       `json:"result"`
	Exception string       `json:"exception"`
	Items     []resultItem `json:"items"`

	// data (inline image)
	URI       string `json:"uri"`
	MediaType string `json:"media_type"`

	Props map[string]any `json:"additional_properties"`
}

type resultItem struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	MediaType string `json:"media_type"`
}

// markerKind returns the _chrys_kind marker ("turn", "interrupted", ...) or "".
func (m *chrysMessage) markerKind() string {
	if m.Props == nil {
		return ""
	}
	s, _ := m.Props["_chrys_kind"].(string)
	return s
}

// interruptedBy returns the interrupted marker's _interrupted_by source:
// "user" (Esc) or "error" (execution failure) for a genuine interruption, or
// "" for chrys's in-flight recovery checkpoint — the sidecar snapshot chrys
// writes at every LLM boundary of a turn that is still running (checkpoint.py
// shapes it with source=""). An empty source therefore means "the turn hasn't
// finished", not "the turn was interrupted".
func (m *chrysMessage) interruptedBy() string {
	if m.Props == nil {
		return ""
	}
	s, _ := m.Props["_interrupted_by"].(string)
	return s
}

// isInFlightCheckpoint reports whether an interrupted marker is chrys's
// crash-recovery checkpoint for a turn still in progress, rather than a real
// user/error interruption.
func (m *chrysMessage) isInFlightCheckpoint() bool {
	return m.markerKind() == "interrupted" && m.interruptedBy() == ""
}

func (m *chrysMessage) createdAt() time.Time {
	if m.Props == nil {
		return time.Time{}
	}
	s, _ := m.Props["_chrys_created_at"].(string)
	return parseTS(s)
}

// groupTokenCount returns the message's _group.token_count, chrys's
// per-message token accounting (0 when absent).
func (m *chrysMessage) groupTokenCount() int64 {
	if m.Props == nil {
		return 0
	}
	g, _ := m.Props["_group"].(map[string]any)
	if g == nil {
		return 0
	}
	if v, ok := g["token_count"].(float64); ok {
		return int64(v)
	}
	return 0
}

// intermediateText is assistant text chrys displays before the message's tool
// calls but stores outside contents (additional_properties._intermediate_text).
func (m *chrysMessage) intermediateText() string {
	if m.Props == nil {
		return ""
	}
	s, _ := m.Props["_intermediate_text"].(string)
	return s
}

func (c *chrysContent) failed() bool {
	// Top-level "failed" is checked first: some chrys sessions have a
	// disagreement where the top-level failed=true (set by the tool wrapper
	// for e.g. argument parsing errors) but _chrys_tool_result_metadata has
	// failed=false or shell_exit_code=0 (set by the tool runtime which ran
	// fine). The top-level field is the authoritative failure signal because
	// it represents the user-visible result, not the internal execution state.
	if c.Props != nil {
		if b, _ := c.Props["failed"].(bool); b {
			return true
		}
	}
	// Structured metadata adds timeout/rejection/exit-code detection that
	// the legacy top-level field cannot express.
	if meta := c.toolResultMetadata(); meta != nil {
		return toolResultMetadataFailureState(meta)
	}
	return false
}

func (c *chrysContent) errorMessage() string {
	if meta := c.toolResultMetadata(); meta != nil {
		if s, _ := meta["tool_error_message"].(string); s != "" {
			return s
		}
	}
	if c.Props != nil {
		if s, _ := c.Props["tool_error_message"].(string); s != "" {
			return s
		}
	}
	return c.Exception
}

// toolResultMetadata returns the nested _chrys_tool_result_metadata dict
// from function_result additional_properties, or nil when absent (old sessions).
func (c *chrysContent) toolResultMetadata() map[string]any {
	if c.Props == nil {
		return nil
	}
	raw, _ := c.Props["_chrys_tool_result_metadata"].(map[string]any)
	return raw
}

// toolKind returns the _chrys_tool_kind from function_call additional_properties.
func (c *chrysContent) toolKind() string {
	if c.Props == nil {
		return ""
	}
	s, _ := c.Props["_chrys_tool_kind"].(string)
	return s
}

// exitCode resolves the actual exit code from structured metadata, falling
// back to -1 (unknown) when metadata is absent. Shell exit codes take
// precedence over generic process exit codes.
func (c *chrysContent) exitCode() int {
	meta := c.toolResultMetadata()
	if meta != nil {
		if code := intFromMeta(meta, "shell_exit_code"); code != nil {
			return *code
		}
		if code := intFromMeta(meta, "process_exit_code"); code != nil {
			return *code
		}
	}
	return -1
}

func (c *chrysContent) timedOut() bool {
	meta := c.toolResultMetadata()
	if meta == nil {
		return false
	}
	if b, _ := meta["shell_timed_out"].(bool); b {
		return true
	}
	if b, _ := meta["process_timed_out"].(bool); b {
		return true
	}
	return false
}

func (c *chrysContent) timeoutSeconds() float64 {
	meta := c.toolResultMetadata()
	if meta == nil {
		return 0
	}
	if v, ok := meta["shell_timeout_seconds"].(float64); ok {
		return v
	}
	if v, ok := meta["process_timeout_seconds"].(float64); ok {
		return v
	}
	return 0
}

// errorKind classifies the failure from structured metadata. Returns "" for
// successful or metadata-less results.
func (c *chrysContent) errorKind() string {
	meta := c.toolResultMetadata()
	if meta == nil {
		if c.Props != nil {
			if s, _ := c.Props["tool_error_kind"].(string); s != "" {
				return s
			}
		}
		return ""
	}
	if b, _ := meta["errored"].(bool); b {
		return "errored"
	}
	if s, _ := meta["approval"].(string); s == "user_rejected" {
		return "approval_rejected"
	}
	if s, _ := meta["tool_error_kind"].(string); s != "" {
		return s
	}
	if c.timedOut() {
		return "timeout"
	}
	if b, _ := meta["failed"].(bool); b {
		return "failed"
	}
	return ""
}

func (c *chrysContent) rejected() bool {
	kind := c.errorKind()
	return kind == "approval_rejected" || kind == "hook_denied"
}

// toolResultMetadataFailureState mirrors chrys's
// tool_result_metadata_failure_state() precedence: errored > rejected >
// failed > timed_out > exit_code != 0.
func toolResultMetadataFailureState(meta map[string]any) bool {
	if meta == nil {
		return false
	}
	if b, _ := meta["errored"].(bool); b {
		return true
	}
	if s, _ := meta["approval"].(string); s == "user_rejected" {
		return true
	}
	kind, _ := meta["tool_error_kind"].(string)
	if kind == "approval_rejected" || kind == "hook_denied" {
		return true
	}
	if b, _ := meta["failed"].(bool); b {
		return true
	}
	if b, _ := meta["shell_timed_out"].(bool); b {
		return true
	}
	if b, _ := meta["process_timed_out"].(bool); b {
		return true
	}
	if code := intFromMeta(meta, "shell_exit_code"); code != nil {
		return *code != 0
	}
	if code := intFromMeta(meta, "process_exit_code"); code != nil {
		return *code != 0
	}
	if b, ok := meta["failed"].(bool); ok && !b {
		return false
	}
	return false
}

func intFromMeta(meta map[string]any, key string) *int {
	if meta == nil {
		return nil
	}
	v, ok := meta[key]
	if !ok {
		return nil
	}
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case int:
		i := n
		return &i
	}
	return nil
}

// resultText joins the textual payload of a function_result, replacing inline
// image blobs with a placeholder (a single result can carry a base64 data URI
// hundreds of KB long, which must never reach the terminal render).
func (c *chrysContent) resultText() string {
	if c.Result != "" {
		return c.Result
	}
	var parts []string
	for _, it := range c.Items {
		switch it.Type {
		case "text":
			if it.Text != "" {
				parts = append(parts, it.Text)
			}
		case "data":
			parts = append(parts, imagePlaceholder(it.MediaType))
		}
	}
	return strings.Join(parts, "\n")
}

func imagePlaceholder(mediaType string) string {
	if mediaType == "" {
		mediaType = "image"
	}
	return fmt.Sprintf("[图片: %s]", mediaType)
}

// userText joins a user message's text contents, replacing attached images
// with placeholders.
func (m *chrysMessage) userText() string {
	var parts []string
	for _, c := range m.Contents {
		switch c.Type {
		case "text":
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		case "data":
			parts = append(parts, imagePlaceholder(c.MediaType))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func validSessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}

func readSessionFile(path string) (*sessionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("chrys session parse %s: %w", path, err)
	}
	return &sf, nil
}

// readEffectiveSession resolves a session directory to its winning source,
// mirroring chrys's own recovery arbitration (StateStore.recovery_session_wins):
// while a turn is in flight — or after a crash/interruption — the newest
// messages live only in the session.recovery.json sidecar, and chrys itself
// treats the sidecar as the effective session whenever its updated_at is
// newer than the primary's. Reading only session.json would show such a
// session ending at the interruption even though it continued. This viewer
// never deletes a stale sidecar (chrys heals it on its next save).
func readEffectiveSession(sessionDir string) (*sessionFile, error) {
	primary, perr := readSessionFile(filepath.Join(sessionDir, "session.json"))
	recovery, rerr := readSessionFile(filepath.Join(sessionDir, "session.recovery.json"))
	if perr != nil {
		if rerr != nil {
			return nil, perr
		}
		// Recovery-only session: crashed before its first primary save.
		return recovery, nil
	}
	if rerr == nil && parseTS(recovery.Meta.UpdatedAt).After(parseTS(primary.Meta.UpdatedAt)) {
		return recovery, nil
	}
	return primary, nil
}

// ---- ListSessions ----

func (r *ChrysReader) ListSessions() ([]model.Session, error) {
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		return nil, err
	}

	var sessions []model.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		sf, err := readEffectiveSession(filepath.Join(r.sessionsDir, id))
		if err != nil {
			continue // directories without any session file (aborted/empty sessions)
		}
		sessions = append(sessions, buildSession(id, sf))
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func buildSession(id string, sf *sessionFile) model.Session {
	meta := sf.Meta

	createdAt := parseTS(meta.CreatedAt)
	updatedAt := parseTS(meta.UpdatedAt)
	// meta.created_at is rewritten on save; the first message's own stamp is
	// the real session start when it is earlier.
	var userMessages []string
	for _, m := range sf.State.Messages {
		if ts := m.createdAt(); !ts.IsZero() {
			if createdAt.IsZero() || ts.Before(createdAt) {
				createdAt = ts
			}
			if ts.After(updatedAt) {
				updatedAt = ts
			}
		}
		if m.Role == "user" && m.markerKind() == "" && len(userMessages) < 5 {
			if txt := m.userText(); txt != "" {
				userMessages = append(userMessages, txt)
			}
		}
	}

	name := meta.CustomTitle
	if name == "" {
		name = meta.Title
	}
	if name == "" {
		name = meta.GeneratedTitle
	}
	if name == "" && len(userMessages) > 0 {
		name = userMessages[0]
	}
	if name == "" {
		name = "Session"
	}
	name = shared.TruncateRunes(strings.ReplaceAll(name, "\n", " "), 50)

	return model.Session{
		ID:           id,
		AgentType:    "chrys",
		CWD:          meta.PrimaryCWD,
		Project:      shared.ResolveProject(meta.PrimaryCWD, ""),
		Name:         name,
		ModelName:    meta.ModelID,
		PreviewText:  shared.BuildPreviewText(userMessages),
		TurnCount:    sf.State.TurnCounter,
		MessageCount: meta.MessageCount,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
}

// ---- GetSession ----

func (r *ChrysReader) GetSession(id string) (*model.SessionDetail, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid chrys session id: %q", id)
	}
	sf, err := readEffectiveSession(filepath.Join(r.sessionsDir, id))
	if err != nil {
		return nil, fmt.Errorf("chrys session not found %q: %w", id, err)
	}

	session := buildSession(id, sf)
	turns := buildTurns(sf)
	session.TurnCount = len(turns)

	detail := &model.SessionDetail{
		Session: session,
		Turns:   turns,
		Billing: buildBilling(sf, turns),
	}
	detail.AnomalySummary = shared.RunAnomalyDetection(turns)
	return detail, nil
}

func buildTurns(sf *sessionFile) []model.TurnVM {
	var (
		turns       []model.TurnVM
		currentTurn *model.TurnVM
		turnStart   time.Time
		turnLatest  time.Time
		toolByCall  = map[string]int{} // call_id → index into currentTurn.ToolDetails
	)

	flush := func() {
		if currentTurn == nil {
			return
		}
		if !turnStart.IsZero() && turnLatest.After(turnStart) {
			// Assistant/tool stamps are assigned at save time (end of turn),
			// so latest-start is a usable approximation of turn duration.
			currentTurn.DurationMs = turnLatest.Sub(turnStart).Milliseconds()
		}
		turns = append(turns, *currentTurn)
		currentTurn = nil
	}

	// bump extends the current turn's end using assistant/tool stamps only.
	// The next turn's user stamp must NOT be used: it would fold the user's
	// idle think-time between turns into the previous turn's duration.
	bump := func(ts time.Time) {
		if currentTurn != nil && !ts.IsZero() && ts.After(turnLatest) {
			turnLatest = ts
		}
	}

	for _, m := range sf.State.Messages {
		kind := m.markerKind()
		ts := m.createdAt()

		switch {
		case kind == "interrupted":
			// An in-flight checkpoint (source empty) is a turn still running,
			// not an anomaly — only genuine user/error interruptions count.
			if m.interruptedBy() != "" && currentTurn != nil {
				currentTurn.Anomalies = append(currentTurn.Anomalies, "interrupted")
				currentTurn.ErrorCount++
			}
			continue
		case kind != "":
			continue // turn markers and other internal bookkeeping

		case m.Role == "user":
			flush()
			currentTurn = &model.TurnVM{
				TurnIndex:   len(turns),
				UserMessage: m.userText(),
				Events:      []model.EventVM{},
			}
			turnStart = ts
			turnLatest = ts
			toolByCall = map[string]int{}

		case m.Role == "assistant":
			if currentTurn == nil {
				continue
			}
			bump(ts)
			currentTurn.RequestCount++
			var textParts []string
			for _, c := range m.Contents {
				switch c.Type {
				case "text":
					currentTurn.AssistantMessage += c.Text
					textParts = append(textParts, c.Text)
				case "text_reasoning":
					currentTurn.AssistantMessage += c.Text
				case "function_call":
					currentTurn.ToolCallCount++
					currentTurn.ToolNames = append(currentTurn.ToolNames, c.Name)
					toolByCall[c.CallID] = len(currentTurn.ToolDetails)
					currentTurn.ToolDetails = append(currentTurn.ToolDetails, model.ToolCallVM{
						Name:     c.Name,
						ToolKind: c.toolKind(),
					})
					switch c.Name {
					case "load_skill":
						if skill := argString(&c, "skill_name"); skill != "" {
							currentTurn.Skills = append(currentTurn.Skills, skill)
						}
					case "explore_agent", "plan_agent", "general_agent":
						currentTurn.Subagents = append(currentTurn.Subagents, c.Name)
					}
				}
			}
			if it := m.intermediateText(); it != "" {
				currentTurn.AssistantMessage += it
				textParts = append(textParts, it)
			}
			currentTurn.Events = append(currentTurn.Events, model.EventVM{
				Type:      "assistant.message",
				Timestamp: m.createdAt().Format(time.RFC3339),
				Data:      map[string]any{"text": strings.Join(textParts, "")},
			})

		case m.Role == "tool":
			if currentTurn == nil {
				continue
			}
			bump(ts)
			for _, c := range m.Contents {
				if c.Type != "function_result" {
					continue
				}
				failed := c.failed()
				exitCode := c.exitCode()
				if failed && exitCode < 0 {
					exitCode = 1
				}
				if !failed && exitCode < 0 {
					exitCode = 0
				}
				errorKind := c.errorKind()
				if failed {
					currentTurn.ErrorCount++
				}
				if idx, ok := toolByCall[c.CallID]; ok && idx < len(currentTurn.ToolDetails) {
					td := &currentTurn.ToolDetails[idx]
					td.ExitCode = exitCode
					td.ErrorKind = errorKind
					td.ErrorMessage = c.errorMessage()
					td.TimedOut = c.timedOut()
					td.TimeoutSeconds = c.timeoutSeconds()
					td.Rejected = c.rejected()
				}
				eventData := map[string]any{"is_error": failed}
				if exitCode >= 0 {
					eventData["exit_code"] = exitCode
				}
				if errorKind != "" {
					eventData["error_kind"] = errorKind
				}
				if c.timedOut() {
					eventData["timed_out"] = true
				}
				if c.rejected() {
					eventData["rejected"] = true
				}
				currentTurn.Events = append(currentTurn.Events, model.EventVM{
					Type:      "tool_result",
					Timestamp: m.createdAt().Format(time.RFC3339),
					Data:      eventData,
				})
			}
		}
	}
	flush()

	return shared.FilterEmptyTurns(turns)
}

// argsMap decodes function_call arguments, accepting both the current
// string-encoded form and the older inline-object form.
func (c *chrysContent) argsMap() map[string]any {
	if len(c.Arguments) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(c.Arguments, &m) == nil {
		return m
	}
	var s string
	if json.Unmarshal(c.Arguments, &s) == nil {
		if json.Unmarshal([]byte(s), &m) == nil {
			return m
		}
		return map[string]any{"raw": s}
	}
	return map[string]any{"raw": string(c.Arguments)}
}

func argString(c *chrysContent, key string) string {
	s, _ := c.argsMap()[key].(string)
	return s
}

func buildBilling(sf *sessionFile, turns []model.TurnVM) *model.SessionBilling {
	st := sf.State
	if st.TotalInput == 0 && st.TotalOutput == 0 {
		return nil
	}
	requests := 0
	for _, t := range turns {
		requests += t.RequestCount
	}
	// Chrys totals use inclusive semantics (input includes cache hits);
	// canonical buckets are exclusive.
	prompt := st.TotalInput - st.TotalCacheHit
	if prompt < 0 {
		prompt = 0
	}
	usage := model.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: st.TotalOutput,
		CacheReadTokens:  st.TotalCacheHit,
		Present: model.TokenPresence{
			Input:     model.PresenceExact,
			Output:    model.PresenceExact,
			CacheRead: model.PresenceExact,
			// DeepSeek's context cache is automatic with no separate write
			// bucket, and reasoning is billed inside output with no count.
			CacheWrite: model.PresenceNA,
			Reasoning:  model.PresenceNA,
		},
	}
	return &model.SessionBilling{
		Precision: model.PrecisionExact,
		Totals:    usage,
		ByModel: []model.ModelUsage{{
			Model:    sf.Meta.ModelID,
			Requests: requests,
			Usage:    usage,
		}},
	}
}

// LiveRevision is a stat-only change marker for live-tail polling; the
// recovery copy counts too since mid-turn content lands there first.
func (r *ChrysReader) LiveRevision(id string) (int64, error) {
	if !validSessionID(id) {
		return 0, fmt.Errorf("invalid chrys session id: %q", id)
	}
	var rev int64
	found := false
	for _, name := range []string{"session.json", "session.recovery.json"} {
		if info, err := os.Stat(filepath.Join(r.sessionsDir, id, name)); err == nil {
			rev += info.ModTime().UnixNano() + info.Size()
			found = true
		}
	}
	if !found {
		return 0, fmt.Errorf("chrys session not found: %s", id)
	}
	return rev, nil
}
