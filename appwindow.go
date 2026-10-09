package main

import (
	"log"
	"net"

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

	go func() {
		// The listener is already bound, so a failure here is only the
		// expected "closed" error after the window shuts down — log it
		// instead of log.Fatal, which would skip deferred cleanup in main.
		if err := serve(); err != nil {
			log.Printf("serve stopped: %v", err)
		}
	}()

	window := webview.New(false)
	defer window.Destroy()
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
