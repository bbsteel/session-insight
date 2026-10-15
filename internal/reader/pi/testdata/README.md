# Pi adapter fixtures

These JSONL files are fully synthetic, bounded fixtures for the Pi adapter's
shared conformance suite. They contain no real home-directory paths, session
IDs, prompts, provider credentials, or user data.

## Provenance and storage facts

- Pi stores one JSONL file per session under `~/.pi/agent/sessions/<encoded-cwd>/`.
- The default encoded-cwd directory replaces `/`, `\`, and `:` with `-` and
  wraps the path as `--<path>--`.
- Filename shape: `<timestamp>_<session-id>.jsonl`. The session header `id`
  is the native `pi --session <id>` identity.
- Session version 3 is current. Versions 1 and 2 are auto-migrated by Pi on
  load; this adapter reads the on-disk JSONL as written.
- Entries form a tree via `id` / `parentId`. Replay uses append order so
  every recorded branch remains visible.
- Assistant `usage.input` is exclusive of `cacheRead` / `cacheWrite`.
  `usage.reasoning` is a subset of `output` and is never added to it.
- Native tools observed in local sessions: `read`, `bash`, `edit`, `write`.
  `edit` arguments use `path` plus `edits[{oldText,newText}]`.
- Pi does not persist sub-agents. `/fork` and `/clone` write a new session
  file; `parentSession` is a fork pointer, not a nested invocation graph.
- Overrides: `PI_CODING_AGENT_SESSION_DIR`, then `PI_CODING_AGENT_DIR/sessions`.
  Native Windows discovery uses `os.UserHomeDir()` + `.pi/agent/sessions` and
  has not been verified on a live Windows Pi installation in this change.
- Terminate uses file-descriptor holders of the open JSONL (Pi keeps the
  file open for append). Linux is the verified platform for that path.

Reference material:

- [Pi session format](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md)
- [Pi sessions](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sessions.md)
- [Pi CLI session options](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/cli.md)

## Scenarios

- `basic.jsonl`: normal completed session for discovery/replay conformance.
- `rich.jsonl`: exact tokens, successful bash, failed bash, and a native edit.
- `interrupted.jsonl`: trailing user message with no assistant close.
- `header-only.jsonl`: session header without replayable turns (metadata_only).
- Tests also create isolated delete/realtime sandboxes in TempDir.
