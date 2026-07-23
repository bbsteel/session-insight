# Agent 能力契约方案

## 目标

建立由 Agent 适配器拥有的唯一能力事实来源，使后端 API、设置页、会话页和共享测试读取同一份数据。契约回答两个不同问题：

1. **Agent 能力**：SI 对这种 Agent 通常能做到什么？
2. **会话状态**：SI 对当前这一条会话实际拿到了什么？

这两层不能合并。Agent 支持 Token，而某次异常退出没有写入 Token，分别应表达为：

```text
Agent 能力：tokens = exact
本次会话：tokens = missing
```

## 统一术语

对用户统一显示五种状态：

| 标识 | 代码值 | 含义 |
|---|---|---|
| `✓` | `exact` | 直接读取结构化事实，或执行结果可精确确认 |
| `≈` | `estimated` | 通过启发式、时间窗口或不完整证据推断 |
| `!` | `missing` | Agent 通常能记录，但本次会话没有该数据 |
| `—` | `not_applicable` | 该 Agent 不存在这个概念 |
| `×` | `unsupported` | 概念存在，但 SI 当前不能可靠读取或管理 |

`—` 不使用圆圈图标。颜色不能作为唯一识别方式，文本和符号必须同时存在。

### 状态允许范围

Agent 级静态声明只允许：

```text
exact | estimated | not_applicable | unsupported
```

会话级解析结果允许全部五种状态。`missing` 永远不能写入 Agent 静态声明，因为它描述的是一条具体记录，而不是产品能力。

开发进度使用另一套内部术语，例如 `investigating`、`implementing`、`verified`、`blocked`，不得进入用户能力契约。

## v0.4.0 基线能力

能力 ID 是稳定 API，不使用显示文案作为标识。

| ID | 用户名称 | 精确定义 |
|---|---|---|
| `discovery` | 发现 | SI 能自动定位并列出该 Agent 的本地会话，无需用户逐条导入 |
| `replay` | 回放 | SI 能按持久化顺序重建用户消息、助手消息和已识别事件 |
| `realtime` | 实时 | 打开会话后，SI 能检测持久化内容修订并增量或重新加载最新记录 |
| `tokens` | Token | SI 能读取或有明确规则估算 Token/计费字段，并保留字段存在性 |
| `tool_results` | 工具结果 | SI 能关联工具调用与其结果、失败或拒绝状态 |
| `diff` | Diff | SI 能从结构化记录恢复文件修改前后内容或等价补丁 |
| `subtasks` | 子任务 | SI 能识别父子 Agent/任务关系并保持稳定身份 |
| `resume` | 恢复 | SI 能提供该 Agent 原生恢复会话所需的稳定标识或命令参数 |
| `delete` | 删除 | SI 能完整删除该 Agent 自有的会话记录，并确认目标身份 |
| `terminate` | 终止运行 | SI 能把当前会话精确关联到运行进程并终止该进程 |

“实时”不等于“运行状态”。内容修订检测和进程存活判断必须分别描述在证据中；未来若 UI 需要独立比较，可新增 `liveness` 能力，不能悄悄改变 `realtime` 的定义。

“终止运行”不等于停止回放，也不表示 SI 能向 Agent 发送消息。未来的 Agent 控制能力应使用新的能力 ID，例如 `message_send`，并单独设计权限、确认和失败语义。

## 建议的 Go 模型

第一阶段以强类型 Go 声明为事实来源，不引入 YAML 或手写 JSON。能力类型放在不依赖具体 reader 的叶子包，例如 `internal/reader/capability`，避免 registry 与适配器之间形成循环依赖。示意：

```go
package capability

type CapabilityID string

const (
    CapabilityDiscovery   CapabilityID = "discovery"
    CapabilityReplay      CapabilityID = "replay"
    CapabilityRealtime    CapabilityID = "realtime"
    CapabilityTokens      CapabilityID = "tokens"
    CapabilityToolResults CapabilityID = "tool_results"
    CapabilityDiff        CapabilityID = "diff"
    CapabilitySubtasks    CapabilityID = "subtasks"
    CapabilityResume      CapabilityID = "resume"
    CapabilityDelete      CapabilityID = "delete"
    CapabilityTerminate   CapabilityID = "terminate"
)

type CapabilityState string

const (
    CapabilityExact         CapabilityState = "exact"
    CapabilityEstimated     CapabilityState = "estimated"
    CapabilityMissing       CapabilityState = "missing"
    CapabilityNotApplicable CapabilityState = "not_applicable"
    CapabilityUnsupported   CapabilityState = "unsupported"
)

type CapabilityDeclaration struct {
    State      CapabilityState
    ReasonCode string
    DetailKey  string
}

type AgentCapabilities struct {
    AgentType       string
    DisplayName     string
    AdapterRevision int
    Capabilities    map[CapabilityID]CapabilityDeclaration
}
```

实现时可以将 `map` 换成固定字段结构以获得编译期完整性，但 API 输出应保留稳定的能力 ID。`DetailKey` 是本地化键，不在后端硬编码用户文案。`ReasonCode` 是机器可读原因，用于测试、遥测和会话级覆盖。

