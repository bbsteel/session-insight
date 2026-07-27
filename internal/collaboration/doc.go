// Package collaboration is the shared, compile-time contract for multi-Agent
// collaboration inside one selected Session.
//
// It normalizes three observed child-work shapes into one representation:
//
//   - standalone child Sessions (Codex, OpenCode) backed by a native Session;
//   - embedded child transcripts (Claude, Chrys) stored inside the parent;
//   - lifecycle-only child invocations (Copilot) with no independent
//     transcript.
//
// Canonical terms (frozen contract input, 2026-07-27):
//
//   - Session remains the product aggregate. Only root Sessions appear in the
//     default Session list.
//   - AgentInvocation is one bounded execution of a main or child Agent.
//     "Lifecycle-only" is a content-availability condition, not a separate
//     invocation type.
//   - Delegation is the causal relation between a parent and child
//     invocation. It carries launch/result anchors, timing, execution mode,
//     and per-fact evidence. Delegation has no Status field: status lives
//     only on AgentInvocation (Phase 0 decision).
//   - BackingSessionRef is optional. Its presence enables "View child Agent
//     record" (and, where supported, native resume/delete); its absence
//     forbids implying independent Session behavior.
//
// Evidence rules:
//
//   - Field-level precision reuses the capability contract states
//     (exact | estimated | missing | not_applicable | unsupported). missing
//     never appears in static adapter declarations.
//   - Precision attaches to individual facts (trigger, timing, task, result,
//     content), not just whole records. Every non-exact fact carries a
//     machine-readable reason code.
//   - Missing evidence stays absent; adapters never synthesize exact values.
//
// Graph rules (see Validate):
//
//   - Exactly one deterministic root invocation per graph.
//   - One canonical causal parent per child in V1; extra relation evidence
//     is preserved on the graph, never discarded.
//   - Self-links are quarantined; cycles are detected and quarantined.
//   - An invocation whose parent evidence is missing attaches to the visible
//     UnlinkedGroupLabel group; its transcript is never discarded.
//   - Nesting depth is data-driven; no depth cap is assumed.
//
// Canonical root-list filtering layer (contract decision): adapters report
// child lineage faithfully on every listed session (ParentSessionID /
// IsSubagent) and never pre-filter children from ListSessions, because the
// indexer needs the child records. Root filtering lives in exactly one
// place — the session-store query predicate shared by the list and the
// root-count queries. The pre-contract split (HTTP handler + root-count SQL
// filtered, Codex reader ListSessions unfiltered) converges onto this layer
// in Phase 1/2; this package defines the decision but does not move the
// existing filters.
//
// Renderer note: this package must stay free of renderer-specific types.
// The frontend layout contract (pure RenderPrimitives, SVG renderer) is
// frozen separately and never enters the Go model.
package collaboration
