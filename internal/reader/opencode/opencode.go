package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"session-insight/internal/model"
	"session-insight/internal/reader/shared"
)

type OpenCodeReader struct {
	db *sql.DB
}

func New(dbPath string) (*OpenCodeReader, error) {
	return newReader(dbPath, "mode=ro")
}

func newReader(dbPath, extraParams string) (*OpenCodeReader, error) {
	params := extraParams
	if params != "" {
		params = "?" + params
	}
	dsn := "file:" + dbPath + params
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &OpenCodeReader{db: db}, nil
}

func (r *OpenCodeReader) AgentType() string   { return "opencode" }
func (r *OpenCodeReader) DisplayName() string { return "OpenCode" }


// ---- data ----

type msgBase struct {
	Role string `json:"role"`
}

type assistantMsgData struct {
	Role       string  `json:"role"`
	ParentID   string  `json:"parentID"`
	ModelID    string  `json:"modelID"`
	ProviderID string  `json:"providerID"`
	Agent      string  `json:"agent"`
	Cost       float64 `json:"cost"`
	Tokens     *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     *struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Time *struct {
		Created   int64  `json:"created"`
		Completed *int64 `json:"completed,omitempty"`
	} `json:"time"`
	Error *json.RawMessage `json:"error,omitempty"`
}

type partData struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Synthetic bool   `json:"synthetic,omitempty"`
	CallID    string `json:"callID,omitempty"`
	Tool      string `json:"tool,omitempty"`
	State     *struct {
		Status string         `json:"status"`
		Output string         `json:"output,omitempty"`
		Error  string         `json:"error,omitempty"`
		Title  string         `json:"title,omitempty"`
		Input  map[string]any `json:"input,omitempty"`
		Time   *struct {
			Start int64  `json:"start"`
			End   *int64 `json:"end,omitempty"`
		} `json:"time,omitempty"`
	} `json:"state,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ---- resolved parts for a message ----

type resolvedParts struct {
	Texts     []string
	Reasoning []string
	Tools     []toolInfo
	Agents    []string
}

type toolInfo struct {
	Name     string
	Title    string
	Status   string
	Duration int64
	ExitCode int
	Input    map[string]any
}

// ---- ListSessions ----

func (r *OpenCodeReader) ListSessions() ([]model.Session, error) {
	rows, err := r.db.Query(`
		SELECT s.id, s.directory, s.title,
		       s.time_created, s.time_updated, s.time_archived,
		       s.model,
		       (SELECT json_extract(p.data, '$.text')
		        FROM message m
		        JOIN part p ON p.message_id = m.id
		        WHERE m.session_id = s.id
		          AND json_extract(m.data, '$.role') = 'user'
		          AND json_extract(p.data, '$.type') = 'text'
		        ORDER BY m.time_created ASC, p.id ASC
		        LIMIT 1) as preview_text,
		       (SELECT COUNT(*) FROM message WHERE session_id = s.id) as message_count,
		       (SELECT COUNT(*) FROM message WHERE session_id = s.id
		          AND json_extract(data, '$.role') = 'user') as turn_count
		FROM session s
		ORDER BY s.time_updated DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		var (
			id, directory, title     string
			timeCreated, timeUpdated int64
			timeArchived             sql.NullInt64
			modelJSON                sql.NullString
			previewText              sql.NullString
			messageCount             int
			turnCount                int
		)
		if err := rows.Scan(&id, &directory, &title,
			&timeCreated, &timeUpdated, &timeArchived,
			&modelJSON, &previewText, &messageCount, &turnCount); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}

		modelName := ""
		if modelJSON.Valid && modelJSON.String != "" {
			modelName = extractModelID(modelJSON.String)
		}

		createdAt := time.UnixMilli(timeCreated)
		updatedAt := time.UnixMilli(timeUpdated)

		sessions = append(sessions, model.Session{
			ID:           id,
			AgentType:    "opencode",
			CWD:          directory,
			Name:         resolveName(title, previewText.String, createdAt),
			ModelName:    modelName,
			PreviewText:  strings.TrimSpace(previewText.String),
			TurnCount:    turnCount,
			MessageCount: messageCount,
			IsLive:       !timeArchived.Valid,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		})
	}

	if sessions == nil {
		sessions = []model.Session{}
	}
	return sessions, nil
}

// ---- GetSession ----

