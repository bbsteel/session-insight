# TODO

## 已确认排期（2026-07-07 分工）

### 主线（Claude Code 本体，按序）

1. [x] 收藏列表加载慢（2d7ffee：逐条 GetSession，1.19s → 0.22s）
2. [x] 右键打开文件 + 编辑器设置（927222f；后续 51a2b85 安全加固：改绑 127.0.0.1 + 写接口 Content-Type/Origin 校验）
3. [x] Ctrl+F 终端页内搜索（addon-search + 浮动搜索条；折叠重写后自动重跑；折叠体内内容不参与搜索——展开后才可搜，与"隐藏即不可见"语义一致）
4. [x] claude 开折叠 + 终端代码块 ANSI 高亮（组头统计式文案 "▼ Tools (n/m) · 2 shell"；chroma terminal256+monokai 高亮 fenced 块；FormatVersion 4→5）
5. [x] DiffModal 明暗主题 + 逐行语法高亮（与 A 包合并收回自做，1e19ca6：refractor 逐行 tokenize + 双调色板）

### 交办普通 agent（三包可并行）

- [x] A 主题/高亮包（收回自做，随 1e19ca6：与 DiffModal 逐行高亮同一提交）
- [x] B 右键菜单 Common 段接线（随 927222f 入库，与打开文件项同区无法分拆；回到顶部/复制会话 ID 已抽查）
- [x] C minimap 到顶/到底按钮（45a8296，C 包 agent 自行提交）

### 待决策 / 附注

- [x] TurnCard 死代码已删（连带只被它引用的 Badge）；MarkdownRenderer 已被 AIPanel 复用，保留
- 行上下文类右键菜单项（edit 行→Diff、截断行→展开）已否决：与左键直达重复
- [x] test:minimap 期望值已随后续提交修正；test:folds 折叠头「(N 行)」徽标期望值补齐，全量 npm test 通过

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

## 搁置 / 待复现

- [ ] Codex 会话最后一段助手消息未出现在交互消息导航窗格（2026-07-27 报告，现场已被破坏，未能复现）
  - 会话：`rollout-2026-07-26T22-16-57-019f9ec9-434a-7f53-b2c0-0935eccf855f`（report 时仍在 live 写入）
  - 已验证：完整重解析能识别全部 13+ 条 agent_message 并各生成一条 assistant position；positions API 与前端面板条数一致；live 追加后 API 约 1s 内同步；未见持续缺失
  - 嫌疑方向（下次复现时优先排查）：
    1. 前端 live 轮询在 `fetchLiveRevision` 任意一次失败时 `rev === null` 直接 `return`，不再调度下一 tick——一次性瞬时错误会让终端与导航窗格永久停更（ReplayView tick 循环）
    2. 报告时刻可能处于 live 滞后窗口（3s 轮询 + positions 重建），消息数秒后自动出现，被误认为未识别
    3. Codex 流式生成期间最后一段尚未写入 rollout 文件（agent_message 只在完成时落盘），属固有延迟而非缺失
  - 复现要点：在 codex 会话 live 写入期间打开该会话并观察导航窗格末条是否与文件末尾 agent_message 同步；同时抓 `fetchLiveRevision` 是否出现过非 404 的失败
