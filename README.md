# Session Insight

A local web app for browsing and replaying AI coding agent sessions. Runs entirely on your machine — no data leaves your device.

## Features

- **Terminal replay** — re-watch any session as an ANSI terminal with tool-call folding, syntax-highlighted code blocks, and live tail for active sessions
- **Full-text search** — search across all sessions with regex support and per-turn highlighting
- **Diff viewer** — side-by-side or inline file diffs with syntax highlighting and soft-wrap
- **Bookmarks** — save sessions with notes; filter by agent or model
- **Analytics** — token usage, cost breakdown, and anomaly detection per session
- **File viewer** — open any file path mentioned in a session, with syntax highlighting and tree navigation
- **Dark / light theme**

## Supported Agents

Session Insight auto-discovers sessions from the following agents (more coming):

| Agent | Session location (auto-detected) |
|-------|----------------------------------|
| [Claude Code](https://claude.ai/code) | `~/.claude/projects/` |
| [Codex](https://github.com/openai/codex) | `~/.codex/sessions/` |
| [GitHub Copilot](https://github.com/features/copilot) | `~/.copilot/session-state/` |
| [opencode](https://opencode.ai) | opencode SQLite database (auto-resolved) |
| [Chrys](https://github.com/chrislatinae/chrys) | `~/.chrys/sessions/` |

## Getting Started

### Prerequisites

- Go 1.25+
- Node.js 18+

### Build and run (macOS / Linux)

```bash
git clone https://github.com/bbsteel/session-insight.git
cd session-insight
bash start.sh all
```

The app starts at **http://127.0.0.1:8080** and opens automatically in your browser.

### Windows

See [BUILD.md](BUILD.md) for the full Windows build guide (requires MSYS2 + mingw-w64 for CGO).

### Configuration

| Environment variable | Default | Description |
|----------------------|---------|-------------|
| `PORT` | `8080` | HTTP port |
| `CHRYS_SESSION_ROOT_DIR` | — | Override Chrys session root directory |

## Pre-compiled Binaries

Pre-compiled binaries for macOS, Linux, and Windows are planned. Watch the releases page.

## License

[MIT](LICENSE) © 2026 bbsteel
