// Package terminal launches Agent resume commands in desktop terminals and
// tracks the strongest terminal binding the host can prove.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	ConfidenceExact    = "exact"
	ConfidenceInstance = "instance"
	ConfidenceUnknown  = "unknown"
)

// Command is a trusted, adapter-generated Agent invocation. User-controlled
// transcript text never becomes an executable or argument.
type Command struct {
	Executable string
	Args       []string
	CWD        string
	Title      string
}

// Binding identifies where a resumed session was launched. WindowID and TabID
// are terminal-native opaque values; only the matching terminal adapter may
// interpret them.
type Binding struct {
	TerminalID   string    `json:"terminal_id"`
	TerminalName string    `json:"terminal_name"`
	InstanceID   string    `json:"instance_id,omitempty"`
	WindowID     string    `json:"window_id,omitempty"`
	TabID        string    `json:"tab_id,omitempty"`
	TerminalPID  int       `json:"terminal_pid,omitempty"`
	AgentPID     int       `json:"agent_pid,omitempty"`
	Confidence   string    `json:"confidence"`
	Focusable    bool      `json:"focusable"`
	LaunchedAt   time.Time `json:"launched_at"`
}

type FocusResult struct {
	TabSelected bool `json:"tab_selected"`
	Foreground  bool `json:"foreground"`
}

// Launcher is injected into the server so HTTP tests never open GUI apps.
type Launcher interface {
	Launch(context.Context, Command) (Binding, error)
	Focus(context.Context, Binding) (FocusResult, error)
}

type SystemLauncher struct{}

func NewSystemLauncher() *SystemLauncher { return &SystemLauncher{} }

func (l *SystemLauncher) Launch(ctx context.Context, command Command) (Binding, error) {
	if strings.TrimSpace(command.Executable) == "" {
		return Binding{}, errors.New("empty executable")
	}
	if command.CWD == "" {
		return Binding{}, errors.New("empty working directory")
	}
	info, err := os.Stat(command.CWD)
	if err != nil || !info.IsDir() {
		return Binding{}, fmt.Errorf("working directory unavailable: %s", command.CWD)
	}
	resolved, err := exec.LookPath(command.Executable)
	if err != nil {
		return Binding{}, fmt.Errorf("agent executable not found: %s", command.Executable)
	}
	command.Executable = resolved

	switch runtime.GOOS {
	case "linux":
		return l.launchLinux(ctx, command)
	case "windows":
		return l.launchWindows(ctx, command)
	case "darwin":
		return l.launchDarwin(ctx, command)
	default:
		return Binding{}, fmt.Errorf("terminal launch unsupported on %s", runtime.GOOS)
	}
}

func (l *SystemLauncher) Focus(ctx context.Context, binding Binding) (FocusResult, error) {
	if binding.TerminalID == "konsole" {
		return focusKonsole(ctx, binding)
	}
	return FocusResult{}, fmt.Errorf("terminal %s cannot focus a recorded tab", binding.TerminalID)
}

func (l *SystemLauncher) launchLinux(ctx context.Context, command Command) (Binding, error) {
	preferred := strings.TrimSpace(os.Getenv("TERMINAL"))
	if preferred != "" {
		if path, err := exec.LookPath(strings.Fields(preferred)[0]); err == nil {
			if filepath.Base(path) == "konsole" {
				return launchKonsole(ctx, path, command)
			}
			return launchLinuxTerminal(ctx, filepath.Base(path), path, command)
		}
	}

	if path, err := exec.LookPath("konsole"); err == nil && strings.Contains(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), "kde") {
		return launchKonsole(ctx, path, command)
	}
	for _, id := range []string{"ghostty", "kitty", "wezterm", "gnome-terminal", "alacritty", "xfce4-terminal", "x-terminal-emulator", "xterm"} {
		if path, err := exec.LookPath(id); err == nil {
			return launchLinuxTerminal(ctx, id, path, command)
		}
	}
	if path, err := exec.LookPath("konsole"); err == nil {
		return launchKonsole(ctx, path, command)
	}
	return Binding{}, errors.New("no supported terminal found")
}

