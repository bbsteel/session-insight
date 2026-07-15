# Grok 接入：首版未做项

> 2026-07-15。Grok Build TUI 已作为第六家 agent 接入 SI。
> 本文件记录**有意延后**的能力，避免与「漏做」混淆。

## 已做（首版范围）

| 接入点 | 状态 |
|--------|------|
| Reader 五件套 + 注册 | ✅ `internal/reader/grok/` + `registry.go` |
| RenderEvent（`updates.jsonl` 主路径，`chat_history` 兜底） | ✅ |
| LiveRevision / WatchRoots | ✅ |
| 末回合 in_progress（`events.jsonl` turn 括号 + LiveWindow） | ✅ |
| 恢复命令普通 + 免确认（`--always-approve`） | ✅ |
| 会话删除（目录 + prompt_history + active_sessions + session_search.sqlite） | ✅ |
| SessionProcesses（active_sessions 活 PID + fd holders） | ✅ |
| 前端图标（用户提供 PNG）/ 配色 | ✅ |
| 合成 fixture 测试 | ✅ |

## 未做 / 延后

### 1. 原生渲染 profile

- **现状**：走 default 双线框布局。
- **原因**：观感差异非功能阻塞；formatter 只有一条路径，profile 是参数。
- **后续**：若要对齐 Grok TUI 的卡片/思考区样式，在 `internal/render/profile.go` 加 `grok` Profile，**禁止**前端按 agent 特判。

### 2. 子 agent / background task 深度展开

- **现状**：`task_backgrounded` / `task_completed` / 子 agent 会话目录**不**展开为独立 RenderEvent 流；`terminal/call-*.log` 不挂载到回放。
- **原因**：首版以父会话 `updates.jsonl` 完整工具链回放为主；子任务日志体积大且结构另议。
- **后续**：可参考 claude 的 subagent 双队列，将 background task 输出作为 ToolResult 或 AgentSpecific 挂到对应 `tool_call_id`。

### 3. `--permission-mode bypassPermissions` 第二套免确认命令

- **现状**：危险菜单仅 `grok --always-approve --resume <id>`。
- **原因**：help 已确认 `--always-approve` 语义；`bypassPermissions` 未做 resume 实机验证，文档要求「未确认不写危险版本」。
- **后续**：本地验证 `grok --permission-mode bypassPermissions --resume <id>` 与 always-approve 边界差异后再决定是否替换或并列。

### 4. hooks / session_recap / plan 的完整 UI

- **现状**：`hook_execution`、`session_recap` 忽略；`plan` 仅发出一条 AgentSpecific 提示。
- **原因**：终端回放以用户/思考/正文/工具为主；plan 面板与 SI 现有 UI 无现成挂点。
- **后续**：若 SI 增加 plan 侧栏，再把 `plan.entries` 结构化进去。

### 5. chat_history 中的工具调用侧

- **现状**：无 `updates.jsonl` 时降级 `chat_history`：**有 tool_result，无 ToolInvocation 输入**。
- **原因**：chat_history 不落 tool call 参数（实测仅 tool_result / assistant / reasoning）。
- **影响**：极少数残缺会话回放不完整；正常会话都有 updates。

### 6. 全局 `session_search.sqlite` 的 FTS 内容级「磁盘 grep 不到」

- **现状**：删除时 `DELETE FROM session_docs WHERE session_id=?`，依赖 Grok 自带的 `session_docs_ad` 触发器清 FTS。
- **风险**：若未来 Grok 改 schema / 去掉触发器，可能残留 FTS 碎片；届时需跟进 schema。

### 7. active_sessions 的 StartToken 防 PID 复用

- **现状**：只校验 `procfind.Alive(pid)`，无 start-token（Grok 未写入等价字段）。
- **影响**：极端 PID 复用窗口可能误报可强杀；与 chrys 无标记方案相比仍更准（有官方 active 列表）。
- **后续**：若 Grok 在 active_sessions 增加 `proc_start` / 类似字段，再对齐 claude heartbeat 校验。

### 8. 文档状态表

- `docs/adding-new-agent.md` 五家表可再补一行 grok；首版以本文件 + 代码为准。

## 验证提示

```bash
# worktree 内
go test ./internal/reader/grok/...
./run.sh all   # 完整应用，端口确认空闲后
# 浏览器打开 http://127.0.0.1:8080 ，筛选 agent=Grok
```

当前会话自身在 `~/.grok/sessions/...` 下，应能在侧栏看到并 live tail。
