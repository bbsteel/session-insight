# Agent 接入指南

本目录是 SessionInsight 接入新 Agent 的开发入口。它服务于维护者、外部贡献者以及执行接入任务的 Claude/Codex，不是面向终端用户的产品文档。

接入工作的优先级和事实来源依次是：

1. [能力契约](capability-contract.md)：定义 SI 能对该 Agent 的记录读取什么、判断什么、执行什么。
2. [共享符合性测试](conformance-testing.md)：证明能力声明与真实实现一致。
3. 本指南：规定研究、实现、验证和交付流程。

能力声明与符合性测试落地前，不新增 `session-insight adapter init` 命令。脚手架只能生成文件，不能代替格式研究、真实样本和行为验证。等目录结构经过多个新 Agent 接入并稳定后，再考虑添加仓库内部的生成工具。

## 核心原则

- **Agent 是用户术语。** UI 使用“Agent 能力”；`reader`、`adapter` 和 `source` 只用于内部实现或数据来源说明。
- **契约即事实来源。** 设置页、会话页和 API 都从适配器能力声明生成，不维护第二张手写能力表。
- **声明必须有证据。** `exact` 或 `estimated` 必须由实现、脱敏 fixture 和符合性测试共同支撑。
- **Agent 能力与本次会话状态分离。** Agent 通常能提供 Token，不代表每次会话都有 Token；异常退出时应呈现 `missing`，不能呈现 `0` 或把 Agent 降级为 `unsupported`。
- **保守声明。** 调研中、开发中或没有测试证据的能力不能对用户声明为支持。
- **不以一个旧适配器为模板。** 新接入必须逐项对照本指南，避免继承某个 Agent 特有的假设。

## 接入目录

每个 Agent 的实现继续放在：

```text
internal/reader/<agent>/
├── <agent>.go
├── <agent>_test.go
├── <agent>_delete.go          # 能力适用时
├── <agent>_delete_test.go
├── <agent>_render.go          # 可按实现规模拆分
└── testdata/
    ├── basic/
    ├── tools/
    ├── interrupted/
    └── ...
```

Go 测试约定使用 `testdata/`，其中只允许提交经过脱敏、规模受控且来源明确的样本。若当前适配器仍以内联临时目录构造样本，可在修改该适配器时逐步迁移，不要求一次重写全部旧测试。

## 接入流程

### 1. 调研 Agent 的持久化事实

先回答并记录：

- 会话保存在哪里，路径能否自动发现？
- 使用 JSONL、JSON、SQLite 还是多文件结构？
- 会话 ID 是否稳定，恢复命令使用哪个 ID？
- 追加写入、覆盖写入和事务提交分别如何发生？
- 正常结束与异常中断有什么可观察差异？
- Token、工具结果、Diff、子任务是否有结构化字段？
- 活跃会话能否与心跳、锁、注册表或 PID 精确关联？
- 删除一个会话需要清理哪些 Agent 自有记录？
- Windows、macOS、Linux 的路径或格式是否不同？
- Agent 版本升级是否改变过 schema？
- 样本中可能包含哪些密钥、身份信息或私人内容？

不能从文件更新时间、模型名称或 UI 文案推断为“精确”的事实。

### 2. 建立最小可回放链路

实现 `reader.BaseSessionReader` 所需行为：

- `AgentType` 返回稳定、全小写、不可本地化的标识。
- `DisplayName` 返回产品名称。
- `ListSessions` 与 `GetSession` 对同一会话使用相同 ID。
- 时间、消息和回合顺序可重复读取且结果稳定。
- `RenderANSI` 和 `GetRenderEvents` 明确区分“不支持”“未找到”和空会话，不静默吞错。

随后在 `internal/reader/registry.go` 中增加自动发现。平台特有路径必须使用 Go 的路径 API，不能拼接硬编码分隔符。

### 3. 声明能力

按照 [能力契约](capability-contract.md) 为十项基线能力逐项声明：

- discovery
- replay
- realtime
- tokens
- tool_results
- diff
- subtasks
- resume
- delete
- terminate

每项声明都要说明依据；`unsupported` 和 `not_applicable` 也需要原因。不要用 `missing` 描述 Agent 的静态能力。

### 4. 准备脱敏 fixture

按适用范围覆盖：

- 最小正常会话；
- 多轮会话；
- 工具调用与工具结果；
- 文件修改或 Diff；
- 子任务及父子关系；
- 正常结束；
- 异常退出或未完成记录；
- 缺少可选字段；
- 格式升级前后的版本；
- 平台差异。

脱敏时必须替换用户名、主目录、仓库 URL、密钥、消息正文中的身份信息和业务数据，同时保持字段形状、事件关系和边界条件不变。无法安全提交的真实样本应转写为最小合成 fixture。

### 5. 运行共享符合性测试

适配器测试调用 `adaptertest.Run`，并显式提供它承诺覆盖的 fixture。详细结构见 [共享符合性测试方案](conformance-testing.md)。

最低验证命令：

```bash
go test ./internal/reader/...
```

若改动影响 API、索引、实时刷新、删除或终止行为，还必须运行对应包测试和完整仓库测试。

### 6. 注册并检查产品表现

能力契约应驱动：

- 设置页中的 Agent 能力比较；
- 会话页中的 Agent 能力入口；
- 本次会话的 `exact / estimated / missing` 状态；
- 恢复、删除和终止运行等操作是否出现或可用；
- 不支持、缺失和空结果的差异化说明。

不得在前端按 `agent_type` 再写一份能力判断。

## Definition of Done

只有同时满足以下条件，才可认为新 Agent 接入完成：

- [ ] 已记录存储位置、格式版本、身份字段和隐私边界。
- [ ] 已实现稳定的发现、列表、详情和回放。
- [ ] 十项基线能力均有声明和原因。
- [ ] 所有支持的能力都有至少一个脱敏 fixture。
- [ ] 共享契约检查通过。
- [ ] 声明为支持的行为检查通过。
- [ ] 异常退出、缺失字段和空会话不会被误报。
- [ ] 已覆盖适用的操作系统差异，未覆盖的平台在声明或 PR 中明确。
- [ ] 注册表、API 和 UI 不包含重复的手写能力表。
- [ ] `go test ./internal/reader/...` 及受影响范围的测试通过。
- [ ] PR 说明列出已验证能力、已知缺口和 fixture 来源。

## 给编码 Agent 的推荐任务描述

```text
按照 developer/agent-adapters/README.md 接入 <AgentName>。

先研究本地持久化格式和跨平台路径，再实现 reader、能力声明与脱敏
fixtures。使用共享符合性测试证明每项声明；没有证据的能力保守标为
unsupported，无法归属于 Agent 概念的能力标为 not_applicable。

不要在前端或文档中维护独立能力矩阵。完成后运行指南要求的测试，并在
PR 中列出能力证据、未覆盖平台和已知缺口。
```

维护者仍然只需提出“新增某 Agent 支持”。本目录负责把这句话展开成稳定、可复核的工程流程。
