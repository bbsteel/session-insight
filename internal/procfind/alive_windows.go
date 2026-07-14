//go:build windows

package procfind

import "golang.org/x/sys/windows"

// Alive reports whether a process with this PID currently exists.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
		false, uint32(pid))
	if err != nil {
		// Access denied still proves the process exists.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	// A signaled handle means the process has already exited.
	event, err := windows.WaitForSingleObject(h, 0)
	return err == nil && event != uint32(windows.WAIT_OBJECT_0)
}
