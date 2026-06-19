# TODO

## MiniMap

- Investigate remaining MiniMap drag jank. Current implementation uses pixel-based scrolling and requestAnimationFrame batching, but real use still feels stuck or stepped.
- Re-evaluate whether the current MiniMap should remain a primary navigation surface. The dense token bars, tiny markers, and drag viewport may be hard to use in real sessions.
- Consider replacing the current MiniMap with a simpler session outline:
  - user prompt anchors
  - anomalies and compaction points
  - search result markers
  - jump buttons and keyboard navigation
- If a MiniMap remains, treat it as a passive overview first and a precision drag control only if it can be made clearly smoother than native scrolling.

Product note: the current MiniMap is visually distinctive, but its practical value is questionable. In long agent sessions, users likely need semantic waypoints more than a compressed visual encoding of every turn.
