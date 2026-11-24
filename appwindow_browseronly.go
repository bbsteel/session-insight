//go:build browseronly

package main

import (
	"log"
	"net"
)

func startAppWindow(_ string, _ net.Listener, _ func() error) bool {
	log.Print("app window unavailable in this browser-only build")
	return false
}
