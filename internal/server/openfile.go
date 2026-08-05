package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/render"
)

// Opening files in the user's editor. The command template lives server-side
// (app_settings, key "editor_command") and is never accepted from the request:
// the HTTP surface only ever supplies a file path that must exist on disk, so
// a hostile page on localhost cannot turn this endpoint into "run anything".
//
// Default open strategy (multi-OS / multi-scenario):
//  1. User-configured editor_command (if set and launchable)
//  2. Platform-specific editor candidates (VS Code / Cursor / Kate / …)
//  3. OS default opener (open / start / xdg-open as last resort)
// Each step falls through on LookPath or Start failure so one broken helper
// cannot strand the user.

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

// lookPath and runtimeGOOS are injectable so tests can cover Windows/macOS
// candidate lists without depending on the host OS or installed binaries.
var (
	lookPath    = exec.LookPath
	runtimeGOOS = runtime.GOOS
	osGetuid    = os.Getuid
)

// desktopEnvKeys are session variables graphical apps (especially KDE/Kate)
// need. SI is often started from a non-graphical parent and lacks them; we
// refill from the live user systemd environment and local sockets.
var desktopEnvKeys = []string{
	"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR",
	"DBUS_SESSION_BUS_ADDRESS", "XDG_CURRENT_DESKTOP", "DESKTOP_SESSION",
	"KDE_FULL_SESSION", "KDE_SESSION_VERSION", "KDE_SESSION_UID",
	"KDE_APPLICATIONS_AS_SCOPE", "XDG_SESSION_TYPE", "XDG_SESSION_CLASS",
	"XDG_SEAT", "XDG_VTNR", "SESSION_MANAGER", "QT_QPA_PLATFORM",
	"QT_WAYLAND_RECONNECT", "XDG_CONFIG_DIRS", "XDG_DATA_DIRS",
	"HOME", "LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE",
}

// loadUserSystemdEnv returns KEY→value from `systemctl --user show-environment`.
// That map is the graphical session's view of DISPLAY/KDE_*/bus — more reliable
// than whatever incomplete env the SI process inherited.
//
// Bounded with a short deadline so a hung user manager cannot stall open-file
// HTTP handlers. Skipped on platforms without systemd user sessions.
func loadUserSystemdEnv() map[string]string {
	if runtimeGOOS == "windows" || runtimeGOOS == "darwin" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "show-environment").Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Values may be shell-quoted: KEY=$'...' or KEY="..."
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			continue
		}
		v = unquoteSystemdEnvValue(v)
		m[k] = v
	}
	return m
}

// desktopEnvCache avoids re-running systemctl for every editor candidate during
// one open cascade (and briefly across nearby opens).
var desktopEnvCache struct {
	mu  sync.Mutex
	env []string
	at  time.Time
}

const desktopEnvCacheTTL = 5 * time.Second

// unquoteSystemdEnvValue strips common systemctl show-environment quoting.
func unquoteSystemdEnvValue(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	// $'...' ANSI-C quoting — strip the outer form; content is usually plain.
	if strings.HasPrefix(v, "$'") && strings.HasSuffix(v, "'") && len(v) >= 3 {
		return v[2 : len(v)-1]
	}
	return v
}

// desktopSessionEnv returns an environment suitable for launching GUI apps
// from a long-running server. SI is often started from a non-graphical parent
// (agent, systemd service, nohup). KDE apps (Kate) then inherit a missing or
// stale DBUS_SESSION_BUS_ADDRESS / KDE_FULL_SESSION and pop "KIO worker /
// file protocol" errors even when the document still opens. We re-point at
// the user's session bus, display, and Plasma session flags.
//
// Results are cached briefly so open cascades (many candidates) do not each
// re-invoke systemctl.
func desktopSessionEnv() []string {
	desktopEnvCache.mu.Lock()
	if desktopEnvCache.env != nil && time.Since(desktopEnvCache.at) < desktopEnvCacheTTL {
		env := append([]string(nil), desktopEnvCache.env...)
		desktopEnvCache.mu.Unlock()
		return env
	}
	desktopEnvCache.mu.Unlock()

	env := buildDesktopSessionEnv()

	desktopEnvCache.mu.Lock()
	desktopEnvCache.env = append([]string(nil), env...)
	desktopEnvCache.at = time.Now()
	desktopEnvCache.mu.Unlock()
	return env
}

