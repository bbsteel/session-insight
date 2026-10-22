package pi

import (
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/procfind"
)

// Pi does not hold its transcript descriptor open. SessionManager writes the
// first flush with openSync(..., "wx"), closes it, and appends every later
// record with appendFileSync (verified against the installed 1.3.0 build), so
// an idle-but-alive Pi has no fd on the JSONL and no heartbeat/lock registry.
// A descriptor scan therefore reports "not running" for a live session, and an
// mtime window is only a heuristic: a session left idle for longer than the
// window is still owned by its process.
//
// The exact, Pi-state-independent evidence is the process itself. Pi stores one
// encoded-cwd directory per working directory:
//
//	--<abs-cwd with /, \, : replaced by - and the leading separator dropped>--
//
// and a session stays in the directory it was created in, so a live Pi process
// whose cwd encodes to the session's directory may own (or resume) that
// session.

// encodesPiSessionDir reproduces Pi's own getDefaultSessionDirPath naming for a
// working directory. It must stay byte-identical to Pi's implementation, or the
// liveness evidence would silently never match.
func encodesPiSessionDir(cwd string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(cwd, "/"), `\`)
	replacer := strings.NewReplacer("/", "-", `\`, "-", ":", "-")
	return "--" + replacer.Replace(trimmed) + "--"
}

// processCwd returns a PID's working directory, or "" when unavailable.
// Replaced in tests so liveness can be exercised without spawning a Pi process.
type processCwd func(pid int) string

// processList returns the live PIDs whose OS process name is name.
// Replaced in tests for the same reason.
type processList func(name string) ([]int, error)

// sessionProcessCandidates returns live Pi PIDs whose working directory maps to
// the directory holding path. Process identity comes from the OS only, so no
// Pi-side state is trusted and no process arguments are read.
func (r *PiReader) sessionProcessCandidates(path string) []int {
	pids, err := r.listProcesses(processName)
	if err != nil || len(pids) == 0 {
		return nil
	}
	dir := filepath.Clean(filepath.Dir(path))
	var matched []int
	for _, pid := range pids {
		cwd := r.processCwd(pid)
		if cwd == "" {
			continue
		}
		candidate := filepath.Join(r.sessionsDir, encodesPiSessionDir(filepath.Clean(cwd)))
		if filepath.Clean(candidate) == dir {
			matched = append(matched, pid)
		}
	}
	return matched
}

// defaultProcessCwd and defaultProcessList are the production process sources.
func defaultProcessCwd(pid int) string { return procfind.WorkingDir(pid) }

func defaultProcessList(name string) ([]int, error) {
	return procfind.ProcessesNamed(name)
}

// withProcessTable returns a reader whose process evidence comes from the given
// table, for tests that must not depend on the host's running processes.
func withProcessTable(r *PiReader, cwd map[int]string) *PiReader {
	pids := make([]int, 0, len(cwd))
	for pid := range cwd {
		pids = append(pids, pid)
	}
	r.listProcesses = func(string) ([]int, error) { return pids, nil }
	r.processCwd = func(pid int) string { return cwd[pid] }
	return r
}
