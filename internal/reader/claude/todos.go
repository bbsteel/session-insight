package claude

import (
	"encoding/json"
	"fmt"
	"io/fs"
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
	// Resolve sidecars within the todo store, including when an entry is a symlink.
	todoRoot, err := os.OpenRoot(filepath.Join(root, "todos"))
	if err != nil {
		return nil
	}
	defer todoRoot.Close()
	entries, err := fs.ReadDir(todoRoot.FS(), ".")
	if err != nil {
		return nil
	}
	var todos []model.Todo
	for _, entry := range entries {
		filename := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(filename, ".json") {
			continue
		}
		// Match the ID literally so glob characters cannot select other sessions.
		if filename != sessionID+".json" && !strings.HasPrefix(filename, sessionID+"-agent-") {
			continue
		}
		body, err := todoRoot.ReadFile(filename)
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
				id = fmt.Sprintf("%s:%d", filename, index)
			}
			todos = append(todos, model.Todo{ID: id, Title: title, Status: item.Status})
		}
	}
	return todos
}
