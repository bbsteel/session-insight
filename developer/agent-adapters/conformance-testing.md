# 共享 Agent 符合性测试方案

## 目标

共享符合性测试用于证明：

1. 能力契约结构完整；
2. 声明与 Go 接口实现一致；
3. 适配器在脱敏 fixture 上表现符合声明；
4. 新增 Agent 不会因为复制旧测试而漏掉通用边界。

它不是替代适配器自己的格式解析测试，而是在所有适配器之上提供同一组最低验收标准。

## 分层设计

### 第一层：契约检查

不读取真实用户目录，只检查 reader 和能力声明：

- Agent 标识、显示名和适配器修订合法；
- 十项基线能力完整；
- 状态和原因码合法；
- 静态声明没有 `missing`；
- 操作能力与可选 Go 接口一致；
- 禁止 `terminate = estimated` 等危险组合。

这一层应能立即覆盖六个现有适配器。

### 第二层：通用行为检查

对调用方提供的 fixture 运行统一断言：

- `ListSessions` 可重复且顺序稳定；
- 列表和详情的 Agent 类型、会话 ID、时间一致；
- 会话 ID 非空且唯一；
- `GetSession` 对未知 ID 返回明确错误；
- 回合和事件顺序稳定；
- 时间戳处于合理范围；
- 有效零值不会变成缺失；
- 相同 fixture 重读不会产生重复事件；
- 渲染不 panic，空记录与不支持可区分。

### 第三层：能力行为检查

这一层必须按顺序执行两个阶段，不能用“没有 fixture”作为跳过理由：

1. **Fixture 覆盖门禁**：枚举所有声明为 `exact` 或 `estimated` 的能力，检查其所需 fixture 是否存在并包含对应能力证据；任何缺口立即失败。
2. **行为验证**：覆盖门禁通过后，仅对已经确认存在的 fixture 运行相应行为断言。

行为断言包括：

- `realtime`：记录变化后 revision 单调变化，未变化时保持稳定；
- `tokens`：精确字段保持 presence，异常退出按预期标记缺失；
- `tool_results`：调用与结果可关联，失败/拒绝状态不丢失；
- `diff`：路径、旧内容、新内容及 replace-all 语义正确；
- `subtasks`：父子 ID 稳定，不把子任务重复列为根会话；
- `resume`：恢复 ID 稳定且不误用文件名；
- `delete`：只删除 fixture 沙箱中的目标及明确关联记录；
- `terminate`：只针对注入的假进程查找器测试目标解析，CI 不终止真实 Agent。

`discovery` 的平台路径逻辑使用临时主目录和环境注入测试，不能依赖运行测试机器恰好安装某个 Agent。

## 推荐包结构

```text
internal/reader/adaptertest/
├── contract.go
├── behavior.go
├── capabilities.go
├── fixtures.go
└── report.go
```

`adaptertest` 可以导入 `internal/model` 和叶子能力包，但不能导入父包 `internal/reader`。当前 `reader/registry.go` 会导入所有具体适配器；若适配器测试再通过 `adaptertest` 反向导入 `reader`，会形成测试期循环依赖。

共享包应声明满足测试所需的最小结构化接口；Go 的隐式接口实现让所有 `BaseSessionReader` 实现无需适配即可传入：

```go
type Reader interface {
    AgentType() string
    DisplayName() string
    ListSessions() ([]model.Session, error)
    GetSession(id string) (*model.SessionDetail, error)
    RenderANSI(id string, cols int) (string, error)
    GetRenderEvents(id string) ([]model.RenderEvent, error)
}
```

删除、进程查找和 revision 等可选能力也在 `adaptertest` 中以相同方法签名声明局部接口。生产 reader 不导入 `adaptertest`。各适配器的 `_test.go` 文件调用共享套件。

示意 API：

```go
type FixtureSet struct {
    Basic       Fixture
    Tools       *Fixture
    Diff        *Fixture
    Subtasks    *Fixture
    Interrupted *Fixture
    Realtime    *MutableFixture
}

type Expectations struct {
    SessionCount int
    SessionIDs   []string
}

func Run(
    t *testing.T,
    newReader func(t *testing.T, fixture Fixture) Reader,
    fixtures FixtureSet,
    expected Expectations,
)
```

每个适配器的入口保持短小：

