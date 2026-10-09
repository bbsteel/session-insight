package main

import (
	"log"
	"net"
	"reflect"
	"unsafe"

	webview "github.com/webview/webview_go"
)

// webviewWindowTitle names the standalone desktop window.
const webviewWindowTitle = "Session Insight"

// startAppWindow embeds the UI in a dedicated desktop window backed by the OS
// webview engine (WebKitGTK on Linux, WebKit on macOS, WebView2 on Windows).
// The HTTP server keeps serving in the background while the window is open;
// closing the window releases the listener so main can exit cleanly. Returns
// true when the window took over (the process exits when it closes), false
// when the caller should fall back to the system browser.
func startAppWindow(url string, listener net.Listener, serve func() error) bool {
	if err := prepareWebviewEngine(); err != nil {
		log.Printf("app window unavailable: %v", err)
		return false
	}

	window := webview.New(false)
	if !webviewHandleValid(window) {
		// Native creation failed (GTK init without a display, missing
		// WebView2). The binding still returns a non-nil wrapper around a
		// NULL handle; using it would crash instead of falling back.
		log.Printf("app window unavailable: native window creation failed")
		return false
	}
	defer window.Destroy()

	// Start serving only after the window is known-valid: the browser
	// fallback path serves in the foreground goroutine, and starting it here
	// as well would run two Serve loops on one listener.
	go func() {
		// The listener is already bound, so a failure here is only the
		// expected "closed" error after the window shuts down — log it
		// instead of log.Fatal, which would skip deferred cleanup in main.
		if err := serve(); err != nil {
			log.Printf("serve stopped: %v", err)
		}
	}()

	window.SetTitle(webviewWindowTitle)
	// Icon must be set after New (GTK/WebKit init) and after SetTitle
	// (Windows locates the window by title).
	platformSetWindowIcon()
	window.SetSize(1440, 900, webview.HintNone)
	window.Navigate(url)
	// Blocks until the user closes the window.
	window.Run()
	// Window closed: unblock Serve so main returns and deferred cleanup runs.
	_ = listener.Close()
	return true
}

// webviewHandleValid reports whether the native webview handle behind the Go
// binding wrapper is non-NULL. The pinned binding always returns a non-nil
// wrapper even when webview_create fails, and any subsequent call
// (SetTitle/Navigate/Run) would dereference the NULL handle. The wrapper
// struct's only field is the webview_t handle (pinned webview_go version), so
// it is read directly; regenerate/check this if the binding is ever re-pinned.
func webviewHandleValid(window webview.WebView) bool {
	rv := reflect.ValueOf(window)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return false
	}
	return *(*unsafe.Pointer)(unsafe.Pointer(rv.Pointer())) != nil
}
