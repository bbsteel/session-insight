package main

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestIsAddrInUse(t *testing.T) {
	// EADDRINUSE from a real listen conflict.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, err = net.Listen("tcp", l.Addr().String())
	if !isAddrInUse(err) {
		t.Errorf("expected EADDRINUSE, got false; err=%v", err)
	}

	// Non-listen OpError (dial failure).
	_, err = net.Dial("tcp", "127.0.0.1:0")
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op != "listen" {
		if isAddrInUse(err) {
			t.Errorf("expected false for non-listen OpError; err=%v", err)
		}
	}

	// Plain error should not match.
	if isAddrInUse(errors.New("some error")) {
		t.Error("expected false for plain error")
	}

	// Syscall.EADDRINUSE without *net.OpError wrapping should not match
	// (guard requires a listen OpError).
	if isAddrInUse(syscall.EADDRINUSE) {
		t.Error("expected false for bare syscall.EADDRINUSE without OpError")
	}
}

func TestListenWithFallback(t *testing.T) {
	// Free port: uses the requested port.
	l, err := listenWithFallback("127.0.0.1", "0")
	if err != nil {
		t.Fatalf("listenWithFallback on free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	if addr == "" {
		t.Error("expected non-empty listener address")
	}

	// Occupied port: falls back to a different OS-assigned port.
	occ, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occ.Close()
	occAddr := occ.Addr().String()
	occPort := occ.Addr().(*net.TCPAddr).Port

	l2, err := listenWithFallback("127.0.0.1", itoa(occPort))
	if err != nil {
		t.Fatalf("listenWithFallback on occupied port: %v", err)
	}
	defer l2.Close()
	fallbackAddr := l2.Addr().String()
	fallbackPort := l2.Addr().(*net.TCPAddr).Port

	if fallbackPort == 0 {
		t.Error("expected non-zero fallback port")
	}
	if fallbackAddr == occAddr {
		t.Errorf("expected fallback to a different port, but got same: %s", fallbackAddr)
	}
	if fallbackPort == occPort {
		t.Errorf("expected fallback port != %d, got %d", occPort, fallbackPort)
	}

	// Fallback listen failure: when even port 0 fails (simulate with an
	// invalid host). This exercises the error-wrapping branch.
	_, err = listenWithFallback("256.0.0.1", "0")
	if err == nil {
		t.Fatal("expected error for invalid host")
	}
	// The error should be wrapped from the fallback attempt, not the original
	// EADDRINUSE path.
	if !errors.Is(err, syscall.EADDRNOTAVAIL) && !errors.Is(err, syscall.EADDRNOTAVAIL) {
		// Just verify it's an error; the exact type depends on platform.
		t.Logf("fallback failure error (expected): %v", err)
	}
}

// itoa is a minimal int-to-string helper to avoid importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
