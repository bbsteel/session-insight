package opencode

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for OpenCode.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: ResolveDBPath + SQLite message parse/render
//   - realtime: LiveRevision (stat of SQLite db + wal/shm)
//   - tokens: assistant message tokens + cost → SessionBilling (exact tests)
//   - tool_results: tool parts paired with results in render path
//   - diff: edit tool old_string/new_string (opencode_test)
//   - subtasks: turn.Subagents from structured agent parts; parent_id cascade on delete
//   - resume: session id is the opencode -s argument
//   - delete: SessionDeleter with parent_id child cascade
//   - terminate: unsupported — only SessionRunning (in-memory busy state not on disk PID)
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "opencode",
		DisplayName:     "OpenCode",
		AdapterRevision: 1,
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.Exact(),
			capability.CapabilityResume:      capability.Exact(),
			capability.CapabilityDelete:      capability.Exact(),
			capability.CapabilityTerminate:   capability.Unsupported("exact_pid_unavailable"),
		},
	}
}
