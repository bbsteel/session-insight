# Session Insight

本地 AI 编程 Agent 会话浏览与回放工具。会话发现、索引、搜索和回放均在本机运行；AI 生成功能需主动启用，并可能向你配置的模型服务发送选定的会话上下文。

## 功能

- **终端回放** — 以 ANSI 终端形式重播任意会话，支持工具调用折叠、代码块语法高亮，以及活跃会话的实时追尾
- **全文搜索** — 跨所有会话搜索，支持正则表达式和逐回合高亮
- **Diff 查看器** — 并排或内联文件 Diff，含语法高亮和软换行
- **收藏夹** — 为会话添加备注，按 Agent 或模型筛选
- **分析面板** — 每个会话的 Token 用量、费用明细和异常检测
- **文件查看器** — 直接打开会话中提到的任意文件路径，含语法高亮和目录树定位
- **AI 辅助** — 通过配置的 OpenAI 兼容 API 或本机 ACP Agent 生成会话总结、标题和交接提示词
- **深色 / 浅色主题**

## 支持的 Agent

Session Insight 自动发现以下 Agent 的会话数据（持续新增中）：

| Agent | 会话路径（自动检测） |
|-------|----------------------|
| [Claude Code](https://claude.ai/code) | `~/.claude/projects/` |
| [Codex](https://github.com/openai/codex) | `~/.codex/sessions/` |
| [GitHub Copilot](https://github.com/features/copilot) | `~/.copilot/session-state/` |
| [opencode](https://opencode.ai) | opencode SQLite 数据库（自动定位） |
| [Chrys](https://github.com/chrislatinae/chrys) | `~/.chrys/sessions/` |

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

## 隐私说明

核心浏览功能均在本机运行。AI 功能在你配置模型源并主动发起生成前不会启用。生成时，应用会把所选会话的一段受长度限制的上下文发送给配置的 OpenAI 兼容接口或 ACP Agent；ACP Agent 可能继续访问其自身的模型服务。

API 凭据保存在本机 Session Insight SQLite 数据库中，保存后不会再返回给浏览器。请将该本地数据库视为敏感数据妥善保护。

## 预编译二进制

macOS、Linux 和 Windows 的预编译二进制计划中，敬请关注 Releases 页面。

## 开源协议

[MIT](LICENSE) © 2026 bbsteel