func buildDesktopSessionEnv() []string {
	env := os.Environ()
	put := func(key, val string) {
		if key == "" {
			return
		}
		prefix := key + "="
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = prefix + val
				return
			}
		}
		env = append(env, prefix+val)
	}
	// Prefer the live graphical session's exported environment.
	if userEnv := loadUserSystemdEnv(); userEnv != nil {
		for _, k := range desktopEnvKeys {
			if v, ok := userEnv[k]; ok && v != "" {
				put(k, v)
			}
		}
	}
	uid := osGetuid()
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	if st, err := os.Stat(runtimeDir); err == nil && st.IsDir() {
		put("XDG_RUNTIME_DIR", runtimeDir)
		busPath := filepath.Join(runtimeDir, "bus")
		if _, err := os.Stat(busPath); err == nil {
			// Always prefer the live user bus socket over a stale inherited value.
			put("DBUS_SESSION_BUS_ADDRESS", "unix:path="+busPath)
		}
		// XAUTHORITY often lives under the runtime dir with a random suffix.
		if cur := envLookup(env, "XAUTHORITY"); cur == "" || fileMissing(cur) {
			if matches, _ := filepath.Glob(filepath.Join(runtimeDir, "xauth_*")); len(matches) > 0 {
				put("XAUTHORITY", matches[0])
			}
		}
		if envLookup(env, "WAYLAND_DISPLAY") == "" {
			if _, err := os.Stat(filepath.Join(runtimeDir, "wayland-0")); err == nil {
				put("WAYLAND_DISPLAY", "wayland-0")
			}
		}
	}
	if envLookup(env, "DISPLAY") == "" {
		if _, err := os.Stat("/tmp/.X11-unix/X0"); err == nil {
			put("DISPLAY", ":0")
		}
	}
	return env
}

func envLookup(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}

func fileMissing(path string) bool {
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err != nil
}

// trySystemdUserRun launches argv inside the calling user's systemd --user
// session so KDE/Qt services (kiod, KIO file workers) are available. Returns
// errNoSystemdRun when systemd-run is missing so callers can fall back.
var errNoSystemdRun = fmt.Errorf("systemd-run unavailable")

func trySystemdUserRun(bin string, args []string) error {
	sr, err := lookPath("systemd-run")
	if err != nil {
		return errNoSystemdRun
	}
	sessionEnv := desktopSessionEnv()
	// systemd-run --user creates a transient service whose environment is the
	// user manager's, not necessarily the caller's. Pass desktop keys via
	// --setenv so Kate/KIO see DISPLAY, the session bus, and KDE_FULL_SESSION.
	srArgs := []string{"--user", "--quiet", "--collect"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		srArgs = append(srArgs, "--working-directory="+home)
	}
	for _, k := range desktopEnvKeys {
		if v := envLookup(sessionEnv, k); v != "" {
			srArgs = append(srArgs, "--setenv="+k+"="+v)
		}
	}
	srArgs = append(srArgs, "--", bin)
	srArgs = append(srArgs, args...)
	cmd := exec.Command(sr, srArgs...)
	// Also set the systemd-run process env so it can reach the user bus.
	cmd.Env = sessionEnv
	cmd.Stdout = nil
	cmd.Stderr = nil
	return startEditorCommand(cmd)
}

// editorCandidate is one preferred open strategy: any of Bins may satisfy the
// candidate; Template is expanded via buildEditorArgs ({path}, {line}).
type editorCandidate struct {
	Bins     []string
	Template string
	// DirTemplate, when non-empty, is used for directories instead of Template.
	DirTemplate string
}

