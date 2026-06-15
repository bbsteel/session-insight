package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"session-insight/internal/model"
)

type ClaudeReader struct {
	projectsDir string
}

func New(projectsDir string) *ClaudeReader {
	return &ClaudeReader{projectsDir: projectsDir}
}

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
	lineCount := estimateLineCount(headLines, headBytes, fileSize)

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
	previewText := buildPreviewText(userMessages)

	return model.Session{
		ID:           sessionID,
		AgentType:    "claude",
		CWD:          cwd,
		Repository:   "",
		Branch:       gitBranch,
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

func estimateLineCount(headLines int, headBytes int64, fileSize int64) int {
	if headLines == 0 || headBytes == 0 {
		// fallback: ~400 bytes per JSONL line
		return int(fileSize / 400)
	}
	avgLineLen := float64(headBytes) / float64(headLines)
	// +10% to compensate for typically longer lines later in sessions
	return int(float64(fileSize)/avgLineLen * 1.1)
}

func resolveSessionName(aiTitle, lastPrompt, firstUserMsg string, createdAt time.Time) string {
	if aiTitle != "" {
		return aiTitle
	}
	if lastPrompt != "" {
		return truncateRunes(lastPrompt, 50)
	}
	if firstUserMsg != "" {
		return truncateRunes(firstUserMsg, 50)
	}
	if !createdAt.IsZero() {
		return "Session " + createdAt.Format("01-02 15:04")
	}
	return "Session"
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func buildPreviewText(messages []string) string {
	// Truncate each message to 200 runes, join, cap total at ~1500 bytes
	const maxPerMsg = 200
	const maxTotal = 1500
	var parts []string
	for _, m := range messages {
		parts = append(parts, truncateRunes(m, maxPerMsg))
	}
	joined := strings.Join(parts, " | ")
	if len(joined) > maxTotal {
		return joined[:maxTotal] + "..."
	}
	return joined
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

	// Anomaly detection
	var durations []int64
	for _, t := range turns {
		if t.DurationMs > 0 {
			durations = append(durations, t.DurationMs)
		}
	}

	if len(durations) > 1 {
		var sum int64
		for _, d := range durations {
			sum += d
		}
		mean := float64(sum) / float64(len(durations))
		var variance float64
		for _, d := range durations {
			variance += (float64(d) - mean) * (float64(d) - mean)
		}
		stdDev := math.Sqrt(variance / float64(len(durations)))
		threshold := mean + 3*stdDev

		summary := model.AnomalySummary{}
		for i := range turns {
			if turns[i].ErrorCount > 0 {
				turns[i].Anomalies = append(turns[i].Anomalies, "tool_failure")
				summary.ToolFailures++
			}
			if float64(turns[i].DurationMs) > threshold && turns[i].DurationMs > 30000 {
				turns[i].Anomalies = append(turns[i].Anomalies, "duration_spike")
				summary.DurationSpikes++
			}
		}
		summary.TotalAnomalies = summary.ToolFailures + summary.DurationSpikes
		if len(turns) > 0 {
			detail.AnomalySummary = summary
		}
	}

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
				currentTurn.TokenUsage.PromptTokens += msg.Usage.InputTokens
				currentTurn.TokenUsage.CompletionTokens += msg.Usage.OutputTokens
				currentTurn.TokenUsage.CacheReadTokens += msg.Usage.CacheReadInputTokens
				currentTurn.TokenUsage.CacheWriteTokens += msg.Usage.CacheCreationInputTokens
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
	turns = filterEmptyTurns(turns)

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

func filterEmptyTurns(turns []model.TurnVM) []model.TurnVM {
	filtered := turns[:0]
	for _, t := range turns {
		if t.UserMessage == "" && t.AssistantMessage == "" && t.ToolCallCount == 0 {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
