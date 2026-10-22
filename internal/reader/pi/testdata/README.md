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
- Terminate is unsupported: Pi closes the transcript descriptor after every
  write (`appendFileSync`) and keeps no heartbeat or lock registry, so an
  idle-but-alive Pi process exposes no per-session PID that could safely be
  killed.
- Delete safety (`SessionRunning`) is process-based, not time-based. Pi scopes
  sessions to a working directory, encoding it as
  `--<abs cwd with / \ : replaced by ->--`, so a live `pi` process whose cwd
  encodes to a session's directory is treated as its owner. The transcript
  mtime is deliberately ignored: Pi keeps a session open and idle at its
  prompt without writing, so any freshness window would eventually declare a
  living session stopped, and deleting it would lose history (Pi's next append
  recreates a headerless transcript).
- Lookup keys files by the `_<session-id>.jsonl` filename suffix under the
  sessions root and its encoded-cwd directories. Duplicate ids (copy, crash
  leftover, in-progress rewrite) always resolve to the newest file by mtime,
  and delete removes every such file so no duplicate can rebind the id on the
  next scan. Listing uses a metadata-only scan, cached per file by size+mtime;
  its turn count follows the same `shared.FilterEmptyTurns` rules as the replay
  parser. A truncated final line without a newline is an in-progress tail, not
  a malformed record.
- Direct `!`/`!!` shell commands are persisted as `message.role="bashExecution"`
  and replay as a `bash` invocation plus result, preserving command, output,
  exit code, and cancellation.
- A standalone `usage` entry bills the model recorded on that entry
  (`UsageEntry.model`), not the session's selected model.

Reference material:

- [Pi session format](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md)
- [Pi sessions](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sessions.md)
- [Pi CLI session options](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/cli.md)

## Scenarios

- `basic.jsonl`: normal completed session for discovery/replay conformance.
- `rich.jsonl`: exact tokens, successful bash, failed bash, and a native edit.
- `interrupted.jsonl`: trailing user message with no assistant close.
- `header-only.jsonl`: session header without replayable turns (metadata_only).
- `failed-turns.jsonl`: model_change, a system message, then two user turns
  whose assistant replies are empty `stopReason: error` records. Pins the
  metadata scan to the detail view's turn count for Pi's real failure shape.
- `shell-and-usage.jsonl`: three native `bashExecution` messages (success,
  non-zero exit, cancelled) plus a standalone `usage` entry that bills a
  different model than the selected one. Covers direct-shell replay and
  usage-entry attribution.
- Tests also create isolated delete/realtime sandboxes in TempDir.
