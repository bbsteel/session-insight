package copilot

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for GitHub Copilot.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: registry + events.jsonl parse/render
//   - realtime: LiveRevision (stat of session files)
//   - tokens: session.shutdown tokenDetails → SessionBilling (billing tests)
//   - tool_results: tool.execution_start/complete → ToolInvocation/ToolResult
//   - diff: tool names pass through; apply_patch is a recognized edit tool
//   - subtasks: subagent.started names on turns + InsightEvidence subagent windows
//   - resume: unsupported — SI has no CLI resume command for copilot (VS Code only)
//   - delete: SessionDeleter purges session dir + store DB
//   - terminate: SessionProcessFinder via fd holders of session files
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       "copilot",
		DisplayName:     "Copilot",
		AdapterRevision: 1,
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityRealtime:    capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.Exact(),
			capability.CapabilityResume:      capability.Unsupported("adapter_not_implemented"),
			capability.CapabilityDelete:      capability.Exact(),
			capability.CapabilityTerminate:   capability.Exact(),
		},
	}
}
