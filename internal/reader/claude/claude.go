package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/shared"
)

type ClaudeReader struct {
	projectsDir string
}

func New(projectsDir string) *ClaudeReader {
	return &ClaudeReader{projectsDir: projectsDir}
}

func (r *ClaudeReader) WatchRoots() []string { return []string{r.projectsDir} }

func (r *ClaudeReader) AgentType() string  { return "claude" }
func (r *ClaudeReader) DisplayName() string { return "Claude Code" }

// ---- JSONL event shapes ----

type claudeEvent struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	ParentUUID  string `json:"parentUuid"`
	SessionID   string `json:"sessionId"`
	Timestamp   string `json:"timestamp"`
	CWD         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	Version     string `json:"version"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`

	Message       *claudeMessage    `json:"message"`
	ToolUseResult *claudeToolResult `json:"toolUseResult"`

	Subtype    string `json:"subtype"`
	Content    string `json:"content"`
	DurationMs int64  `json:"durationMs"`

	AITitle    string `json:"aiTitle"`
	LastPrompt string `json:"lastPrompt"`
}

type claudeMessage struct {
	ID      string           `json:"id"`
	Role    string           `json:"role"`
	Model   string           `json:"model"`
	Content json.RawMessage  `json:"content"`
	Usage   *claudeUsage     `json:"usage"`

	// StopReason closes a turn: the CLI's final assistant line for a turn
	// carries "end_turn", while mid-turn lines carry "tool_use" (or nothing
	// on older versions). Used for the trailing in-progress detection.
	StopReason string `json:"stop_reason"`
}

type claudeContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type claudeToolResult struct {
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	IsError bool   `json:"is_error"`

	// AgentID is only present when this ToolUseResult wraps an "Agent"
	// (subagent/Task) tool call. It matches the <id> in the sibling
	// "subagents/agent-<id>.jsonl" transcript file, and is the join key
	// used by ParseClaudeRenderEventsWithSubagents to stitch the subagent's
	// full transcript into the parent session's render stream. Additive
	// field — does not change behavior for any existing consumer of this
	// struct (parseClaudeEvents ignores fields it doesn't read).
	AgentID string `json:"agentId"`
}

// ---- Content helpers (handles both string and array content) ----

func (m *claudeMessage) contentString() string {
	if m == nil || len(m.Content) == 0 {
		return ""
	}
	// Try string first
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	// Try array of blocks
	var blocks []claudeContentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "")
}

func (m *claudeMessage) contentBlocks() []claudeContentBlock {
	if m == nil {
		return nil
	}
	var blocks []claudeContentBlock
	if json.Unmarshal(m.Content, &blocks) == nil {
		return blocks
	}
	// String content → no blocks
	return nil
}

// ---- ListSessions ----

func (r *ClaudeReader) ListSessions() ([]model.Session, error) {
	entries, err := os.ReadDir(r.projectsDir)
	if err != nil {
		return nil, err
	}

	type fileJob struct {
		path, id string
	}
	var jobs []fileJob
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "memory" {
			continue
		}
		projDir := filepath.Join(r.projectsDir, entry.Name())
		jsonlFiles, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
		for _, jf := range jsonlFiles {
			jobs = append(jobs, fileJob{jf, strings.TrimSuffix(filepath.Base(jf), ".jsonl")})
		}
	}

	results := make(chan model.Session, len(jobs))
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j fileJob) {
			defer func() { <-sem; wg.Done() }()
			if sess, ok := readSessionMeta(j.path, j.id); ok {
				results <- sess
			}
		}(job)
	}
	wg.Wait()
	close(results)

	sessions := make([]model.Session, 0, len(results))
	for s := range results {
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func readSessionMeta(jsonlPath, sessionID string) (model.Session, bool) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return model.Session{}, false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return model.Session{}, false
	}
	fileSize := fi.Size()

	var (
		cwd          string
		gitBranch    string
		modelName    string
		aiTitle      string
		lastPrompt   string
		firstUserMsg string
		userMessages []string
		createdAt    time.Time
		updatedAt    time.Time
		headLines    int
		headBytes    int64
		foundCWD     bool
	)

	// Phase 1: scan first ~200 lines (with JSON parse) for early metadata
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	const headMax = 200
	for scanner.Scan() && headLines < headMax {
		headLines++
		headBytes += int64(len(scanner.Bytes()) + 1)
		parseEventLine(scanner.Bytes(), &cwd, &gitBranch, &modelName, &aiTitle, &lastPrompt,
			&firstUserMsg, &userMessages, &createdAt, &updatedAt, &foundCWD)
	}

	// Estimate total lines from head scan average (avoids full-file read)
	lineCount := shared.EstimateLineCount(headLines, headBytes, fileSize)

	// Phase 3: seek to last 8 KB for tail metadata (ai-title, last-prompt, updatedAt)
	const tailBytes = 8 * 1024
	if fileSize > tailBytes {
		seekPos := fileSize - tailBytes
		if _, err := f.Seek(seekPos, 0); err != nil {
			return model.Session{}, false
		}
		// skip partial first line at seek boundary
		tailScanner := bufio.NewScanner(f)
		tailScanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
		tailScanner.Scan()
		for tailScanner.Scan() {
			parseTailEvent(tailScanner.Bytes(), &aiTitle, &lastPrompt, &updatedAt)
		}
	}

	name := resolveSessionName(aiTitle, lastPrompt, firstUserMsg, createdAt)
	previewText := shared.BuildPreviewText(userMessages)

	return model.Session{
		ID:           sessionID,
		AgentType:    "claude",
		CWD:          cwd,
		Branch:       gitBranch,
		Project:      shared.ResolveProject(cwd, ""),
		Name:         name,
		ModelName:    modelName,
		PreviewText:  previewText,
		MessageCount: lineCount,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, true
}

