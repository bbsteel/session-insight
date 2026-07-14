//go:build darwin

package procfind

import (
	"os/exec"
	"strconv"
	"strings"
)

// holdersOf shells out to lsof, the standard macOS answer to "who has
// this file open" (no /proc there). `-t` prints bare PIDs, one per line.
func holdersOf(path string) ([]int, error) {
	out, err := exec.Command("lsof", "-t", "--", path).Output()
	if err != nil {
		// lsof exits 1 when no process holds the file — that is the
		// normal "not running" answer, not a failure.
		if _, ok := err.(*exec.ExitError); ok {
			return nil, nil
		}
		return nil, err
	}

	var pids []int
	for _, line := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(line); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
