package copilot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"session-insight/internal/model"
)

type CopilotReader struct {
	sessionDir string
}

type workspaceYAML struct {
	ID         string `yaml:"id"`
	CWD        string `yaml:"cwd"`
	Repository string `yaml:"repository"`
	Branch     string `yaml:"branch"`
	Name       string `yaml:"name"`
	UserNamed  bool   `yaml:"user_named"`
	CreatedAt  string `yaml:"created_at"`
	UpdatedAt  string `yaml:"updated_at"`
}

func New(sessionDir string) *CopilotReader {
	return &CopilotReader{sessionDir: sessionDir}
}

func (r *CopilotReader) AgentType() string { return "copilot" }

func (r *CopilotReader) ListSessions() ([]model.Session, error) {
	entries, err := os.ReadDir(r.sessionDir)
	if err != nil {
		return nil, err
	}

	var sessions []model.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Only list sessions that have events.jsonl
		eventsPath := filepath.Join(r.sessionDir, entry.Name(), "events.jsonl")
		if _, err := os.Stat(eventsPath); err != nil {
			continue
		}

		wsPath := filepath.Join(r.sessionDir, entry.Name(), "workspace.yaml")
		data, err := os.ReadFile(wsPath)
		if err != nil {
			continue
		}

		var ws workspaceYAML
		if err := yaml.Unmarshal(data, &ws); err != nil {
			continue
		}

		sessions = append(sessions, toSession(ws))
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func toSession(ws workspaceYAML) model.Session {
	name := ""
	if ws.UserNamed && ws.Name != "" {
		name = ws.Name
	}

	createdAt, _ := time.Parse(time.RFC3339, ws.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, ws.UpdatedAt)

	return model.Session{
		ID:         ws.ID,
		AgentType:  "copilot",
		CWD:        ws.CWD,
		Repository: ws.Repository,
		Branch:     ws.Branch,
		Name:       name,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
}

type jsonlEvent struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

func (r *CopilotReader) GetSession(id string) (*model.SessionDetail, error) {
	wsPath := filepath.Join(r.sessionDir, id, "workspace.yaml")
	data, err := os.ReadFile(wsPath)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	var ws workspaceYAML
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("invalid workspace.yaml: %w", err)
	}

	session := toSession(ws)

	eventsPath := filepath.Join(r.sessionDir, id, "events.jsonl")
	turns, modelName, err := parseEventsJSONL(eventsPath)
	if err != nil {
		return &model.SessionDetail{Session: session, Turns: []model.TurnVM{}}, nil
	}

	if modelName != "" {
		session.ModelName = modelName
	}
	session.TurnCount = len(turns)

	msgCount := 0
	for _, t := range turns {
		msgCount += len(t.Events)
	}
	session.MessageCount = msgCount

	detail := &model.SessionDetail{Session: session, Turns: turns}

	// Anomaly detection
	hasShutdown := false
	var durations []int64
	for _, t := range turns {
		for _, e := range t.Events {
			if e.Type == "session.shutdown" {
				hasShutdown = true
			}
		}
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
		if !hasShutdown && len(turns) > 0 {
			summary.MissingShutdown = true
		}
		summary.TotalAnomalies = summary.ToolFailures + summary.DurationSpikes
		if summary.MissingShutdown {
			summary.TotalAnomalies++
		}
		detail.AnomalySummary = summary
	}

	return detail, nil
}

func parseEventsJSONL(path string) ([]model.TurnVM, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	var turns []model.TurnVM
	var foundModel string
	var currentTurn *model.TurnVM
	var turnStartTimestamp string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt jsonlEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		switch {
		case evt.Type == "user.message":
			if currentTurn != nil {
				turns = append(turns, *currentTurn)
			}
			currentTurn = &model.TurnVM{
				TurnIndex: len(turns),
				Events:    []model.EventVM{},
			}
			turnStartTimestamp = evt.Timestamp
			if content, ok := extractString(evt.Data, "content"); ok {
				currentTurn.UserMessage = content
			}

		case evt.Type == "assistant.message":
			if currentTurn == nil {
				continue
			}
			content, hasContent := extractString(evt.Data, "content")
			if !hasContent {
				content, hasContent = extractString(evt.Data, "encryptedContent")
			}
			if hasContent {
				currentTurn.AssistantMessage += content
			}
			currentTurn.TokenUsage.CompletionTokens += extractInt64(evt.Data, "outputTokens")

		case evt.Type == "tool.execution_start":
			if currentTurn == nil {
				continue
			}
			if name, ok := extractString(evt.Data, "toolName"); ok && name != "" {
				currentTurn.ToolNames = append(currentTurn.ToolNames, name)
			}

		case evt.Type == "tool.execution_complete":
			if currentTurn == nil {
				continue
			}
			currentTurn.ToolCallCount++
			if code := extractFloat(evt.Data, "exit_code"); code != 0 {
				currentTurn.ErrorCount++
			}

		case evt.Type == "session.model_change":
			if foundModel == "" {
				if name, ok := extractString(evt.Data, "newModel"); ok && name != "" {
					foundModel = name
				}
			}
		case evt.Type == "session.shutdown":
			if currentTurn != nil {
				currentTurn.TokenUsage.PremiumRequests = int(extractFloat(evt.Data, "premium_requests"))
			}
		}

		if currentTurn != nil {
			currentTurn.Events = append(currentTurn.Events, model.EventVM{
				Type:      evt.Type,
				Timestamp: evt.Timestamp,
				Data:      evt.Data,
			})
		}

		if currentTurn != nil && turnStartTimestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, turnStartTimestamp); err == nil {
				if t2, err2 := time.Parse(time.RFC3339Nano, evt.Timestamp); err2 == nil {
					currentTurn.DurationMs = t2.Sub(t).Milliseconds()
				}
			}
		}
	}

	if currentTurn != nil {
		turns = append(turns, *currentTurn)
	}

	return turns, foundModel, scanner.Err()
}

func extractString(data map[string]any, key string) (string, bool) {
	if v, ok := data[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

func extractInt64(data map[string]any, key string) int64 {
	if v, ok := data[key]; ok {
		if f, ok := v.(float64); ok {
			return int64(f)
		}
	}
	return 0
}

func extractFloat(data map[string]any, key string) float64 {
	if v, ok := data[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
