package hermes

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	_ "github.com/mattn/go-sqlite3"
)

const (
	agentType   = "hermes"
	displayName = "Hermes Agent"
)

var sessionFields = []string{
	"id", "source", "model", "model_config", "parent_session_id", "started_at", "ended_at",
	"end_reason", "message_count", "tool_call_count", "input_tokens", "output_tokens",
	"cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "cwd", "git_branch",
	"git_repo_root", "billing_provider", "base_url", "mode", "estimated_cost_usd",
	"actual_cost_usd", "cost_status", "title", "last_activity_at", "description",
	"api_call_count", "profile_name", "archived",
}

var messageFields = []string{
	"id", "session_id", "role", "content", "tool_call_id", "tool_calls", "tool_name",
	"effect_disposition", "timestamp", "token_count", "finish_reason", "reasoning",
	"reasoning_content", "active", "compacted", "display_kind",
}

var usageFields = []string{
	"session_id", "model", "provider", "mode", "task", "api_call_count", "input_tokens",
	"output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens",
	"estimated_cost_usd", "actual_cost_usd", "cost_status",
}

// HermesReader reads Hermes' canonical SQLite state.db. Hermes has kept the
// session/message tables backward compatible across schema migrations; the
// reader selects known columns conditionally so older stores remain readable.
type HermesReader struct {
	db     *sql.DB
	dbPath string
	schema schemaInfo
}

type schemaInfo struct {
	tables  map[string]bool
	columns map[string]map[string]bool
}

func (s schemaInfo) hasTable(table string) bool {
	return s.tables[table]
}

func (s schemaInfo) hasColumn(table, column string) bool {
	return s.columns[table][column]
}