`AdapterRevision` 在能力语义、解析映射或支持范围变化时递增；它不是 Agent 自身版本。

每个适配器包导出静态声明：

```go
func Capabilities() capability.AgentCapabilities
```

静态声明不能只挂在已创建的 reader 实例上。当前 `Discover()` 只有在本机发现 Agent 存储时才创建实例，而设置页需要描述全部已支持 Agent，包括尚未安装或尚无会话的 Agent。

`internal/reader/registry.go` 因此同时维护：

- **定义目录**：汇总六个适配器的静态声明，始终可查询；
- **发现结果**：根据本机存储创建的 `BaseSessionReader` 实例。

registry 只聚合各适配器导出的声明，不能重新填写能力值。未来可进一步把发现函数与声明组合为注册定义，但不应为消除几行显式注册引入 `init()` 全局副作用。

## 会话级覆盖

会话详情不应让前端根据字段是否为零自行猜测状态。建议由后端解析为：

```go
type SessionCapabilityStatus struct {
    State      CapabilityState
    ReasonCode string
}

type SessionCapabilities struct {
    AgentType       string
    AdapterRevision int
    Status          map[CapabilityID]SessionCapabilityStatus
}
```

解析规则：

1. 以 Agent 静态声明为基线。
2. `unsupported` 和 `not_applicable` 原样进入会话状态。
3. 静态为 `exact` 或 `estimated`，但本次应有的数据没有落盘时，覆盖为 `missing`。
4. 有效的零值仍保持 `exact`，例如精确记录的 `0` 次工具调用。
5. 当前会话只能降低数据可用性，不能把 Agent 未声明支持的能力临时提升为 `exact`。

操作能力还需返回当前可用性。例如 Agent 支持恢复，但当前会话缺少稳定的 `ResumeID`，本次状态为 `missing`，恢复按钮不可用，并展示对应原因。

## 原因码

原因码保持稳定、不可本地化，文案由前端 i18n 映射。建议初始集合：

```text
source_not_recorded
session_not_finalized
resume_id_missing
exact_pid_unavailable
timestamp_heuristic
revision_polling
structured_event
adapter_not_implemented
concept_absent
platform_not_supported
```

每个 `estimated`、`missing`、`unsupported` 和 `not_applicable` 声明必须提供原因码。`exact` 可以省略，但高风险操作如删除和终止运行仍建议注明证据类型。

## 契约校验规则

共享测试至少强制：

- 十项基线能力全部存在且只出现一次。
- `AgentType` 与 reader 的 `AgentType()` 相同。
- `DisplayName` 非空。
- Agent 静态声明不包含 `missing`。
- 状态值属于已知枚举。
- 非 `exact` 状态包含 `ReasonCode`。
- 声明 `realtime = exact/estimated` 时，实现内容修订检测接口。
- 声明 `delete = exact` 时，实现 `SessionDeleter`。
- 声明 `terminate = exact` 时，实现 `SessionProcessFinder`。
- `terminate` 不允许 `estimated`。
- 支持型数据能力具有对应 fixture 和行为检查。

Go 接口存在只能证明“有实现入口”，不能证明实现正确；最终可信度来自 fixture 行为测试。

## API 与 UI 消费

建议提供 Agent 列表级 API：

```text
GET /api/agents
```

它返回全部已注册 Agent 的显示名、适配器修订、能力声明以及本机是否已发现。会话详情返回或链接会话级状态。未安装 Agent 的能力仍然可见，但不能伪造会话数量或可执行操作。

设置页展示 Agent 之间的基础能力比较；会话页展示当前 Agent 的入口和本次会话覆盖。两处都不得硬编码 Agent 能力。

会话页只摘要影响当前使用的问题，例如“2 项缺失”，不展示十个常驻徽章。完整状态在 Agent 详情面板中查看。

## 扩展方式

能力 ID 可以按稳定命名空间扩展，例如：

```text
memory.inspect
memory.history
storage.inventory
storage.usage
message_send
liveness
```

记忆与本地存储分析涉及敏感路径、内容读取、保留策略和删除语义，应单独设计能力组与权限边界，不塞进 `replay` 或 `discovery`。

新增能力时必须：

1. 给出不依赖某个 Agent 的精确定义。
2. 明确允许的状态和会话级覆盖规则。
3. 增加契约校验及至少一个支持、一个不支持案例。
4. 为全部已注册 Agent 补充显式声明。
5. 再由 API 和 UI 自动呈现。

## 落地顺序

1. 定义类型、十项 ID 和契约校验，不改变 UI。
2. 为六个现有 reader 补齐能力声明及原因。
3. 引入共享符合性测试，并让六个适配器通过契约层检查。
4. 逐步补足 fixture 行为层，不把旧测试一次性重写。
5. 暴露 Agent 能力 API。
6. 设置页和会话页改为消费 API。
7. 删除任何重复的前端或说明性能力表。

每一步都应独立可测试，不能先上线宣传表再等待适配器声明追上。
