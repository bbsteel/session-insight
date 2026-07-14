//go:build linux

package procfind

import (
	"os"
	"strconv"
	"strings"
)

// StartToken returns an opaque token identifying when this PID started —
// /proc/<pid>/stat field 22 (starttime in clock ticks since boot). Two
// observations of the same PID with different tokens are different
// processes (PID reuse). Claude Code writes this exact value as
// "procStart" in its ~/.claude/sessions/<pid>.json heartbeat, which is
// what makes it the reuse guard for kill decisions.
func StartToken(pid int) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	// comm (field 2) is in parens and may contain spaces; fields resume
	// after the last ')' with state as field 3, so starttime (field 22)
	// is index 19 of the remainder.
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}
