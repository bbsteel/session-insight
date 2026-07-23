# Agent Adapter Onboarding Guide

This directory is the development entry point for adding a new Agent to SessionInsight. It is intended for maintainers, external contributors, and Claude/Codex sessions performing adapter work. It is not end-user product documentation.

Use the authoritative source for each concern:

- For implementation details, use the current Go interfaces and adapter implementation.
- For research, validation, and delivery workflow, use this guide.
- For user-facing capability declarations and conformance, use the shared runtime contract and conformance suite after they land in code.

Architecture proposals and unpublished implementation designs belong in the private SessionInsight documentation repository, not in this code repository. This guide records only the durable instructions that a future coding Agent needs while changing an adapter.

Do not add a `session-insight adapter init` command before capability declarations and conformance tests exist. Scaffolding can generate files, but it cannot replace format research, representative fixtures, or behavioral evidence. Consider an internal generator only after several new adapters have established a stable directory structure.

## Core Principles

- **Agent is the user-facing term.** The UI uses "Agent capabilities." Use `reader`, `adapter`, and `source` only for internal implementation or storage provenance.
- **The contract is the source of truth.** Settings, session views, and APIs derive their claims from adapter capability declarations. Do not maintain a second hand-written matrix.
- **Declarations require evidence.** Every `exact` or `estimated` declaration must be supported by an implementation, sanitized fixtures, and conformance tests.
- **Agent capabilities and session status are separate.** An Agent may normally record tokens while an interrupted session lacks them. That session is `missing`; it is not `0`, and the Agent does not become `unsupported`.
- **Declare conservatively.** Research in progress, partial implementations, and untested behavior cannot be presented to users as supported.
- **Do not copy one existing adapter as the specification.** Check every requirement in this guide so Agent-specific assumptions are not inherited accidentally.

## Adapter Layout

Keep each Agent implementation under:

```text
internal/reader/<agent>/
├── <agent>.go
├── <agent>_test.go
├── <agent>_delete.go          # when applicable
├── <agent>_delete_test.go
├── <agent>_render.go          # split as implementation size requires
└── testdata/
    ├── basic/
    ├── tools/
    ├── interrupted/
    └── ...
```

Go tests conventionally use `testdata/`. Only commit sanitized, size-bounded fixtures with known provenance. Existing adapters that build inline temporary fixtures may migrate incrementally when they are changed; do not rewrite every old test at once.

## Onboarding Workflow

### 1. Research the Agent's persisted facts

Answer and record:

- Where are sessions stored, and can SI discover the path automatically?
- Does the Agent use JSONL, JSON, SQLite, or a multi-file layout?
- Is the session ID stable, and which ID does the native resume command use?
- How do append writes, rewrites, and transactional commits occur?
- What observable differences distinguish a normal finish from an interrupted session?
- Are tokens, tool results, diffs, and subtasks represented by structured fields?
- Can a live session be tied exactly to a heartbeat, lock, registry entry, or PID?
- Which Agent-owned records must be removed when deleting a session?
- Do Windows, macOS, and Linux use different paths or formats?
- Has an Agent upgrade changed the persisted schema?
- Which secrets, identities, or private content may appear in fixtures?

Do not infer an `exact` fact from file timestamps, model names, or UI copy.

### 2. Establish the minimum replay path

Implement the behavior required by `reader.BaseSessionReader`:

- `AgentType` returns a stable, lowercase, non-localized identifier.
- `DisplayName` returns the product name.
- `ListSessions` and `GetSession` use the same ID for a session.
- Time, message, and turn ordering remain stable across repeated reads.
- `RenderANSI` and `GetRenderEvents` distinguish unsupported behavior, not-found sessions, and empty sessions instead of swallowing errors.

Then add automatic discovery in `internal/reader/registry.go`. Platform-specific paths must use Go path APIs rather than hard-coded separators.

### 3. Declare capabilities

After the runtime capability contract lands, declare all ten baseline capabilities:

- discovery
- replay
- realtime
- tokens
- tool_results
- diff
- subtasks
- resume
- delete
- terminate

Every declaration needs evidence. `unsupported` and `not_applicable` also require a reason. Never use `missing` as a static Agent declaration.

### 4. Prepare sanitized fixtures

Cover each applicable scenario:

- a minimal normal session;
- a multi-turn session;
- tool calls and tool results;
- file edits or diffs;
- subtasks and parent-child relationships;
- a normal finish;
- an interrupted or unfinished record;
- absent optional fields;
- versions before and after a format change;
- platform-specific differences.

Sanitization must replace usernames, home directories, repository remotes, secrets, identifying message content, and business data while preserving field shapes, event relationships, and boundary conditions. Convert a real sample into a minimal synthetic fixture when it cannot be committed safely.

### 5. Run shared conformance tests

After the shared suite lands, the adapter test calls `adaptertest.Run` and explicitly supplies the fixtures that cover its declarations. Until then, do not invent a parallel capability schema or copy a provisional test API into an adapter.

Minimum validation:

```bash
go test ./internal/reader/...
```

If a change affects APIs, indexing, live refresh, deletion, or termination, also run the affected package tests and the full repository test suite.

### 6. Register and inspect product behavior

The capability contract must drive:

- the Agent comparison in settings;
- the Agent capability entry point on a session;
- the current session's `exact / estimated / missing` status;
- whether resume, delete, and terminate actions are shown or enabled;
- distinct explanations for unsupported, missing, and empty results.

Do not add another frontend capability decision keyed by `agent_type`.

## Definition of Done

A new Agent adapter is complete only when every applicable item is satisfied:

- [ ] Storage locations, format versions, identity fields, and privacy boundaries are recorded.
- [ ] Discovery, listing, detail loading, and replay are stable.
- [ ] All ten baseline capabilities have an explicit declaration and reason where required.
- [ ] Every supported capability has at least one sanitized fixture.
- [ ] Shared contract checks pass.
- [ ] Behavior checks pass for every supported declaration.
- [ ] Interrupted sessions, absent fields, and empty sessions are not misreported.
- [ ] Applicable operating-system differences are covered; unverified platforms are explicit in declarations or the PR.
- [ ] The registry, API, and UI contain no duplicate hand-written capability matrix.
- [ ] `go test ./internal/reader/...` and all tests in the affected scope pass.
- [ ] The PR lists verified capabilities, known gaps, and fixture provenance.

## Recommended Prompt for a Coding Agent

```text
Add support for <AgentName> by following
developer/agent-adapters/README.md.

Research the local persistence format and cross-platform paths first. Then
implement the reader, capability declarations, and sanitized fixtures. Use the
shared conformance suite to prove every declaration. Conservatively mark
capabilities without evidence as unsupported, and use not_applicable only when
the concept does not exist for the Agent.

Do not maintain a separate capability matrix in frontend code or documentation.
Run all validation required by the guide, and list capability evidence,
unverified platforms, and known gaps in the PR.
```

Maintainers should still be able to ask only for "support this Agent." This directory expands that request into a stable, reviewable engineering workflow.
