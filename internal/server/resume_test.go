package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/terminal"
)

type fakeTerminalLauncher struct {
	binding  terminal.Binding
	command  terminal.Command
	launches int
	focuses  int
}

func (f *fakeTerminalLauncher) Launch(_ context.Context, command terminal.Command) (terminal.Binding, error) {
	f.launches++
	f.command = command
	return f.binding, nil
}

func (f *fakeTerminalLauncher) Focus(_ context.Context, binding terminal.Binding) (terminal.FocusResult, error) {
	f.focuses++
	return terminal.FocusResult{TabSelected: true, Foreground: true}, nil
}

func stoppedClaudeReader(cwd string) *capsAPIReader {
	live := false
	now := time.Now()
	return &capsAPIReader{
		live: &live, hasRev: true,
		detail: &model.SessionDetail{
			Session: model.Session{
				ID: "s1", AgentType: "claude", ResumeID: "native-1", CWD: cwd,
				ModelName: "claude-sonnet", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			},
			Turns: []model.TurnVM{{TurnIndex: 0, UserMessage: "hi"}},
		},
	}
}

func TestResumePlanUsesAdapterCommandAndStrictCWD(t *testing.T) {
	cwd := t.TempDir()
	rd := stoppedClaudeReader(cwd)
	srv := New(nil, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/resume?agent=claude", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var plan resumePlanResponse
	if err := json.NewDecoder(w.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.Status != "ready" || !strings.Contains(plan.Command, "--resume") || !strings.Contains(plan.Command, "native-1") {
		t.Fatalf("%+v", plan)
	}

	rd.detail.CWD = cwd + "/deleted"
	req = httptest.NewRequest(http.MethodGet, "/api/sessions/s1/resume?agent=claude", nil)
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	plan = resumePlanResponse{}
	if err := json.NewDecoder(w.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.Status != "cwd_unavailable" || plan.Command != "" {
		t.Fatalf("%+v", plan)
	}
}

func TestResumeLaunchPersistsExactTerminalBindingAndFocuses(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cwd := t.TempDir()
	rd := stoppedClaudeReader(cwd)
	launcher := &fakeTerminalLauncher{binding: terminal.Binding{
		TerminalID: "konsole", TerminalName: "Konsole", InstanceID: "org.kde.konsole",
		WindowID: "/Windows/1", TabID: "9", Confidence: terminal.ConfidenceExact,
		Focusable: true, LaunchedAt: time.Now().UTC(),
	}}
	srv := New(database, []reader.BaseSessionReader{rd})
	srv.terminalLauncher = launcher

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/resume?agent=claude", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if launcher.launches != 1 || launcher.command.CWD != cwd || launcher.command.Args[1] != "native-1" {
		t.Fatalf("launches=%d command=%+v", launcher.launches, launcher.command)
	}

	record, ok, err := database.GetTerminalBinding("claude", "s1")
	if err != nil || !ok || record.TabID != "9" || !record.Focusable {
		t.Fatalf("ok=%v err=%v record=%+v", ok, err, record)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/s1/terminal/focus?agent=claude", nil)
	w = httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || launcher.focuses != 1 {
		t.Fatalf("status=%d focuses=%d body=%s", w.Code, launcher.focuses, w.Body.String())
	}
}

func TestResumeRefusesAlreadyRunningSession(t *testing.T) {
	cwd := t.TempDir()
	rd := stoppedClaudeReader(cwd)
	live := true
	rd.live = &live
	launcher := &fakeTerminalLauncher{}
	srv := New(nil, []reader.BaseSessionReader{rd})
	srv.terminalLauncher = launcher

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/resume?agent=claude", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || launcher.launches != 0 || !strings.Contains(w.Body.String(), "session_running") {
		t.Fatalf("status=%d launches=%d body=%s", w.Code, launcher.launches, w.Body.String())
	}
}

func TestLiveSessionWithoutProvableTerminalIsReportedUnknown(t *testing.T) {
	cwd := t.TempDir()
	rd := stoppedClaudeReader(cwd)
	live := true
	rd.live = &live
	srv := New(nil, []reader.BaseSessionReader{rd})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/terminal?agent=claude", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var status terminalStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State != "active_unknown" || status.Confidence != terminal.ConfidenceUnknown {
		t.Fatalf("%+v", status)
	}
}