func (r *OpenCodeReader) GetSession(id string) (*model.SessionDetail, error) {
	meta, err := r.readSessionMeta(id)
	if err != nil {
		return nil, err
	}

	turns, modelName := r.parseMessages(id)
	if modelName != "" && meta.ModelName == "" {
		meta.ModelName = modelName
	}
	meta.TurnCount = len(turns)

	todos := r.readTodos(id)

	detail := &model.SessionDetail{Session: meta, Turns: turns, Todos: todos}
	detail.AnomalySummary = shared.RunAnomalyDetection(turns)
	return detail, nil
}

func (r *OpenCodeReader) readSessionMeta(id string) (model.Session, error) {
	var (
		directory, title         string
		timeCreated, timeUpdated int64
		timeArchived             sql.NullInt64
		modelJSON                sql.NullString
	)
	err := r.db.QueryRow(`
		SELECT directory, title, time_created, time_updated, time_archived, model
		FROM session WHERE id = ?
	`, id).Scan(&directory, &title, &timeCreated, &timeUpdated, &timeArchived, &modelJSON)
	if err != nil {
		return model.Session{}, fmt.Errorf("openCode session not found: %s", id)
	}

	modelName := ""
	if modelJSON.Valid && modelJSON.String != "" {
		modelName = extractModelID(modelJSON.String)
	}

	createdAt := time.UnixMilli(timeCreated)
	updatedAt := time.UnixMilli(timeUpdated)

	msgCount := 0
	r.db.QueryRow("SELECT COUNT(*) FROM message WHERE session_id = ?", id).Scan(&msgCount)

	return model.Session{
		ID:           id,
		AgentType:    "opencode",
		CWD:          directory,
		Name:         title,
		ModelName:    modelName,
		TurnCount:    0,
		MessageCount: msgCount,
		IsLive:       !timeArchived.Valid,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}

// ---- Message/Turn parsing ----

func (r *OpenCodeReader) parseMessages(sessionID string) ([]model.TurnVM, string) {
	rows, err := r.db.Query(`
		SELECT id, time_created, data FROM message
		WHERE session_id = ?
		ORDER BY time_created ASC
	`, sessionID)
	if err != nil {
		return nil, ""
	}
	defer rows.Close()

	type row struct {
		id          string
		timeCreated int64
		data        string
	}
	var msgs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.timeCreated, &r.data); err != nil {
			continue
		}
		msgs = append(msgs, r)
	}

	var userMsgIDs []string
	userMsgs := make(map[string]row)
	assistantMsgs := make(map[string][]row)

	var foundModel string
	for _, m := range msgs {
		var base msgBase
		if json.Unmarshal([]byte(m.data), &base) != nil {
			continue
		}
		switch base.Role {
		case "user":
			userMsgIDs = append(userMsgIDs, m.id)
			userMsgs[m.id] = m
		case "assistant":
			var a assistantMsgData
			if json.Unmarshal([]byte(m.data), &a) == nil {
				assistantMsgs[a.ParentID] = append(assistantMsgs[a.ParentID], m)
				if foundModel == "" && a.ModelID != "" {
					foundModel = a.ModelID
				}
				if foundModel == "" && a.ProviderID != "" {
					foundModel = a.ProviderID + "/" + a.ModelID
				}
			}
		}
	}

	var turns []model.TurnVM
	for _, uid := range userMsgIDs {
		uMsg := userMsgs[uid]
		aMsgs := assistantMsgs[uid]
		if len(aMsgs) == 0 {
			continue
		}

		turn := model.TurnVM{
			TurnIndex: len(turns),
			Events:    []model.EventVM{},
		}

		uParts := r.readParts(uMsg.id)
		turn.UserMessage = strings.Join(uParts.Texts, "\n")

		var turnStartedAt, turnCompletedAt int64
		hasTurnTiming := false
		for _, aMsg := range aMsgs {
			var aData assistantMsgData
			if json.Unmarshal([]byte(aMsg.data), &aData) == nil {
				turn.AssistantMessage += buildAssistantText(aData)

				if aData.Tokens != nil {
					if aData.Tokens.Cache != nil {
						turn.TokenUsage.CacheReadTokens += aData.Tokens.Cache.Read
						turn.TokenUsage.CacheWriteTokens += aData.Tokens.Cache.Write
					}
					turn.TokenUsage.PromptTokens += aData.Tokens.Input
					turn.TokenUsage.CompletionTokens += aData.Tokens.Output + aData.Tokens.Reasoning
				}

				if aData.Time != nil && aData.Time.Completed != nil && *aData.Time.Completed >= aData.Time.Created {
					if !hasTurnTiming || aData.Time.Created < turnStartedAt {
						turnStartedAt = aData.Time.Created
					}
					if !hasTurnTiming || *aData.Time.Completed > turnCompletedAt {
						turnCompletedAt = *aData.Time.Completed
					}
					hasTurnTiming = true
				}
			}

			aParts := r.readParts(aMsg.id)
			turn.AssistantMessage += strings.Join(aParts.Texts, "\n")
			if len(aParts.Reasoning) > 0 {
				reasoning := strings.Join(aParts.Reasoning, "\n")
				if turn.AssistantMessage != "" {
					turn.AssistantMessage += "\n"
				}
				turn.AssistantMessage += "[思考]\n" + reasoning
			}

			for _, t := range aParts.Tools {
				turn.ToolCallCount++
				if t.Name != "" {
					turn.ToolNames = append(turn.ToolNames, t.Name)
				}
				if t.ExitCode != 0 {
					turn.ErrorCount++
				}
				turn.ToolDetails = append(turn.ToolDetails, model.ToolCallVM{
					Name:     t.Name,
					ExitCode: t.ExitCode,
					Duration: t.Duration,
				})
			}

			for _, ag := range aParts.Agents {
				turn.Subagents = append(turn.Subagents, ag)
			}
		}

		if hasTurnTiming {
			turn.DurationMs = turnCompletedAt - turnStartedAt
		}
		turn.AssistantMessage = strings.TrimSpace(turn.AssistantMessage)
		turns = append(turns, turn)
	}

	return turns, foundModel
}

