//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <stdlib.h>
*/
import "C"

import (
	_ "embed"
	"os"
	"path/filepath"
	"unsafe"
)

// windowIconPNG is the app icon (same artwork as the site favicon), embedded
// so the desktop window carries the app's identity without external files.
//
//go:embed frontend/public/favicon.png
var windowIconPNG []byte

// platformSetWindowIcon applies the embedded icon to the app window.
// GDK can load a PNG from a path but not from memory here, so the icon is
// written to a per-user cache file (tiny, overwritten each run) and loaded
// via gtk_window_set_default_icon_from_file, which applies to all current
// and future windows — no native window handle needed. Must be called after
// webview.New (GTK needs to be initialized first).
func platformSetWindowIcon() {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	iconDir := filepath.Join(cacheDir, "session-insight")
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return
	}
	iconPath := filepath.Join(iconDir, "window-icon.png")
	if err := os.WriteFile(iconPath, windowIconPNG, 0o644); err != nil {
		return
	}
	cPath := C.CString(iconPath)
	defer C.free(unsafe.Pointer(cPath))
	C.gtk_window_set_default_icon_from_file(cPath, nil)
}
