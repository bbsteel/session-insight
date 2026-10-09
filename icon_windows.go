//go:build windows

package main

import (
	_ "embed"
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowIconICO is the app icon (same artwork as the site favicon) in ICO
// format, suitable for the Win32 window icon APIs.
//
//go:embed frontend/public/favicon.ico
var windowIconICO []byte

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procSendMessageW             = user32.NewProc("SendMessageW")
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1
)

// platformSetWindowIcon sets the taskbar/title-bar icon from the embedded
// ICO. The ICO container layout is: a 6-byte ICONDIR header followed by
// 16-byte ICONDIRENTRY records, with image data at each record's
// dwImageOffset; favicon.ico ships multiple sizes, the first entry is fine
// because CreateIconFromResourceEx scales to the requested dimensions.
// Must be called after webview.New + SetTitle so FindWindow can locate the
// window by its title.
func platformSetWindowIcon() {
	if len(windowIconICO) < 22 {
		return
	}
	dataOffset := int(binary.LittleEndian.Uint32(windowIconICO[18:22]))
	if dataOffset <= 0 || dataOffset >= len(windowIconICO) {
		return
	}
	iconData := windowIconICO[dataOffset:]
	icon, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&iconData[0])),
		uintptr(len(iconData)),
		1,          // fIcon = TRUE
		0x00030000, // dwResVersion
		0, 0,       // cxDesired, cyDesired: 0 = first resource size
		0, // LR_DEFAULTCOLOR
	)
	if icon == 0 {
		return
	}
	title, err := windows.UTF16PtrFromString(webviewWindowTitle)
	if err != nil {
		return
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return
	}
	procSendMessageW.Call(hwnd, wmSetIcon, iconBig, icon)
	procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, icon)
}
