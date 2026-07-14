//go:build linux || darwin

package procfind

import (
	"fmt"
	"syscall"
	"time"
)

// Terminate stops exactly one process by PID: SIGTERM first so the agent
// can flush its session log and restore the terminal, then SIGKILL if it
// hasn't exited within grace. Returns nil if the process is already gone.
func Terminate(pid int, grace time.Duration) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if gone(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("SIGKILL pid %d: %w", pid, err)
	}
	// SIGKILL cannot be ignored; give the kernel a moment to reap.
	for i := 0; i < 20; i++ {
		if gone(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d still running after SIGKILL", pid)
}

func gone(pid int) bool {
	return syscall.Kill(pid, 0) == syscall.ESRCH
}
