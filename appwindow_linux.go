//go:build linux

package main

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// prepareWebviewEngine loads the WebKitGTK stack on demand so the binary does
// not list it as a startup dependency: the Linux link step leaves the
// GTK/WebKit symbols unresolved (see scripts/buildtools/) and the first call
// into them is preceded by this dlopen, which publishes the whole dependency
// tree (libwebkit2gtk pulls in GTK3, GLib, …) into the global symbol scope.
// Without this, the dynamic loader would refuse to start the binary on any
// machine without WebKitGTK installed, even for plain browser mode.
func prepareWebviewEngine() error {
	candidates := []string{
		"libwebkit2gtk-4.1.so.0",
		"libwebkit2gtk-4.0.so.37",
	}
	var detail string
	for _, name := range candidates {
		cName := C.CString(name)
		handle := C.dlopen(cName, C.RTLD_NOW|C.RTLD_GLOBAL)
		C.free(unsafe.Pointer(cName))
		if handle != nil {
			return nil
		}
		if detail == "" {
			detail = C.GoString(C.dlerror())
		}
	}
	return fmt.Errorf("WebKitGTK not found (tried %v): %s — install webkit2gtk-4.1 (Arch: pacman -S webkit2gtk-4.1; Debian/Ubuntu: apt install libwebkit2gtk-4.1-0) or run without --app", candidates, detail)
}