func launchLinuxTerminal(ctx context.Context, id, path string, command Command) (Binding, error) {
	args, name, err := linuxTerminalArgs(id, command)
	if err != nil {
		return Binding{}, err
	}
	// The terminal is the result of the request and must outlive the request
	// context. Discovery and focus calls remain context-bound.
	proc := exec.Command(path, args...)
	proc.Dir = command.CWD
	if err := proc.Start(); err != nil {
		return Binding{}, err
	}
	pid := proc.Process.Pid
	go func() { _ = proc.Wait() }()
	return Binding{
		TerminalID: id, TerminalName: name, TerminalPID: pid,
		Confidence: ConfidenceInstance, Focusable: false, LaunchedAt: time.Now(),
	}, nil
}

func linuxTerminalArgs(id string, command Command) ([]string, string, error) {
	switch id {
	case "ghostty":
		return append([]string{"--working-directory=" + command.CWD, "-e", command.Executable}, command.Args...), "Ghostty", nil
	case "kitty":
		return append([]string{"--directory", command.CWD, command.Executable}, command.Args...), "kitty", nil
	case "wezterm":
		return append([]string{"start", "--cwd", command.CWD, "--", command.Executable}, command.Args...), "WezTerm", nil
	case "gnome-terminal":
		return append([]string{"--tab", "--working-directory=" + command.CWD, "--", command.Executable}, command.Args...), "GNOME Terminal", nil
	case "alacritty":
		return append([]string{"--working-directory", command.CWD, "-e", command.Executable}, command.Args...), "Alacritty", nil
	case "xfce4-terminal":
		return append([]string{"--tab", "--working-directory=" + command.CWD, "-x", command.Executable}, command.Args...), "Xfce Terminal", nil
	case "x-terminal-emulator", "xterm":
		return append([]string{"-e", command.Executable}, command.Args...), "Terminal", nil
	default:
		return nil, "", fmt.Errorf("unsupported terminal executable: %s", id)
	}
}

func (l *SystemLauncher) launchWindows(ctx context.Context, command Command) (Binding, error) {
	wt, err := exec.LookPath("wt.exe")
	if err != nil {
		return Binding{}, errors.New("Windows Terminal was not found")
	}
	args := []string{"-w", "0", "new-tab", "-d", command.CWD, "--", command.Executable}
	args = append(args, command.Args...)
	proc := exec.Command(wt, args...)
	if err := proc.Start(); err != nil {
		return Binding{}, err
	}
	pid := proc.Process.Pid
	go func() { _ = proc.Wait() }()
	return Binding{TerminalID: "windows-terminal", TerminalName: "Windows Terminal", TerminalPID: pid, Confidence: ConfidenceInstance, LaunchedAt: time.Now()}, nil
}

func (l *SystemLauncher) launchDarwin(ctx context.Context, command Command) (Binding, error) {
	oscript, err := exec.LookPath("osascript")
	if err != nil {
		return Binding{}, errors.New("macOS osascript command was not found")
	}
	shellCommand := "cd " + quotePOSIX(command.CWD) + " && exec " + formatPOSIXCommand(command.Executable, command.Args)
	script := `tell application "Terminal"
activate
if (count of windows) is 0 then
do script "` + quoteAppleScript(shellCommand) + `"
else
do script "` + quoteAppleScript(shellCommand) + `" in front window
end if
end tell`
	// AppleScript returns after creating the tab; Terminal.app owns the shell
	// process. Exact tab tracking is intentionally not claimed.
	if out, err := exec.CommandContext(ctx, oscript, "-e", script).CombinedOutput(); err != nil {
		return Binding{}, fmt.Errorf("open Terminal tab: %s", strings.TrimSpace(string(out)))
	}
	return Binding{TerminalID: "terminal-app", TerminalName: "Terminal", Confidence: ConfidenceInstance, LaunchedAt: time.Now()}, nil
}

func FormatCommand(command Command) string {
	if runtime.GOOS == "windows" {
		parts := []string{quotePowerShell(command.Executable)}
		for _, arg := range command.Args {
			parts = append(parts, quotePowerShell(arg))
		}
		return "Set-Location -LiteralPath " + quotePowerShell(command.CWD) + "; & " + strings.Join(parts, " ")
	}
	return "cd " + quotePOSIX(command.CWD) + " && " + formatPOSIXCommand(command.Executable, command.Args)
}

func formatPOSIXCommand(executable string, args []string) string {
	parts := []string{quotePOSIX(executable)}
	for _, arg := range args {
		parts = append(parts, quotePOSIX(arg))
	}
	return strings.Join(parts, " ")
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteAppleScript(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	return strings.ReplaceAll(value, "\n", `\n`)
}
