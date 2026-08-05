# Hermes adapter fixtures

These SQL files are fully synthetic, bounded fixtures for the Hermes adapter's
shared conformance suite. They contain no real home-directory paths, session
IDs, prompts, provider credentials, or user data.

## Provenance and storage facts

- The fixture shape is reduced from Hermes' canonical `~/.hermes/state.db`
  SQLite store and its `sessions`, `messages`, and `session_model_usage`
  tables.
- The current Hermes schema is documented as schema version 23. The adapter
  treats newer columns as optional and keeps a legacy fixture with the older
  core columns to exercise migration compatibility.
- `messages.id` is the append order. `messages.tool_calls` and
  `messages.tool_call_id` are the native invocation/result association fields.
- `sessions.id` is the native `hermes --resume <id>` identity.
- `parent_session_id` plus the delegated child model-config marker are used for
  the conservative partial subtask estimate. The adapter does not claim a
  complete invocation graph.
- `ended_at` being null and a fresh state-store mtime represent the bounded
  interrupted/in-progress signal. Hermes does not persist an exact
  session-to-process PID mapping, so terminate remains unsupported.
- `HERMES_HOME` is the supported storage override. Native Windows path
  discovery follows `LOCALAPPDATA\\hermes` when `HERMES_HOME` is absent; this
  path has not been verified on a live Windows Hermes installation in this
  change.

Reference material:

- [Hermes session storage](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/session-storage.md)
- [Hermes sessions and resume](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/sessions.md)
- [Hermes delegation](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/delegation.md)
- [Hermes tools](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/tools.md)

## Scenarios

- `basic.sql`: normal completed session for discovery/replay conformance.
- `rich.sql`: exact session token aggregates, successful and failed tool
  results, the native `patch` edit form, and a delegated child session.
- `interrupted.sql`: nullable `ended_at` and an assistant row without a
  `finish_reason`.
- `legacy.sql`: pre-expansion core schema without newer optional columns.
- The tests also create a zero-row database and an isolated delete sandbox.