// New opens a Hermes state database read-only. The application never holds a
// write-capable handle to an agent's primary store.
func New(dbPath string) (*HermesReader, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("hermes database path is empty")
	}
	db, err := sql.Open("sqlite3", sqliteDSN(dbPath, "mode=ro&_busy_timeout=5000&_foreign_keys=on"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	schema, err := loadSchema(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !schema.hasTable("sessions") || !schema.hasTable("messages") {
		_ = db.Close()
		return nil, fmt.Errorf("hermes database %q has no sessions/messages tables", dbPath)
	}
	return &HermesReader{db: db, dbPath: dbPath, schema: schema}, nil
}

func sqliteDSN(path, params string) string {
	escaped := (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()
	return "file:" + escaped + "?" + params
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func loadSchema(db *sql.DB) (schemaInfo, error) {
	s := schemaInfo{tables: map[string]bool{}, columns: map[string]map[string]bool{}}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type IN ('table', 'view')`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return s, err
		}
		s.tables[name] = true
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	for _, table := range tables {
		rows, err := db.Query("PRAGMA table_info(" + quoteIdentifier(table) + ")")
		if err != nil {
			return s, err
		}
		cols := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue any
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				_ = rows.Close()
				return s, err
			}
			cols[name] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return s, err
		}
		_ = rows.Close()
		s.columns[table] = cols
	}
	return s, nil
}

func (r *HermesReader) AgentType() string   { return agentType }
func (r *HermesReader) DisplayName() string { return displayName }

// WatchRoots returns the SQLite file. The watcher also observes SQLite's
// -wal/-shm sidecars when a state.db change is reported.
func (r *HermesReader) WatchRoots() []string { return []string{r.dbPath} }

// LiveRevision is a stat-only store revision for polling. Hermes writes active
// turns to the WAL, so the freshest mtime among the database and sidecars is
// the useful cheap marker.
func (r *HermesReader) LiveRevision(id string) (int64, error) {
	if _, err := r.readSessionRow(id); err != nil {
		return 0, err
	}
	latest, err := r.lastStoreWrite()
	if err != nil {
		return 0, err
	}
	return latest.UnixNano(), nil
}

func (r *HermesReader) lastStoreWrite() (time.Time, error) {
	paths := []string{r.dbPath, r.dbPath + "-wal", r.dbPath + "-shm"}
	var latest time.Time
	var found bool
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !found || info.ModTime().After(latest) {
			latest = info.ModTime()
			found = true
		}
	}
	if !found {
		return time.Time{}, fmt.Errorf("hermes database disappeared: %s", r.dbPath)
	}
	return latest, nil
}

// SessionLive is a bounded native marker: an unfinalized session with a
// recently-written Hermes store is considered live. No exact process/session
// PID mapping is persisted by Hermes, so this is intentionally not used for
// the terminate capability.
func (r *HermesReader) SessionLive(id string) (bool, error) {
	row, err := r.readSessionRow(id)
	if err != nil {
		return false, err
	}
	if r.schema.hasColumn("sessions", "ended_at") && row.EndedAtSet {
		return false, nil
	}
	last, err := r.lastStoreWrite()
	if err != nil {
		return false, err
	}
	return model.IsSessionLive(last), nil
}

// ListSessions returns both root and child rows. Parent/child lineage is a
// persisted fact in Hermes and is kept in the unified index rather than
// silently hiding child transcripts.
func (r *HermesReader) ListSessions() ([]model.Session, error) {
	rows, err := r.querySessionRows("")
	if err != nil {
		return nil, fmt.Errorf("hermes query sessions: %w", err)
	}
	// Materialize the small session metadata result before querying messages
	// for previews/counts. The reader uses one SQLite connection so reads stay
	// deterministic; leaving this cursor open while issuing the nested summary
	// query can otherwise wait forever on a live state.db.
	var parsedRows []sessionRow
	for rows.Next() {
		values, err := scanValues(rows, len(sessionFields)+1)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("hermes scan session: %w", err)
		}
		parsedRows = append(parsedRows, parseSessionRow(values))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("hermes iterate sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("hermes close sessions: %w", err)
	}
	summaries, err := r.allSessionSummaries()
	if err != nil {
		return nil, err
	}
	previews, err := r.allSessionPreviews()
	if err != nil {
		return nil, err
	}
	var sessions []model.Session
	for _, row := range parsedRows {
		summary := summaries[row.ID]
		sessions = append(sessions, r.toSession(row, summary.Count, summary.Turns, previews[row.ID]))
	}
	if sessions == nil {
		sessions = []model.Session{}
	}
	return sessions, nil
}

// GetSession reads one session and its append-only message transcript.
func (r *HermesReader) GetSession(id string) (*model.SessionDetail, error) {
	row, err := r.readSessionRow(id)
	if err != nil {
		return nil, err
	}
	messages, err := r.readMessages(id)
	if err != nil {
		return nil, err
	}
	turns := buildTurns(messages)
	preview := firstUserPreview(messages)
	session := r.toSession(row, len(messages), len(turns), preview)
	billing, err := r.buildBilling(row)
	if err != nil {
		return nil, err
	}
	detail := &model.SessionDetail{
		Session: session,
		Turns:   turns,
		Billing: billing,
	}
	detail.AnomalySummary = shared.RunAnomalyDetection(turns)
	return detail, nil
}

func (r *HermesReader) readSessionRow(id string) (sessionRow, error) {
	if strings.TrimSpace(id) == "" {
		return sessionRow{}, fmt.Errorf("hermes session id is empty")
	}
	rows, err := r.querySessionRows(id)
	if err != nil {
		return sessionRow{}, fmt.Errorf("hermes query session %q: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return sessionRow{}, err
		}
		return sessionRow{}, fmt.Errorf("hermes session not found: %s", id)
	}
	values, err := scanValues(rows, len(sessionFields)+1)
	if err != nil {
		return sessionRow{}, fmt.Errorf("hermes scan session %q: %w", id, err)
	}
	return parseSessionRow(values), nil
}

func (r *HermesReader) querySessionRows(id string) (*sql.Rows, error) {
	selectSQL := r.sessionSelect("s") + ", " + r.updatedAtExpr("s")
	query := "SELECT " + selectSQL + " FROM " + quoteIdentifier("sessions") + " s"
	var args []any
	var where []string
	if id != "" {
		where = append(where, `s."id" = ?`)
		args = append(args, id)
	}
	if r.schema.hasColumn("sessions", "archived") {
		where = append(where, `COALESCE(s."archived", 0) = 0`)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY __updated_at DESC, s."id" ASC`
	return r.db.Query(query, args...)
}

func (r *HermesReader) sessionSelect(alias string) string {
	parts := make([]string, 0, len(sessionFields))
	for _, field := range sessionFields {
		if r.schema.hasColumn("sessions", field) {
			parts = append(parts, alias+"."+quoteIdentifier(field))
		} else {
			parts = append(parts, "NULL")
		}
	}
	return strings.Join(parts, ", ")
}

func (r *HermesReader) updatedAtExpr(alias string) string {
	messageMax := "NULL"
	if r.schema.hasColumn("messages", "timestamp") && r.schema.hasColumn("messages", "session_id") {
		messageMax = `(SELECT MAX(m."timestamp") FROM "messages" m WHERE m."session_id" = ` + alias + `."id"`
		if r.schema.hasColumn("messages", "active") {
			messageMax += ` AND COALESCE(m."active", 1) = 1`
		}
		messageMax += ")"
	}
	started := "0"
	if r.schema.hasColumn("sessions", "started_at") {
		started = alias + `."started_at"`
	}
	if r.schema.hasColumn("sessions", "last_activity_at") {
		return `COALESCE(NULLIF(` + alias + `."last_activity_at", 0), ` + messageMax + `, ` + started + `, 0) AS __updated_at`
	}
	return `COALESCE(` + messageMax + `, ` + started + `, 0) AS __updated_at`
}

type sessionSummaryRow struct {
	Count int
	Turns int
}

func (r *HermesReader) allSessionSummaries() (map[string]sessionSummaryRow, error) {
	out := map[string]sessionSummaryRow{}
	if !r.schema.hasTable("messages") || !r.schema.hasColumn("messages", "session_id") {
		return out, nil
	}
	turnsExpr := "0"
	if r.schema.hasColumn("messages", "role") {
		turnsExpr = `SUM(CASE WHEN LOWER(COALESCE("role", '')) = 'user' THEN 1 ELSE 0 END)`
	}
	query := `SELECT "session_id", COUNT(*), ` + turnsExpr + ` FROM "messages"`
	if r.schema.hasColumn("messages", "active") {
		query += ` WHERE COALESCE("active", 1) = 1`
	}
	query += ` GROUP BY "session_id"`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("hermes query message summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count, turns int64
		if err := rows.Scan(&id, &count, &turns); err != nil {
			return nil, fmt.Errorf("hermes scan message summary: %w", err)
		}
		if id != "" {
			out[id] = sessionSummaryRow{Count: nativeIntOrZero(count), Turns: nativeIntOrZero(turns)}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hermes iterate message summaries: %w", err)
	}
	return out, nil
}

func (r *HermesReader) allSessionPreviews() (map[string]string, error) {
	out := map[string]string{}
	if !r.schema.hasTable("messages") ||
		!r.schema.hasColumn("messages", "session_id") ||
		!r.schema.hasColumn("messages", "role") ||
		!r.schema.hasColumn("messages", "content") {
		return out, nil
	}
	ids := `SELECT DISTINCT "session_id" FROM "messages"`
	if r.schema.hasColumn("messages", "active") {
		ids += ` WHERE COALESCE("active", 1) = 1`
	}
	first := `SELECT first."content" FROM "messages" first WHERE first."session_id" = session_ids."session_id" AND LOWER(COALESCE(first."role", '')) = 'user'`
	if r.schema.hasColumn("messages", "active") {
		first += ` AND COALESCE(first."active", 1) = 1`
	}
	if r.schema.hasColumn("messages", "id") {
		first += ` ORDER BY first."id" ASC`
	}
	first += ` LIMIT 1`
	query := `SELECT session_ids."session_id", (` + first + `) FROM (` + ids + `) session_ids`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("hermes query message previews: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var content any
		if err := rows.Scan(&id, &content); err != nil {
			return nil, fmt.Errorf("hermes scan message preview: %w", err)
		}
		if id != "" {
			text := strings.TrimSpace(contentText(asString(content)))
			if text != "" {
				out[id] = shared.TruncateRunes(text, 200)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hermes iterate message previews: %w", err)
	}
	return out, nil
}

func (r *HermesReader) readMessages(sessionID string) ([]hermesMessage, error) {
	parts := make([]string, 0, len(messageFields))
	for _, field := range messageFields {
		if r.schema.hasColumn("messages", field) {
			parts = append(parts, "m."+quoteIdentifier(field))
		} else {
			parts = append(parts, "NULL")
		}
	}
	query := "SELECT " + strings.Join(parts, ", ") + ` FROM "messages" m WHERE m."session_id" = ?`
	if r.schema.hasColumn("messages", "active") {
		query += ` AND COALESCE(m."active", 1) = 1`
	}
	query += ` ORDER BY m."id" ASC`
	rows, err := r.db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("hermes query messages %q: %w", sessionID, err)
	}
	defer rows.Close()
	var messages []hermesMessage
	for rows.Next() {
		values, err := scanValues(rows, len(messageFields))
		if err != nil {
			return nil, fmt.Errorf("hermes scan message: %w", err)
		}
		messages = append(messages, parseMessage(values))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hermes iterate messages: %w", err)
	}
	if messages == nil {
		messages = []hermesMessage{}
	}
	return messages, nil
}

type sessionRow struct {
	ID, Source, Model, ModelConfig, ParentID, EndReason string
	CWD, Branch, Repo, Provider, BaseURL, Mode          string
	CostStatus, Title, Description, ProfileName         string
	StartedAt, EndedAt, UpdatedAt, LastActivityAt       time.Time
	StartedAtSet, EndedAtSet, LastActivityAtSet         bool
	MessageCount, ToolCallCount                         int64
	InputTokens, OutputTokens                           int64
	CacheReadTokens, CacheWriteTokens, ReasoningTokens  int64
	EstimatedCost, ActualCost                           float64
	APICallCount                                        int64
	MessageCountSet, ToolCallCountSet                   bool
	InputTokensSet, OutputTokensSet                     bool
	CacheReadTokensSet, CacheWriteTokensSet             bool
	ReasoningTokensSet, EstimatedCostSet, ActualCostSet bool
	APICallCountSet                                     bool
	Archived                                            bool
}

func parseSessionRow(values []any) sessionRow {
	row := sessionRow{}
	row.ID = asString(valueAt(values, sessionFields, "id"))
	row.Source = asString(valueAt(values, sessionFields, "source"))
	row.Model = asString(valueAt(values, sessionFields, "model"))
	row.ModelConfig = asString(valueAt(values, sessionFields, "model_config"))
	row.ParentID = asString(valueAt(values, sessionFields, "parent_session_id"))
	row.EndReason = asString(valueAt(values, sessionFields, "end_reason"))
	row.CWD = asString(valueAt(values, sessionFields, "cwd"))
	row.Branch = asString(valueAt(values, sessionFields, "git_branch"))
	row.Repo = asString(valueAt(values, sessionFields, "git_repo_root"))
	row.Provider = asString(valueAt(values, sessionFields, "billing_provider"))
	row.BaseURL = asString(valueAt(values, sessionFields, "base_url"))
	row.Mode = asString(valueAt(values, sessionFields, "mode"))
	row.CostStatus = asString(valueAt(values, sessionFields, "cost_status"))
	row.Title = asString(valueAt(values, sessionFields, "title"))
	row.Description = asString(valueAt(values, sessionFields, "description"))
	row.ProfileName = asString(valueAt(values, sessionFields, "profile_name"))
	row.StartedAt, row.StartedAtSet = asUnixTime(valueAt(values, sessionFields, "started_at"))
	row.EndedAt, row.EndedAtSet = asUnixTime(valueAt(values, sessionFields, "ended_at"))
	row.LastActivityAt, row.LastActivityAtSet = asUnixTime(valueAt(values, sessionFields, "last_activity_at"))
	row.UpdatedAt, _ = asUnixTime(valuesAtEnd(values))
	row.MessageCount, row.MessageCountSet = asInt64(valueAt(values, sessionFields, "message_count"))
	row.ToolCallCount, row.ToolCallCountSet = asInt64(valueAt(values, sessionFields, "tool_call_count"))
	row.InputTokens, row.InputTokensSet = asInt64(valueAt(values, sessionFields, "input_tokens"))
	row.OutputTokens, row.OutputTokensSet = asInt64(valueAt(values, sessionFields, "output_tokens"))
	row.CacheReadTokens, row.CacheReadTokensSet = asInt64(valueAt(values, sessionFields, "cache_read_tokens"))
	row.CacheWriteTokens, row.CacheWriteTokensSet = asInt64(valueAt(values, sessionFields, "cache_write_tokens"))
	row.ReasoningTokens, row.ReasoningTokensSet = asInt64(valueAt(values, sessionFields, "reasoning_tokens"))
	row.EstimatedCost, row.EstimatedCostSet = asFloat64(valueAt(values, sessionFields, "estimated_cost_usd"))
	row.ActualCost, row.ActualCostSet = asFloat64(valueAt(values, sessionFields, "actual_cost_usd"))
	row.APICallCount, row.APICallCountSet = asInt64(valueAt(values, sessionFields, "api_call_count"))
	row.Archived, _ = asBool(valueAt(values, sessionFields, "archived"))
	return row
}

func valuesAtEnd(values []any) any {
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1]
}

type hermesMessage struct {
	ID, SessionID, Role, Content, ToolCallID, ToolCalls, ToolName, EffectDisposition string
	Timestamp                                                                        time.Time
	TokenCount                                                                       int64
	TimestampSet, TokenCountSet                                                      bool
	FinishReason, Reasoning, ReasoningContent                                        string
}

func parseMessage(values []any) hermesMessage {
	m := hermesMessage{}
	m.ID = asString(valueAt(values, messageFields, "id"))
	m.SessionID = asString(valueAt(values, messageFields, "session_id"))
	m.Role = strings.ToLower(strings.TrimSpace(asString(valueAt(values, messageFields, "role"))))
	m.Content = asString(valueAt(values, messageFields, "content"))
	m.ToolCallID = asString(valueAt(values, messageFields, "tool_call_id"))
	m.ToolCalls = asString(valueAt(values, messageFields, "tool_calls"))
	m.ToolName = asString(valueAt(values, messageFields, "tool_name"))
	m.EffectDisposition = asString(valueAt(values, messageFields, "effect_disposition"))
	m.Timestamp, m.TimestampSet = asUnixSeconds(valueAt(values, messageFields, "timestamp"))
	m.TokenCount, m.TokenCountSet = asInt64(valueAt(values, messageFields, "token_count"))
	m.FinishReason = asString(valueAt(values, messageFields, "finish_reason"))
	m.Reasoning = asString(valueAt(values, messageFields, "reasoning"))
	m.ReasoningContent = asString(valueAt(values, messageFields, "reasoning_content"))
	return m
}

func scanValues(rows *sql.Rows, count int) ([]any, error) {
	values := make([]any, count)
	dest := make([]any, count)
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	return values, nil
}

func valueAt(values []any, fields []string, name string) any {
	for i, field := range fields {
		if field == name && i < len(values) {
			return values[i]
		}
	}
	return nil
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func asInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int64:
		return x, true
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case float64:
		return int64(x), true
	case []byte:
		i, err := strconv.ParseInt(string(x), 10, 64)
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func asFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case []byte:
		f, err := strconv.ParseFloat(string(x), 64)
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func asBool(v any) (bool, bool) {
	switch x := v.(type) {
	case nil:
		return false, false
	case bool:
		return x, true
	case int64:
		return x != 0, true
	case int:
		return x != 0, true
	case []byte:
		b, err := strconv.ParseBool(string(x))
		return b, err == nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		return b, err == nil
	default:
		return false, false
	}
}

func asUnixTime(v any) (time.Time, bool) {
	return asUnixSeconds(v)
}

func asUnixSeconds(v any) (time.Time, bool) {
	f, ok := asFloat64(v)
	if !ok || f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return time.Time{}, false
	}
	// A few early development databases used milliseconds; accept them while
	// keeping the canonical Hermes seconds representation exact.
	if f > 100000000000 {
		f /= 1000
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC(), true
}

func (r *HermesReader) toSession(row sessionRow, messageCount, turnCount int, preview string) model.Session {
	created := row.StartedAt
	if created.IsZero() {
		created = row.UpdatedAt
	}
	updated := row.UpdatedAt
	if updated.IsZero() {
		updated = row.LastActivityAt
	}
	if updated.IsZero() {
		updated = created
	}
	if messageCount < 0 {
		messageCount = 0
	}
	name := strings.TrimSpace(row.Title)
	if name == "" {
		name = strings.TrimSpace(preview)
	}
	if name == "" && !created.IsZero() {
		name = "Hermes " + created.Format("01-02 15:04")
	}
	if name == "" {
		name = "Hermes Session"
	}
	return model.Session{
		ID:              row.ID,
		AgentType:       agentType,
		CWD:             row.CWD,
		Repository:      row.Repo,
		Branch:          row.Branch,
		Project:         shared.ResolveProject(row.CWD, row.Repo),
		Name:            name,
		ModelName:       row.Model,
		ModelProvider:   row.Provider,
		ResumeID:        row.ID,
		ParentSessionID: row.ParentID,
		IsSubagent:      isDelegateSession(row),
		PreviewText:     strings.TrimSpace(preview),
		TurnCount:       turnCount,
		MessageCount:    messageCount,
		CreatedAt:       created,
		UpdatedAt:       updated,
	}
}

func firstUserPreview(messages []hermesMessage) string {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if text := strings.TrimSpace(contentText(message.Content)); text != "" {
			return shared.TruncateRunes(text, 200)
		}
	}
	return ""
}

func contentText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	text := flattenContent(value)
	if strings.TrimSpace(text) == "" {
		return raw
	}
	return text
}

func flattenContent(value any) string {
	switch x := value.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, item := range x {
			if text := strings.TrimSpace(flattenContent(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "output", "stdout", "result", "message"} {
			if item, ok := x[key]; ok {
				if text := strings.TrimSpace(flattenContent(item)); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func jsonMap(raw string) map[string]any {
	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

func isDelegateSession(row sessionRow) bool {
	return isDelegateConfig(row.Source, row.ModelConfig)
}

func isDelegateConfig(source, modelConfig string) bool {
	if strings.EqualFold(source, "tool") {
		return true
	}
	config := jsonMap(modelConfig)
	if config == nil {
		return false
	}
	for _, key := range []string{"_delegate_from", "delegate_from", "parent_session_id"} {
		if value, ok := config[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// ---- tool/message normalization ----

type toolInvocation struct {
	ID, Name string
	Input    map[string]any
}

type toolResult struct {
	CallID, Name              string
	Stdout, Stderr, ErrorKind string
	ExitCode                  int
	DurationMs                int64
	TimedOut, Rejected        bool
	TimeoutSeconds            float64
	ToolKind                  string
}

func parseToolInvocations(message hermesMessage) []toolInvocation {
	raw := strings.TrimSpace(message.ToolCalls)
	if raw == "" {
		if strings.TrimSpace(message.ToolName) == "" {
			return nil
		}
		name, input := normalizeTool(message.ToolName, map[string]any{})
		return []toolInvocation{{ID: "msg-" + message.ID, Name: name, Input: input}}
	}
	var values []any
	if json.Unmarshal([]byte(raw), &values) != nil {
		var one map[string]any
		if json.Unmarshal([]byte(raw), &one) != nil {
			return nil
		}
		values = []any{one}
	}
	var out []toolInvocation
	for index, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name := asString(object["name"])
		if function, ok := object["function"].(map[string]any); ok {
			if name == "" {
				name = asString(function["name"])
			}
			if args := function["arguments"]; args != nil {
				object["arguments"] = args
			}
		}
		if name == "" {
			name = asString(object["tool_name"])
		}
		input := toolArguments(object["arguments"])
		if len(input) == 0 {
			input = toolArguments(object["input"])
		}
		name, input = normalizeTool(name, input)
		id := asString(object["id"])
		if id == "" {
			id = asString(object["call_id"])
		}
		if id == "" {
			id = fmt.Sprintf("msg-%s-%d", message.ID, index)
		}
		if name != "" {
			out = append(out, toolInvocation{ID: id, Name: name, Input: input})
		}
	}
	return out
}

func toolArguments(value any) map[string]any {
	switch x := value.(type) {
	case map[string]any:
		return x
	case string:
		var out map[string]any
		if json.Unmarshal([]byte(x), &out) == nil {
			return out
		}
	}
	return map[string]any{}
}

func normalizeTool(name string, input map[string]any) (string, map[string]any) {
	name = strings.TrimSpace(name)
	if !strings.EqualFold(name, "patch") {
		return name, input
	}
	if patch := asString(input["patch"]); patch != "" {
		return "apply_patch", map[string]any{"patch": patch}
	}
	normalized := map[string]any{}
	for key, value := range input {
		normalized[key] = value
	}
	if path := asString(input["path"]); path != "" {
		normalized["file_path"] = path
	}
	if _, ok := input["old_string"]; ok {
		normalized["old_string"] = input["old_string"]
	}
	if _, ok := input["new_string"]; ok {
		normalized["new_string"] = input["new_string"]
	}
	if _, ok := input["replace_all"]; ok {
		normalized["replace_all"] = input["replace_all"]
	}
	return "edit", normalized
}

func toolKind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "patch"), strings.Contains(lower, "edit"), strings.Contains(lower, "write"), strings.Contains(lower, "read"):
		return "file"
	case strings.Contains(lower, "delegate"), strings.Contains(lower, "subtask"):
		return "subtask"
	case strings.Contains(lower, "terminal"), strings.Contains(lower, "shell"), strings.Contains(lower, "exec"), lower == "process":
		return "terminal"
	default:
		return ""
	}
}

// Hermes wraps MCP tool results in an <untrusted_tool_result> envelope: an
// opening tag carrying the tool name, a fixed security preamble paragraph,
// the raw payload, and a closing tag. The envelope makes the stored content
// invalid JSON as a whole, so it must be stripped before the payload can be
// parsed — otherwise the UI shows the raw JSON text with literal \n and
// \uXXXX escapes.
const (
	untrustedOpenPrefix = "<untrusted_tool_result"
	untrustedCloseTag   = "</untrusted_tool_result>"
	untrustedPreamble   = "The following content was retrieved from an external source."
)

// unwrapUntrustedResult strips the envelope and returns the inner payload.
// The second return value reports whether an envelope was present. Content
// without an envelope is returned unchanged.
func unwrapUntrustedResult(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, untrustedOpenPrefix) {
		return raw, false
	}
	openEnd := strings.Index(trimmed, ">")
	if openEnd < 0 {
		return raw, false
	}
	body := trimmed[openEnd+1:]
	if i := strings.LastIndex(body, untrustedCloseTag); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, untrustedPreamble) {
		// The preamble is a single paragraph; the payload follows the first
		// blank line.
		if i := strings.Index(body, "\n\n"); i >= 0 {
			return strings.TrimSpace(body[i+2:]), true
		}
		return "", true
	}
	return body, true
}

func parseToolResult(message hermesMessage) toolResult {
	result := toolResult{CallID: message.ToolCallID, Name: message.ToolName, ToolKind: toolKind(message.ToolName)}
	content, wrapped := unwrapUntrustedResult(message.Content)
	object := jsonMap(content)
	if object != nil {
		result.Stdout = firstString(object, "stdout", "output", "result")
		result.Stderr = firstString(object, "stderr", "error")
		result.ExitCode = firstInt(object, "exit_code", "exitCode", "returncode", "return_code")
		if success, ok := object["success"].(bool); ok && !success && result.ExitCode == 0 {
			result.ExitCode = 1
		}
		result.DurationMs = int64(firstFloat(object, "duration_ms", "duration"))
		result.TimedOut, _ = object["timed_out"].(bool)
		result.Rejected, _ = object["rejected"].(bool)
		result.TimeoutSeconds = firstFloat(object, "timeout_seconds")
		if result.Stderr == "" {
			result.Stderr = firstString(object, "message")
		}
		if wrapped && result.Stdout == "" {
			// Envelope-wrapped JSON payloads use tool-specific key shapes
			// (browser_navigate stores url/title/snapshot, ...). When none of
			// the canonical output keys match, fall back to the pretty-printed
			// payload so the reply stays readable instead of vanishing.
			if pretty, err := json.MarshalIndent(object, "", "  "); err == nil {
				result.Stdout = string(pretty)
			}
		}
	} else {
		result.Stdout = contentText(content)
	}
	// Compressed/binary payloads relayed by upstream tools (gzipped HTTP
	// bodies, archives) decode into pages of mojibake; collapse them.
	result.Stdout = shared.CollapseBinarySpans(result.Stdout)
	result.Stderr = shared.CollapseBinarySpans(result.Stderr)
	disposition := strings.ToLower(message.EffectDisposition)
	if strings.Contains(disposition, "reject") {
		result.Rejected = true
	}
	if strings.Contains(disposition, "error") || strings.Contains(disposition, "fail") {
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		if result.ErrorKind == "" {
			result.ErrorKind = "tool_error"
		}
	}
	if result.ExitCode != 0 || result.TimedOut || result.Rejected {
		if result.ErrorKind == "" {
			result.ErrorKind = "tool_error"
		}
	}
	return result
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(asString(object[key])); value != "" {
			return value
		}
	}
	return ""
}

func firstInt(object map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := asInt64(object[key]); ok {
			if converted, ok := asNativeInt(value); ok {
				return converted
			}
			// An out-of-range exit code is malformed input, but it must not
			// wrap to zero and make a failed tool look successful.
			return 1
		}
	}
	return 0
}

// asNativeInt keeps JSON/SQLite int64 values from silently truncating on a
// 32-bit build. Atoi applies the host's native int range and reports overflow.
func asNativeInt(value int64) (int, bool) {
	converted, err := strconv.Atoi(strconv.FormatInt(value, 10))
	return converted, err == nil
}

func nativeIntOrZero(value int64) int {
	converted, ok := asNativeInt(value)
	if !ok {
		return 0
	}
	return converted
}

func firstFloat(object map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := asFloat64(object[key]); ok {
			return value
		}
	}
	return 0
}

func buildTurns(messages []hermesMessage) []model.TurnVM {
	turns := make([]model.TurnVM, 0)
	current := -1
	invocations := map[string]int{}
	for _, message := range messages {
		switch message.Role {
		case "user":
			turns = append(turns, model.TurnVM{TurnIndex: len(turns)})
			current = len(turns) - 1
			invocations = map[string]int{}
			turns[current].UserMessage = strings.TrimSpace(contentText(message.Content))
		case "assistant":
			if current < 0 {
				turns = append(turns, model.TurnVM{TurnIndex: len(turns)})
				current = len(turns) - 1
				invocations = map[string]int{}
			}
			turn := &turns[current]
			turn.RequestCount++
			text := strings.TrimSpace(contentText(message.Content))
			if reasoning := strings.TrimSpace(message.Reasoning + "\n" + message.ReasoningContent); reasoning != "" {
				if text != "" {
					text += "\n"
				}
				text += strings.TrimSpace(reasoning)
			}
			if text != "" {
				if turn.AssistantMessage != "" {
					turn.AssistantMessage += "\n"
				}
				turn.AssistantMessage += text
			}
			for _, invocation := range parseToolInvocations(message) {
				turn.ToolCallCount++
				turn.ToolNames = append(turn.ToolNames, invocation.Name)
				if strings.EqualFold(invocation.Name, "delegate_task") {
					turn.Subagents = append(turn.Subagents, invocation.Name)
				}
				turn.ToolDetails = append(turn.ToolDetails, model.ToolCallVM{
					Name: invocation.Name, ToolKind: toolKind(invocation.Name),
				})
				invocations[invocation.ID] = len(turn.ToolDetails) - 1
			}
		case "tool", "tool_result", "function":
			if current < 0 {
				continue
			}
			result := parseToolResult(message)
			if idx, ok := invocations[result.CallID]; ok {
				turn := &turns[current]
				applyToolResult(&turn.ToolDetails[idx], result)
				if result.ExitCode != 0 || result.Rejected || result.TimedOut || result.ErrorKind != "" {
					turn.ErrorCount++
				}
			} else if result.Name != "" {
				turn := &turns[current]
				turn.ToolDetails = append(turn.ToolDetails, model.ToolCallVM{
					Name: result.Name, ExitCode: result.ExitCode, Duration: result.DurationMs,
					ErrorKind: result.ErrorKind, ErrorMessage: result.Stderr, TimedOut: result.TimedOut,
					TimeoutSeconds: result.TimeoutSeconds, Rejected: result.Rejected, ToolKind: result.ToolKind,
				})
				if result.ExitCode != 0 || result.Rejected || result.TimedOut || result.ErrorKind != "" {
					turn.ErrorCount++
				}
			}
		}
	}
	for i := range turns {
		turns[i].UserMessage = strings.TrimSpace(turns[i].UserMessage)
		turns[i].AssistantMessage = strings.TrimSpace(turns[i].AssistantMessage)
	}
	return shared.FilterEmptyTurns(turns)
}

func applyToolResult(detail *model.ToolCallVM, result toolResult) {
	detail.ExitCode = result.ExitCode
	detail.Duration = result.DurationMs
	detail.ErrorKind = result.ErrorKind
	detail.ErrorMessage = result.Stderr
	detail.TimedOut = result.TimedOut
	detail.TimeoutSeconds = result.TimeoutSeconds
	detail.Rejected = result.Rejected
	if detail.ToolKind == "" {
		detail.ToolKind = result.ToolKind
	}
}

// buildBilling converts Hermes' session aggregate buckets into the canonical
// exclusive token model. session_model_usage is retained as the per-model
// breakdown when present.
func (r *HermesReader) buildBilling(row sessionRow) (*model.SessionBilling, error) {
	usageRows, err := r.readUsageRows(row.ID)
	if err != nil {
		return nil, err
	}
	tokenEvidence := row.APICallCountSet && row.APICallCount > 0
	tokenEvidence = tokenEvidence || row.InputTokens != 0 || row.OutputTokens != 0 || row.CacheReadTokens != 0 || row.CacheWriteTokens != 0 || row.ReasoningTokens != 0
	for _, usage := range usageRows {
		tokenEvidence = tokenEvidence || usage.InputSet || usage.OutputSet || usage.CacheReadSet || usage.CacheWriteSet || usage.ReasoningSet
	}
	costEvidence := row.EstimatedCostSet || row.ActualCostSet || len(usageRows) > 0
	if !tokenEvidence && !costEvidence {
		return &model.SessionBilling{Precision: model.PrecisionMissing}, nil
	}

	billing := &model.SessionBilling{Precision: billingPrecision(row, usageRows)}
	if row.Provider != "" || row.EstimatedCostSet || row.ActualCostSet || len(usageRows) > 0 {
		billing.BillingUnit = "usd"
	}
	billing.Totals = sessionTokenUsage(row, tokenEvidence)
	if row.ActualCostSet {
		billing.BillingAmount = row.ActualCost
	} else if row.EstimatedCostSet {
		billing.BillingAmount = row.EstimatedCost
	}
	for _, usage := range usageRows {
		billing.ByModel = append(billing.ByModel, usage.toModelUsage())
	}
	if len(billing.ByModel) == 0 && row.Model != "" && tokenEvidence {
		billing.ByModel = []model.ModelUsage{{Model: row.Model, Requests: nativeIntOrZero(row.APICallCount), Usage: billing.Totals}}
	}
	sort.SliceStable(billing.ByModel, func(i, j int) bool {
		return billing.ByModel[i].Model < billing.ByModel[j].Model
	})
	return billing, nil
}

func billingPrecision(row sessionRow, usageRows []usageRow) string {
	estimated := false
	if precision := costStatusPrecision(row.CostStatus); precision == model.PrecisionExact {
		return model.PrecisionExact
	} else if precision == model.PrecisionEstimated {
		estimated = true
	}
	if row.ActualCostSet {
		return model.PrecisionExact
	}
	if row.EstimatedCostSet {
		estimated = true
	}
	for _, usage := range usageRows {
		if precision := costStatusPrecision(usage.CostStatus); precision == model.PrecisionExact {
			return model.PrecisionExact
		} else if precision == model.PrecisionEstimated {
			estimated = true
		}
		if usage.ActualCostSet {
			return model.PrecisionExact
		}
		if usage.EstimatedCostSet {
			estimated = true
		}
	}
	if estimated {
		return model.PrecisionEstimated
	}
	return model.PrecisionExact
}

func costStatusPrecision(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "exact":
		return model.PrecisionExact
	case "estimated":
		return model.PrecisionEstimated
	default:
		return ""
	}
}

func sessionTokenUsage(row sessionRow, evidence bool) model.TokenUsage {
	u := model.TokenUsage{
		PromptTokens: row.InputTokens, CompletionTokens: row.OutputTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		ReasoningTokens: row.ReasoningTokens,
	}
	if evidence {
		if row.InputTokensSet {
			u.Present.Input = model.PresenceExact
		}
		if row.OutputTokensSet {
			u.Present.Output = model.PresenceExact
		}
		if row.CacheReadTokensSet {
			u.Present.CacheRead = model.PresenceExact
		}
		if row.CacheWriteTokensSet {
			u.Present.CacheWrite = model.PresenceExact
		}
		if row.ReasoningTokensSet {
			u.Present.Reasoning = model.PresenceExact
		}
	}
	return u
}

type usageRow struct {
	Model, Provider, Mode, Task     string
	CostStatus                      string
	Requests                        int64
	Input, Output                   int64
	CacheRead, CacheWrite           int64
	Reasoning                       int64
	EstimatedCost, ActualCost       float64
	RequestsSet, InputSet           bool
	OutputSet, CacheReadSet         bool
	CacheWriteSet, ReasoningSet     bool
	EstimatedCostSet, ActualCostSet bool
}

func (r *HermesReader) readUsageRows(sessionID string) ([]usageRow, error) {
	if !r.schema.hasTable("session_model_usage") || !r.schema.hasColumn("session_model_usage", "session_id") {
		return nil, nil
	}
	parts := make([]string, 0, len(usageFields))
	for _, field := range usageFields {
		if r.schema.hasColumn("session_model_usage", field) {
			parts = append(parts, quoteIdentifier(field))
		} else {
			parts = append(parts, "NULL")
		}
	}
	rows, err := r.db.Query("SELECT "+strings.Join(parts, ", ")+` FROM "session_model_usage" WHERE "session_id" = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("hermes query usage %q: %w", sessionID, err)
	}
	defer rows.Close()
	var out []usageRow
	for rows.Next() {
		values, err := scanValues(rows, len(usageFields))
		if err != nil {
			return nil, fmt.Errorf("hermes scan usage %q: %w", sessionID, err)
		}
		u := usageRow{
			Model:      asString(valueAt(values, usageFields, "model")),
			Provider:   asString(valueAt(values, usageFields, "provider")),
			Mode:       asString(valueAt(values, usageFields, "mode")),
			Task:       asString(valueAt(values, usageFields, "task")),
			CostStatus: asString(valueAt(values, usageFields, "cost_status")),
		}
		u.Requests, u.RequestsSet = asInt64(valueAt(values, usageFields, "api_call_count"))
		u.Input, u.InputSet = asInt64(valueAt(values, usageFields, "input_tokens"))
		u.Output, u.OutputSet = asInt64(valueAt(values, usageFields, "output_tokens"))
		u.CacheRead, u.CacheReadSet = asInt64(valueAt(values, usageFields, "cache_read_tokens"))
		u.CacheWrite, u.CacheWriteSet = asInt64(valueAt(values, usageFields, "cache_write_tokens"))
		u.Reasoning, u.ReasoningSet = asInt64(valueAt(values, usageFields, "reasoning_tokens"))
		u.EstimatedCost, u.EstimatedCostSet = asFloat64(valueAt(values, usageFields, "estimated_cost_usd"))
		u.ActualCost, u.ActualCostSet = asFloat64(valueAt(values, usageFields, "actual_cost_usd"))
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hermes iterate usage %q: %w", sessionID, err)
	}
	return out, nil
}

func (u usageRow) toModelUsage() model.ModelUsage {
	tokens := model.TokenUsage{PromptTokens: u.Input, CompletionTokens: u.Output, CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite, ReasoningTokens: u.Reasoning}
	if u.InputSet {
		tokens.Present.Input = model.PresenceExact
	}
	if u.OutputSet {
		tokens.Present.Output = model.PresenceExact
	}
	if u.CacheReadSet {
		tokens.Present.CacheRead = model.PresenceExact
	}
	if u.CacheWriteSet {
		tokens.Present.CacheWrite = model.PresenceExact
	}
	if u.ReasoningSet {
		tokens.Present.Reasoning = model.PresenceExact
	}
	amount := 0.0
	if u.ActualCostSet {
		amount = u.ActualCost
	} else if u.EstimatedCostSet {
		amount = u.EstimatedCost
	}
	return model.ModelUsage{Model: u.Model, Requests: nativeIntOrZero(u.Requests), BillingAmount: amount, Usage: tokens}
}
