package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bbsteel/session-insight/internal/render"
)

// Opening files in the user's editor. The command template lives server-side
// (app_settings, key "editor_command") and is never accepted from the request:
// the HTTP surface only ever supplies a file path that must exist on disk, so
// a hostile page on localhost cannot turn this endpoint into "run anything".

const (
	editorCommandKey = "editor_command"
	// Extension allowlist for the terminal file-open affordance; empty means
	// the frontend's built-in default list, "*" means no restriction.
	fileOpenExtsKey = "file_open_extensions"
	// Which message kinds get an HH:MM:SS prefix in the terminal render;
	// comma-separated subset of "user", "assistant", "tool". Empty = off.
	timestampKindsKey = "timestamp_kinds"
)

// rejectUnsafeWrite guards the state-changing endpoints against cross-site
// requests from web pages: a strict JSON Content-Type kills text/plain
// "simple request" smuggling (no preflight), and any present Origin header
// must be loopback. Non-browser local clients (curl) send no Origin and pass.
func rejectUnsafeWrite(w http.ResponseWriter, r *http.Request) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeAPIError(w, http.StatusUnsupportedMediaType, "invalid_request", "expected application/json")
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		host := ""
		if err == nil {
			host = u.Hostname()
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			writeAPIError(w, http.StatusForbidden, "request_forbidden", "cross-origin request rejected")
			return true
		}
	}
	return false
}

// startEditorCommand is swapped out by tests to capture the argv instead of
// actually launching an editor.
var startEditorCommand = func(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (s *Server) editorCommandTemplate() string {
	if s.DB != nil {
		if v, err := s.DB.GetSetting(editorCommandKey); err == nil && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if _, err := exec.LookPath("code"); err == nil {
		return "code --goto {path}:{line}"
	}
	return "xdg-open {path}"
}

// buildEditorArgs expands {path} and {line} inside each whitespace-separated
// template field. Templates without a {path} placeholder get the path appended.
func buildEditorArgs(template, path string, line int) []string {
	if line <= 0 {
		line = 1
	}
	fields := strings.Fields(template)
	args := make([]string, 0, len(fields)+1)
	sawPath := false
	for _, f := range fields {
		if strings.Contains(f, "{path}") {
			sawPath = true
		}
		f = strings.ReplaceAll(f, "{path}", path)
		f = strings.ReplaceAll(f, "{line}", strconv.Itoa(line))
		args = append(args, f)
	}
	if !sawPath {
		args = append(args, path)
	}
	return args
}

// resolveExistingFile normalises path (expanding ~ and joining relative paths
// onto cwd) and returns the absolute path only if it is an existing regular
// file.
func resolveExistingFile(path, cwd string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		if cwd == "" || !filepath.IsAbs(cwd) {
			return "", fmt.Errorf("relative path without absolute cwd: %s", path)
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	return path, nil
}

// findLineBySearch returns the 1-based line whose trimmed content matches the
// trimmed needle (exact first, then substring), or 0 when not found. Used for
// best-effort "jump to the edit" — the file may have changed since the session.
func findLineBySearch(path, needle string) int {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	firstContains := 0
	for scanner.Scan() {
		lineNo++
		text := strings.TrimSpace(scanner.Text())
		if text == needle {
			return lineNo
		}
		if firstContains == 0 && strings.Contains(text, needle) {
			firstContains = lineNo
		}
	}
	return firstContains
}

// handleResolveFile checks whether a path (possibly relative to the session
// cwd) exists as a regular file, so the context menu only offers "open in
// editor" for real files.
func (s *Server) handleResolveFile(w http.ResponseWriter, r *http.Request) {
	resolved, err := resolveExistingFile(r.URL.Query().Get("path"), r.URL.Query().Get("cwd"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "open_file_failed", "file not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": resolved})
}

func (s *Server) handleOpenFile(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) {
		return
	}
	var req struct {
		Path   string `json:"path"`
		Cwd    string `json:"cwd"`
		Line   int    `json:"line"`
		Search string `json:"search"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "open_file_failed", "invalid request body")
		return
	}
	resolved, err := resolveExistingFile(req.Path, req.Cwd)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "open_file_failed", "file not found")
		return
	}

	line := req.Line
	if line <= 0 && req.Search != "" {
		line = findLineBySearch(resolved, req.Search)
	}

	args := buildEditorArgs(s.editorCommandTemplate(), resolved, line)
	if len(args) == 0 {
		writeAPIError(w, http.StatusInternalServerError, "open_file_failed", "editor command not configured")
		return
	}
	if err := startEditorCommand(exec.Command(args[0], args[1:]...)); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "open_file_failed", fmt.Sprintf("failed to launch editor: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"path": resolved, "line": line})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	editorCmd, fileExts, tsKinds := "", "", ""
	if s.DB != nil {
		if v, err := s.DB.GetSetting(editorCommandKey); err == nil {
			editorCmd = v
		}
		if v, err := s.DB.GetSetting(fileOpenExtsKey); err == nil {
			fileExts = v
		}
		if v, err := s.DB.GetSetting(timestampKindsKey); err == nil {
			tsKinds = v
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"editor_command":         editorCmd,
		"editor_command_default": s.editorCommandTemplate(),
		"file_open_extensions":   fileExts,
		"timestamp_kinds":        tsKinds,
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	if rejectUnsafeWrite(w, r) {
		return
	}
	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	var req struct {
		EditorCommand      *string `json:"editor_command"`
		FileOpenExtensions *string `json:"file_open_extensions"`
		TimestampKinds     *string `json:"timestamp_kinds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.EditorCommand != nil {
		if err := s.DB.SetSetting(editorCommandKey, strings.TrimSpace(*req.EditorCommand)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.FileOpenExtensions != nil {
		if err := s.DB.SetSetting(fileOpenExtsKey, strings.TrimSpace(*req.FileOpenExtensions)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.TimestampKinds != nil {
		// Canonicalize through the parser so only known kinds are stored.
		canonical := render.ParseTimestampKinds(*req.TimestampKinds).KindsString()
		if err := s.DB.SetSetting(timestampKindsKey, canonical); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
