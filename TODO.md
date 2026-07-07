# TODO

## 已确认排期（2026-07-07 分工）

### 主线（Claude Code 本体，按序）

1. [ ] 收藏列表加载慢：handleListBookmarks 逐条 GetSession(id) 取摘要，去掉全量 ListSessions 扫描（实测 4 条收藏耗时 1.19s 的根因）
2. [ ] 右键打开文件 + 编辑器设置：POST /api/open-file 按服务端命令模板执行；edit 行取 edits API 的 file_path，普通行正则 + 会话 cwd 解析 + stat 校验后才显示菜单项；edit 行用 new_string 首行搜索尽力定位行号；编辑器命令模板存 DB（不随请求传，避免命令注入面），设置面板加配置项
3. [ ] Ctrl+F 终端页内搜索：@xterm/addon-search + 浮动搜索条（Ctrl+F/Enter/Shift+Enter/Esc）；折叠重写后需重跑当前搜索；命中在折叠体内的策略（跳过或自动展开）待定
4. [ ] claude 开折叠 + 终端代码块 ANSI 高亮：claude 档案开 GroupToolRuns，组头统计式文案（对齐 Claude Code TUI 折叠摘要风格，该摘要不在 JSONL 中、需渲染 pre-pass 自行统计）；chroma 高亮 assistant 消息 fenced 代码块；两者合并一次 FormatVersion bump
5. [ ] DiffModal 逐行语法高亮：按文件扩展名 Prism tokenize，注意大 diff 性能；必须排在 A 包合入之后（同文件）

### 交办普通 agent（三包可并行）

- [ ] A 主题/高亮包：DiffModal 明暗主题同步（现全部硬编码暗色 hex）+ SyntaxCodeBlock oneDark/oneLight 跟随 + OutputModal JSON 探测高亮
- [ ] B 右键菜单 Common 段接线：跳转类（上/下用户消息、上/下 turn、到顶/到底）+ 复制选中文本、复制会话 ID/cwd、导出、收藏切换；依赖 1bd3e4c 的菜单框架
- [ ] C minimap 到顶/到底按钮：上下端小箭头，纯前端

### 待决策 / 附注

- TurnCard / MarkdownRenderer 已无引用（卡片视图移除后的死代码），是否删除待定
- 行上下文类右键菜单项（edit 行→Diff、截断行→展开）已否决：与左键直达重复
- test:minimap 存量失败（e2e2b2 改 48px 后期望值未更新 16.25 vs 22.5）待单独修

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
