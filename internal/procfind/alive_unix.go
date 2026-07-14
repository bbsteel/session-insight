//go:build linux || darwin

package procfind

import "syscall"

// Alive reports whether a process with this PID currently exists. EPERM
// counts as alive: the process exists but belongs to another user.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
