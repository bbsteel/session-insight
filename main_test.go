package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
)

func TestIsPrimaryCheckout(t *testing.T) {
	checkoutRoot := t.TempDir()
	if isPrimaryCheckout(checkoutRoot) {
		t.Fatal("directory without .git must not be treated as the primary checkout")
	}

	if err := os.Mkdir(filepath.Join(checkoutRoot, ".git"), 0o755); err != nil {
		t.Fatalf("create primary .git directory: %v", err)
	}
	if !isPrimaryCheckout(checkoutRoot) {
		t.Fatal(".git directory must identify the primary checkout")
	}

	linkedCheckoutRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(linkedCheckoutRoot, ".git"), []byte("gitdir: /tmp/worktree"), 0o600); err != nil {
		t.Fatalf("create linked-worktree .git file: %v", err)
	}
	if isPrimaryCheckout(linkedCheckoutRoot) {
		t.Fatal(".git file must not be treated as the primary checkout")
	}
}

func TestResolveIndexerEnabled(t *testing.T) {
	tests := []struct {
		name            string
		configuredValue string
		primaryCheckout bool
		wantEnabled     bool
		wantReason      string
	}{
		{
			name:            "primary default",
			primaryCheckout: true,
			wantEnabled:     true,
			wantReason:      "primary checkout",
		},
		{
			name:            "primary ignores disabled value",
			configuredValue: "0",
			primaryCheckout: true,
			wantEnabled:     true,
			wantReason:      "primary checkout forces enabled",
		},
		{
			name:        "linked default",
			wantEnabled: true,
			wantReason:  "default enabled",
		},
		{
			name:            "linked disabled",
			configuredValue: " off ",
			wantEnabled:     false,
			wantReason:      "SI_INDEXER_ENABLED disabled",
		},
		{
			name:            "linked enabled",
			configuredValue: "true",
			wantEnabled:     true,
			wantReason:      "SI_INDEXER_ENABLED enabled",
		},
		{
			name:            "invalid value defaults enabled",
			configuredValue: "sometimes",
			wantEnabled:     true,
			wantReason:      `invalid SI_INDEXER_ENABLED="sometimes"; default enabled`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			gotEnabled, gotReason := resolveIndexerEnabled(testCase.configuredValue, testCase.primaryCheckout)
			if gotEnabled != testCase.wantEnabled || gotReason != testCase.wantReason {
				t.Fatalf("resolveIndexerEnabled(%q, %t) = (%t, %q), want (%t, %q)",
					testCase.configuredValue,
					testCase.primaryCheckout,
					gotEnabled,
					gotReason,
					testCase.wantEnabled,
					testCase.wantReason,
				)
			}
		})
	}
}

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
	// Must make exactly one call to the requested address, no fallback.
	nonAddrErr := &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EACCES}
	var addrs []string
	_, err = listenWithFallbackFn("127.0.0.1", "8080", func(network, addr string) (net.Listener, error) {
		addrs = append(addrs, addr)
		return nil, nonAddrErr
	})
	if !errors.Is(err, syscall.EACCES) {
		t.Errorf("expected EACCES to pass through, got: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "127.0.0.1:8080" {
		t.Errorf("expected one call to 127.0.0.1:8080, got %v", addrs)
	}

	// Fallback listen failure: first attempt EADDRINUSE, fallback port-0 also fails.
	// Must make exactly two calls: 127.0.0.1:8080 then 127.0.0.1:0.
	secondErr := errors.New("port exhausted")
	addrs = nil
	_, err = listenWithFallbackFn("127.0.0.1", "8080", func(network, addr string) (net.Listener, error) {
		addrs = append(addrs, addr)
		if len(addrs) == 1 {
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
	if len(addrs) != 2 || addrs[0] != "127.0.0.1:8080" || addrs[1] != "127.0.0.1:0" {
		t.Errorf("expected [127.0.0.1:8080 127.0.0.1:0], got %v", addrs)
	}
}

func TestBrowserOpenCmd(t *testing.T) {
	const url = "http://127.0.0.1:8080/"
	cases := []struct {
		goos string
		name string
		args []string
	}{
		{"windows", "cmd", []string{"/c", "start", "", url}},
		{"darwin", "open", []string{url}},
		{"linux", "xdg-open", []string{url}},
		{"freebsd", "xdg-open", []string{url}},
	}
	for _, tc := range cases {
		name, args := browserOpenCmd(tc.goos, url)
		if name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("goos=%s: got (%q, %v), want (%q, %v)", tc.goos, name, args, tc.name, tc.args)
		}
	}
}

func TestOpenBrowser(t *testing.T) {
	const url = "http://127.0.0.1:9090/"
	origStart := startBrowserCommand
	defer func() { startBrowserCommand = origStart }()

	t.Run("invokes platform command", func(t *testing.T) {
		var gotName string
		var gotArgs []string
		startBrowserCommand = func(name string, args ...string) error {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return nil
		}
		openBrowser(url)
		wantName, wantArgs := browserOpenCmd(runtime.GOOS, url)
		if gotName != wantName || !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("got (%q, %v), want (%q, %v)", gotName, gotArgs, wantName, wantArgs)
		}
	})

	t.Run("start failure does not panic", func(t *testing.T) {
		startBrowserCommand = func(name string, args ...string) error {
			return errors.New("no browser")
		}
		openBrowser(url)
	})
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
