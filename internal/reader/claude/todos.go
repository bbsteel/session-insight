package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// readClaudeTodos loads the session's todo sidecars. A missing directory is
// an empty list, not a failed session read.
func readClaudeTodos(root, sessionID string) []model.Todo {
	if root == "" || !validSessionID(sessionID) {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(root, "todos", sessionID+"*.json"))
	if err != nil || len(matches) == 0 {
		return nil
	}
	var todos []model.Todo
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var items []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Title   string `json:"title"`
			Status  string `json:"status"`
		}
		if json.Unmarshal(body, &items) != nil {
			continue
		}
		for index, item := range items {
			title := strings.TrimSpace(item.Content)
			if title == "" {
				title = strings.TrimSpace(item.Title)
			}
			id := item.ID
			if id == "" {
				id = fmt.Sprintf("%s:%d", filepath.Base(path), index)
			}
			todos = append(todos, model.Todo{ID: id, Title: title, Status: item.Status})
		}
	}
	return todos
}
