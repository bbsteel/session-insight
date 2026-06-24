# MiniMap 真实位点缓存 Feature 设计

## Context

当前 MiniMap 使用 turn index 近似映射到右侧导航条位置。这个模型对短会话、长会话、以及单个 turn 内容高度差异较大的会话都不稳定：

- 短会话会被拉满整个 MiniMap，viewport 和事件点比例失真。
- 长会话的事件点会挤在一起，难以点击和识别。
- 一个 turn 在终端里可能占几行，也可能占几百行，仅按 turn index 均分无法反映真实滚动位置。
- 点击用户输入、异常、压缩 marker 后，终端落位和 MiniMap 高亮之间可能缺少明确对应关系。

更合理的方向是让 MiniMap 使用终端渲染后的真实行位点，而不是前端临时按 turn 数量估算。

## Goal

为每个 session 预计算并缓存 MiniMap 所需的关键位点，让 marker、viewport、点击跳转共享同一套 terminal line 坐标系。

核心效果：

- MiniMap marker 位置对应终端中的真实行号。
- viewport 位置和高度来自 xterm viewport 行范围。
- 点击 marker 能滚到该事件实际出现位置。
- 短会话不再被强行拉满，长会话可以在 MiniMap 内继续滚动或跟随当前 viewport。
- live 会话只需要基于最后已知位点增量处理。

## Non-goals

- 第一版不做完整的 MiniMap 虚拟滚动优化。
- 第一版不要求在窗口宽度变化时复用旧位点。
- 第一版不要求把所有终端行都映射为 MiniMap 像素，只缓存关键事件位点。
- 第一版不替换 xterm 作为终端渲染主体。

## Architecture

新增一条后端能力：在生成 ANSI render output 的同时，记录结构化事件对应的 terminal line offset。

```
session detail / render events
-> ANSI formatter
-> ANSI output + position events
-> cache by (agent_type, session_id, revision, cols)
-> frontend MiniMap consumes positions
```

关键点是 `cols` 必须进入 cache key。终端换行受列宽影响，同一段 ANSI 在 100 列和 160 列下会产生不同 line offsets。如果不区分 `cols`，MiniMap 位点会在窗口宽度变化后失准。

## Data Model

建议新增 `session_position_caches` 表保存一次 render 的摘要：

```sql
CREATE TABLE session_position_caches (
  agent_type TEXT NOT NULL,
  session_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  cols INTEGER NOT NULL,
  total_lines INTEGER NOT NULL,
  generated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (agent_type, session_id, revision, cols)
);
```

新增 `session_positions` 表保存关键位点：

```sql
CREATE TABLE session_positions (
  agent_type TEXT NOT NULL,
  session_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  cols INTEGER NOT NULL,
  kind TEXT NOT NULL,
  turn_index INTEGER NOT NULL,
  line_start INTEGER NOT NULL,
  line_end INTEGER,
  label TEXT NOT NULL DEFAULT '',
  severity TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (agent_type, session_id, revision, cols, kind, turn_index, line_start)
);
```

`kind` 初始枚举：

- `turn`
- `user`
- `anomaly`
- `compaction`
- `tool_error`

## Position Generation

Formatter 需要在输出 ANSI 文本时维护当前 logical terminal line：

```go
type RenderPosition struct {
    Kind      string
    TurnIndex int
    LineStart int
    LineEnd   int
    Label     string
    Severity  string
    Payload   map[string]any
}
```

生成规则：

- 输出 turn boundary 前记录 `kind=turn`。
- 输出 user prompt 前记录 `kind=user`。
- 输出 compaction lifecycle event 前记录 `kind=compaction`。
- 输出 tool failure 或 anomaly block 前记录 `kind=anomaly` 或 `kind=tool_error`。
- `line_end` 可在对应 block 输出结束后补齐；第一版只依赖 `line_start` 也可以工作。

行数统计必须和最终写入 xterm 的文本一致。ANSI escape sequence 不计入可见列宽，但换行、自动 wrap 和全角字符宽度会影响行号。建议复用 formatter 的 width-aware 逻辑集中计算，不在前端重复推断。

## API

新增 endpoint：

```text
GET /api/sessions/:id/positions?cols=120
```

返回：

```json
{
  "session_id": "abc",
  "agent_type": "codex",
  "revision": 123456,
  "cols": 120,
  "total_lines": 4210,
  "positions": [
    {
      "kind": "user",
      "turn_index": 8,
      "line_start": 730,
      "line_end": 735,
      "label": "用户输入",
      "severity": ""
    }
  ]
}
```

接口行为：

- cache hit：直接从 DB 返回。
- cache miss：同步生成 render + positions，写入 DB 后返回。
- session revision 变化：旧 revision cache 保留或异步清理，不覆盖。
- `cols` 缺失：使用当前 render API 的默认 cols。

## Frontend Integration

MiniMap 从 `turns` 推断位置改为消费 `positions`：

```ts
type MiniMapPosition = {
  kind: 'turn' | 'user' | 'anomaly' | 'compaction' | 'tool_error'
  turn_index: number
  line_start: number
  line_end?: number
  label: string
  severity?: string
}
```

渲染映射：

```text
markerY = line_start / total_lines * minimapContentHeight
viewportTop = terminalViewportY / total_lines * minimapContentHeight
viewportHeight = terminalRows / total_lines * minimapContentHeight
```

当 `minimapContentHeight` 大于可视高度时，MiniMap 内部可以滚动。ReplayView 滚动时，MiniMap 自动保持当前 viewport 附近可见。

点击 marker：

```text
scrollToLine(position.line_start)
setActivePosition(position)
ensurePositionVisibleInMiniMap(position)
```

## Live Sessions

第一版可以用 revision 失效整 session 重算，逻辑简单且可靠。

后续增量方案：

- cache 表记录 `last_event_id` 或 `last_line_end`。
- live session 更新时只处理新增 render events。
- 新增 positions append 到 DB。
- 若 cols 变化，仍然整 session 重算，因为 wrap 结果全局变化。

## Testing Strategy

后端测试：

- 同一 session 在不同 `cols` 下产生不同 cache key。
- cache hit 不重复运行 formatter。
- user/anomaly/compaction positions 的 `line_start` 单调递增。
- revision 变化后返回新 cache。

前端测试：

- marker 使用 `line_start / total_lines` 映射，而不是 turn index。
- viewport 最小高度仍可拖动。
- 点击 marker 调用 `scrollToLine(line_start)`。
- active marker 在点击后有可见反馈。

手动验证：

```bash
cd frontend && npm test
cd frontend && npm run build
GOCACHE=/tmp/session-insight-go-build go build -tags sqlite_fts5 -o session-insight .
PORT=8092 ./session-insight
curl --noproxy '*' 'http://127.0.0.1:8092/api/sessions'
```

## Rollout Plan

1. Add DB schema and repository methods for position cache.
2. Extend formatter to emit positions alongside ANSI.
3. Add `/api/sessions/:id/positions?cols=` endpoint.
4. Update ReplayView to request positions after terminal cols are known.
5. Update MiniMap to render from line positions.
6. Keep current turn-index MiniMap as fallback when positions are missing.
7. Add live-session invalidation; defer incremental append until correctness is proven.

## Open Questions

- Should `tool_error` and generic `anomaly` be separate marker kinds, or should MiniMap show only one error kind?
- Should MiniMap content height be exactly `total_lines` scaled by a factor, or should it use a capped normalized height for usability?
- Should cache cleanup be time-based, revision-count-based, or left to manual maintenance initially?
