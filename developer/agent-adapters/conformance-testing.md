# Shared Agent Conformance Testing Design

## Goal

The shared conformance suite proves that:

1. the capability contract is structurally complete;
2. declarations agree with Go interface implementations;
3. adapter behavior on sanitized fixtures agrees with its declarations;
4. a new Agent cannot miss common boundaries by copying an existing adapter's tests.

The suite does not replace format-specific parser tests. It establishes a shared minimum acceptance standard above every adapter.

## Layered Design

### Layer 1: contract checks

This layer reads no real user directories. It checks only the reader and capability declaration:

- the Agent ID, display name, and adapter revision are valid;
- all ten baseline capabilities are present;
- states and reason codes are valid;
- static declarations do not contain `missing`;
- operation capabilities agree with optional Go interfaces;
- unsafe combinations such as `terminate = estimated` are rejected.

This layer should be immediately applicable to all six existing adapters.

### Layer 2: common behavior checks

Run shared assertions against caller-provided fixtures:

- repeated `ListSessions` calls are stable and deterministically ordered;
- list and detail results agree on Agent type, session ID, and timestamps;
- session IDs are non-empty and unique;
- `GetSession` returns a clear error for an unknown ID;
- turn and event ordering is stable;
- timestamps are within a reasonable range;
- valid zero values do not become missing;
- rereading the same fixture does not duplicate events;
- rendering does not panic, and empty records remain distinguishable from unsupported rendering.

### Layer 3: capability behavior checks

This layer must run in two ordered phases. Missing fixtures are never a reason to skip a supported declaration:

1. **Fixture coverage gate:** enumerate every capability declared `exact` or `estimated`, verify that its required fixture exists and contains matching capability evidence, and fail immediately on any gap.
2. **Behavior validation:** after the coverage gate passes, run the relevant behavior assertions against the confirmed fixtures.

Behavior assertions include:

- `realtime`: the revision changes monotonically when the record changes and remains stable otherwise;
- `tokens`: exact fields preserve presence, and interrupted sessions become missing as expected;
- `tool_results`: calls remain associated with results, including failure and rejection states;
- `diff`: paths, old content, new content, and replace-all semantics are preserved;
- `subtasks`: parent and child IDs are stable, and child sessions are not duplicated as roots;
- `resume`: the resume ID is stable and is not confused with a filename;
- `delete`: only the target and explicitly associated records inside the fixture sandbox are removed;
- `terminate`: target resolution uses an injected fake process finder; CI never terminates a real Agent.

Test platform path logic for `discovery` with a temporary home directory and injected environment. Never rely on the CI host having a particular Agent installed.

## Proposed Package Layout

```text
internal/reader/adaptertest/
├── contract.go
├── behavior.go
├── capabilities.go
├── fixtures.go
└── report.go
```

`adaptertest` may import `internal/model` and the leaf capability package, but it must not import the parent `internal/reader` package. Today, `reader/registry.go` imports every concrete adapter. If an adapter test imported `reader` through `adaptertest`, the test build would form an import cycle.

The shared package declares the smallest structural interface required by its tests. Go's implicit interface implementation allows every `BaseSessionReader` implementation to satisfy it without an adapter:

```go
type Reader interface {
    AgentType() string
    DisplayName() string
    ListSessions() ([]model.Session, error)
    GetSession(id string) (*model.SessionDetail, error)
    RenderANSI(id string, cols int) (string, error)
    GetRenderEvents(id string) ([]model.RenderEvent, error)
}
```

Declare local optional interfaces with matching method signatures for deletion, process discovery, revisions, and other optional capabilities. Production readers never import `adaptertest`. Each adapter's `_test.go` file calls the shared suite.

Proposed API:

```go
type FixtureSet struct {
    Basic       Fixture
    Tools       *Fixture
    Diff        *Fixture
    Subtasks    *Fixture
    Interrupted *Fixture
    Realtime    *MutableFixture
}

type Expectations struct {
    SessionCount int
    SessionIDs   []string
}

func Run(
    t *testing.T,
    newReader func(t *testing.T, fixture Fixture) Reader,
    fixtures FixtureSet,
    expected Expectations,
)
```

Keep each adapter entry point short:

```go
func TestClaudeConformance(t *testing.T) {
    adaptertest.Run(t, newFixtureReader, adaptertest.FixtureSet{
        Basic:       fixture("testdata/basic"),
        Tools:       fixturePtr("testdata/tools"),
        Diff:        fixturePtr("testdata/diff"),
        Subtasks:    fixturePtr("testdata/subtasks"),
        Interrupted: fixturePtr("testdata/interrupted"),
    }, adaptertest.Expectations{
        SessionCount: 1,
        SessionIDs:   []string{"sanitized-session-id"},
    })
}
```

The implementation may adjust the API for differences between SQLite-backed and directory-backed readers, but it must preserve these properties:

- the adapter test provides the factory;
- the shared package never reads the developer's real home directory;
- an absent fixture cannot become an implicit skip;
- a supported capability without a fixture fails explicitly.

## Fixture Inventory and Metadata

Each fixture includes machine-readable metadata such as `fixture.json`:

```json
{
  "agent_type": "claude",
  "agent_format_version": "observed-2026-07",
  "scenario": "interrupted",
  "synthetic": false,
  "sanitized": true,
  "platforms": ["linux", "darwin"],
  "expected_capabilities": ["replay", "tokens", "tool_results"]
}
```

Metadata does not duplicate the full capability contract. It identifies the behaviors that a sample can verify. Tests enforce:

- `sanitized` is `true`;
- `agent_type` matches the adapter;
- before any capability behavior assertion runs, every supported capability is covered by at least one applicable fixture or the test fails immediately;
- platform-limited evidence is never presented as all-platform verification.

Never commit real usernames, absolute home paths, repository remotes, tokens, device identifiers, or unreviewed conversation content.

## Capability-to-Evidence Mapping

The shared report generates an evidence mapping instead of a manually maintained completion percentage:

| Capability | Declaration | Interface evidence | Fixture evidence | Result |
|---|---|---|---|---|
| replay | exact | BaseSessionReader | basic | pass |
| realtime | estimated | LiveRevisionProvider | realtime | pass |
| tokens | exact | SessionDetail.Billing | interrupted, basic | pass |
| diff | exact | RenderEvent/EditCall | diff | pass |
| terminate | unsupported | — | — | pass |

Result rules:

- supported declaration with missing interface evidence: fail;
- supported declaration with missing fixture evidence: fail;
- unsupported declaration that exposes a high-risk operation interface: fail or require an explicit exception;
- not-applicable declaration: no behavior fixture required, but a reason code is mandatory;
- session-level `missing`: a fixture must prove the override rule instead of weakening the static declaration.

## CI Integration

Do not add a standalone CLI in the first phase. Attach shared tests to the existing Go suite:

```bash
go test ./internal/reader/...
```

The full repository CI continues to run:

```bash
go test ./...
```

If a standalone report becomes useful later, extract a read-only tool from the testing library:

```bash
go run ./internal/tools/adapter-report
```

The reporting tool must read the same capability declarations and test-evidence index. It cannot introduce another configuration file. Consider a scaffolding command only when external contributor volume or onboarding frequency demonstrates a need.

## Incremental Migration of the Six Existing Adapters

Avoid rewriting every reader test at once:

1. Make all six adapters pass contract checks.
2. Reuse existing test data for basic behavior checks.
3. Organize sanitized fixtures capability by capability.
4. Add a minimal regression fixture whenever a real parser defect is fixed.
5. Enable the strict "no evidence, no support" gate after every supported capability has fixture coverage.

During migration, an exception must be explicit in code with a reason and an expiration condition. Do not use an unexplained `t.Skip`.

## Non-Goals

The shared conformance suite does not:

- judge an Agent's commercial quality;
- compare how many features Agents provide;
- use a developer's real sessions as CI input;
- estimate engineering effort from passing test counts;
- delete or terminate real Agent sessions in CI;
- replace parser unit tests for format-specific details.

It verifies only that SI's capability claims have repeatable engineering evidence.

## Implementation Order

1. Implement pure contract checks and their self-tests.
2. Add declarations for all six existing Agents and make them pass.
3. Implement the basic behavior suite.
4. Connect the smallest existing samples.
5. Implement capability behavior suites.
6. Fill fixture gaps and enable strict gates capability by capability.
7. Emit the evidence mapping in the PR template or CI summary.

Every phase must keep `go test ./internal/reader/...` independently verifiable and must not depend on a locally installed Agent.
