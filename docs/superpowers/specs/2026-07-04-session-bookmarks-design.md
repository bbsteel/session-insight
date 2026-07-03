# Session Bookmarks Design

## Context

SessionInsight needs a session bookmark feature that persists across browser sessions, browser instances, and app restarts. Bookmarks are user state for this local developer tool, not metadata owned by upstream agent session stores.

The app already persists local server data under `~/.session-insight/`, with SQLite at `~/.session-insight/index.db`. Bookmark state should use that existing backend persistence path instead of browser storage.

Project UI scope is desktop-only for this feature. New mobile-specific behavior is out of scope.

## Goals

- Allow users to bookmark and unbookmark sessions from the session detail toolbar.
- Persist bookmarks server-side so every browser using the same local service sees the same state.
- Show bookmark state in the session list without changing the list's existing sort order.
- Provide a central bookmark management modal for opening and removing bookmarked sessions.
- Keep bookmark state independent from agent reader data so temporary reader failures do not delete user bookmarks.

## Non-Goals

- No mobile optimization.
- No bookmark notes, folders, tags, drag sorting, or search within bookmarks.
- No syncing across machines.
- No automatic cleanup of bookmarks whose source sessions are temporarily unavailable.
- No row-level star button that can conflict with selecting a session.

## API

Use bookmarks as a small backend resource collection:

- `GET /api/bookmarks`
  - Returns bookmarked sessions with available session metadata.
  - Skips unavailable source sessions in the response, but does not delete stored bookmark records.
- `PUT /api/sessions/{id}/bookmark?agent=<agent_type>`
  - Adds a bookmark for `(agent_type, session_id)`.
  - Is idempotent.
- `DELETE /api/sessions/{id}/bookmark?agent=<agent_type>`
  - Removes a bookmark for `(agent_type, session_id)`.
  - Is idempotent.

`GET /api/sessions` and `GET /api/sessions/{id}` include `bookmarked: boolean` so the UI can render state without extra per-row requests.

The `agent` query parameter is required for write endpoints because session IDs may collide across readers.

## Persistence

Add a SQLite table to the existing app database:

```sql
CREATE TABLE IF NOT EXISTS bookmarked_sessions (
    agent_type TEXT NOT NULL,
    session_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (agent_type, session_id)
);
```

The database layer exposes methods for adding, removing, checking, and listing bookmarks. Listing returns bookmark records ordered by `created_at DESC`; handlers join those records with reader-provided session metadata.

## Backend Behavior

Session list handling loads bookmarks once into a set and annotates each `SessionSummary`.

Session detail handling annotates the returned detail with its current bookmark status.

`GET /api/bookmarks` walks stored bookmark records and resolves each one against the matching reader. If a reader cannot find a session, the bookmark record remains stored and the item is omitted from that response.

Write handlers validate that `agent` and `id` are non-empty. They do not need to verify the session currently exists before writing the bookmark, because reader availability can be transient and bookmarks are user-owned state.

## Frontend Behavior

The session detail toolbar gets a bookmark button near the existing actions such as analytics and export:

- Empty star: add bookmark.
- Filled star: remove bookmark.
- The button acts on the currently open session only.
- Failed writes surface an error and leave local state unchanged.

The session list displays bookmark state with a passive indicator, such as a small filled star beside the session name. It does not expose the main toggle action in the row, and it does not move bookmarked sessions to the top. Existing filters and sort behavior remain unchanged.

The sidebar header gets a bookmark management entry that opens a modal:

- Title: `Bookmarks`.
- Lists all bookmarked sessions returned by `GET /api/bookmarks`.
- Each row shows agent icon, session name, project or repository context, branch when available, and updated time.
- Clicking a row opens the session and closes the modal.
- Each row has a remove action that deletes the bookmark and removes the row from the modal.
- Empty state: `暂无书签`.

After a bookmark mutation, the frontend updates the current session state, the session list state, and the modal list if it is open.

## Error Handling

- Storage failures return `500` from bookmark APIs and show a user-visible failure in the frontend.
- Missing `agent` on write endpoints returns `400`.
- Removing an already absent bookmark returns success.
- Adding an already present bookmark returns success.
- Unavailable bookmarked sessions are omitted from `GET /api/bookmarks` without destructive cleanup.

## Testing

Backend tests:

- Database store adds, removes, checks, and idempotently handles duplicate writes.
- Bookmark records persist after reopening the database.
- `GET /api/sessions` includes correct `bookmarked` flags.
- `GET /api/bookmarks` returns resolved session metadata and skips unavailable sessions.
- `PUT` and `DELETE` bookmark handlers validate required inputs and are idempotent.

Frontend tests:

- Session list sorting remains time-based when sessions are bookmarked.
- Session list renders bookmark indicators passively.
- Detail toolbar bookmark toggle calls the expected API and updates state on success.
- Bookmark modal lists bookmarks, opens sessions, removes bookmarks, and handles empty state.

## Open Decisions

None. The user approved server-side persistence, `GET /api/bookmarks`, detail-toolbar toggling, passive list indicators, unchanged sorting, and a centralized management modal.
