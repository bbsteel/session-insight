# Agent Capability Contract Design

## Goal

Establish a single, adapter-owned source of truth for Agent capabilities so the backend API, settings, session views, and shared tests all consume the same data. The contract answers two separate questions:

1. **Agent capability:** What can SI normally do with this kind of Agent?
2. **Session status:** What did SI actually obtain for this specific session?

These layers must not be merged. If an Agent supports tokens but an interrupted session never persisted them, represent that as:

```text
Agent capability: tokens = exact
Current session: tokens = missing
```

## Shared Terminology

Use five consistent user-facing states:

| Symbol | Code value | Meaning |
|---|---|---|
| `✓` | `exact` | Read directly from structured facts, or an operation whose result can be confirmed exactly |
| `≈` | `estimated` | Inferred from a heuristic, time window, or incomplete evidence |
| `!` | `missing` | The Agent normally records the data, but this session does not contain it |
| `—` | `not_applicable` | The concept does not exist for this Agent |
| `×` | `unsupported` | The concept exists, but SI cannot currently read or manage it reliably |

Use an em dash, not a circle icon, for `not_applicable`. Color must never be the only differentiator; always pair the symbol with text.

### Allowed states by layer

Static Agent declarations allow only:

```text
exact | estimated | not_applicable | unsupported
```

Resolved session status allows all five values. `missing` must never appear in a static Agent declaration because it describes one concrete record rather than a product capability.

Engineering progress uses a separate internal vocabulary such as `investigating`, `implementing`, `verified`, and `blocked`. Progress states never enter the user-facing capability contract.

## v0.4.0 Baseline Capabilities

Capability IDs are stable API values. Display labels are not identifiers.

| ID | User label | Exact definition |
|---|---|---|
| `discovery` | Discovery | SI can automatically locate and list the Agent's local sessions without importing them individually |
| `replay` | Replay | SI can reconstruct user messages, assistant messages, and recognized events in persisted order |
| `realtime` | Realtime | While a session is open, SI can detect persisted content revisions and incrementally load or refresh the record |
| `tokens` | Tokens | SI can read token or billing fields, or estimate them under an explicit rule, while preserving field presence |
| `tool_results` | Tool results | SI can associate tool calls with results, failures, or rejection states |
| `diff` | Diff | SI can reconstruct before-and-after edit content or an equivalent patch from structured records |
| `subtasks` | Subtasks | SI can identify parent-child Agent or task relationships with stable identities |
| `resume` | Resume | SI can provide the stable identifier or command argument required by the Agent's native resume flow |
| `delete` | Delete | SI can completely remove the Agent-owned session records and confirm the target identity |
| `terminate` | Terminate run | SI can map the current session to its running process exactly and terminate that process |

`realtime` does not mean process liveness. Content revision detection and process-state detection must be described separately in evidence. If the UI later needs an independent comparison, add a `liveness` capability rather than silently changing the meaning of `realtime`.

`terminate` does not mean stopping replay, and it does not imply that SI can send a message to an Agent. Future Agent-control features require separate capability IDs such as `message_send` plus their own authorization, confirmation, and failure semantics.

## Proposed Go Model

Use strongly typed Go declarations as the first source of truth. Do not introduce YAML or hand-written JSON. Put capability types in a leaf package such as `internal/reader/capability` that does not depend on concrete readers, avoiding cycles between the registry and adapters:

```go
package capability

type CapabilityID string

const (
    CapabilityDiscovery   CapabilityID = "discovery"
    CapabilityReplay      CapabilityID = "replay"
    CapabilityRealtime    CapabilityID = "realtime"
    CapabilityTokens      CapabilityID = "tokens"
    CapabilityToolResults CapabilityID = "tool_results"
    CapabilityDiff        CapabilityID = "diff"
    CapabilitySubtasks    CapabilityID = "subtasks"
    CapabilityResume      CapabilityID = "resume"
    CapabilityDelete      CapabilityID = "delete"
    CapabilityTerminate   CapabilityID = "terminate"
)

type CapabilityState string

const (
    CapabilityExact         CapabilityState = "exact"
    CapabilityEstimated     CapabilityState = "estimated"
    CapabilityMissing       CapabilityState = "missing"
    CapabilityNotApplicable CapabilityState = "not_applicable"
    CapabilityUnsupported   CapabilityState = "unsupported"
)

type CapabilityDeclaration struct {
    State      CapabilityState
    ReasonCode string
    DetailKey  string
}

type AgentCapabilities struct {
    AgentType       string
    DisplayName     string
    AdapterRevision int
    Capabilities    map[CapabilityID]CapabilityDeclaration
}
```

The implementation may replace the map with fixed fields for compile-time completeness, but API output must retain stable capability IDs. `DetailKey` is a localization key; the backend does not hard-code user-facing explanations. `ReasonCode` is machine-readable and supports tests, diagnostics, and session-level overrides.

