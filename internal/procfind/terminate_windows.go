//go:build windows

package procfind

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// Terminate stops exactly one process by PID. Windows has no SIGTERM
// equivalent for arbitrary console processes, so this is TerminateProcess
// directly, then a bounded wait for the handle to signal exit.
func Terminate(pid int, grace time.Duration) error {
	h, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER: no such process — already gone.
		if err == windows.ERROR_INVALID_PARAMETER {
			return nil
		}
		return fmt.Errorf("open pid %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("terminate pid %d: %w", pid, err)
	}
	event, err := windows.WaitForSingleObject(h, uint32(grace.Milliseconds()))
	if err != nil {
		return fmt.Errorf("wait pid %d: %w", pid, err)
	}
	if event != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("process %d still running after terminate", pid)
	}
	return nil
}
