package chrys

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for Chrys.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: registry + session JSON parse/render
//   - realtime: LiveRevision (stat of session files)
//   - tokens: total_session_* token fields + group token_count → SessionBilling
//   - tool_results: tool messages + _chrys_tool_result_metadata failure states
//   - diff: edit_file path/old_string/new_string normalized for ExtractEditCalls
//   - subtasks: sub-agent tool results spliced into parent render depth
//   - resume: session id is the chrys -s argument
//   - delete: SessionDeleter removes whole session directory
//   - terminate: unsupported — only SessionRunning (turn markers); no exact PID
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "chrys",
		DisplayName:     "Chrys",
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
