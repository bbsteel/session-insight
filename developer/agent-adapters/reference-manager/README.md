# Terminal Reference Manager

Development-time tool for collecting and managing native-Agent terminal
reference screenshots, per the Agent terminal reference input design. It is
**not** part of the SessionInsight product: nothing here ships in the product
UI, the public API or release binaries, and the tool never starts a real Agent
CLI, injects keystrokes or creates sessions.

## Run

```bash
./scripts/terminal-reference          # all onboarded Agents
./scripts/terminal-reference grok     # one Agent
./scripts/terminal-reference verify-work-order --work-order /abs/path/WORK_ORDER.md
```

The tool binds `127.0.0.1` on a random OS-assigned port and prints a
`Ready: http://127.0.0.1:<port>` line. Only that post-bind address is valid.

## What it does

- Shows the fixed 22-item screenshot checklist (plus fold `-toggled` pairs)
  with per-item allowed-gap rules. Missing images never block an adapter.
- Discovers candidate sessions from normalized `RenderEvent`s (exact
  structured facts only; softer signals are listed separately as
  low-confidence suggestions) and prints the native resume command to copy.
- Accepts drag-and-drop full-screen screenshots (PNG/JPEG/GIF), generates the
  canonical logical file names (including `-toggled` suffixes), computes
  content hashes, pairs fold states and rejects one image masquerading as two
  states.
- Also imports canonically named images dropped directly into the Agent's
  store directory on scan — no `prepare`/`--capture` command exists.
- Tracks per-logical-file evidence states: `missing`, `found`, `captured`,
  `used`, `update_available`, `not_applicable` (the last only via an explicit
  researched reason). `used` is derived from the local `origin/main` evidence
  lock, never from a local accept button. Agent version changes are context,
  never bulk invalidation.
- Freezes pending inputs into a schema v2 work order under
  `.runtime/reference-work/<agent>-<id>/`. The markdown records full SHA-256
  hashes, the main baseline commit, the main lock summary, and a copy-paste
  `verify-work-order` command. A work order is `stale` when an input changes
  after freezing; old schema work orders must be regenerated. An active work
  order that already freezes the current pending hashes cannot be duplicated
  — regenerate only after an input changes. The UI can open that frozen
  directory in the desktop file manager.

The manager's boundary ends at the work order: it does not create goals,
branches or PRs, and does not edit product code. Local accept is disabled.

## Storage and privacy

Everything lives outside the Git repository:

```text
~/.session-insight-dev/terminal-references/
└── <agent>/
    ├── catalog.json          # local-only catalog (never commit)
    ├── blobs/<sha256><ext>   # content-addressed screenshots, old kept
    └── <logical-name>.png    # optional manual drop-in, imported on scan
```

Override the root with `SI_TERMINAL_REFERENCE_ROOT`. Sandboxed agents must
grant write access to `~/.session-insight-dev` (Grok profile `session-insight`
in `.grok/sandbox.toml`, Codex `writable_roots` in `.codex/config.toml`).
Create that directory once if the sandbox requires it to exist:

```bash
mkdir -p ~/.session-insight-dev
```

If the default home store is still not writable, the tool falls back to
`<checkout>/.runtime/terminal-references` and logs the path.
Original screenshots,
session IDs, resume commands, local paths and the catalog must never enter
Git, PRs, issues or public logs. Work orders are written to the Git-ignored
`.runtime/reference-work/` directory of the current checkout.

## Security posture

Loopback-only listener on a random port; image blobs are served by content
hash only when the catalog knows them (no static directory exposure); uploads
are size-capped and must decode as PNG/JPEG/GIF; candidate scanning is
read-only against Agent session storage; `POST /api/work-orders/open` launches
the OS folder opener only for a catalogued work-order id resolved under
`.runtime/reference-work/` (the request never supplies a filesystem path).
