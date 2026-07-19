# Session Insight

[![CI](https://github.com/bbsteel/session-insight/actions/workflows/ci.yml/badge.svg)](https://github.com/bbsteel/session-insight/actions/workflows/ci.yml)

一款本地优先的 AI 编程 Agent 会话浏览、回放与分析工具。它在交互式终端中重现带 ANSI 样式的对话、工具调用和代码输出；会话发现、索引、搜索和回放均在本机运行。AI 生成功能需主动启用，且只使用你配置的模型服务。

[English](README.md)

![带工具调用和语义小地图的会话回放](assets/screenshots/replay.png)

<p align="center"><sub>截图来自真实开发会话，个人路径和联系方式已脱敏。</sub></p>

## 功能亮点

- **多 Agent 会话库** — 自动发现并索引 6 种编程 Agent 的会话，会话列表实时刷新，准确识别活跃状态，并实时追尾运行中的会话
- **终端原生回放** — 保留 ANSI 输出、助手排版、工具调用、代码与错误信息；可折叠冗长细节，并持续跟随活跃会话
- **快速会话导航** — 默认从首条提示词开始，通过滚动置顶的当前用户消息、语义小地图，或合并用户消息与助手回复的交互消息面板快速跳转
- **搜索与整理** — 在后台索引并显示进度，可跨会话检索元数据、提示词、助手回复、Skill、工具输入和错误；支持项目与 Agent 筛选，以及带备注的收藏
- **工具、Diff 与代码查看** — 筛选工具调用并跳回原 Turn；查看内联或并排 Diff；在结构化代码阅读器或本机编辑器中打开会话提及的文件
- **用量分析** — 查看输入、输出和缓存 Token，费用估算、工具用量、错误、异常、续写压力与逐 Turn 趋势
- **会话生命周期工具** — 导出会话、复制按 Shell 生成的恢复命令，以及在运行进程保护和可用的强制停止流程下安全删除会话
- **可选 AI 辅助** — 通过配置的 OpenAI 兼容 API 或本机 ACP Agent 生成会话总结、标题和交接提示词
- **桌面个性化** — 支持深色 / 浅色主题、Agent 图标、自定义用户头像、可调整面板，以及相互独立的界面/终端字体和字号

## 更多截图

| 交互消息 | 设置与字体 |
|:--:|:--:|
| ![合并用户消息与助手回复的导航面板](assets/screenshots/interaction.png) | ![提供界面和终端字体控制的设置中心](assets/screenshots/settings.png) |

| 会话分析 | 结构化代码阅读器 |
|:--:|:--:|
| ![Token、缓存、工具用量和异常分析](assets/screenshots/analytics.png) | ![文件树、代码视图、搜索和结构大纲](assets/screenshots/code-reader.png) |

## 支持的 Agent

Session Insight 自动发现以下 Agent 的会话数据：

| Agent | 会话路径（自动检测） |
|-------|----------------------|
| [Claude Code](https://claude.ai/code) | `~/.claude/projects/` |
| [Codex](https://github.com/openai/codex) | `~/.codex/sessions/` |
| [GitHub Copilot](https://github.com/features/copilot) | `~/.copilot/session-state/` |
| [opencode](https://opencode.ai) | opencode SQLite 数据库（自动定位） |
| [Chrys](https://github.com/chrislatinae/chrys) | `~/.chrys/sessions/` |
| [Grok](https://grok.com) | `~/.grok/sessions/` |

## 快速开始

### 环境依赖

- Go 1.25+
- Node.js 18+

### 构建并运行（macOS / Linux）

```bash
git clone https://github.com/bbsteel/session-insight.git
cd session-insight
bash run.sh all
```

启动后访问 **http://127.0.0.1:8080**，浏览器会自动打开。

常用运行命令：

```bash
./run.sh status       # 列出当前应用及 linked worktree 实例
./run.sh restart      # 重新构建并重启当前 checkout
./run.sh stop         # 只停止当前 checkout 的实例
```

### Windows

参见 [BUILD_ZH.md](BUILD_ZH.md)，需要安装 MSYS2 + mingw-w64 以支持 CGO 编译。

### 配置

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `PORT` | `8080` | HTTP 监听端口 |
| `SI_DATA_DIR` | `~/.session-insight` | 覆盖应用数据库目录 |
| `CHRYS_SESSION_ROOT_DIR` | — | 覆盖 Chrys 会话根目录 |

从 linked Git worktree 执行 `run.sh` 时，脚本在首次运行使用操作系统分配的随机
loopback 端口，并在后续重启时复用同一端口（持久化到 `.runtime/session-insight.port`），
数据库隔离在 `.runtime/session-insight` 中。`Ready:` 行会输出实际可访问的完整应用地址。

## 隐私说明

核心浏览功能均在本机运行。AI 功能在你配置模型源并主动发起生成前不会启用。生成时，应用会把所选会话的一段受长度限制的上下文发送给配置的 OpenAI 兼容接口或 ACP Agent；ACP Agent 可能继续访问其自身的模型服务。

API 凭据保存在本机 Session Insight SQLite 数据库中，保存后不会再返回给浏览器。请将该本地数据库视为敏感数据妥善保护。

## 预编译二进制

版本化压缩包会发布到 [GitHub Releases](https://github.com/bbsteel/session-insight/releases) 页面，支持：

- Linux x86-64 和 arm64
- macOS Intel 和 Apple Silicon
- Windows x86-64

每个压缩包都包含可执行文件、中英文 README 和许可证。可从同一个 Release 下载 `checksums.txt`，再将对应记录与 Linux/macOS 上的 `sha256sum <archive>` 或 PowerShell 中的 `Get-FileHash <archive> -Algorithm SHA256` 结果比较。

## 开源协议

[MIT](LICENSE) © 2026 bbsteel