func parseEventLine(line []byte, cwd, gitBranch, modelName, aiTitle, lastPrompt, firstUserMsg *string,
	userMessages *[]string, createdAt, updatedAt *time.Time, foundCWD *bool) {

	var evt claudeEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		return
	}

	if evt.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, evt.Timestamp); err == nil {
			if createdAt.IsZero() || t.Before(*createdAt) {
				*createdAt = t
			}
			if t.After(*updatedAt) {
				*updatedAt = t
			}
		}
	}

	if !*foundCWD && evt.CWD != "" {
		*cwd = evt.CWD
		*foundCWD = true
	}
	if *gitBranch == "" && evt.GitBranch != "" {
		*gitBranch = evt.GitBranch
	}

	switch evt.Type {
	case "ai-title":
		if evt.AITitle != "" {
			*aiTitle = evt.AITitle
		}
	case "last-prompt":
		if evt.LastPrompt != "" {
			*lastPrompt = evt.LastPrompt
		}
	case "assistant":
		if *modelName == "" && evt.Message != nil && evt.Message.Model != "" {
			*modelName = evt.Message.Model
		}
	case "user":
		if !evt.IsMeta && evt.ToolUseResult == nil && evt.Message != nil {
			msg := evt.Message.contentString()
			if *firstUserMsg == "" {
				*firstUserMsg = msg
			}
			if len(*userMessages) < 5 && msg != "" {
				*userMessages = append(*userMessages, msg)
			}
		}
	}
}

func parseTailEvent(line []byte, aiTitle, lastPrompt *string, updatedAt *time.Time) {
	var evt claudeEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		return
	}

	if evt.Type == "ai-title" && evt.AITitle != "" {
		*aiTitle = evt.AITitle
	}
	if evt.Type == "last-prompt" && evt.LastPrompt != "" {
		*lastPrompt = evt.LastPrompt
	}
	if evt.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, evt.Timestamp); err == nil {
			if t.After(*updatedAt) {
				*updatedAt = t
			}
		}
	}
}

func resolveSessionName(aiTitle, lastPrompt, firstUserMsg string, createdAt time.Time) string {
	if aiTitle != "" {
		return aiTitle
	}
	if lastPrompt != "" {
		return shared.TruncateRunes(lastPrompt, 50)
	}
	if firstUserMsg != "" {
		return shared.TruncateRunes(firstUserMsg, 50)
	}
	if !createdAt.IsZero() {
		return "Session " + createdAt.Format("01-02 15:04")
	}
	return "Session"
}

// ---- GetSession ----

func (r *ClaudeReader) GetSession(id string) (*model.SessionDetail, error) {
	jsonlPath := r.findSessionFile(id)
	if jsonlPath == "" {
		return nil, fmt.Errorf("claude session not found: %s", id)
	}

	session, ok := readSessionMeta(jsonlPath, id)
	if !ok {
		return nil, fmt.Errorf("failed to read session: %s", id)
	}

	turns, modelName, err := parseClaudeEvents(jsonlPath)
	if err != nil {
		return &model.SessionDetail{Session: session, Turns: []model.TurnVM{}}, nil
	}

	if modelName != "" {
		session.ModelName = modelName
	}
	session.TurnCount = len(turns)

	detail := &model.SessionDetail{Session: session, Turns: turns}

	detail.AnomalySummary = shared.RunAnomalyDetection(turns)

	return detail, nil
}

