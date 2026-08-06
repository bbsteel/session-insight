package terminal

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type konsoleSnapshot struct {
	service  string
	windowID string
	sessions map[string]bool
}

const (
	konsoleDiscoveryTimeout = 2500 * time.Millisecond
	konsolePollInterval     = 100 * time.Millisecond
)

func launchKonsole(ctx context.Context, path string, command Command) (Binding, error) {
	scanCtx, cancel := context.WithTimeout(ctx, konsoleDiscoveryTimeout)
	defer cancel()
	before := snapshotKonsole(scanCtx)
	args := []string{"--workdir", command.CWD}
	if len(before) > 0 {
		args = append(args, "--new-tab", "--force-reuse")
	}
	args = append(args, "-e", command.Executable)
	args = append(args, command.Args...)
	// Konsole may retain this process as the terminal instance, so it must not
	// inherit the HTTP request's cancellation lifetime.
	proc := exec.Command(path, args...)
	proc.Dir = command.CWD
	if err := proc.Start(); err != nil {
		return Binding{}, err
	}
	pid := proc.Process.Pid
	go func() { _ = proc.Wait() }()

	binding := Binding{
		TerminalID: "konsole", TerminalName: "Konsole", TerminalPID: pid,
		Confidence: ConfidenceInstance, LaunchedAt: time.Now(),
	}
	for scanCtx.Err() == nil {
		after := snapshotKonsole(scanCtx)
		if service, windowID, tabID, ok := newKonsoleSession(before, after); ok {
			binding.InstanceID = service
			binding.WindowID = windowID
			binding.TabID = tabID
			binding.Confidence = ConfidenceExact
			binding.Focusable = true
			if command.Title != "" {
				_ = konsoleCall(scanCtx, service, "/Sessions/"+tabID, "org.kde.konsole.Session.setTitle", "1", command.Title)
			}
			return binding, nil
		}
		timer := time.NewTimer(konsolePollInterval)
		select {
		case <-scanCtx.Done():
			timer.Stop()
			return binding, nil
		case <-timer.C:
		}
	}
	return binding, nil
}

func focusKonsole(ctx context.Context, binding Binding) (FocusResult, error) {
	if binding.InstanceID == "" || binding.WindowID == "" || binding.TabID == "" {
		return FocusResult{}, fmt.Errorf("Konsole binding has no exact tab handle")
	}
	focusCtx, cancel := context.WithTimeout(ctx, konsoleDiscoveryTimeout)
	defer cancel()
	if err := konsoleCall(focusCtx, binding.InstanceID, binding.WindowID, "org.kde.konsole.Window.setCurrentSession", binding.TabID); err != nil {
		return FocusResult{}, err
	}
	result := FocusResult{TabSelected: true}
	// QWidget activation is exported by some Konsole/Qt builds. Wayland may
	// still refuse foreground focus; report that distinction to the caller.
	if err := konsoleCall(focusCtx, binding.InstanceID, binding.WindowID, "org.qtproject.Qt.QWidget.raise"); err == nil {
		if err := konsoleCall(focusCtx, binding.InstanceID, binding.WindowID, "org.qtproject.Qt.QWidget.activateWindow"); err == nil {
			result.Foreground = true
		}
	}
	return result, nil
}

func snapshotKonsole(ctx context.Context) []konsoleSnapshot {
	qdbus, err := qdbusPath()
	if err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, qdbus).Output()
	if err != nil {
		return nil
	}
	var services []string
	for _, line := range strings.Fields(string(out)) {
		if line == "org.kde.konsole" || strings.HasPrefix(line, "org.kde.konsole-") {
			services = append(services, line)
		}
	}
	sort.Strings(services)
	var result []konsoleSnapshot
	for _, service := range services {
		objects, err := exec.CommandContext(ctx, qdbus, service).Output()
		if err != nil {
			continue
		}
		var windows []string
		for _, object := range strings.Fields(string(objects)) {
			if strings.HasPrefix(object, "/Windows/") {
				windows = append(windows, object)
			}
		}
		sort.Strings(windows)
		for _, windowID := range windows {
			sessionsOut, err := exec.CommandContext(ctx, qdbus, service, windowID, "org.kde.konsole.Window.sessionList").Output()
			if err != nil {
				continue
			}
			sessions := map[string]bool{}
			for _, value := range strings.FieldsFunc(string(sessionsOut), func(r rune) bool {
				return r == '\n' || r == ',' || r == ' ' || r == '\t'
			}) {
				value = strings.TrimSpace(value)
				if _, err := strconv.Atoi(value); err == nil {
					sessions[value] = true
				}
			}
			result = append(result, konsoleSnapshot{service: service, windowID: windowID, sessions: sessions})
		}
	}
	return result
}

func newKonsoleSession(before, after []konsoleSnapshot) (string, string, string, bool) {
	seen := map[string]bool{}
	for _, snapshot := range before {
		for id := range snapshot.sessions {
			seen[snapshot.service+"\x00"+id] = true
		}
	}
	type found struct{ service, window, tab string }
	var added []found
	for _, snapshot := range after {
		for id := range snapshot.sessions {
			if !seen[snapshot.service+"\x00"+id] {
				added = append(added, found{snapshot.service, snapshot.windowID, id})
			}
		}
	}
	if len(added) != 1 {
		return "", "", "", false
	}
	return added[0].service, added[0].window, added[0].tab, true
}

func konsoleCall(ctx context.Context, service, object, method string, args ...string) error {
	qdbus, err := qdbusPath()
	if err != nil {
		return err
	}
	callArgs := []string{service, object, method}
	callArgs = append(callArgs, args...)
	if out, err := exec.CommandContext(ctx, qdbus, callArgs...).CombinedOutput(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("Konsole D-Bus %s: %w", method, ctxErr)
		}
		return fmt.Errorf("Konsole D-Bus %s: %s", method, strings.TrimSpace(string(out)))
	}
	return nil
}

func qdbusPath() (string, error) {
	if path, err := exec.LookPath("qdbus6"); err == nil {
		return path, nil
	}
	return exec.LookPath("qdbus")
}
