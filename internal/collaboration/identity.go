package collaboration

import "strings"

// Identity rules (frozen contract):
//
//   - Invocation IDs derive from native stable source IDs, namespaced by
//     root Agent type + root Session ID.
//   - The root invocation ID is deterministic: <agent_type>:<root_session_id>:root.
//   - Forbidden as identity: positional syntheses (evt-*-%04d counters),
//     array order, TurnIndex, display names, filesystem mtime, and absolute
//     paths.
//
// Two-parse stability is the V1 conformance floor; resume/compaction and
// partial-write stability extend the same assertions when fixtures exist.

// RootInvocationID returns the deterministic ID of the root invocation:
// <agent_type>:<root_session_id>:root. Both components must be non-empty.
func RootInvocationID(agentType, rootSessionID string) string {
	return agentType + ":" + rootSessionID + ":root"
}

// ChildInvocationID namespaces a native stable child ID under the root
// Session. nativeID must be a native stable source ID (Codex payload.id,
// Chrys parent_provider_call_id, Copilot toolCallId, Claude agentId), never
// a positional synthesis, array index, turn index, display name, file
// modification time, or absolute path.
func ChildInvocationID(agentType, rootSessionID, nativeID string) string {
	return agentType + ":" + rootSessionID + ":child:" + nativeID
}

// DelegationIDFor derives the deterministic delegation ID from the parent
// and child invocation IDs. Because every retry is a separate invocation
// (and therefore a separate child ID), one canonical delegation per
// parent-child pair keeps this ID unique in a valid graph.
func DelegationIDFor(parentInvocationID, childInvocationID string) string {
	return parentInvocationID + "->" + childInvocationID
}

// IsRootInvocationID reports whether id has the deterministic root shape
// for the given graph coordinates.
func IsRootInvocationID(agentType, rootSessionID, id string) bool {
	return id == RootInvocationID(agentType, rootSessionID)
}

// validIDComponent reports whether s is usable as an ID component:
// non-empty and free of surrounding whitespace. The ":" separator is
// allowed inside native IDs because IDs are never parsed back apart.
func validIDComponent(s string) bool {
	return s != "" && strings.TrimSpace(s) == s
}
