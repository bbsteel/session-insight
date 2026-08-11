package terminal

import (
	"context"
	"fmt"
	"os"
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

// konsoleTarget is a running Konsole window that can host a resumed-session
// tab via D-Bus, regardless of Konsole's single-process setting.
type konsoleTarget struct {
	service  string
	windowID string
	pid      int
}

const (
	konsoleDiscoveryTimeout = 2500 * time.Millisecond
	konsolePollInterval     = 100 * time.Millisecond
)

func launchKonsole(ctx context.Context, path string, command Command) (Binding, error) {
	scanCtx, cancel := context.WithTimeout(ctx, konsoleDiscoveryTimeout)
	defer cancel()
	before := snapshotKonsole(scanCtx)
	targets := konsoleTargets(before)
	if target, ok := pickKonsoleTarget(targets, os.Getenv("KONSOLE_DBUS_SERVICE"), os.Getenv("KONSOLE_DBUS_WINDOW"),
		func(service string) int { return resolveKonsolePID(scanCtx, service) }); ok {
		if binding, err := konsoleOpenTab(scanCtx, target, command); err == nil {
			return binding, nil
		}
	}
	return spawnKonsole(scanCtx, path, command, before)
}

// konsoleOpenTab adds a tab to a running Konsole window over D-Bus. Unlike
// `konsole --new-tab`, this does not depend on Konsole's single-process
// setting: that CLI flag forwards through the well-known org.kde.konsole bus
// name, which no process owns unless single-process mode is enabled, so the
// launch silently becomes a new instance. Talking to the discovered
// org.kde.konsole-<pid> service directly works in both modes.
func konsoleOpenTab(ctx context.Context, target konsoleTarget, command Command) (Binding, error) {
	out, err := konsoleOutput(ctx, target.service, target.windowID, "org.kde.konsole.Window.newSession", "", command.CWD)
	if err != nil {
		// Some Konsole builds reject an empty profile; fall back to the
		// no-argument overload and let the command line establish the
		// directory instead.
		out, err = konsoleOutput(ctx, target.service, target.windowID, "org.kde.konsole.Window.newSession")
		if err != nil {
			return Binding{}, err
		}
	}
	tabID := strings.TrimSpace(out)
	if _, err := strconv.Atoi(tabID); err != nil {
		return Binding{}, fmt.Errorf("konsole newSession returned %q", tabID)
	}
	sessionPath := "/Sessions/" + tabID
	line := FormatCommand(command)
	if err := konsoleCall(ctx, target.service, sessionPath, "org.kde.konsole.Session.runCommand", line); err != nil {
		if err := konsoleCall(ctx, target.service, sessionPath, "org.kde.konsole.Session.sendText", line+"\n"); err != nil {
			return Binding{}, err
		}
	}
	if command.Title != "" {
		_ = konsoleCall(ctx, target.service, sessionPath, "org.kde.konsole.Session.setTitle", "1", command.Title)
	}
	pid := target.pid
	if pid <= 0 {
		pid = resolveKonsolePID(ctx, target.service)
	}
	binding := Binding{
		TerminalID: "konsole", TerminalName: "Konsole",
		InstanceID: target.service, WindowID: target.windowID, TabID: tabID,
		TerminalPID: pid, Confidence: ConfidenceExact, Focusable: true, LaunchedAt: time.Now(),
	}
	// Land the user on the new tab. Raise/activate are best-effort: Wayland
	// and some Qt builds refuse foreground steals, and focusKonsole already
	// treats them as optional.
	_, _ = focusKonsole(ctx, binding)
	return binding, nil
}

func spawnKonsole(scanCtx context.Context, path string, command Command, before []konsoleSnapshot) (Binding, error) {
	args := []string{"--workdir", command.CWD, "-e", command.Executable}
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
		return FocusResult{}, fmt.Errorf("konsole binding has no exact tab handle")
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

func konsoleTargets(snapshots []konsoleSnapshot) []konsoleTarget {
	targets := make([]konsoleTarget, 0, len(snapshots))
	for _, snapshot := range snapshots {
		pid, _ := konsoleServicePID(snapshot.service)
		targets = append(targets, konsoleTarget{service: snapshot.service, windowID: snapshot.windowID, pid: pid})
	}
	return targets
}

// pickKonsoleTarget chooses the window that receives a resumed-session tab.
// The Konsole instance hosting the SessionInsight backend (advertised through
// KONSOLE_DBUS_SERVICE/WINDOW) is preferred; otherwise the first discovered
// window keeps the choice deterministic. Snapshots are sorted by service and
// window, so targets[0] is stable.
func pickKonsoleTarget(targets []konsoleTarget, preferredService, preferredWindow string, resolvePID func(string) int) (konsoleTarget, bool) {
	if len(targets) == 0 {
		return konsoleTarget{}, false
	}
	if preferredService != "" {
		preferredPID := resolvePID(preferredService)
		for _, target := range targets {
			if target.service != preferredService && (preferredPID <= 0 || target.pid != preferredPID) {
				continue
			}
			if preferredWindow != "" {
				for _, candidate := range targets {
					if candidate.service == target.service && candidate.windowID == preferredWindow {
						return candidate, true
					}
				}
			}
			return target, true
		}
	}
	return targets[0], true
}

func konsoleServicePID(service string) (int, bool) {
	suffix, ok := strings.CutPrefix(service, "org.kde.konsole-")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(suffix)
	return pid, err == nil
}

// resolveKonsolePID maps any bus name (well-known or unique) to its process
// id, returning 0 when the name is gone or the bus cannot answer.
func resolveKonsolePID(ctx context.Context, service string) int {
	if pid, ok := konsoleServicePID(service); ok {
		return pid
	}
	out, err := konsoleOutput(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus.GetConnectionUnixProcessID", service)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return pid
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
	_, err := konsoleOutput(ctx, service, object, method, args...)
	return err
}

func konsoleOutput(ctx context.Context, service, object, method string, args ...string) (string, error) {
	qdbus, err := qdbusPath()
	if err != nil {
		return "", err
	}
	callArgs := []string{service, object, method}
	callArgs = append(callArgs, args...)
	out, err := exec.CommandContext(ctx, qdbus, callArgs...).CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("konsole D-Bus %s: %w", method, ctxErr)
		}
		return "", fmt.Errorf("konsole D-Bus %s: %s", method, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func qdbusPath() (string, error) {
	if path, err := exec.LookPath("qdbus6"); err == nil {
		return path, nil
	}
	return exec.LookPath("qdbus")
}
