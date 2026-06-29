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
- 短会话不再被强行拉满，长会话渲染为固定长 MiniMap 条，屏幕只显示其中一截。
- live 会话只需要基于最后已知位点增量处理。

## Non-goals

- 第一版不做完整的 MiniMap 虚拟滚动优化。
- 第一版不做用户可独立操作的 MiniMap 内部滚动条。MiniMap 可以有一个比可视区域更高的内容条，但它不使用 `overflow-y: scroll` 暴露第二套滚动状态，而是由终端滚动位置驱动 `transform` 同步移动。
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

`revision` 第一版明确使用 `model.Session.UpdatedAt.UnixNano()`。现有各 reader 已经把 session 内容变化折算到 `UpdatedAt`：例如 Copilot 使用 `events.jsonl` mtime，Codex/Claude/OpenCode reader 也通过 session 更新时间排序和失效。后端新增一个 `SessionRevision(session model.Session) int64` helper，indexer 和 position cache 共用它，避免不同模块各自解释 revision。后续如果改成事件数或自增版本号，只替换这个 helper，不改变 API contract。

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
  position_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('turn', 'user', 'compaction', 'error')),
  turn_index INTEGER NOT NULL,
  line_start INTEGER NOT NULL,
  line_end INTEGER,
  label TEXT NOT NULL DEFAULT '',
  severity TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (agent_type, session_id, revision, cols, position_key),
  FOREIGN KEY (agent_type, session_id, revision, cols)
    REFERENCES session_position_caches(agent_type, session_id, revision, cols)
    ON DELETE CASCADE
);

CREATE INDEX idx_session_positions_lookup
  ON session_positions(agent_type, session_id, revision, cols, line_start);
```

`kind` 初始枚举：

- `turn`
- `user`
- `compaction`
- `error`

`position_key` 由 formatter 生成，格式为 `${kind}:${turn_index}:${line_start}`，供 DB、API、前端 active state 使用。若同一个 `(kind, turn_index, line_start)` 上出现多个同类事件，第一版合并为一个 marker，并在 `payload_json.count` 里记录数量，保持 key 唯一。

基数约束：

- `turn`：每个 turn boundary 正好一条。
- `user`：每个 turn 最多一条。
- `compaction`：每个 turn 可以多条；同一行同类 marker 合并。
- `error`：每个 turn 可以多条，覆盖 tool failure 和 generic anomaly；同一行同类 marker 合并。

## Position Generation

Formatter 需要在输出 ANSI 文本时维护当前 logical terminal line：

```go
type RenderPosition struct {
    PositionKey string
    Kind      string
    TurnIndex int
    LineStart int
    LineEnd   *int
    Label     string
    Severity  string
    Payload   map[string]any
}
```

生成规则：

- 输出 turn boundary 前记录 `kind=turn`。
- 输出 user prompt 前记录 `kind=user`。
- 输出 compaction lifecycle event 前记录 `kind=compaction`。
- 输出 tool failure 或 anomaly block 前记录 `kind=error`。
- 第一版 `line_end = NULL`。API 序列化时省略 `line_end` 字段，前端按缺失处理；只有在后续需要 marker 区间高亮或 block range 交互时再补齐。

行数统计必须和最终写入 xterm 的文本一致。实现上不允许用 `len(line)/cols` 推断，必须在 formatter 里增加一个共享的 `terminalLineTracker`：

- tracker 只消费 formatter 实际写入 builder 的文本，确保记录的 `line_start` 和最终 ANSI output 同源。
- ANSI SGR escape sequence 不增加可见列宽。
- `\n` 是硬换行，直接进入下一 terminal line。
- 普通 printable rune 按 display width 累加；CJK、Hangul、Fullwidth 等全角字符按 2 列处理，和当前 `displayWidth`/`splitAtWidth` 的规则一致。
- 当当前列数加 rune width 超过 `cols` 时，先产生软换行，再写入该 rune。
- 滚动条占位不在后端估算，后端只接受前端在 `fitAddon.fit()` 后得到的 `term.cols`。

测试必须覆盖 ASCII、中文全角、混合中英文、ANSI color、硬换行、刚好等于 `cols`、超过 `cols` 1 列等样例。第一版可复用当前 `displayWidth` 宽字符判断；如果测试发现组合字符、emoji 或 East Asian Ambiguous 字符和 xterm 偏差明显，再引入 `go-runewidth` 类库替换内部实现。

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
      "position_key": "user:8:730",
      "turn_index": 8,
      "line_start": 730,
      "label": "用户输入",
      "severity": ""
    }
  ]
}
```

接口行为：

