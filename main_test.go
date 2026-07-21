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

	// Non-listen OpError must not match — deterministic, no network dependency.
	nonListenErr := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	if isAddrInUse(nonListenErr) {
		t.Errorf("expected false for non-listen OpError; err=%v", nonListenErr)
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
	occPort := occ.Addr().(*net.TCPAddr).Port

	l2, err := listenWithFallback("127.0.0.1", itoa(occPort))
	if err != nil {
		t.Fatalf("listenWithFallback on occupied port: %v", err)
	}
	defer l2.Close()
	fallbackPort := l2.Addr().(*net.TCPAddr).Port

	if fallbackPort == 0 {
		t.Error("expected non-zero fallback port")
	}
	if fallbackPort == occPort {
		t.Errorf("expected fallback port != %d, got %d", occPort, fallbackPort)
	}

	// Non-EADDRINUSE error is not a fallback candidate: returned as-is.
	nonAddrErr := &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EACCES}
	_, err = listenWithFallbackFn("127.0.0.1", "8080", func(network, addr string) (net.Listener, error) {
		return nil, nonAddrErr
	})
	if !errors.Is(err, syscall.EACCES) {
		t.Errorf("expected EACCES to pass through, got: %v", err)
	}

	// Fallback listen failure: first attempt EADDRINUSE, fallback port-0 also fails.
	secondErr := errors.New("port exhausted")
	callCount := 0
	_, err = listenWithFallbackFn("127.0.0.1", "8080", func(network, addr string) (net.Listener, error) {
		callCount++
		if callCount == 1 {
			return nil, &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EADDRINUSE}
		}
		return nil, secondErr
	})
	if err == nil {
		t.Fatal("expected error when both attempts fail")
	}
	// The returned error should wrap the second failure.
	if !errors.Is(err, secondErr) {
		t.Errorf("expected error wrapping secondErr, got: %v", err)
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
