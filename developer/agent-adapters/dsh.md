# DeepSeek Harness storage research

Validated against the locally installed `@deepseek-ai/dsh` 0.2.1-rc.2 and
synthetic fixtures shaped from its persisted session records. Upstream:
https://github.com/deepseek-ai/deepseek-harness.

## Persistence and identity

The `dsh-home-paths` package resolves `$DSH_HOME`, otherwise `~/.dsh`, using
the platform home directory. Sessions live under
`sessions/<encoded-cwd>/<encoded-session-id>/session.vN.jsonl.zstd`;
plaintext `session.vN.jsonl` is an alternative configured encoding.
The adapter reads generations 3 and 4, selects the highest canonical generation,
and refuses future generations rather than silently replaying an older backup.
The immutable header contains `id`, `version`, `createdAt` (Unix milliseconds),
optional `cwd` and `parentSession`, `isSeeded`, and `delegationDepth`.
The directory identity must agree with the header; standard native IDs are
`session-<uuid>`. Custom path-encoded IDs and pre-generation flat layouts have
not been verified and are not silently interpreted as current storage.

The persistence package appends independent checksummed Zstandard frames.
Migrations publish a new generation and preserve the historical generation;
compaction is itself recorded as events. Replay preserves historical events,
including material later pruned from the model context, rather than projecting
only the current model input. Complete newline-terminated records are required.
Torn tails keep the readable prefix and produce completeness warnings. Normal
turns have `turn/end`; absent endings never acquire an invented duration or
successful finalization. `session/end-seed` marks inherited-prefix boundaries,
not a completed run. The adapter does not declare authoritative finalization.

## Event mapping

`user/message`, `assistant/message`, `tool/call`, and `tool/result` are the
transcript facts. `agent/inbox/spliced` and assistant stream chunks are not
replayed again, avoiding duplicate messages. Every model-visible message is
written as `user/message`; the adapter classifies it by the producer
`source.kind`. Only `kind: "user"` is a human prompt. Subagent interaction
(`agent-message`, `subagent-settled`, `subagent-report`, `team-message`,
`coordinator`) renders as an agent message, while harness injections
(`time-context`, `runtime-context`, `agent-instructions`, `skill-catalog`,
`tool-jobs`, `goal`, `tool-goal`, `repeat-tool-reminder`, `compact-checkpoint`,
`model-selection`, `user-approval`, plugin notices, ...) render as system
messages. Neither counts as a user prompt, contributes to a turn's user
message, or appears in the user-message navigation list. Turn numbers and event
sequences provide stable ordering and pairing. Tool arguments are JSON strings;
`callId` joins results to invocations. Generation 3 wraps results in a user
message containing `tool-result` blocks; generation 4 uses a tool-role message
with `toolCallId`, ordinary content blocks, and `isError`.

`dsh-llm`'s TokenUsage contract explicitly defines disjoint uncached input,
cache read, cache write, and output buckets. Reasoning is an output subset.
Absent optional counters remain missing, not zero or not-applicable. One
assistant response is one provider request, so the adapter also records its
full input side (`input + cache read + cache write`) as the session's peak
context, and takes the context window from `request/context.contextWindow`
instead of guessing from the model name. Per-response usage is grouped by the
recorded model; auxiliary title/search model calls are not guessed into the
session bill. Sessions lacking assistant usage keep missing billing precision,
as do snapshots with skipped or truncated token-bearing records; retained
counters remain partial totals. Native `edit` arguments already use the shared
edit contract. Bash nonzero command exits are represented by the final
`[exit code: N]` line, even when `isError` is false; `isError` instead indicates
delivery/infrastructure errors.
Live refresh watches the sessions root and detects primary-file size/mtime
changes. Indexing independently captures and hashes raw source bytes through
the authoritative snapshot contract. It revalidates those bytes even when event
timestamps, file size, or mtime stay unchanged, so malformed tails and generation
replacements update persisted provenance. Event timestamps remain presentation
metadata rather than source revision identifiers. It is not a declaration of
exact process liveness.

## Sources, privacy, and operation boundaries

The selected generation is `primary_transcript`; preserved generations are
`snapshot` sources. The persistent `session.lock` is a kernel lease rendezvous,
not an active-PID record: POSIX uses flock and Windows uses a named mutex.
Its existence alone cannot prove that a session is running. No exact process
association or destructive operation is implemented by this adapter.

Global `storages/session_projcache`, workspace storage, profiles, settings,
credentials, environment files, and attachments are excluded from individual
source inventories. No real transcript content is committed: `testdata/` is
synthetic, with fictional IDs, paths, model routes, messages, and tool output.
Tests generate both plain and compressed versions, including concatenated and
torn frames, rather than requiring an external `zstd` executable.

The shipped web profile has no verified interactive terminal-resume command;
the headless `--session-id` option is a one-task runner, not the interactive
terminal workflow SI launches. Resume remains unavailable in SI. Parent IDs are
preserved, but a complete nested invocation graph is not implemented. Deletion
would also need to reconcile project-cache records and lease ownership; simply
unlinking a transcript is insufficient.

Linux real-session discovery/replay is the live validation target. Portable
fixture tests cover both encodings and versions. Native macOS/Windows paths,
locking, legacy generations, and custom session-root plugin configuration are
not verified; the adapter never guesses platform process or deletion behavior.
