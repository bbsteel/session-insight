package main

import (
	"log"
	"net"
)

// runUI launches the user interface for the bound URL. With appMode the UI
// opens in a native desktop window (see appwindow.go); when the window is
// unavailable — e.g. WebKitGTK is not installed on Linux — it logs the reason
// and falls back to the system browser. Without appMode it always uses the
// browser: open the URL, then serve until killed. Opening the browser is
// fire-and-forget so a slow browser never delays Serve.
func runUI(url string, listener net.Listener, serve func() error, appMode bool) {
	if appMode && startAppWindow(url, listener, serve) {
		return
	}
	openBrowser(url)
	log.Fatal(serve())
}
