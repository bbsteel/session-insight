package worktreereview

import "os"

// osOpenAppend is a tiny indirection so conformance tests append to a
// fixture the same way the writer does (append-only, no truncation).
func osOpenAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
}
