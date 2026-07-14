package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openAIClient speaks the OpenAI-compatible HTTP dialect: GET /models for
// discovery, POST /chat/completions (non-streaming) for generation.
type openAIClient struct {
	cfg Config
}

var openAIHTTPClient = &http.Client{Timeout: 5 * time.Minute}

func (c *openAIClient) endpoint(path string) string {
	return strings.TrimRight(c.cfg.BaseURL, "/") + path
}

func (c *openAIClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := openAIHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, truncateErrBody(data))
	}
	return data, nil
}

// truncateErrBody keeps upstream error text readable in the UI without
// dumping an entire HTML error page.
func truncateErrBody(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	if s == "" {
		s = "(empty response body)"
	}
	return s
}

func (c *openAIClient) ListModels(ctx context.Context) ([]Model, error) {
	data, err := c.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse /models response: %w", err)
	}
	models := make([]Model, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, Model{ID: m.ID, Label: m.ID})
	}
	return models, nil
}

func (c *openAIClient) Generate(ctx context.Context, prompt string, onStatus StatusFunc) (string, error) {
	if c.cfg.ModelID == "" {
		return "", fmt.Errorf("no model selected for this provider")
	}
	if onStatus != nil {
		onStatus("准备模型请求")
	}
	body := map[string]any{
		"model": c.cfg.ModelID,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream": false,
	}
	if onStatus != nil {
		onStatus("等待模型响应")
	}
	data, err := c.do(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	if onStatus != nil {
		onStatus("接收模型响应")
		onStatus("整理模型结果")
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("parse chat completion response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("model error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("model returned no choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("model returned empty content")
	}
	return content, nil
}
