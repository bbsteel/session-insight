//go:build windows

package procfind

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// WorkingDir returns "" on Windows. There is no supported way to read another
// process's current directory without debug privileges, and this package does
// not escalate. Callers treat an empty value as "no evidence".
func WorkingDir(pid int) string { return "" }

// ProcessesNamed returns live PIDs whose executable basename equals name, using
// the Toolhelp process snapshot.
func ProcessesNamed(name string) ([]int, error) {
	if name == "" {
		return nil, nil
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	self := uint32(windows.GetCurrentProcessId())
	var pids []int
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == self {
			continue
		}
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if len(exe) > 4 && exe[len(exe)-4:] == ".exe" {
			exe = exe[:len(exe)-4]
		}
		if exe == name {
			pids = append(pids, int(entry.ProcessID))
		}
	}
	return pids, nil
}
