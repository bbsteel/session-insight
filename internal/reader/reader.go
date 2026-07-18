package reader

import "github.com/bbsteel/session-insight/internal/model"

type BaseSessionReader interface {
	AgentType() string
	DisplayName() string
	ListSessions() ([]model.Session, error)
	GetSession(id string) (*model.SessionDetail, error)

	// RenderANSI returns the session's RenderEvent stream formatted as ANSI
	// terminal text (see internal/render). Readers without a RenderEvent
	// adapter yet (Codex/Copilot, as of Phase 2) should return a non-nil
	// error rather than panic or silently produce empty output, so the API
	// layer can tell "session not found" apart from "rendering not
	// supported for this agent type" and report it clearly instead of
	// falling through to the next reader.
	// cols is the terminal column count from the frontend (0 = use default).
	RenderANSI(id string, cols int) (string, error)

	// GetRenderEvents returns the raw render event stream for a session.
	// Used by the server to extract structured data (e.g. Edit calls).
	GetRenderEvents(id string) ([]model.RenderEvent, error)
}

// WatchRootProvider is an optional reader capability: the on-disk paths whose
// changes mean "this agent's session list may have changed". Directories are
// watched recursively; a file path (e.g. a SQLite database) means "watch this
// file and its derivatives (-wal/-shm)". Readers without it simply don't
// participate in live sidebar refresh.
type WatchRootProvider interface {
	WatchRoots() []string
}

// SessionDeleter is an optional reader capability: permanently remove a
// session's data from the agent's own storage (the session file plus any
// agent-side records like global history entries). It does not touch this
// app's index — the server clears that separately — and it does not check
// whether the session is running: liveness gating is the server's job via
// SessionProcessFinder. Agents without it simply can't be deleted from the UI.
type SessionDeleter interface {
	DeleteSession(id string) error
}

// SessionProcessFinder is an optional reader capability: the PIDs of running
// agent processes that own a session (e.g. hold its log file open). Used to
// refuse deleting a live session and to offer force-stop with an exact PID.
type SessionProcessFinder interface {
	SessionProcesses(id string) ([]int, error)
}

// SessionLivenessProvider is an optional, cheap reader capability for sidebar
// and session-detail presence checks. Implementations run on list requests, so
// they must use an agent-native lightweight signal (heartbeat/lock/registry),
// not scan process file descriptors or parse a full transcript. An error makes
// the shared resolver fall back to the timestamp window.
type SessionLivenessProvider interface {
	SessionLive(id string) (bool, error)
}

// SessionLivenessChecker is an optional reader capability for agents whose
// process↔session mapping cannot be recovered from the OS (opencode keeps
// busy state in a per-process in-memory map; chrys and copilot may hold no
// file descriptor while idle). SessionRunning answers "does this session
// look live" from the agent's own on-disk turn markers bounded by
// model.LiveWindow. It is used as the delete safety fallback. It yields no
// PIDs, so the server can refuse deletion but never offer force-stop from this
// signal alone. Readers
// with an exact PID source implement SessionProcessFinder instead;
// implementing both is fine (the finder is consulted first).
type SessionLivenessChecker interface {
	SessionRunning(id string) (bool, error)
}

// LiveRevisionProvider is an optional reader capability: a cheap, stat-level
// (no parsing) revision of a session's on-disk source. Live-tail polling
// hits this every few seconds, so implementations must not read file
// contents. Readers without it simply don't get live tail.
type LiveRevisionProvider interface {
	LiveRevision(id string) (int64, error)
}

// InsightEvidenceProvider is an optional reader capability: agent-specific
// facts that the unified SessionDetail discards but Deep Insight causal
// analysis needs (e.g. Copilot subagent delegation descriptions, models,
// modes, timing and response attribution). It returns the evidence plus the
// session revision it actually read from, so the generation layer can confirm
// the evidence came from the same logical snapshot as the SessionDetail and
// Findings (see the consistency-snapshot rules). Readers without it leave the
// bundle to declare an evidence gap. expectedRevision is advisory (0 = "read
// current"); the returned revision is authoritative for cross-checking.
type InsightEvidenceProvider interface {
	GetInsightEvidence(id string, expectedRevision int64) (*model.InsightEvidence, int64, error)
}
