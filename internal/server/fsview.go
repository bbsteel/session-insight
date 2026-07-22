package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// Read-only filesystem endpoints backing the new-tab file viewer: directory
// listing for the tree pane and capped file content for the code pane. Both
// run with the server user's OS permissions on a loopback-only listener.

const fsReadLimit = 2 << 20 // files above 2 MiB remain virtual plain-text previews

type fsEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

func filesystemErrorStatus(err error) int {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return http.StatusNotFound
	case errors.Is(err, fs.ErrPermission):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Clean(r.URL.Query().Get("dir"))
	if !filepath.IsAbs(dir) {
		writeAPIError(w, http.StatusBadRequest, "directory_list_failed", "dir must be absolute")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeAPIError(w, filesystemErrorStatus(err), "directory_list_failed", err.Error())
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
		writeAPIError(w, http.StatusNotFound, "file_read_failed", "file not found")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeAPIError(w, filesystemErrorStatus(err), "file_read_failed", err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "file_read_failed", err.Error())
		return
	}

	buf := make([]byte, fsReadLimit+1)
	n, _ := f.Read(buf)
	truncated := n > fsReadLimit
	if truncated {
		n = fsReadLimit
	}
	content := buf[:n]
	if bytes.IndexByte(content, 0) >= 0 {
		writeAPIError(w, http.StatusUnsupportedMediaType, "binary_file")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":      path,
		"content":   string(content),
		"truncated": truncated,
		"size":      info.Size(),
	})
}
