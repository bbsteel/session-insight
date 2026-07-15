package db

import (
	"database/sql"
	"fmt"
)

// LLMProvider is one configured model source. kind='api' is an
// OpenAI-compatible HTTP endpoint; kind='acp' is a local agent CLI driven
// over the Agent Client Protocol. ModelID is always the user's explicit
// choice — generation never falls back to a provider-side default model.
type LLMProvider struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key,omitempty"`
	Agent      string `json:"agent"`
	ModelID    string `json:"model_id"`
	ModelLabel string `json:"model_label"`
	CreatedAt  string `json:"created_at"`
}

// AIGeneration is one saved AI output (summary / title / handoff / insight).
// Metadata is a JSON string of kind-specific structured extras (handoff:
// difficulty assessment + recommended executor list; insight: the validated
// structured Deep Insight plus its minimal cited-evidence projection); empty
// when absent. SourceRevision/PromptVersion/SourceFingerprint are populated for
// insight generations to drive freshness/staleness; other kinds leave them
// zero-valued for backward compatibility.
type AIGeneration struct {
	ID                int64  `json:"id"`
	Kind              string `json:"kind"`
	AgentType         string `json:"agent_type"`
	SessionID         string `json:"session_id"`
	ProviderName      string `json:"provider_name"`
	ModelID           string `json:"model_id"`
	Content           string `json:"content"`
	Metadata          string `json:"metadata,omitempty"`
	SourceRevision    int64  `json:"source_revision,omitempty"`
	PromptVersion     string `json:"prompt_version,omitempty"`
	SourceFingerprint string `json:"source_fingerprint,omitempty"`
	CreatedAt         string `json:"created_at"`
}

const llmDefaultProviderKey = "llm_default_provider_id"

