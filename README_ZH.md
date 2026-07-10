# Session Insight

本地 AI 编程 Agent 会话浏览与回放工具。完全在本机运行，数据不离开设备。

## 功能

- **终端回放** — 以 ANSI 终端形式重播任意会话，支持工具调用折叠、代码块语法高亮，以及活跃会话的实时追尾
- **全文搜索** — 跨所有会话搜索，支持正则表达式和逐回合高亮
- **Diff 查看器** — 并排或内联文件 Diff，含语法高亮和软换行
- **收藏夹** — 为会话添加备注，按 Agent 或模型筛选
- **分析面板** — 每个会话的 Token 用量、费用明细和异常检测
- **文件查看器** — 直接打开会话中提到的任意文件路径，含语法高亮和目录树定位
- **深色 / 浅色主题**

## 支持的 Agent

Session Insight 自动发现以下 Agent 的会话数据（持续新增中）：

| Agent | 会话路径（自动检测） |
|-------|----------------------|
| [Claude Code](https://claude.ai/code) | `~/.claude/projects/` |
| [Codex](https://github.com/openai/codex) | `~/.codex/sessions/` |
| [GitHub Copilot](https://github.com/features/copilot) | `~/.copilot/session-state/` |
| [opencode](https://opencode.ai) | opencode SQLite 数据库（自动定位） |

## 快速开始

### 环境依赖

- Go 1.25+
- Node.js 18+

### 构建并运行（macOS / Linux）

```bash
git clone https://github.com/bbsteel/session-insight.git
cd session-insight
bash start.sh all
```

启动后访问 **http://127.0.0.1:8080**，浏览器会自动打开。

### Windows

参见 [BUILD_ZH.md](BUILD_ZH.md)，需要安装 MSYS2 + mingw-w64 以支持 CGO 编译。

### 配置

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `PORT` | `8080` | HTTP 监听端口 |
| `CHRYS_SESSION_ROOT_DIR` | — | 覆盖 Chrys 会话根目录 |

## 预编译二进制

macOS、Linux 和 Windows 的预编译二进制计划中，敬请关注 Releases 页面。

## 开源协议

[MIT](LICENSE) © 2026 bbsteel
