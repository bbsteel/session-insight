# Golden collaboration contract fixtures

Synthetic, sanitized, hand-authored golden JSON for the shared collaboration
contract (`internal/collaboration`). Each file is one `CollaborationGraph`
covering a required frozen-contract case:

| File | Case |
|---|---|
| `standalone-child.json` | Codex archetype: backed child Session, exact identity/lineage, missing launch/result anchors |
| `embedded-child.json` | Chrys archetype: exact two-sided `call_id` trigger/result anchors, embedded transcript content |
| `lifecycle-only.json` | Copilot archetype: exact `toolCallId` identity/timing, estimated aggregate content window |
| `orphaned.json` | started, never completed, session closed: `orphaned` status, open end evidence |
| `estimated-facts.json` | Claude archetype: exact launch anchor, FIFO-estimated result link with reason code |
| `nested.json` | depth-2 nesting with data-driven canonical parents |
| `missing-parent.json` | child attaches to the "Unlinked child Agents" group, transcript preserved |
| `interrupted.json` | last valid indexed graph retained with `stale_graph_retained`, no empty-graph overwrite |
| `malformed-self-link.json` | self-link quarantine |
| `malformed-cycle.json` | cycle detection, duplicate-relation quarantine, one-canonical-parent selection |
| `unknown-status.json` | first-class `unknown` status, never collapsed into success or failure |

Provenance: all IDs, timestamps, and labels are synthetic (aligned with the
sanitized archetype evidence fixtures merged at session-insight commit
`dc584ae`). No real session content is used. The files are generated from
the builders in `golden_test.go`; regenerate after a deliberate contract
change with:

```bash
go test ./internal/collaboration/ -run TestGolden -update
```
