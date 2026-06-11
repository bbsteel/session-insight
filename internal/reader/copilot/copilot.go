package copilot

import (
	"os"
	"path/filepath"
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
