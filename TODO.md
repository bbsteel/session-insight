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