// defaultEditorCandidates returns ordered open strategies for goos.
// Prefer real text editors over generic desktop openers: xdg-open/KIO and
// similar can start successfully then fail async (file: protocol sockets).
func defaultEditorCandidates(goos string) []editorCandidate {
	switch goos {
	case "windows":
		return []editorCandidate{
			{Bins: []string{"code.cmd", "code.exe", "code"}, Template: "code --goto {path}:{line}"},
			{Bins: []string{"cursor.cmd", "cursor.exe", "cursor"}, Template: "cursor --goto {path}:{line}"},
			{Bins: []string{"codium.cmd", "codium.exe", "codium"}, Template: "codium --goto {path}:{line}"},
			{Bins: []string{"notepad++.exe", "notepad++"}, Template: "notepad++ -n{line} {path}"},
			{Bins: []string{"notepad.exe", "notepad"}, Template: "notepad {path}"},
		}
	case "darwin":
		return []editorCandidate{
			{Bins: []string{"code"}, Template: "code --goto {path}:{line}"},
			{Bins: []string{"cursor"}, Template: "cursor --goto {path}:{line}"},
			{Bins: []string{"codium"}, Template: "codium --goto {path}:{line}"},
			{Bins: []string{"subl"}, Template: "subl {path}:{line}"},
			// -t: open in default *text* editor (not Preview for images/logs).
			// Untyped open falls through to platformDefaultOpen.
			{Bins: []string{"open"}, Template: "open -t {path}", DirTemplate: "open {path}"},
		}
	default:
		// Linux, FreeBSD, and other Unix-likes.
		return []editorCandidate{
			{Bins: []string{"code", "code-insiders"}, Template: "code --goto {path}:{line}"},
			{Bins: []string{"cursor"}, Template: "cursor --goto {path}:{line}"},
			{Bins: []string{"codium", "code-oss"}, Template: "codium --goto {path}:{line}"},
			{Bins: []string{"subl"}, Template: "subl {path}:{line}"},
			// Prefer lighter non-IDE editors before full Kate (cold Kate can still
			// touch KIO; we also launch via systemd --user when available).
			{Bins: []string{"kwrite"}, Template: "kwrite {path}"},
			{Bins: []string{"kate"}, Template: "kate -l {line} {path}"},
			{Bins: []string{"gedit"}, Template: "gedit +{line} {path}"},
			{Bins: []string{"gnome-text-editor"}, Template: "gnome-text-editor +{line} {path}"},
			{Bins: []string{"xed"}, Template: "xed +{line} {path}"},
			{Bins: []string{"mousepad"}, Template: "mousepad {path}"},
			{Bins: []string{"leafpad"}, Template: "leafpad {path}"},
			// Avoid xdg-open for local files: often routes through KIO file: and
			// surfaces socket errors even when a document window already opened.
		}
	}
}

// findFirstBin returns the first LookPath hit among names.
func findFirstBin(names []string) (string, error) {
	var last error
	for _, n := range names {
		if n == "" {
			continue
		}
		p, err := lookPath(n)
		if err == nil {
			return p, nil
		}
		last = err
	}
	if last == nil {
		return "", fmt.Errorf("no binary names")
	}
	return "", last
}

// pickDefaultEditorTemplate returns the first launchable default template for
// display in settings (editor_command_default). It does not launch anything.
func pickDefaultEditorTemplate(goos string) string {
	for _, c := range defaultEditorCandidates(goos) {
		if _, err := findFirstBin(c.Bins); err == nil {
			return c.Template
		}
	}
	// Platform opener templates (not always in PATH as a single bin name).
	switch goos {
	case "windows":
		return "cmd /c start {path}"
	case "darwin":
		return "open {path}"
	default:
		return "xdg-open {path}"
	}
}

