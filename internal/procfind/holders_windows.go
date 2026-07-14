//go:build windows

package procfind

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no /proc and no lsof; the supported way to ask "which
// processes hold this file" is the Restart Manager API (what installers
// use to find file lockers).
var (
	modRstrtmgr             = windows.NewLazySystemDLL("rstrtmgr.dll")
	procRmStartSession      = modRstrtmgr.NewProc("RmStartSession")
	procRmRegisterResources = modRstrtmgr.NewProc("RmRegisterResources")
	procRmGetList           = modRstrtmgr.NewProc("RmGetList")
	procRmEndSession        = modRstrtmgr.NewProc("RmEndSession")
)

const (
	cchRmSessionKey = 32  // CCH_RM_SESSION_KEY
	errorMoreData   = 234 // ERROR_MORE_DATA
)

type rmUniqueProcess struct {
	ProcessID        uint32
	ProcessStartTime windows.Filetime
}

// RM_PROCESS_INFO
type rmProcessInfo struct {
	Process          rmUniqueProcess
	AppName          [256]uint16 // CCH_RM_MAX_APP_NAME + 1
	ServiceShortName [64]uint16  // CCH_RM_MAX_SVC_NAME + 1
	ApplicationType  uint32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

func holdersOf(path string) ([]int, error) {
	var handle uint32
	sessionKey := make([]uint16, cchRmSessionKey+1)
	ret, _, _ := procRmStartSession.Call(
		uintptr(unsafe.Pointer(&handle)),
		0,
		uintptr(unsafe.Pointer(&sessionKey[0])),
	)
	if ret != 0 {
		return nil, fmt.Errorf("RmStartSession failed: %d", ret)
	}
	defer procRmEndSession.Call(uintptr(handle))

	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	resources := []*uint16{pathPtr}
	ret, _, _ = procRmRegisterResources.Call(
		uintptr(handle),
		1,
		uintptr(unsafe.Pointer(&resources[0])),
		0, 0, 0, 0,
	)
	if ret != 0 {
		return nil, fmt.Errorf("RmRegisterResources failed: %d", ret)
	}

	// RmGetList reports how many slots it needs; grow and retry on
	// ERROR_MORE_DATA (a process can appear between the two calls).
	var pids []int
	procInfoCount := uint32(4)
	for {
		procInfos := make([]rmProcessInfo, procInfoCount)
		needed := uint32(0)
		count := procInfoCount
		var rebootReasons uint32
		ret, _, _ = procRmGetList.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&needed)),
			uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&procInfos[0])),
			uintptr(unsafe.Pointer(&rebootReasons)),
		)
		if ret == errorMoreData {
			procInfoCount = needed
			continue
		}
		if ret != 0 {
			return nil, fmt.Errorf("RmGetList failed: %d", ret)
		}
		for i := uint32(0); i < count; i++ {
			pids = append(pids, int(procInfos[i].Process.ProcessID))
		}
		return pids, nil
	}
}
