package grok

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for Grok Build.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: registry + updates.jsonl / chat_history parse/render
//   - realtime: LiveRevision (mtime+size of content files)
//   - tokens: turn usage → SessionBilling (exact tests)
//   - tool_results: tool_call + tool_call_update → ToolInvocation/ToolResult
//   - diff: search_replace tool recognized by IsEditTool; render fixtures use it
//   - subtasks: exact — structured subagents/<id>/meta.json plus parent-stream
//     subagent_spawned / subagent_finished, joined on subagent_id; standalone
//     child Sessions carry is_subagent + parent_session_id; backing Session
//     when child_session_id resolves (collaboration.go + fixtures + suite)
//   - resume: ResumeID equals session UUID; CLI --resume (tests)
//   - delete: SessionDeleter cleans dir + global footprints
//   - terminate: SessionProcessFinder via active_sessions + fd holders
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "grok",
		DisplayName:     "Grok",
		AdapterRevision: 3,
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
			capability.CapabilityTerminate:   capability.Exact(),
		},
	}
}