func (r *ClaudeReader) findSessionFile(sessionID string) string {
	entries, err := os.ReadDir(r.projectsDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "memory" {
			continue
		}
		candidate := filepath.Join(r.projectsDir, entry.Name(), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// ---- Event parsing ----

func parseClaudeEvents(path string) ([]model.TurnVM, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	var (
		turns       []model.TurnVM
		foundModel  string
		currentTurn *model.TurnVM
		toolUseMap  = make(map[string]string)
		turnStartTS string
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt claudeEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		switch {
		// New turn: user message (not tool result, not meta)
		case evt.Type == "user" && !evt.IsMeta && evt.ToolUseResult == nil && evt.Message != nil:
			if currentTurn != nil {
				turns = append(turns, *currentTurn)
			}
			currentTurn = &model.TurnVM{
				TurnIndex: len(turns),
				Events:    []model.EventVM{},
			}
			turnStartTS = evt.Timestamp
			currentTurn.UserMessage = cleanUserContent(evt.Message.contentString())

		// Tool result (Claude wraps these as user-type events)
		case evt.Type == "user" && evt.ToolUseResult != nil:
			if currentTurn == nil {
				continue
			}
			currentTurn.Events = append(currentTurn.Events, newEventVM("tool_result", evt.Timestamp, map[string]any{
				"stdout":   evt.ToolUseResult.Stdout,
				"stderr":   evt.ToolUseResult.Stderr,
				"is_error": evt.ToolUseResult.IsError,
			}))
			if evt.ToolUseResult.IsError || evt.ToolUseResult.Stderr != "" {
				currentTurn.ErrorCount++
			}

		// Assistant message
		case evt.Type == "assistant" && evt.Message != nil:
			if currentTurn == nil {
				continue
			}
			msg := evt.Message

			if foundModel == "" && msg.Model != "" {
				foundModel = msg.Model
			}

			if msg.Usage != nil {
				currentTurn.RequestCount++
				// Claude's usage is already in canonical exclusive semantics:
				// input_tokens excludes both cache buckets.
				u := &currentTurn.TokenUsage
				u.PromptTokens += msg.Usage.InputTokens
				u.CompletionTokens += msg.Usage.OutputTokens
				u.CacheReadTokens += msg.Usage.CacheReadInputTokens
				u.CacheWriteTokens += msg.Usage.CacheCreationInputTokens
				u.Present.Input = model.PresenceExact
				u.Present.Output = model.PresenceExact
				u.Present.CacheRead = model.PresenceExact
				u.Present.CacheWrite = model.PresenceExact
				// Thinking tokens are billed inside output_tokens with no
				// separate count: the reasoning bucket does not exist here.
				u.Present.Reasoning = model.PresenceNA
			}

			var textParts []string
			for _, block := range msg.contentBlocks() {
				switch block.Type {
				case "thinking":
					currentTurn.AssistantMessage += block.Thinking
				case "text":
					currentTurn.AssistantMessage += block.Text
					textParts = append(textParts, block.Text)
				case "tool_use":
					currentTurn.ToolCallCount++
					currentTurn.ToolNames = append(currentTurn.ToolNames, block.Name)
					if block.ID != "" {
						toolUseMap[block.ID] = block.Name
					}
					currentTurn.ToolDetails = append(currentTurn.ToolDetails, model.ToolCallVM{
						Name: block.Name,
					})
				}
			}

			currentTurn.Events = append(currentTurn.Events, newEventVM("assistant.message", evt.Timestamp, map[string]any{
				"model":  msg.Model,
				"blocks": len(msg.contentBlocks()),
				"text":   strings.Join(textParts, ""),
			}))

		// Turn boundary marker
		case evt.Type == "system" && evt.Subtype == "turn_duration":
			if currentTurn != nil {
				currentTurn.DurationMs = evt.DurationMs
			}

		case evt.Type == "system" && evt.Subtype == "compact_boundary":
			if currentTurn != nil {
				currentTurn.Anomalies = append(currentTurn.Anomalies, "compaction")
			}
		}

		// Track duration from turn start to latest event
		if currentTurn != nil && turnStartTS != "" && evt.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339, turnStartTS); err == nil {
				if t2, err2 := time.Parse(time.RFC3339, evt.Timestamp); err2 == nil {
					dur := t2.Sub(t).Milliseconds()
					if dur > currentTurn.DurationMs {
						currentTurn.DurationMs = dur
					}
				}
			}
		}
	}

	if currentTurn != nil {
		turns = append(turns, *currentTurn)
	}

	// Filter out noise turns (slash commands, hook events, etc.)
	turns = shared.FilterEmptyTurns(turns)

	// Resolve tool exit codes from tool_result events in same turn
	for i := range turns {
		for j := range turns[i].ToolDetails {
			for _, evt := range turns[i].Events {
				if evt.Type == "tool_result" {
					if isErr, ok := evt.Data["is_error"].(bool); ok && isErr {
						turns[i].ToolDetails[j].ExitCode = 1
					}
				}
			}
		}
	}

	_ = toolUseMap
	return turns, foundModel, scanner.Err()
}

func cleanUserContent(s string) string {
	for _, tag := range []string{
		"command-name", "command-message", "command-args",
		"local-command-stdout", "local-command-caveat",
		"bash-input", "bash-stdout", "bash-stderr",
	} {
		s = stripTag(s, tag)
	}
	return strings.TrimSpace(s)
}

func stripTag(s, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	for {
		start := strings.Index(s, openTag)
		if start < 0 {
			break
		}
		end := strings.Index(s, closeTag)
		if end < 0 {
			break
		}
		cutEnd := end + len(closeTag)
		if cutEnd < len(s) && s[cutEnd] == '\n' {
			cutEnd++
		}
		s = s[:start] + s[cutEnd:]
	}
	return s
}

func newEventVM(typ, ts string, data map[string]any) model.EventVM {
	return model.EventVM{Type: typ, Timestamp: ts, Data: data}
}

// LiveRevision is a stat-only change marker for live-tail polling.
func (r *ClaudeReader) LiveRevision(id string) (int64, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return 0, fmt.Errorf("claude session not found: %s", id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano() + info.Size(), nil
}
