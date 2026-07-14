//go:build !linux

package procfind

// StartToken is only implemented on Linux (via /proc/<pid>/stat). On other
// platforms callers fall back to the bare Alive check — acceptable because
// the token is a PID-reuse guard, not the primary liveness signal.
func StartToken(pid int) (string, bool) {
	return "", false
}
