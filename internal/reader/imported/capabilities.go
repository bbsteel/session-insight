package imported

import "github.com/bbsteel/session-insight/internal/reader/capability"

// Capabilities returns the static Agent capability declaration for imported
// bundle sessions.
//
// Evidence (implementation + tests; not product guesswork):
//   - discovery/replay: ListSessionsDetailed walks bundle manifests;
//     GetSession/GetRenderEvents/RenderANSI serve the stored snapshot
//   - tokens/tool_results/diff/subtasks: the stored SessionDetail and render
//     stream carry whatever the origin instance normalized
//   - realtime: a bundle never changes after extraction; there is no live
//     signal to poll, so the concept is not applicable (SessionLive is
//     pinned to false so fresh imports never flash a live badge)
//   - resume: the original agent process lives on a foreign host; the
//     imported copy has no resume identity here
//   - delete: the imported copy is managed as a whole bundle via
//     DELETE /api/imports/{bundle}, not per session
//   - terminate: there is no process behind an imported snapshot
func Capabilities() capability.AgentCapabilities {
	return capability.AgentCapabilities{
		AgentType:       AgentType,
		DisplayName:     "Imported",
		AdapterRevision: 1,
		ResumeCommand:   nil,
		Capabilities: map[capability.CapabilityID]capability.CapabilityDeclaration{
			capability.CapabilityDiscovery:   capability.Exact(),
			capability.CapabilityReplay:      capability.Exact(),
			capability.CapabilityTokens:      capability.Exact(),
			capability.CapabilityToolResults: capability.Exact(),
			capability.CapabilityDiff:        capability.Exact(),
			capability.CapabilitySubtasks:    capability.Exact(),
			capability.CapabilityRealtime:    capability.NotApplicable("static_snapshot"),
			capability.CapabilityResume:      capability.Unsupported("foreign_host"),
			capability.CapabilityDelete:      capability.Unsupported("imported_copy_managed_via_bundle"),
			capability.CapabilityTerminate:   capability.NotApplicable("no_process"),
		},
	}
}