Increment `AdapterRevision` when capability semantics, parser mappings, or supported scope changes. It is not the Agent's own version.

Each adapter package exports a static declaration:

```go
func Capabilities() capability.AgentCapabilities
```

Do not attach the declaration only to an instantiated reader. The current `Discover()` creates readers only when Agent storage exists locally, while settings must be able to describe every supported Agent, including Agents that are not installed or have no sessions.

`internal/reader/registry.go` therefore maintains both:

- **definition catalog:** aggregates static declarations from all six adapters and is always queryable;
- **discovery result:** contains `BaseSessionReader` instances created from storage found on the current machine.

The registry aggregates declarations exported by adapters; it never re-enters capability values. A future registration definition may combine discovery functions and declarations, but do not introduce `init()` global side effects merely to remove a few explicit registry lines.

## Session-Level Overrides

The session detail API must not make the frontend infer status from whether a number is zero. Resolve status in the backend:

```go
type SessionCapabilityStatus struct {
    State      CapabilityState
    ReasonCode string
}

type SessionCapabilities struct {
    AgentType       string
    AdapterRevision int
    Status          map[CapabilityID]SessionCapabilityStatus
}
```

Resolution rules:

1. Start with the static Agent declaration.
2. Preserve `unsupported` and `not_applicable`.
3. If the static declaration is `exact` or `estimated` but expected data was not persisted for this session, override it with `missing`.
4. Preserve valid zero values as `exact`, such as an exactly recorded count of zero tool calls.
5. A session may reduce availability, but it cannot promote an undeclared capability to `exact`.

Operation capabilities also return current availability. For example, an Agent may support resume while the current session lacks a stable `ResumeID`; the session status is `missing`, the resume action is disabled, and the reason is displayed.

## Reason Codes

Reason codes are stable and non-localized. Frontend i18n maps them to display copy. An initial set may include:

```text
source_not_recorded
session_not_finalized
resume_id_missing
exact_pid_unavailable
timestamp_heuristic
revision_polling
structured_event
adapter_not_implemented
concept_absent
platform_not_supported
```

Every `estimated`, `missing`, `unsupported`, and `not_applicable` state requires a reason code. `exact` may omit one, although high-risk operations such as delete and terminate should still identify their evidence type.

## Contract Validation Rules

At minimum, shared tests enforce:

- all ten baseline capabilities exist exactly once;
- `AgentType` matches the reader's `AgentType()`;
- `DisplayName` is non-empty;
- static Agent declarations never contain `missing`;
- every state is a known enum value;
- every non-`exact` state contains a `ReasonCode`;
- `realtime = exact/estimated` requires a content revision implementation;
- `delete = exact` requires `SessionDeleter`;
- `terminate = exact` requires `SessionProcessFinder`;
- `terminate` cannot be `estimated`;
- every supported data capability has a fixture and behavior check.

The existence of a Go interface proves only that an implementation entry point exists. Fixture-backed behavioral tests provide the final evidence.

## API and UI Consumption

Provide an Agent catalog API:

```text
GET /api/agents
```

It returns every registered Agent's display name, adapter revision, capability declarations, and whether the Agent was discovered locally. Session detail returns or links to resolved session status. Capabilities remain visible for an uninstalled Agent, but the API must not invent session counts or executable actions.

Settings compares baseline capabilities across Agents. A session view provides an entry point for the current Agent and overlays current-session status. Neither surface hard-codes Agent capabilities.

The session header summarizes only issues relevant to current use, such as "2 missing," rather than showing ten persistent badges. The full state belongs in the Agent details panel.

## Extension Model

Capability IDs may later use stable namespaces:

```text
memory.inspect
memory.history
storage.inventory
storage.usage
message_send
liveness
```

Memory and local-storage analysis involve sensitive paths, content access, retention rules, and deletion semantics. Design them as separate capability groups with explicit permission boundaries rather than overloading `replay` or `discovery`.

Adding a capability requires:

1. a precise definition independent of any one Agent;
2. explicit allowed states and session override rules;
3. contract validation plus at least one supported and one unsupported case;
4. an explicit declaration for every registered Agent;
5. API and UI presentation generated from those declarations.

## Implementation Order

1. Define types, the ten IDs, and contract validation without changing the UI.
2. Add declarations and reasons for all six existing readers.
3. Introduce shared conformance tests and make all six adapters pass contract-level checks.
4. Add fixture-backed behavior coverage incrementally rather than rewriting every old test at once.
5. Expose the Agent capability API.
6. Make settings and session views consume the API.
7. Remove any duplicate frontend or explanatory capability matrix.

Each step must be independently testable. Do not ship a promotional matrix before adapter declarations can support it.