func (db *DB) ListLLMProviders() ([]LLMProvider, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, kind, base_url, api_key, agent, model_id, model_label, created_at
		 FROM llm_providers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list llm providers: %w", err)
	}
	defer rows.Close()
	var out []LLMProvider
	for rows.Next() {
		var p LLMProvider
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey,
			&p.Agent, &p.ModelID, &p.ModelLabel, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan llm provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) GetLLMProvider(id int64) (*LLMProvider, error) {
	var p LLMProvider
	err := db.conn.QueryRow(
		`SELECT id, name, kind, base_url, api_key, agent, model_id, model_label, created_at
		 FROM llm_providers WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey,
		&p.Agent, &p.ModelID, &p.ModelLabel, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get llm provider %d: %w", id, err)
	}
	return &p, nil
}

func (db *DB) AddLLMProvider(p LLMProvider) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO llm_providers(name, kind, base_url, api_key, agent, model_id, model_label)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Kind, p.BaseURL, p.APIKey, p.Agent, p.ModelID, p.ModelLabel,
	)
	if err != nil {
		return 0, fmt.Errorf("add llm provider: %w", err)
	}
	return res.LastInsertId()
}

func (db *DB) UpdateLLMProvider(p LLMProvider) error {
	_, err := db.conn.Exec(
		`UPDATE llm_providers SET name = ?, kind = ?, base_url = ?, api_key = ?,
		 agent = ?, model_id = ?, model_label = ? WHERE id = ?`,
		p.Name, p.Kind, p.BaseURL, p.APIKey, p.Agent, p.ModelID, p.ModelLabel, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update llm provider %d: %w", p.ID, err)
	}
	return nil
}

func (db *DB) DeleteLLMProvider(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM llm_providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete llm provider %d: %w", id, err)
	}
	return nil
}

// DefaultLLMProviderID returns 0 when no default is set.
func (db *DB) DefaultLLMProviderID() (int64, error) {
	v, err := db.GetSetting(llmDefaultProviderKey)
	if err != nil || v == "" {
		return 0, err
	}
	var id int64
	if _, err := fmt.Sscanf(v, "%d", &id); err != nil {
		return 0, nil
	}
	return id, nil
}

func (db *DB) SetDefaultLLMProviderID(id int64) error {
	return db.SetSetting(llmDefaultProviderKey, fmt.Sprintf("%d", id))
}

func (db *DB) AddAIGeneration(g AIGeneration) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO ai_generations(kind, agent_type, session_id, provider_name, model_id, content, metadata, source_revision, prompt_version, source_fingerprint)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.Kind, g.AgentType, g.SessionID, g.ProviderName, g.ModelID, g.Content, g.Metadata,
		g.SourceRevision, g.PromptVersion, g.SourceFingerprint,
	)
	if err != nil {
		return 0, fmt.Errorf("add ai generation: %w", err)
	}
	return res.LastInsertId()
}

// ListAIGenerations filters by kind and/or session when non-empty, newest first.
func (db *DB) ListAIGenerations(kind, agentType, sessionID string, limit int) ([]AIGeneration, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT id, kind, agent_type, session_id, provider_name, model_id, content, metadata, source_revision, prompt_version, source_fingerprint, created_at
	      FROM ai_generations WHERE 1=1`
	var args []any
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	if agentType != "" && sessionID != "" {
		q += ` AND agent_type = ? AND session_id = ?`
		args = append(args, agentType, sessionID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list ai generations: %w", err)
	}
	defer rows.Close()
	var out []AIGeneration
	for rows.Next() {
		var g AIGeneration
		if err := rows.Scan(&g.ID, &g.Kind, &g.AgentType, &g.SessionID,
			&g.ProviderName, &g.ModelID, &g.Content, &g.Metadata,
			&g.SourceRevision, &g.PromptVersion, &g.SourceFingerprint, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ai generation: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// LatestAIGeneration returns the newest generation of kind for a session,
// or nil — used as the summary cache so reopening doesn't re-spend tokens.
func (db *DB) LatestAIGeneration(kind, agentType, sessionID string) (*AIGeneration, error) {
	var g AIGeneration
	err := db.conn.QueryRow(
		`SELECT id, kind, agent_type, session_id, provider_name, model_id, content, metadata, source_revision, prompt_version, source_fingerprint, created_at
		 FROM ai_generations WHERE kind = ? AND agent_type = ? AND session_id = ?
		 ORDER BY id DESC LIMIT 1`,
		kind, agentType, sessionID,
	).Scan(&g.ID, &g.Kind, &g.AgentType, &g.SessionID,
		&g.ProviderName, &g.ModelID, &g.Content, &g.Metadata,
		&g.SourceRevision, &g.PromptVersion, &g.SourceFingerprint, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest ai generation: %w", err)
	}
	return &g, nil
}

func (db *DB) DeleteAIGeneration(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM ai_generations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete ai generation %d: %w", id, err)
	}
	return nil
}

func (db *DB) SetTitleOverride(agentType, sessionID, title string) error {
	_, err := db.conn.Exec(
		`INSERT INTO session_title_overrides(agent_type, session_id, title, updated_at)
		 VALUES (?, ?, ?, datetime('now'))
		 ON CONFLICT(agent_type, session_id) DO UPDATE SET
		   title = excluded.title, updated_at = excluded.updated_at`,
		agentType, sessionID, title,
	)
	if err != nil {
		return fmt.Errorf("set title override: %w", err)
	}
	return nil
}

func (db *DB) RemoveTitleOverride(agentType, sessionID string) error {
	_, err := db.conn.Exec(
		`DELETE FROM session_title_overrides WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	)
	if err != nil {
		return fmt.Errorf("remove title override: %w", err)
	}
	return nil
}

// TitleOverrides returns all overrides keyed by BookmarkKey(agentType, sessionID).
func (db *DB) TitleOverrides() (map[string]string, error) {
	rows, err := db.conn.Query(`SELECT agent_type, session_id, title FROM session_title_overrides`)
	if err != nil {
		return nil, fmt.Errorf("list title overrides: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var agentType, sessionID, title string
		if err := rows.Scan(&agentType, &sessionID, &title); err != nil {
			return nil, fmt.Errorf("scan title override: %w", err)
		}
		out[BookmarkKey(agentType, sessionID)] = title
	}
	return out, rows.Err()
}

// TitleOverride returns "" when the session has no override.
func (db *DB) TitleOverride(agentType, sessionID string) (string, error) {
	var title string
	err := db.conn.QueryRow(
		`SELECT title FROM session_title_overrides WHERE agent_type = ? AND session_id = ?`,
		agentType, sessionID,
	).Scan(&title)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get title override: %w", err)
	}
	return title, nil
}
