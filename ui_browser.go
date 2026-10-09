//go:build !webview

package main

import (
	"log"
	"net"
)

// runUI default build: open the bound URL in the system browser, then serve
// until killed. Opening the browser is fire-and-forget so a slow browser
// never delays Serve.
func runUI(url string, _ net.Listener, serve func() error) {
	openBrowser(url)
	log.Fatal(serve())
}