func (s *Server) userEditorTemplate() string {
	if s.DB == nil {
		return ""
	}
	v, err := s.DB.GetSetting(editorCommandKey)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func (s *Server) editorCommandTemplate() string {
	if t := s.userEditorTemplate(); t != "" {
		return t
	}
	return pickDefaultEditorTemplate(runtimeGOOS)
}

// buildEditorArgs expands {path} and {line} inside each whitespace-separated
// template field. Templates without a {path} placeholder get the path appended.
// Paths may contain spaces: Fields splits the *template* only, then substitution
// keeps a path with spaces as a single argv element when {path} occupies one field.
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

// tryLaunchArgs starts an editor from an already-built argv. binOverride, when
// non-empty, replaces argv[0] (used when we already resolved LookPath).
//
// On Linux, prefer systemd-run --user so the process joins the graphical user
// session (fixes Kate cold-start KIO "file protocol" dialogs when SI itself
// was started without a full desktop bus).
func tryLaunchArgs(args []string, binOverride string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty editor command")
	}
	bin := args[0]
	if binOverride != "" {
		bin = binOverride
	}
	rest := args[1:]

	if runtimeGOOS != "windows" && runtimeGOOS != "darwin" {
		if err := trySystemdUserRun(bin, rest); err == nil {
			return nil
		}
		// errNoSystemdRun or launch failure: fall through to direct spawn with
		// desktopSessionEnv so editors still start without a full user unit.
	}

	cmd := exec.Command(bin, rest...)
	cmd.Env = desktopSessionEnv()
	// Detach from our stdio so chatty editors cannot block the API process.
	cmd.Stdout = nil
	cmd.Stderr = nil
	return startEditorCommand(cmd)
}

// tryLaunchTemplate expands template and starts the process. PATH lookup is
// deferred to exec.Command/Start so test doubles and unusual PATHs still work
// the same way as a user shell.
func tryLaunchTemplate(template, path string, line int) error {
	return tryLaunchArgs(buildEditorArgs(template, path, line), "")
}

// isFolderCapableEditor reports editors that open a directory as a workspace.
func isFolderCapableEditor(bins []string) bool {
	for _, b := range bins {
		lb := strings.ToLower(filepath.Base(b))
		lb = strings.TrimSuffix(lb, ".exe")
		lb = strings.TrimSuffix(lb, ".cmd")
		switch lb {
		case "code", "code-insiders", "cursor", "codium", "code-oss":
			return true
		}
	}
	return false
}

// platformDefaultOpen launches the OS-native “open this path” helper.
// Used as the final fallback after named editors; argv is built without
// strings.Fields so Windows `start` empty-title works with spaced paths.
//
// On Linux: directories prefer desktop file managers; regular files prefer
// xdg-open (registered handler) so transcripts open as documents rather than
// a folder browser on the containing directory.
func platformDefaultOpen(goos, path string, isDir bool) error {
	switch goos {
	case "windows":
		// FileProtocolHandler avoids cmd.exe metacharacter reinterpretation of
		// the path (safer than `cmd /c start "" path` for hostile names).
		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
		cmd.Env = desktopSessionEnv()
		cmd.Stdout = nil
		cmd.Stderr = nil
		return startEditorCommand(cmd)
	case "darwin":
		cmd := exec.Command("open", path)
		cmd.Env = desktopSessionEnv()
		cmd.Stdout = nil
		cmd.Stderr = nil
		return startEditorCommand(cmd)
	default:
		names := []string{"xdg-open"}
		if isDir {
			names = []string{"dolphin", "nautilus", "nemo", "thunar", "pcmanfm", "xdg-open"}
		}
		for _, name := range names {
			bin, err := lookPath(name)
			if err != nil {
				continue
			}
			if err := tryLaunchArgs([]string{bin, path}, bin); err == nil {
				return nil
			}
		}
		return fmt.Errorf("no linux open helper found")
	}
}

