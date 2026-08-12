# PR/MR creation evidence

SessionInsight treats a successful code-review creation action as a local,
adapter-recorded fact. This lets a pasted Pull Request or Merge Request URL
resolve back to the session that created it without contacting GitHub, GitLab,
or another code host.

## Evidence contract

A URL mention is not creation evidence. An exact creation fact requires all of
the following in the same authoritative session snapshot:

1. A supported structured tool invocation whose command starts with an
   allowlisted creator (`gh pr create` or `glab mr create`).
2. The paired tool result, linked by the recorded event/call identity.
3. A successful exit code, with no timeout, rejection, error, or truncation.
4. Exactly one provider-matching PR/MR URL in the result.
5. The source revision of the immutable transcript bytes from which both
   events were parsed.

The local index stores the normalized URL, session identity, command kind,
event identity, turn and invocation coordinates, timestamp, source revision,
and an `exact` assessment. Re-indexing a session atomically replaces the whole
set, including replacing it with an empty set after rollback or source rewrite.

The evidence proves only that the session successfully invoked a creation tool
and received that URL. It does not prove the current PR state, title, commit
set, file set, or mergeability. Those are hosted metadata and remain a separate,
optional read that follows the host approval policy.

## PR #124 recorded example

PR #124 was created by Codex session
`rollout-2026-08-11T10-31-33-019feea9-32f5-7810-b403-3a39a6c0cf7d`.
Its authoritative transcript records this closed evidence chain:

- Tool invocation event at `2026-08-11T16:17:16.781Z`:
  `gh pr create --base main --head feat/v0.6.0-git-association ...`
- Paired completed command at `2026-08-11T16:17:21.704Z`, exit code `0`.
- Standard output:
  `https://github.com/bbsteel/session-insight/pull/124`

After indexing, searching that URL uses only the local
`session_change_request_creation_evidence` index and returns the session above.
No DNS resolution, provider API call, credential lookup, or host approval is
needed for this reverse lookup.

## UI behavior

The first search is always local. Exact creation matches are shown immediately.
The user may separately choose **Load hosted details** to retrieve title,
lifecycle, commits, and diff data. That second action can ask for read-only host
approval; it never changes the meaning of the local creation fact.

## Deliberate non-evidence

The index rejects:

- PR/MR URLs in user or assistant prose;
- `echo gh pr create` and other command mentions;
- failed, rejected, timed-out, or truncated tool results;
- an unpaired URL output;
- output containing multiple distinct provider-matching PR/MR URLs; and
- a GitHub URL returned by a GitLab creation command, or vice versa.

Future provider or agent integrations must preserve this same closed-loop
contract. They may add structured creator kinds, but must not infer creation
from text proximity, branch names, or hosted metadata alone.
