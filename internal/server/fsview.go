package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// Read-only filesystem endpoints backing the new-tab file viewer: directory
// listing for the tree pane and capped file content for the code pane. Both
// run with the server user's OS permissions on a loopback-only listener.

const fsReadLimit = 1 << 20 // 1 MiB is plenty for a code viewer

type fsEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Clean(r.URL.Query().Get("dir"))
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be absolute", http.StatusBadRequest)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "cannot list directory", http.StatusNotFound)
		return
	}
	out := make([]fsEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, fsEntry{Name: e.Name(), IsDir: e.IsDir()})
		if len(out) >= 2000 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir // directories first
		}
		return out[i].Name < out[j].Name
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleFsRead(w http.ResponseWriter, r *http.Request) {
	path, err := resolveExistingFile(r.URL.Query().Get("path"), r.URL.Query().Get("cwd"))
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "cannot open file", http.StatusNotFound)
		return
	}
	defer f.Close()

	buf := make([]byte, fsReadLimit+1)
	n, _ := f.Read(buf)
	truncated := n > fsReadLimit
	if truncated {
		n = fsReadLimit
	}
	content := buf[:n]
	if bytes.IndexByte(content, 0) >= 0 {
		http.Error(w, "binary file", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":      path,
		"content":   string(content),
		"truncated": truncated,
	})
}