// openExistingPath tries user template → editor candidates → OS default open.
// isDir selects directory-oriented templates when available.
func (s *Server) openExistingPath(path string, line int, isDir bool) error {
	var errs []string

	if user := s.userEditorTemplate(); user != "" {
		if err := tryLaunchTemplate(user, path, line); err == nil {
			return nil
		} else {
			errs = append(errs, "user: "+err.Error())
		}
	}

	for _, c := range defaultEditorCandidates(runtimeGOOS) {
		bin, err := findFirstBin(c.Bins)
		if err != nil {
			continue
		}
		tmpl := c.Template
		if isDir {
			// Directories: only use candidates that know how to open a folder.
			// Feeding "kate -l 1 /some/dir" or "code --goto dir:1" often fails
			// or falls through to a broken desktop file: handler (KIO).
			if c.DirTemplate == "" {
				// VS Code / Cursor open folders when given a bare path.
				if isFolderCapableEditor(c.Bins) {
					// tryLaunchArgs overrides argv[0] with the resolved binary.
					args := []string{bin, path}
					if err := tryLaunchArgs(args, bin); err == nil {
						return nil
					} else {
						errs = append(errs, c.Bins[0]+": "+err.Error())
					}
					continue
				}
				continue
			}
			tmpl = c.DirTemplate
		}
		args := buildEditorArgs(tmpl, path, line)
		if len(args) == 0 {
			continue
		}
		// Use the resolved binary path so Windows code.cmd / PATH variants work.
		if err := tryLaunchArgs(args, bin); err == nil {
			return nil
		} else {
			errs = append(errs, c.Bins[0]+": "+err.Error())
		}
	}

	if err := platformDefaultOpen(runtimeGOOS, path, isDir); err == nil {
		return nil
	} else {
		errs = append(errs, "platform: "+err.Error())
	}

	if len(errs) == 0 {
		return fmt.Errorf("no editor or open helper available on %s", runtimeGOOS)
	}
	return fmt.Errorf("open failed (%s)", strings.Join(errs, "; "))
}

// resolveExistingFile normalises path (expanding ~ and joining relative paths
// onto cwd) and returns the absolute path only if it is an existing regular
// file.
func resolveExistingFile(path, cwd string) (string, error) {
	abs, info, err := resolveExisting(path, cwd)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", abs)
	}
	return abs, nil
}

// resolveExisting accepts regular files or directories (for open-folder).
func resolveExisting(path, cwd string) (string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil, fmt.Errorf("empty path")
	}
	// Windows: strip file:/// or file:// prefixes if a client sends them.
	if strings.HasPrefix(strings.ToLower(path), "file:") {
		if u, err := url.Parse(path); err == nil && u.Path != "" {
			path = u.Path
			// url.Path on Windows file:///C:/x is /C:/x — strip leading slash.
			if runtimeGOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
		}
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		path = home
	} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		// Remainder after ~/ or ~\ — bare "~" is handled above so Join does not
		// produce a trailing separator-only segment.
		rest := strings.TrimPrefix(strings.TrimPrefix(path, "~/"), `~\`)
		path = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(path) {
		if cwd == "" || !filepath.IsAbs(cwd) {
			return "", nil, fmt.Errorf("relative path without absolute cwd: %s", path)
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return "", nil, fmt.Errorf("not a file or directory: %s", path)
	}
	return path, info, nil
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
	// Files and directories: provenance sources may be a transcript dir (e.g. Grok).
	resolved, info, err := resolveExisting(req.Path, req.Cwd)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "open_file_failed", "file not found")
		return
	}
	isDir := info.IsDir()

	line := req.Line
	if !isDir && line <= 0 && req.Search != "" {
		line = findLineBySearch(resolved, req.Search)
	}

	if err := s.openExistingPath(resolved, line, isDir); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "open_file_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":   resolved,
		"line":   line,
		"is_dir": isDir,
	})
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
