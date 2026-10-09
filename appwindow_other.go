//go:build darwin || windows

package main

// prepareWebviewEngine is a no-op on macOS and Windows: the webview engine is
// part of the OS there (WebKit framework / WebView2 runtime), so the binary
// carries no extra startup dependency and nothing needs lazy loading.
func prepareWebviewEngine() error { return nil }
