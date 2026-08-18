# README screenshot workflow

The six images for each locale are reproducible captures of the running app, not hand-made mockups.

## Choose a session

Use a completed session from this repository that shows representative terminal rendering and has useful analytics. Before capture, review its title and visible content for confidential project names, credentials, customer data, or other material that should not be published. Do not use an actively changing session.

The current set intentionally contains:

1. `replay.png` — the primary replay UI, terminal find, tool calls, filters, and semantic minimap.
2. `interaction.png` — combined user/assistant message navigation over terminal replay.
3. `tool-calls.png` — filterable tool-call history with arguments, status, duration, and replay jumps.
4. `settings.png` — the settings center's UI and terminal font controls.
5. `analytics.png` — token/cache totals, tool usage, findings, and per-turn charts.
6. `code-reader.png` — the file tree, reader, search action, and document outline.

Together they cover replay, navigation, personalization, analysis, and code inspection without turning the project README into an exhaustive gallery.

## Capture

Start the full application first, then install Playwright's Chromium build once and capture both locales with an exact local session title:

```bash
./run.sh all
npm --prefix frontend exec -- playwright install chromium
npm --prefix frontend run capture:screenshots -- --locale en --theme light --session-title "<exact session title>" --terminal-query "<representative query>"
npm --prefix frontend run capture:screenshots -- --locale zh-CN --theme light --session-title "<exact session title>" --terminal-query "<representative query>"
```

The script fixes the viewport and selected theme, filters the sidebar to the chosen session, opens terminal find when a query is supplied, replaces the repository and home paths, replaces email addresses, and limits the code-reader tree to Git-tracked files. It writes six PNG files under the selected locale directory or `--output-dir`.

For dark-theme product pages or marketing captures, use `--theme dark` and a separate output directory so the README set remains unchanged:

```bash
npm --prefix frontend run capture:screenshots -- \
  --locale en \
  --theme dark \
  --session-title "<exact English session title>" \
  --output-dir site/assets/screenshots/en
```

## Privacy check

The automated redaction is a guardrail, not a guarantee. Terminal rendering can include incremental or canvas-backed content, so visually inspect every generated image at full resolution before committing it. In particular, check tool arguments, terminal output, session titles, file paths, email addresses, API keys, tokens, and the edges of the cropped replay image.
