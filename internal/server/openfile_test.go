package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"session-insight/internal/db"
	"session-insight/internal/reader"
)

func TestBuildEditorArgs(t *testing.T) {
	cases := []struct {
		template string
		path     string
		line     int
		want     []string
	}{
		{"code --goto {path}:{line}", "/tmp/a.go", 42, []string{"code", "--goto", "/tmp/a.go:42"}},
		{"code --goto {path}:{line}", "/tmp/a.go", 0, []string{"code", "--goto", "/tmp/a.go:1"}},
		{"xdg-open {path}", "/tmp/a.go", 7, []string{"xdg-open", "/tmp/a.go"}},
		{"subl", "/tmp/a.go", 3, []string{"subl", "/tmp/a.go"}}, // no {path} → appended
	}
	for _, c := range cases {
		got := buildEditorArgs(c.template, c.path, c.line)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("buildEditorArgs(%q, %q, %d) = %v, want %v", c.template, c.path, c.line, got, c.want)
		}
	}
}

func TestResolveExistingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := resolveExistingFile(file, ""); err != nil || got != file {
		t.Errorf("absolute path: got %q, %v", got, err)
	}
	if got, err := resolveExistingFile("main.go", dir); err != nil || got != file {
		t.Errorf("relative path + cwd: got %q, %v", got, err)
	}
	if _, err := resolveExistingFile("missing.go", dir); err == nil {
		t.Error("missing file should fail")
	}
	if _, err := resolveExistingFile("main.go", ""); err == nil {
		t.Error("relative path without cwd should fail")
	}
	if _, err := resolveExistingFile(dir, ""); err == nil {
		t.Error("directory should fail (regular files only)")
	}
}

func TestFindLineBySearch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	os.WriteFile(file, []byte("alpha\n  beta gamma  \ndelta\nbeta\n"), 0o644)

	if got := findLineBySearch(file, "beta gamma"); got != 2 {
		t.Errorf("trimmed exact match: got %d, want 2", got)
	}
	if got := findLineBySearch(file, "elt"); got != 3 {
		t.Errorf("substring fallback: got %d, want 3", got)
	}
	if got := findLineBySearch(file, "nope"); got != 0 {
		t.Errorf("no match: got %d, want 0", got)
	}
}

func TestOpenFileAndSettingsHandlers(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open db: %v", err)
	}
	defer database.Close()
	srv := New(database, []reader.BaseSessionReader{})

	dir := t.TempDir()
	file := filepath.Join(dir, "target.go")
	os.WriteFile(file, []byte("package x\nfunc Hit() {}\n"), 0o644)

	jsonReq := func(method, target, body string) *http.Request {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return req
	}

	// Configure a template via the settings API.
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, jsonReq("PUT", "/api/settings",
		`{"editor_command":"myedit --jump {path}:{line}"}`))
	if w.Code != 204 {
		t.Fatalf("PUT settings: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/settings", nil))
	var settings map[string]string
	json.NewDecoder(w.Body).Decode(&settings)
	if settings["editor_command"] != "myedit --jump {path}:{line}" {
		t.Fatalf("GET settings: %+v", settings)
	}

	// resolve-file: hit and miss.
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/resolve-file?path=target.go&cwd="+dir, nil))
	if w.Code != 200 {
		t.Fatalf("resolve-file hit: %d", w.Code)
	}
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/resolve-file?path=nope.go&cwd="+dir, nil))
	if w.Code != 404 {
		t.Fatalf("resolve-file miss: %d", w.Code)
	}

	// open-file: capture argv instead of launching, search resolves the line.
	var captured []string
	orig := startEditorCommand
	startEditorCommand = func(cmd *exec.Cmd) error {
		captured = cmd.Args
		return nil
	}
	defer func() { startEditorCommand = orig }()

	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, jsonReq("POST", "/api/open-file",
		`{"path":"target.go","cwd":"`+dir+`","search":"func Hit() {}"}`))
	if w.Code != 200 {
		t.Fatalf("open-file: %d %s", w.Code, w.Body.String())
	}
	want := []string{"myedit", "--jump", file + ":2"}
	if !reflect.DeepEqual(captured, want) {
		t.Fatalf("editor argv = %v, want %v", captured, want)
	}

	// open-file with a missing file must not launch anything.
	captured = nil
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, jsonReq("POST", "/api/open-file", `{"path":"/definitely/not/here.go"}`))
	if w.Code != 404 || captured != nil {
		t.Fatalf("open-file missing: code %d, captured %v", w.Code, captured)
	}

	// Cross-site guards: text/plain smuggling and foreign Origins must be
	// rejected before any file/exec logic runs.
	body := `{"path":"target.go","cwd":"` + dir + `"}`
	captured = nil
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/open-file", strings.NewReader(body)))
	if w.Code != 415 || captured != nil {
		t.Fatalf("text/plain smuggle: code %d, captured %v", w.Code, captured)
	}
	evil := jsonReq("POST", "/api/open-file", body)
	evil.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, evil)
	if w.Code != 403 || captured != nil {
		t.Fatalf("evil origin open-file: code %d, captured %v", w.Code, captured)
	}
	evilPut := jsonReq("PUT", "/api/settings", `{"editor_command":"pwned"}`)
	evilPut.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, evilPut)
	if w.Code != 403 {
		t.Fatalf("evil origin settings: code %d", w.Code)
	}
	ok := jsonReq("POST", "/api/open-file", body)
	ok.Header.Set("Origin", "http://127.0.0.1:8080")
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, ok)
	if w.Code != 200 || captured == nil {
		t.Fatalf("loopback origin should pass: code %d, captured %v", w.Code, captured)
	}
}
