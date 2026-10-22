//go:build darwin

package procfind

import (
	"os/exec"
	"strconv"
	"strings"
)

// WorkingDir returns the process's current working directory, or "" when it
// cannot be determined. macOS has no /proc; lsof reports the cwd link.
func WorkingDir(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}

// ProcessesNamed returns live PIDs whose executable basename equals name.
// `ps -xco pid=,comm=` prints one "pid comm" pair per process.
func ProcessesNamed(name string) ([]int, error) {
	if name == "" {
		return nil, nil
	}
	out, err := exec.Command("ps", "-xco", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] != name {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