```go
func TestClaudeConformance(t *testing.T) {
    adaptertest.Run(t, newFixtureReader, adaptertest.FixtureSet{
        Basic:       fixture("testdata/basic"),
        Tools:       fixturePtr("testdata/tools"),
        Diff:        fixturePtr("testdata/diff"),
        Subtasks:    fixturePtr("testdata/subtasks"),
        Interrupted: fixturePtr("testdata/interrupted"),
    }, adaptertest.Expectations{
        SessionCount: 1,
        SessionIDs:   []string{"sanitized-session-id"},
    })
}
```

具体 API 在实现阶段可根据 SQLite 与目录型 reader 的差异调整，但必须保持：

- 工厂由适配器测试提供；
- 共享包不读取开发者真实主目录；
- fixture 缺失不能自动视为跳过；
- 支持的能力没有 fixture 时明确失败。

## Fixture 清单与元数据

每个 fixture 应附带机器可读元数据，例如 `fixture.json`：

```json
{
  "agent_type": "claude",
  "agent_format_version": "observed-2026-07",
  "scenario": "interrupted",
  "synthetic": false,
  "sanitized": true,
  "platforms": ["linux", "darwin"],
  "expected_capabilities": ["replay", "tokens", "tool_results"]
}
```

元数据不复制完整能力契约，只说明该样本可验证哪些行为。测试需要验证：

- `sanitized` 必须为 `true`；
- `agent_type` 与适配器一致；
- 在运行任何能力行为断言前，声明支持的每项能力至少被一个适用 fixture 覆盖，否则测试立即失败；
- 平台限定不会被误报为全平台验证。

不要提交真实用户名、绝对主目录、仓库远端、令牌、设备标识或未经审核的对话正文。

## 能力与测试证据映射

共享报告应生成如下映射，而不是维护人工完成百分比：

| 能力 | 声明 | 接口证据 | Fixture 证据 | 结果 |
|---|---|---|---|---|
| replay | exact | BaseSessionReader | basic | 通过 |
| realtime | estimated | LiveRevisionProvider | realtime | 通过 |
| tokens | exact | SessionDetail.Billing | interrupted, basic | 通过 |
| diff | exact | RenderEvent/EditCall | diff | 通过 |
| terminate | unsupported | — | — | 通过 |

结果规则：

- 声明支持、接口缺失：失败。
- 声明支持、fixture 缺失：失败。
- 声明不支持，却实现高风险操作接口：失败或要求显式例外。
- 声明不适用：不要求行为 fixture，但要求原因码。
- 某次会话为 `missing`：必须有 fixture 证明覆盖规则，而不是降低静态声明。

## CI 集成

第一阶段不新增独立 CLI。共享测试挂入现有 Go 测试：

```bash
go test ./internal/reader/...
```

仓库完整 CI 仍运行：

```bash
go test ./...
```

如果未来报告需要独立展示，可以从测试库提取只读报告工具：

```bash
go run ./internal/tools/adapter-report
```

报告工具必须读取同一份能力声明和测试证据索引，不能另建配置文件。只有当外部贡献者数量或接入频率证明有需要时，才考虑脚手架命令。

## 渐进迁移六个现有适配器

为了避免一次重写全部 reader 测试：

1. 先让六个适配器通过契约检查。
2. 复用现有测试数据接入基础行为检查。
3. 按能力逐步整理脱敏 fixture。
4. 每当修复一个真实解析缺陷，就补充最小回归 fixture。
5. 所有支持能力拥有 fixture 后，启用“无证据即失败”的严格门禁。

过渡期间的豁免必须是代码中带原因和到期条件的显式条目，不能使用无说明的 `t.Skip`。

## 非目标

共享符合性测试不负责：

- 评估某个 Agent 的商业质量；
- 比较 Agent 本身的功能多少；
- 访问开发者的真实会话作为 CI 输入；
- 根据通过用例数估算开发工时；
- 在 CI 中删除或终止真实 Agent 会话；
- 替代解析器针对格式细节的单元测试。

它只验证 SI 对能力所作的声明是否具有可重复的工程证据。

## 实施顺序

1. 实现纯契约检查及其自测试。
2. 为六个现有 Agent 添加声明并通过检查。
3. 实现 basic 行为套件。
4. 接入已有的最小样本。
5. 实现能力行为套件。
6. 补齐缺失 fixture，逐项开启严格门禁。
7. 在 PR 模板或 CI 摘要中输出证据映射。

每个阶段都应保持 `go test ./internal/reader/...` 可独立验证，不能依赖本机安装 Agent。
