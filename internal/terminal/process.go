package terminal

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DetectFromAgentPID derives only a terminal instance from an exact Agent PID.
// It never claims an exact tab: external sessions were not launched by SI and
// arbitrary terminal-tab enumeration is deliberately outside this contract.
func DetectFromAgentPID(agentPID int) (Binding, bool) {
	if runtime.GOOS != "linux" || agentPID <= 0 {
		return Binding{}, false
	}
	pid := agentPID
	seen := map[int]bool{}
	for range 64 {
		if pid <= 1 || seen[pid] {
			break
		}
		seen[pid] = true
		executable, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		if err == nil {
			if id, name, ok := terminalIdentity(filepath.Base(executable)); ok {
				return Binding{
					TerminalID: id, TerminalName: name, TerminalPID: pid, AgentPID: agentPID,
					Confidence: ConfidenceInstance,
				}, true
			}
		}
		parent, ok := linuxParentPID(pid)
		if !ok {
			break
		}
		pid = parent
	}
	return Binding{}, false
}

func linuxParentPID(pid int) (int, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "PPid:") {
			continue
		}
		parent, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
		return parent, err == nil
	}
	return 0, false
}

func terminalIdentity(executable string) (string, string, bool) {
	name := strings.ToLower(strings.TrimSpace(executable))
	switch name {
	case "konsole":
		return "konsole", "Konsole", true
	case "gnome-terminal", "gnome-terminal-server":
		return "gnome-terminal", "GNOME Terminal", true
	case "xfce4-terminal":
		return "xfce4-terminal", "Xfce Terminal", true
	case "kitty":
		return "kitty", "kitty", true
	case "wezterm", "wezterm-gui":
		return "wezterm", "WezTerm", true
	case "ghostty":
		return "ghostty", "Ghostty", true
	case "alacritty":
		return "alacritty", "Alacritty", true
	case "xterm":
		return "xterm", "xterm", true
	default:
		return "", "", false
	}
}
