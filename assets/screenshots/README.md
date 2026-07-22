# README screenshot workflow

The five images in this directory are reproducible captures of the running app, not hand-made mockups.

## Choose a session

Use a completed session from this repository that shows representative terminal rendering and has useful analytics. Before capture, review its title and visible content for confidential project names, credentials, customer data, or other material that should not be published. Do not use an actively changing session.

The current set intentionally contains:

1. `replay.png` — the primary replay UI, tool calls, filters, and semantic minimap.
2. `interaction.png` — combined user/assistant message navigation over terminal replay.
3. `settings.png` — the settings center's UI and terminal font controls.
4. `analytics.png` — token/cache totals, tool usage, findings, and per-turn charts.
5. `code-reader.png` — the file tree, reader, search action, and document outline.

Together they cover replay, navigation, personalization, analysis, and code inspection without turning the project README into an exhaustive gallery.

## Capture

Start the full application first, then install Playwright's Chromium build once and capture both locales with an exact local session title:

```bash
./run.sh all
npm --prefix frontend exec -- playwright install chromium
npm --prefix frontend run capture:screenshots -- --locale en --session-title "<exact session title>"
npm --prefix frontend run capture:screenshots -- --locale zh-CN --session-title "<exact session title>"
```

The script fixes the viewport and light theme, filters the sidebar to the chosen session, replaces the repository and home paths, replaces email addresses, and limits the code-reader tree to Git-tracked files. It writes five PNG files under the selected locale directory.

## Privacy check

The automated redaction is a guardrail, not a guarantee. Terminal rendering can include incremental or canvas-backed content, so visually inspect every generated image at full resolution before committing it. In particular, check tool arguments, terminal output, session titles, file paths, email addresses, API keys, tokens, and the edges of the cropped replay image.