- cache hit：直接从 DB 返回。
- cache miss：同步生成 render + positions，写入 DB 后返回；目标延迟为 `< 500ms @ 1000 events, cols=120`，新增 benchmark 固定这个目标。
- cache miss 超过 1500ms：请求返回 `202 Accepted` 和 `{ "status": "building" }`，后端继续完成生成；前端每 1s 重试同一接口，等待 200 返回后再渲染 MiniMap。building 期间不展示 MiniMap（不降级到 turn-index fallback）。
- session revision 变化：旧 revision cache 保留或异步清理，不覆盖。
- `cols` 缺失：使用当前 render API 的默认 cols。
- `line_end` 为 NULL 时不出现在 JSON 中。

## Frontend Integration

MiniMap 从 `turns` 推断位置改为消费 `positions`：

```ts
type MiniMapPosition = {
  kind: 'turn' | 'user' | 'error' | 'compaction'
  position_key: string
  turn_index: number
  line_start: number
  line_end?: number
  label: string
  severity?: string
}
```

渲染模型：

- `visibleTrackHeight` 是屏幕上实际可见的 MiniMap 容器高度。
- `minimapContentHeight` 是内部固定长条高度，第一版按 session/cols 计算一次并 cap，例如 `clamp(visibleTrackHeight, total_lines * 0.6, visibleTrackHeight * 4)`。
- 容器使用 `overflow: hidden`，内部长条用 `translateY(-contentOffset)` 移动；不引入 MiniMap 自身的 native scrollTop。
- 左侧终端滚动时，同时更新 MiniMap 长条偏移和 viewport 框。

```text
scrollRatio = terminalViewportY / max(total_lines - terminalRows, 1)
contentOffset = scrollRatio * max(minimapContentHeight - visibleTrackHeight, 0)

markerYInContent = line_start / total_lines * minimapContentHeight
markerYInView = markerYInContent - contentOffset

viewportTopInContent = terminalViewportY / total_lines * minimapContentHeight
viewportTopInView = viewportTopInContent - contentOffset
viewportHeight = terminalRows / total_lines * minimapContentHeight
```

这种模型和“MiniMap 内部滚动”不同：用户没有第二个可滚动区域，MiniMap 的可见截面始终由终端当前 viewport 决定。好处是长会话 marker 不必全部压进一个屏幕高度里，同时现有终端滚动仍是唯一滚动真源。

- marker 的视觉高度可以小于 16px，但点击命中区最小 16px。
- 同一可视局部范围内间距小于 2px 的同类 marker 可以聚合显示，点击聚合 marker 跳到其中第一个 `line_start`。
- `minimapContentHeight` 必须 capped，不能直接按 `total_lines` 无限增长。
- `total_lines` 用于 terminal line 到 MiniMap 内容坐标的比例计算，不直接决定 DOM 无限高度。

点击 marker：

```text
scrollToLine(position.line_start)
setActivePositionKey(position.position_key)
```

当前 `activeIndex: number | null` 需要改成 `activePositionKey: string | null`，不再依赖 positions 数组下标。position key 必须随 API 返回，不能由前端重新猜测。

`cols` 请求时序：

- `TerminalPanel` 在 `document.fonts.ready` 后 `term.open(container)`，调用 `fitAddon.fit()`，读取稳定的 `term.cols`。
- `ReplayView` 只有收到 terminal cols 后才请求 `/positions?cols=`；positions 和 render 使用同一个 cols。
- resize 后 cols 变化时，用 debounce 重新请求 render + positions。第一版不复用旧 cols 的 positions。
- 如果 positions 请求返回 202（building），不展示 MiniMap，前端每 1s 轮询直到 200 返回。
- 如果 positions 请求失败（网络错误、5xx），MiniMap 降级为 turn-index fallback。
- 终端 `onScroll` 是同步真源：每次 scroll metrics 更新时，MiniMap 根据 `terminalViewportY` 重新计算 `contentOffset`，让长条可视截面和左侧终端 viewport 一起移动。

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
- `session_positions` 通过 FK 级联依赖 `session_position_caches`，删除 header 后 positions 不残留。
- cache hit 不重复运行 formatter。
- user/error/compaction positions 的 `line_start` 单调递增。
- CJK 全角字符、ANSI sequence、硬换行和软换行的 line tracker 结果符合预期。
- revision 变化后返回新 cache。
- benchmark：`BenchmarkRenderPositions1000Events` 在本地目标 `< 500ms`。

前端测试：

- marker 使用 `line_start / total_lines` 映射，而不是 turn index。
- viewport 最小高度仍可拖动。
- 点击 marker 调用 `scrollToLine(line_start)`。
- active marker 使用 `position_key`，点击后有可见反馈。
- terminal cols 在 `fitAddon.fit()` 后才触发 positions 请求；resize cols 变化会 debounce 重新请求。

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
4. Update TerminalPanel/ReplayView to expose stable cols after `fitAddon.fit()` and request positions with the same cols as render.
5. Update MiniMap to render from line positions.
6. Keep current turn-index MiniMap as fallback when positions are missing.
7. Add live-session invalidation; defer incremental append until correctness is proven.

## Open Questions

- Should cache cleanup be time-based, revision-count-based, or left to manual maintenance initially?
