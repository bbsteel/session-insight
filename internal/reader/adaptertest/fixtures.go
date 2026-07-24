package adaptertest

// Fixture describes one test-owned, sanitized storage root for a scenario.
// Phase 2 only requires adapters to build a Reader that already points at
// the fixture; this type documents the scenario and leaves room for Layer 3
// capability suites without changing every call site.
//
// Root is either a directory (directory-backed agents) or a SQLite file path
// (OpenCode). Never point Root at the developer's real home agent roots.
type Fixture struct {
	// Name is a short scenario label (e.g. "basic").
	Name string
	// Root is the absolute path to the test-owned fixture root or database.
	Root string
}

// FixtureSet groups optional scenario fixtures. Phase 2 uses only Basic.
// Non-nil optional fields are reserved for a later capability-suite phase;
// this package does not run empty placeholder checks on them.
type FixtureSet struct {
	Basic       Fixture
	Tools       *Fixture
	Diff        *Fixture
	Subtasks    *Fixture
	Interrupted *Fixture
}

// Expectations describes what the basic behavior suite should observe on the
// fixture bound into the Reader returned by Config.NewReader.
type Expectations struct {
	// SessionCount is the expected number of sessions from ListSessions.
	// Required; use 0 only when the fixture is intentionally empty.
	SessionCount int
	// SessionIDs, when non-empty, must match the listed IDs as a set
	// (order is still checked for stability across repeated ListSessions).
	SessionIDs []string
	// UnknownSessionID is used for the "not found" path. Empty means a
	// synthetic ID that cannot collide with fixture IDs.
	UnknownSessionID string
}