func (r *OpenCodeReader) readParts(messageID string) resolvedParts {
	rows, err := r.db.Query(`
		SELECT data FROM part
		WHERE message_id = ?
		ORDER BY id ASC
	`, messageID)
	if err != nil {
		return resolvedParts{}
	}
	defer rows.Close()

	var out resolvedParts
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		var p partData
		if json.Unmarshal([]byte(data), &p) != nil {
			continue
		}
		switch p.Type {
		case "text":
			if p.Text != "" {
				out.Texts = append(out.Texts, p.Text)
			}
		case "reasoning":
			if p.Text != "" {
				out.Reasoning = append(out.Reasoning, p.Text)
			}
		case "tool":
			ti := toolInfo{Name: p.Tool, Status: "unknown"}
			if p.State != nil {
				ti.Status = p.State.Status
				ti.Title = p.State.Title
				ti.Input = p.State.Input
				if p.State.Status == "error" {
					ti.ExitCode = 1
				}
				if p.State.Status == "completed" && p.State.Time != nil && p.State.Time.End != nil {
					ti.Duration = *p.State.Time.End - p.State.Time.Start
				}
			}
			out.Tools = append(out.Tools, ti)
		case "agent":
			if p.Name != "" {
				out.Agents = append(out.Agents, p.Name)
			}
		}
	}
	return out
}

// ---- Todo ----

func (r *OpenCodeReader) readTodos(sessionID string) []model.Todo {
	rows, err := r.db.Query(`
		SELECT content, status, position FROM todo
		WHERE session_id = ?
		ORDER BY position ASC
	`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var todos []model.Todo
	for rows.Next() {
		var content, status string
		var position int
		if err := rows.Scan(&content, &status, &position); err != nil {
			continue
		}
		todos = append(todos, model.Todo{
			ID:     fmt.Sprintf("pos-%d", position),
			Title:  content,
			Status: status,
		})
	}
	return todos
}

// ---- helpers ----

func extractModelID(modelJSON string) string {
	var m struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(modelJSON), &m) == nil && m.ID != "" {
		return m.ID
	}
	return ""
}

func resolveName(title, previewText string, createdAt time.Time) string {
	if previewText != "" {
		return shared.TruncateRunes(previewText, 50)
	}
	if title != "" && !strings.HasPrefix(title, "New session") {
		return title
	}
	if !createdAt.IsZero() {
		return "OpenCode " + createdAt.Format("01-02 15:04")
	}
	return "OpenCode Session"
}

func buildAssistantText(a assistantMsgData) string {
	if a.Error != nil {
		return fmt.Sprintf("[错误: %s]", string(*a.Error))
	}
	return ""
}

// ---- Resolve DB path ----

func ResolveDBPath() (string, bool) {
	if env := os.Getenv("OPENCODE_DB"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, true
		}
		return "", false
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" {
		xdgData = filepath.Join(homeDir, ".local", "share")
	}
	dbPath := filepath.Join(xdgData, "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); err == nil {
		return dbPath, true
	}

	return "", false
}
