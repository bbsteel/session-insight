package codex

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for Codex.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: registry + BaseSessionReader, JSONL rollout parse/render
//   - realtime: LiveRevision (stat of rollout file)
//   - tokens: token_count events → TokenUsage (codex_tokens_test)
//   - tool_results: function/custom tool call + result pairing
//   - diff: apply_patch (+ exec-wrapper unwrap) → ExtractEditCalls (codex_render_test)
//   - subtasks: session_meta parent_thread_id / thread_source=subagent lineage
//   - resume: native payload.id as ResumeID (codex_resume_test); file stem is not CLI id
//   - delete: SessionDeleter (history.jsonl + rollout)
//   - terminate: SessionProcessFinder via fd holders of the open rollout file
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "codex",
		DisplayName:     "Codex",
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
			capability.CapabilityTerminate:   capability.Exact(),
		},
	}
}
