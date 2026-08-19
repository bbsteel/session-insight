# Session Insight

[![CI](https://github.com/bbsteel/session-insight/actions/workflows/ci.yml/badge.svg)](https://github.com/bbsteel/session-insight/actions/workflows/ci.yml)

A local-first web app for browsing and analyzing AI coding agent sessions through terminal-native replay. It reconstructs ANSI-styled conversations, tool calls, and code output in an interactive terminal, while discovery, indexing, search, and replay stay on your machine. AI generation is opt-in and only uses a provider you configure.

Built-in observability connects Agent capabilities, record provenance, collaboration timelines, Token and tool analytics, and Git/Change Request evidence. Exact, estimated, missing, and unavailable states make the confidence of each conclusion visible instead of hiding uncertainty.

[Website](https://session-insight.dev/) · [中文](README_ZH.md)

![Session replay with terminal find, tool calls, and semantic minimap](assets/screenshots/en/replay.png)

<p align="center"><sub>Real development session shown with personal paths and contact details sanitized.</sub></p>

## Highlights

- **Multi-agent session library** — auto-discover and index sessions from seven coding agents, with live list refresh, accurate active-state detection, and live tail for running sessions
- **Agent capability transparency** — inspect each Agent's support for discovery, replay, realtime updates, tokens, tools, diffs, subtasks, resume, deletion, and termination, with session-level exact, estimated, or missing states
- **Session record completeness** — see whether SI fully read a session (complete, degraded, metadata-only, or source-missing), inspect provenance and source files, and open them in your editor
- **Sub-agent collaboration** — when an Agent records nested work, open a horizontal collaboration dock with a zoomable timeline and jump from each launch or result back into the terminal evidence (Claude Code, Grok, Codex, Chrys, and Copilot)
- **Terminal-native replay** — preserve ANSI output, formatted assistant text, tool calls, code, and errors; keep parallel tool results beside their invocations, fold noisy details, and follow active sessions as they grow
- **Fast session navigation** — start at the first prompt, keep the current user message visible while scrolling, search inside the terminal with first/last-match jumps, open clickable links, use the semantic minimap, or browse the combined user/assistant interaction panel
- **Search and organization** — search metadata, prompts, assistant replies, skills, tool inputs, and errors across sessions while background indexing reports progress; narrow results by project or agent, sort projects by name, session count, or recent activity, and keep bookmarks with notes
- **Git and Change Request evidence** — inspect repository-scoped local changes, retained patches, candidate commits, and replay edit anchors; recognize PR/MR/review-shaped links from any agent and host, distinguish link mentions from CLI creation evidence, and optionally link fixed GitHub PR or GitLab MR snapshots with explicit evidence quality and revocable read-only host approval
- **Tool, diff, and code inspection** — filter tool calls and jump to their source turn; inspect inline or side-by-side diffs; open referenced files in the structured code reader or your editor
- **Usage analytics** — inspect prompt, output, and cache tokens, cost estimates, tool usage, errors, anomalies, continuation pressure, and per-turn trends
- **Session lifecycle tools** — export Markdown or portable `.sibundle` packs for offline migration, resume sessions in place (or copy a shell command), and safely delete with running-process protection and supported force-stop flows
- **Optional AI assistance** — generate summaries, titles, and handoff prompts on historical or live sessions through a configured OpenAI-compatible API or local ACP agent
- **Desktop personalization** — use light or dark themes, recognizable agent icons, a custom user avatar, resizable panels, localized compact or exact Token totals, and independent UI/terminal font and size controls

## More Screenshots

| Interaction messages | Settings and fonts |
|:--:|:--:|
| ![Combined user and assistant message navigation](assets/screenshots/en/interaction.png) | ![Settings center with UI and terminal font controls](assets/screenshots/en/settings.png) |

| Tool-call observability |
|:--:|
| ![Filterable tool-call history with arguments, status, duration, and replay jumps](assets/screenshots/en/tool-calls.png) |

| Session analytics | Structured code reader |
|:--:|:--:|
| ![Token, cache, tool usage, and anomaly analytics](assets/screenshots/en/analytics.png) | ![File tree, code view, search, and document outline](assets/screenshots/en/code-reader.png) |

## Supported Agents

Session Insight auto-discovers sessions from the following agents:

| Agent | Session location (auto-detected) |
|-------|----------------------------------|
| [Claude Code](https://claude.ai/code) | `~/.claude/projects/` |
| [Codex](https://github.com/openai/codex) | `~/.codex/sessions/` |
| [GitHub Copilot](https://github.com/features/copilot) | `~/.copilot/session-state/` |
| [opencode](https://opencode.ai) | opencode SQLite database (auto-resolved) |
| [Chrys](https://github.com/chrislatinae/chrys) | `~/.chrys/sessions/` |
| [Grok](https://grok.com) | `~/.grok/sessions/` |
| [Hermes Agent](https://github.com/NousResearch/hermes-agent) | `~/.hermes/state.db` (or `HERMES_HOME`) |

## Download and Run

No Go or Node.js installation is required for the pre-built release.

1. Open the [latest GitHub Release](https://github.com/bbsteel/session-insight/releases/latest).
2. Download the archive for your platform:

   | Platform | Archive name |
   |----------|--------------|
   | Linux x86-64 | `session-insight-*-linux-amd64.tar.gz` |
   | Linux arm64 | `session-insight-*-linux-arm64.tar.gz` |
   | macOS Intel | `session-insight-*-darwin-amd64.tar.gz` |
   | macOS Apple Silicon | `session-insight-*-darwin-arm64.tar.gz` |
   | Windows x86-64 | `session-insight-*-windows-amd64.zip` |

3. Extract the archive and run `session-insight` (`session-insight.exe` on Windows).
4. Open **http://127.0.0.1:8080** if the browser does not open automatically.

Each archive includes the executable, both READMEs, and the license. To verify the download, get `checksums.txt` from the same Release and compare its matching entry with `sha256sum <archive>` on Linux/macOS or `Get-FileHash <archive> -Algorithm SHA256` in PowerShell.

## Build from Source

### Prerequisites

- Go 1.25+
- Node.js 18+

### Build and run (macOS / Linux)

```bash
git clone https://github.com/bbsteel/session-insight.git
cd session-insight
bash run.sh all
```

The app starts at **http://127.0.0.1:8080** and opens automatically in your browser.

Useful runtime commands:

```bash
./run.sh status       # list the current app and linked-worktree instances
./run.sh restart      # stop and start this checkout without rebuilding
./run.sh stop         # stop only this checkout's instance
./run.sh stopall      # stop all related-checkout instances and clean stale PID records
./run.sh converge     # fast-forward main, rebuild, and start one fresh primary instance
```

### Windows

See [BUILD.md](BUILD.md) for the full Windows build guide (requires MSYS2 + mingw-w64 for CGO).

### Configuration

| Environment variable | Default | Description |
|----------------------|---------|-------------|
| `PORT` | `8080` | HTTP port |
| `SI_DATA_DIR` | `~/.session-insight` | Override the application database directory |
| `CHRYS_SESSION_ROOT_DIR` | — | Override Chrys session root directory |
| `HERMES_HOME` | `~/.hermes` | Override Hermes state directory; Session Insight reads `state.db` inside it |

When `run.sh` is executed from a linked Git worktree, it uses an OS-assigned
random loopback port on the first run and reuses the same port on subsequent
restarts (persisted to `.runtime/session-insight.port`), with an isolated
`.runtime/session-insight` data directory. `PORT=8080` is ignored in a worktree
because that port belongs to the primary checkout. The `Ready:` line reports the
actual bound URL, not the requested port.

### Contributing localized UI

Every new or changed user-facing string must be added to both `en` and `zh-CN` in `frontend/src/i18n.tsx` and rendered through `t(...)`. Keep raw session text, tool output, and file content unchanged. Before opening a PR, run:

```bash
npm --prefix frontend run test:i18n
npm --prefix frontend run test:i18n-source
```

Rendered UI changes must also be checked in both languages against the full app. Update the source ratchet only when removing or intentionally migrating legacy literals, and review the baseline diff.

## Privacy

Core browsing features operate locally. AI features remain disabled until you configure a model provider and explicitly request a generation. A generation sends a bounded excerpt of the selected session to the configured OpenAI-compatible endpoint or ACP agent; an ACP agent may in turn contact its own model provider.

API credentials are stored locally in the Session Insight SQLite database and are not returned to the browser after saving. Treat that local database as sensitive data.

## License

[MIT](LICENSE) © 2026 bbsteel
