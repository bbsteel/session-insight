//go:build darwin

package main

// platformSetWindowIcon is a no-op on macOS: native macOS windows do not
// display title-bar icons, and the dock icon belongs to the app bundle
// (NSApplication), not the window. Follow-up: build an .app bundle with
// ICNS icon so the Dock shows the app icon.

func platformSetWindowIcon() {}
