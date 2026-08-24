# 未知变更托管平台 OpenAPI 自动适配 V1 详细设计

> **状态**：待实现设计；作为 V1 开发与验收依据
> **设计日期**：2026-08-24
> **适用范围**：通过 Swagger / OpenAPI 和只读 API，为 SessionInsight 接入此前未知的内部 PR、MR 或 Code Review 平台
> **核心约束**：平台方不需要修改接口或遵循 SessionInsight 规范；所有适配工作由 SessionInsight 完成

---

## 1. 背景与目标

SessionInsight 当前通过内置 GitHub、GitLab Provider 获取托管变更详情，并将结果转换为统一的 `ChangeRequestSnapshot`。面对一个新的内部托管平台，当前实现仍需要增加新的 Provider 代码、重新编译并发布产品。

V1 的目标是让用户提供以下输入：

- 平台名称；
- Swagger 2.0 或 OpenAPI 3.x 定义；
- API Base URL；
- 一个真实、可由该 Token 读取的示例 PR / MR / Change URL；
- 只读 API Token。

SessionInsight 随后完成：

1. 识别变更页面 URL 的结构；
2. 从 OpenAPI 中寻找详情、文件、提交和 Diff 接口；
3. 使用示例变更执行受限的只读探测；
4. 推断平台响应字段到 SessionInsight 标准模型的映射；
5. 对无法可靠判断的字段请求一次性确认；
6. 保存可复用的平台适配配置；
7. 让同平台其他变更 URL 可以直接查询和展示详情。

V1 的“自动适配”定义为：**自动产生并验证绝大部分适配配置，只在语义存在歧义时要求用户确认；配置激活后，日常查询完全自动。**

Swagger 只描述接口结构和类型，通常不能证明 `source.sha` 是 Head SHA、`base.sha` 是目标分支最新提交还是 Diff Base。因此 V1 不承诺仅凭 Swagger 在所有平台上实现无确认、100% 准确的语义映射。

---

## 2. V1 范围

### 2.1 必须支持

- Swagger 2.0、OpenAPI 3.0 和 OpenAPI 3.1；
- REST API；
- JSON 详情、文件和提交响应；
- JSON 中内嵌的 patch，或独立返回的 unified diff 文本；
- HTTPS；HTTP 和私有网络地址继续使用现有显式 Host Approval；
- `GET`、`HEAD` 只读接口；
- Bearer Token；
- OpenAPI `securitySchemes` 声明的自定义 Header Token，例如 `PRIVATE-TOKEN` 或 `X-API-Key`；
- 常见页码、Link Header 和 Cursor 分页；
- 元数据、文件集合、patch、文件 mode、提交五个维度独立降级；
- Profile 失效和响应结构漂移检测；
- 同一 Host 在任意时刻只有一个 Active Profile。

### 2.2 明确不支持

- GraphQL；
- 必须通过 `POST` 才能读取数据的接口；
- OAuth 登录流程、Cookie、mTLS；
- Token 放在 URL Query 中；
- JavaScript、模板脚本或任意用户代码转换；
- Webhook 和主动后台同步；
- 一个变更的数据需要跨多个未共同批准的服务拼装；
- 运行时调用大模型重新猜测字段；
- Profile 运行期间静默切换到另一个相似字段；
- 不经过示例响应验证，仅凭低置信度 Swagger 推断直接激活。

### 2.3 后续版本候选

- 语义只读的 `POST` 查询；
- GraphQL Schema 与 Query Profile；
- 多示例变更联合验证；
- OAuth Device Flow；
- Profile 导出、导入和团队共享；
- 已知平台模板库；
- OpenAPI 源地址定期漂移检查。

---

## 3. 总体架构

```text
OpenAPI 文档 ---------> OpenAPI 分析器 --------+
                                              |
示例变更 URL ---------> URL 模板分析器 --------+--> 只读探测器
                                              |         |
凭据引用 --------------> 安全 HTTP Client -----+         v
                                                   字段推断与验证
                                                         |
                                                         v
                                                Draft Provider Profile
                                                         |
                                              用户确认歧义字段
                                                         |
                                                         v
                                                Active Provider Profile
                                                         |
                                                         v
                                                OpenAPI Runtime Provider
                                                         |
                                                         v
                                              ChangeRequestSnapshot
                                                         |
                                                         v
                                        现有变更详情、Diff 与会话关联 UI
```

架构分为两条路径：

1. **配置生成路径**：导入 OpenAPI、探测示例、推断并验证 Profile；
2. **正常查询路径**：只执行已经激活、版本固定的 Profile，不再做接口发现和字段猜测。

这两条路径必须隔离。推断逻辑的变化不能改变一个已激活 Profile 的运行时含义。

---

## 4. 核心领域决策

### 4.1 使用统一 `openapi` Provider Kind

新增：

```go
const ChangeProviderOpenAPI ChangeProviderKind = "openapi"
```

未知内部平台不增加新的 Provider 枚举，而由以下身份区分：

- `provider = openapi`：执行引擎类型；
- `host_id`：具体托管服务实例；
- `profile_id`：该服务使用的不可变适配配置版本。

示例：

```text
provider:   openapi
host_id:    review-company-internal
profile_id: profile-01J...
```

这可以继续使用现有 Provider、Host、Snapshot 和 evidence 模型，同时避免每接入一个平台就修改 Go 枚举和前端代码。

### 4.2 Adapter 与 Profile 分离

- `OpenAPIProvider` 是产品内置的通用执行引擎；
- `ProviderProfile` 是用户导入并经过验证的声明式配置；
- Profile 不包含可执行代码；
- Profile 每个已激活 revision 都是不可变的；
- 修改映射会创建新 revision，不能原地改变历史快照的解释方式。

### 4.3 Reference 必须携带 `host_id`

当前 `ChangeRequestReference` 只携带 Provider 和 Display Origin。V1 增加可选 `host_id`：

```go
type ChangeRequestReference struct {
    Provider             ChangeProviderKind `json:"provider"`
    HostID               string             `json:"host_id,omitempty"`
    DisplayOrigin        string             `json:"display_origin"`
    TargetRepositorySlug string             `json:"target_repository_slug,omitempty"`
    DisplayNumber        string             `json:"display_number,omitempty"`
    NormalizedURL        string             `json:"normalized_url"`
}
```

内置 GitHub/GitLab 可以同步填充固定 Host ID；OpenAPI Profile 必须填充自己的 Host ID。服务端根据 Host ID 选择 Profile，不能只根据 `provider = openapi` 选择。

### 4.4 不引入第二套展示模型

OpenAPI Provider 最终仍输出：

- `ChangeRequestSummary`；
- `ChangeRequestSnapshot`；
- `GitFileChange`；
- `GitCandidateCommit`；
- `ChangeRequestCompleteness`。

现有存储、详情展示、Diff 展开和会话关联逻辑继续消费这些标准模型。前端不得为 OpenAPI 平台维护独立字段模型或平台能力矩阵。

---

## 5. Provider Profile 契约

### 5.1 顶层结构

```json
{
  "schema_version": 1,
  "profile_id": "profile-01J...",
  "profile_revision": 1,
  "display_name": "Internal Review",
  "adapter": "openapi",
  "host_id": "review-company-internal",
  "display_origin": "https://review.internal",
  "endpoint_origins": [
    "https://review.internal",
    "https://api.review.internal"
  ],
  "reference": {},
  "authentication": {},
  "operations": {},
  "capabilities": {},
  "limits": {},
  "spec_digest": "sha256:...",
  "verified_at": "2026-08-24T12:00:00Z"
}
```

### 5.2 Reference 模板

```json
{
  "reference": {
    "origin": "https://review.internal",
    "path_template": "/projects/{repository}/reviews/{number}",
    "repository_parameter": "repository",
    "number_parameter": "number"
  }
}
```

URL Parser 必须：

- 精确匹配 scheme、host 和 port；
- 对字面路径段进行精确匹配；
- 只对声明的参数段进行 percent decode；
- 拒绝 userinfo、fragment、路径穿越和控制字符；
- 从 Normalized URL 中移除无关 Query；
- 不把 Query 中的 Token 或其他敏感值写入 Reference；
- 一个 URL 同时匹配多个 Active Profile 时返回歧义错误，不任意选择。

### 5.3 Authentication

```json
{
  "authentication": {
    "scheme": "header",
    "header_name": "Authorization",
    "value_prefix": "Bearer ",
    "credential_reference": "keyring:profile-01J..."
  }
}
```

允许的 V1 认证形式：

```text
Authorization: Bearer <secret>
Authorization: token <secret>
PRIVATE-TOKEN: <secret>
X-API-Key: <secret>
```

Header 名必须来自导入文档中的 Security Scheme，或属于产品审核过的受限 Header 集合。Profile API 不返回 `credential_reference` 的具体值，只返回认证是否已配置及认证模式。

现有 `AuthorizationSource` 只返回 `Authorization` Header，V1 需要将其收敛为“凭据来源返回 secret，受验证的 Profile 决定 Header 名和前缀”。凭据来源不能自行注入任意 Header。

### 5.4 Operation

每个 Operation 使用相同结构：

```json
{
  "method": "GET",
  "origin": "https://api.review.internal",
  "path_template": "/api/projects/{repository}/reviews/{number}",
  "parameters": {
    "repository": "reference.repository",
    "number": "reference.number"
  },
  "headers": {
    "Accept": "application/json"
  },
  "pagination": {
    "mode": "none"
  },
  "response": {
    "item_pointer": "",
    "fields": {}
  }
}
```

Operation 只能引用：

- 从变更 URL 解析出的参数；
- 上一个已验证 Operation 输出的稳定身份字段；
- Profile 中的固定非敏感参数；
- 分页器产生的页码或 Cursor。

不能从任意用户输入拼接完整 URL，不能覆盖 scheme 或 host。

### 5.5 字段选择器

V1 使用受限 JSON Pointer，不实现完整 JSONPath。对象字段示例：

```json
{
  "fields": {
    "provider_object_id": "/id",
    "display_number": "/number",
    "title": "/title",
    "lifecycle_state": "/state",
    "head_sha": "/source/latestCommit/id",
    "target_ref": "/destination/branch/name"
  }
}
```

列表接口先声明数组位置，字段选择器再相对于单个元素：

```json
{
  "items_pointer": "/values",
  "fields": {
    "path": "/new/path",
    "old_path": "/old/path",
    "status": "/status",
    "patch": "/diff"
  }
}
```

允许的固定转换：

- `string`；
- `integer_to_string`；
- `boolean`；
- `rfc3339_time`；
- `unix_time`；
- `lowercase`；
- `coalesce`；
- `enum_map`；
- `git_sha`；
- `repository_slug`；
- `change_status`；
- `file_status`。

转换由结构化参数描述，不接受表达式字符串或脚本。

### 5.6 分页

V1 支持：

| Mode | 输入 | 下一页来源 |
| --- | --- | --- |
| `none` | 无 | 无 |
| `page_number` | page / per-page Query | 响应数量或总页数 |
| `link_header` | 可选 page Query | RFC Link Header 中的 next |
| `cursor_body` | cursor Query | JSON Pointer 指定的 next cursor |
| `cursor_header` | cursor Query | 指定响应 Header |

分页必须同时受以下上限约束：

- Profile 声明的 Provider 原生上限；
- SessionInsight 独立安全上限；
- 最大页数；
- 最大累计响应字节；
- 最大文件数；
- 最大提交数。

达到安全上限时保留已获取的数据，只降低受影响维度的 completeness，不能把截断结果标为 exact。

---

## 6. 标准字段与激活门槛

### 6.1 最低激活要求

| 标准字段 | 可能来源 | V1 要求 |
| --- | --- | --- |
| Web URL | 示例 URL或详情响应 | 必须 |
| Display Number | URL或详情响应 | 必须 |
| Provider Object ID | 详情响应 | 必须 |
| Title | 详情响应 | 必须 |
| Lifecycle State | 详情响应 | 必须；未知值可映射为 `unknown` |
| Target Repository Slug | URL或详情响应 | 必须 |
| Target Repository Stable ID | 详情响应 | 推荐 |
| Head SHA 或原生内容版本 | 详情响应 | 至少一个必须 |
| Source / Target Ref | 详情响应 | 推荐 |
| File List | 文件或 Diff 接口 | 可选 |
| Commit List | Commit 接口 | 可选 |
| Patch | 文件字段或 Diff 接口 | 可选 |

至少需要一个稳定的内容锚点，例如 Head SHA、Diff Version 或平台原生内容 revision。`updated_at`、ETag、标题或状态不能单独作为内容版本，因为它们可能只反映元数据变化。

### 6.2 仓库稳定 ID 降级

优先使用平台返回的不可变 Repository ID。如果平台只暴露 slug，V1 可以生成：

```text
slug:<normalized-repository-slug>
```

但必须：

- 将身份相关 Assessment 标记为 `estimated`；
- 在激活页面提示仓库重命名可能产生新身份；
- 不声称该值是平台原生 immutable ID。

### 6.3 能力降级

| 可获得的数据 | V1 行为 |
| --- | --- |
| 只有详情和内容锚点 | 展示元数据；文件、patch、mode、提交不可用 |
| 有文件列表但没有 patch | 展示文件路径和状态；不能展开 Diff |
| 有 patch 但没有 mode | 展示 Diff；mode 标记不可用 |
| 没有 Commit 接口 | Commit 维度标记不可用 |
| 文件或提交达到上限 | 保留已获取数据；对应维度标记不完整 |
| 生命周期值未知 | 展示 `unknown`，不把请求整体判为失败 |

Profile 的运行时能力声明是前端唯一事实来源，不得增加按平台名称判断的第二套能力矩阵。

---

## 7. 自动推断流程

### 7.1 导入和标准化 OpenAPI

1. 验证文件格式、版本、大小和文档深度；
2. 将 Swagger 2.0 标准化为内部统一表示；
3. 解析 `servers`、`host`、`basePath`、`paths`、参数和 Response；
4. 解析 `securitySchemes`；
5. 解析本地 `$ref`；
6. 默认拒绝外部 `$ref`，避免导入过程产生未批准网络请求；
7. 提取 GET/HEAD Operation；
8. 记录 Response Schema、描述、Example 和分页线索；
9. 计算规范化文档的 SHA-256 digest。

OpenAPI 原文默认不持久化。数据库只保存文档摘要、导出的 Profile 和脱敏推断报告。用户重新编辑时可以再次上传原文。

### 7.2 分析示例变更 URL

示例变更是平台中真实存在、Token 可读取的一条 PR / MR / Change，例如：

```text
https://review.internal/projects/team/repo/pulls/1234
```

推荐使用已关闭或内容稳定、文件和提交数量适中、且不包含高敏感内容的测试变更。

分析器提取：

- Display Origin；
- 字面路径段；
- Repository Slug 候选；
- Display Number 或 Opaque ID 候选。

如果页面 URL 与 API Path 不同，则结合参数名称、Schema 类型、Example 和真实值进行绑定。最终 Profile 保存模板，不保存示例编号。

如果没有示例 URL，但 OpenAPI 提供完整 Example，系统可以生成 Draft Profile；在对真实变更完成探测前不能激活。

### 7.3 Operation 候选评分

每个 GET/HEAD Operation 根据以下证据评分：

- `operationId` 是否包含 pull、merge、review、change 等领域词；
- Tag 和描述是否表达详情、文件、提交或 Diff；
- Path 是否包含 project/repository 和 change number 参数；
- Response 是单对象还是列表；
- Schema 是否包含 title、state、source、target、commit、path 等字段；
- 示例 URL 参数能否完整绑定；
- Operation Origin 是否在待批准范围内；
- 是否存在无法安全绑定的必需参数。

分别生成 `resolve_change`、`list_files`、`list_commits`、`get_diff` 和可选 `resolve_repository` 候选。

### 7.4 只读探测

探测前必须向用户展示 Endpoint Origins、HTTP / 私有网络风险、Operation 数量和认证模式，但不展示 Token。

探测器只执行：

- GET/HEAD；
- 已批准 Origin；
- 必需参数可以安全绑定的请求；
- 每类得分最高且超过最低阈值的前三个候选。

探测响应只保存在内存中。日志不得记录 Token、认证 Header、原始响应、完整 Query 或可能含敏感参数的完整 URL。

### 7.5 字段候选评分

字段映射综合字段名称和路径、Schema 类型和描述、OpenAPI Example、实际响应值、示例 URL 已知值以及不同接口之间的交叉一致性。

例如 Head SHA 候选可能包括：

```text
/head/sha
/source/commit/id
/latestRevision
/fromRef/latestCommit
```

候选值必须满足 Git SHA 格式，并尽可能与 Commit 或 Diff 响应相互验证。字段名相似但值不满足领域约束时必须降低分数。

建议置信度阈值：

- `>= 0.90`：自动选择；
- `0.65–0.89`：要求用户确认；
- `< 0.65`：不映射。

必填字段存在多个相近候选、候选值互相冲突或低于阈值时，Profile 不能自动进入 Verified。

### 7.6 交叉验证

生成 Profile 前至少执行：

- 返回的 Display Number 与示例 URL 一致；
- Web URL 属于 Display Origin；
- Repository 与 URL 中的路径一致；
- Head/Base/Diff Base SHA 格式合法；
- File Status 可以映射为标准状态；
- 文件列表没有非法路径或重复身份；
- Commit 列表没有重复 SHA；
- Commit 列表与 Head SHA 不存在无法解释的矛盾；
- 第二页数据没有重复第一页；
- 连续两次读取的 Object ID 和 Repository ID 保持稳定；
- Snapshot 前后再次读取详情时，内容锚点没有变化。

最后一项继续沿用现有 Capture Race 防护：采集期间内容锚点发生变化时放弃发布快照，要求重试。

---

## 8. Profile 生命周期与漂移

```text
draft --> verified --> active --> degraded
  |          |           |
  +-------> invalid      +------> revoked
```

| 状态 | 含义 |
| --- | --- |
| `draft` | OpenAPI 已解析，但尚未完成真实探测或必填映射 |
| `verified` | 示例变更已通过验证，等待用户确认启用 |
| `active` | 参与 URL 解析和详情查询 |
| `degraded` | 运行时响应缺字段、类型变化或接口失效 |
| `invalid` | 必需映射无法成立 |
| `revoked` | 用户停用该配置 |

运行时发现漂移时：

1. 当前 Operation 返回结构化安全错误；
2. 只降低真正受影响的能力维度；
3. 必填身份或内容锚点失效时拒绝发布新快照；
4. 记录 Profile ID、Operation ID、错误码、时间、状态码和字节数；
5. 不记录原始响应或凭据；
6. 将 Profile 标记为 degraded；
7. 不自动改映射；
8. 用户重新导入或重新探测后生成新 revision。

已有快照继续绑定原 Profile Revision，不因新 Profile 激活而改变。

---

## 9. 凭据与网络安全

### 9.1 凭据存储

支持 OS Keyring 和环境变量引用。数据库只保存类似 `keyring:profile-01J...` 或 `env:INTERNAL_REVIEW_TOKEN` 的引用。

对外 DTO 只返回：

```json
{
  "authentication_configured": true,
  "authentication_mode": "os_keyring"
}
```

不能返回 Token、Credential Reference 名称、Header 值或 Keyring Key。

### 9.2 SSRF 与 Origin 控制

- OpenAPI 不能自动扩大 Endpoint Origin 白名单；
- 所有 Origin 在探测前展示并经过 Host Approval；
- 私有网络、HTTP 地址继续要求单独确认；
- DNS 在批准时解析并固定，沿用现有 Pinned Dialer；
- Redirect 时移除所有认证信息；
- Redirect 目标仍必须属于批准的 Origin；
- 外部 `$ref` 默认禁用；
- API 响应中的 next URL 只能在原批准 Origin 内使用；
- URL userinfo、fragment 和 Query Token 一律拒绝。

### 9.3 执行限制

- GET/HEAD only；
- 单请求和连接超时；
- 每响应体与累计响应字节限制；
- 最大并发、分页、文件数和提交数限制；
- Profile 文档大小、引用深度和 Operation 数量限制；
- JSON 解析深度和字符串长度限制。

SessionInsight 安全上限始终优先于平台声明上限。

---

## 10. 数据库设计

新增 `change_host_profiles`：

```sql
CREATE TABLE change_host_profiles (
    profile_id TEXT PRIMARY KEY,
    host_id TEXT NOT NULL,
    profile_revision INTEGER NOT NULL,
    schema_version INTEGER NOT NULL,
    display_name TEXT NOT NULL,
    lifecycle TEXT NOT NULL,
    profile_json TEXT NOT NULL,
    inference_report_json TEXT NOT NULL,
    spec_digest TEXT NOT NULL,
    spec_version TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    verified_at TEXT,
    activated_at TEXT,
    last_success_at TEXT,
    last_failure_at TEXT,
    last_failure_code TEXT NOT NULL DEFAULT '',
    UNIQUE (host_id, profile_revision),
    FOREIGN KEY (host_id) REFERENCES change_hosts(host_id) ON DELETE RESTRICT
);
```

约束要求：

- 同一 Host 最多一个 `active` Profile；
- Profile JSON 必须通过 schema version 对应的结构化校验；
- Active Profile 不允许原地修改 `profile_json`；
- 重新推断或修改映射创建新 revision；
- Profile 不能删除仍被历史快照引用的 revision；
- `change_hosts.provider` 和相关表约束增加 `openapi`；
- 启用现有 `change_hosts.credential_reference`，但不把它投影到 API DTO。

V1 推荐在快照记录中保存 Profile ID / Revision，以便诊断历史数据由哪一版映射生成；该字段不参与用户可见 Change Request Identity。

以下内容不得持久化：Token、原始探测响应、原始 API 错误响应、未脱敏 OpenAPI Example，以及完整 OpenAPI 原文。

---

## 11. Registry 与运行时调用链

### 11.1 Registry 改造

当前 Registry 使用 `map[ChangeProviderKind]ReferenceParser`，同一种 Provider 只能注册一个 Parser。V1 需要拆分：

- 内置 Adapter Factory：按 `provider kind` 注册执行引擎；
- Host-bound Parser：按 `host_id/profile_id` 注册多个 URL Parser；
- Provider 创建：根据已批准 Host 和 Active Profile 创建；
- Profile 激活、撤销后原子刷新动态 Parser 集合。

建议接口方向：

```go
type RegisteredReferenceParser struct {
    ID     string
    HostID string
    Parser ReferenceParser
}

func (r *Registry) RegisterHostParser(parser RegisteredReferenceParser) error
func (r *Registry) ReplaceHostParsers(parsers []RegisteredReferenceParser) error
func (r *Registry) NewProviderForHost(host HostIdentity, client *HTTPClient, profile *ProviderProfile) (Provider, error)
```

具体命名可以调整，但选择 Provider 的依据必须包含 Host ID，不能继续只依赖 Provider Kind。

### 11.2 正常查询流程

1. 内置 GitHub/GitLab Parser 尝试匹配；
2. Active Profile Parser 尝试匹配；
3. 没有自动 Provider 匹配时才回退到 Generic URL；
4. 多个自动 Parser 匹配时返回 ambiguous；
5. Reference 携带 Host ID；
6. 服务端读取 Host 和 Active Profile；
7. 检查 Host Approval；
8. 根据 Credential Reference 获取 secret；
9. 创建 Origin-scoped 安全 HTTP Client；
10. 创建 `OpenAPIProvider`；
11. `Resolve()` 获取并验证详情；
12. `GetSnapshot()` 获取详情、文件、提交和 Diff；
13. 转换为现有 `ChangeRequestSnapshot`；
14. 使用现有 `SyncSnapshot` 原子发布；
15. 前端通过现有变更详情和会话关联流程展示。

### 11.3 Metadata-only Snapshot

如果 Profile 只有详情接口，但存在可靠内容锚点，Provider 仍可生成合法 Snapshot：

- `Files` 和 `Commits` 必须是空数组而不是 `null`；
- Metadata 使用真实 Assessment；
- FileSet、Patches、Modes、Commits 标记 unavailable；
- Content Version 由稳定内容锚点生成；
- 不允许用 `updated_at` 单独生成 Content Version。

---

## 12. 后端 API

建议增加：

```text
POST   /api/change-host-profiles/import
GET    /api/change-host-profiles
GET    /api/change-host-profiles/{profileId}
POST   /api/change-host-profiles/{profileId}/probe
PATCH  /api/change-host-profiles/{profileId}/mapping
POST   /api/change-host-profiles/{profileId}/verify
POST   /api/change-host-profiles/{profileId}/activate
POST   /api/change-host-profiles/{profileId}/test
POST   /api/change-host-profiles/{profileId}/revoke
```

### 12.1 Import

使用 bounded multipart 请求接收 OpenAPI 文件、Display Name、API Base URL、示例变更 URL、Credential Mode，以及可选的一次性 Token 或环境变量引用。

一次性 Token 在写入 Keyring 后立即从内存结构中清除，不进入 Profile JSON、普通数据库或日志。Import 返回 Draft Profile 摘要、待批准 Origins 和静态 Operation 候选，不执行网络探测。

### 12.2 Probe

Probe 在 Host/Origin 已明确批准后执行只读调用，返回：

```json
{
  "operations": [],
  "field_candidates": [],
  "capabilities": {},
  "required_confirmations": [],
  "warnings": []
}
```

响应只包含脱敏的字段路径、类型、置信度和验证结果，不包含原始响应值。对识别所必需的示例值，只返回类型、长度、格式或哈希摘要。

### 12.3 Mapping、Verify 与 Activate

- Mapping 只允许选择探测报告中的候选，或使用受限 JSON Pointer 和固定转换；
- Verify 对完整 Draft Profile 进行无副作用测试；
- Activate 必须确认 Host Approval、Credential、必填映射和示例测试都成功；
- Activate 原子替换同 Host 的旧 Active Profile；
- 旧 Profile Revision 保留用于历史诊断。

---

## 13. 前端交互

增加“变更平台”管理入口，使用五步向导：

1. **基本信息**：平台名称、OpenAPI 文件、API Base URL、示例变更 URL；
2. **网络和认证**：展示 Origins 和网络风险，配置 Keyring Token 或环境变量引用；
3. **自动分析**：展示接口候选、只读探测进度和结构化拒绝原因；
4. **映射确认**：折叠高置信度字段，要求确认中置信度字段，必填字段未确定时禁止继续；
5. **测试并启用**：展示最终运行时能力和精度警告。

能力摘要示例：

```text
PR metadata      Supported
File list        Supported
Patch            Unsupported
File modes       Unsupported
Commits          Supported
```

所有文案进入 `frontend/src/i18n.tsx` 的全部 locale，并通过 `t(...)` 渲染。能力数据来自后端 Profile 声明，前端不按平台名称判断能力。

---

## 14. 错误与可观测性

新增稳定错误码建议：

| Code | 场景 |
| --- | --- |
| `openapi_document_invalid` | 文档无法解析或超限 |
| `openapi_external_reference_rejected` | 文档引用未批准外部资源 |
| `change_profile_mapping_incomplete` | 必填字段未完成映射 |
| `change_profile_mapping_ambiguous` | 必填字段存在多个候选 |
| `change_profile_probe_failed` | 示例探测失败 |
| `change_profile_schema_drift` | 已激活字段路径或类型失效 |
| `change_profile_reference_ambiguous` | 多个 Profile 匹配同一 URL |
| `change_profile_content_anchor_missing` | 没有可靠内容版本锚点 |
| `change_profile_credential_unavailable` | Credential Reference 无法读取 |

日志可以记录 Profile ID / Revision、Operation ID、响应状态码、延迟、页数、字节数、稳定错误码和是否发生 schema drift。

日志不得记录 Secret、认证 Header、Credential Reference 名称、原始响应、完整请求 URL、敏感 Query 或未经脱敏的 OpenAPI Example。

---

## 15. 当前代码改造清单

| 位置 | 当前限制 | V1 改造 |
| --- | --- | --- |
| `internal/model/git_evidence.go` | Provider 是关闭枚举，Reference 无 Host ID | 增加 `openapi` 和 `host_id` |
| `internal/db/git_association_migration.go` | SQLite CHECK 不允许 `openapi` | 迁移 Provider 约束，新增 Profile 表 |
| `internal/changehost/registry.go` | 每个 Kind 只能有一个 Parser / Factory | 支持多个 Host-bound Profile Parser |
| `internal/changehost/contract.go` | Provider 契约可复用 | 增加 Profile 驱动实现，不增加写操作 |
| `internal/changehost/httpclient.go` | 只支持 Authorization 值 | 支持经过 Profile 校验的自定义认证 Header |
| `internal/server/change_hosts.go` | `PublicHost` 硬编码 GitHub/GitLab | 根据 Reference Host ID 和 DB Profile 选择 Host |
| `internal/server/change_hosts.go` | HTTP Client 的 Authorization Source 为 `nil` | 接入 Keyring / Environment Credential Source |
| `frontend/src/components/ChangeRequestLookupDialog.tsx` | Hosted Details 限制 GitHub/GitLab | 改为读取 Host/Profile 运行时能力 |
| `frontend/src/api.ts` | 只有 Host Preview/Approve | 增加 Profile 导入、探测、验证和激活 API |
| `frontend/src/gitEvidence.ts` | 无 Profile DTO | 增加 credential-safe Profile DTO |

建议新增目录：

```text
internal/changehost/openapi/
    document.go
    normalize.go
    candidates.go
    inference.go
    profile.go
    profile_validate.go
    parser.go
    provider.go
    pagination.go
    transform.go
```

文件可以按实现规模合并，但文档解析、推断和运行时执行必须保持清晰边界。

---

## 16. 测试设计

### 16.1 OpenAPI 解析与推断

使用脱敏 fixture 覆盖：

- Swagger 2.0、OpenAPI 3.0 / 3.1；
- 内联 Schema、本地 `$ref` 和被拒绝的外部 `$ref`；
- Bearer 和自定义 Header Token；
- Page、Link 和 Cursor 分页；
- 对象和数组响应；
- 嵌套 head/base 字段；
- 文件内嵌 patch 和独立 unified diff；
- 缺少 Repository ID；
- 多个同分候选；
- 错误类型和 Schema Drift。

### 16.2 Provider 共享契约

OpenAPI Provider 必须复用 Provider 共享验证，覆盖每个声明能力：Parse Reference、Parse Remote、Resolve Repository、Resolve Change、Discover Head / Commit、Snapshot Metadata、File Set、Patches、Modes 和 Commits。

V1 未配置的 Discovery 等能力必须明确声明 unsupported 并携带稳定 reason，不能从前端或平台名推断。

### 16.3 安全测试

- 未批准 Origin 和私有 IP；
- DNS Rebinding；
- Redirect 泄露 Header；
- Query Token、userinfo URL 和 Path Traversal；
- 外部 `$ref` SSRF；
- 超大文档、响应和分页；
- 恶意 Header 名；
- Token 不进入 DB、日志和 API DTO；
- Profile 不能执行代码或扩大 Origin。

### 16.4 数据库与 API

- Provider 枚举迁移；
- Profile Revision 不可变；
- 每 Host 只有一个 Active Profile；
- 激活原子切换；
- 被历史快照引用的 Profile 不被删除；
- credential-safe DTO；
- Profile degraded / revoked 状态；
- 服务重启后动态 Parser 恢复。

### 16.5 前端与集成

- durable 逻辑测试加入 `frontend/package.json` 的 `test` aggregator；
- 运行 `test:i18n` 和 `test:i18n-source`；
- 完整应用连接后端的 Playwright 流程；
- `en` 和 `zh-CN` 都验证导入、探测、确认、激活、错误和 degraded 状态；
- 断言 Token 不会回显到 DOM、网络响应或错误提示。

建立仅测试使用的本地 HTTP Fixture Server，至少提供两种虚构平台：

1. 字段接近 GitHub，但 URL 和认证不同；
2. 字段完全不同、嵌套较深且使用 Cursor 分页。

端到端测试必须证明第二种平台无需增加专用 Provider 代码即可完成适配。

---

## 17. 验收标准

V1 完成需要同时满足：

1. 导入一份产品此前未知的 Swagger / OpenAPI 文档；
2. 不修改目标平台 API；
3. 使用一个示例变更完成安全只读探测；
4. 用户不需要编写代码或表达式；
5. 高置信度字段自动完成，歧义字段一次性确认；
6. 激活后输入同平台另一个变更 URL 能展示详情；
7. 有文件接口时展示文件列表；
8. 有 patch 时可以展开 Diff；
9. 缺失能力准确降级，不伪造数据；
10. 没有可靠内容锚点时拒绝发布权威快照；
11. Token 不出现在普通数据库字段、API 响应、日志和 URL 中；
12. OpenAPI 中的恶意 Origin、外部 `$ref` 和写接口不能执行；
13. API 字段变化后 Profile 进入 degraded，不静默错误映射；
14. GitHub/GitLab 现有能力不回归；
15. 全部用户文案在所有 locale 中完整；
16. 英文和中文主流程通过完整应用 Playwright 验证。

---

## 18. 推荐实施拆分

### PR 1：基础模型与持久化

- `openapi` Provider Kind；
- Reference Host ID；
- Profile 数据契约和校验；
- Profile 表和迁移；
- Registry 支持 Host-bound Parser；
- Credential Reference 接口。

### PR 2：导入、探测与推断

- OpenAPI 标准化；
- Operation 和字段候选评分；
- 受限 Probe；
- 脱敏推断报告；
- Draft / Verified 生命周期。

### PR 3：运行时 OpenAPI Provider

- Resolve 和 Snapshot；
- 分页、Patch 和 Commit 转换；
- Completeness 降级；
- Capture Race 与 Drift Detection；
- Provider conformance 与安全测试。

### PR 4：配置界面与完整验收

- 五步配置向导；
- Mapping 确认；
- Capability Summary；
- Active / Degraded / Revoked 管理；
- 全 locale 文案；
- Playwright 中英文端到端验证。

每个 PR 都必须保持 GitHub/GitLab 原路径可用。运行时能力声明是 UI 的唯一事实来源，不得在 PR 之间引入临时前端平台矩阵。

---

## 19. V1 成功标准总结

第一版不以“仅凭任意 Swagger 100% 无确认适配”为目标，而以以下结果为准：

> 对结构合理的 REST/OpenAPI 平台，SessionInsight 自动完成接口发现、字段映射和真实响应验证；平台方无需改造 API，用户只在存在语义歧义时确认一次，之后该平台可以作为正常变更托管服务长期使用。

这一边界能够先交付真正可用的通用能力，同时为后续根据真实平台反馈增加推断规则、分页类型、认证方式和模板库保留稳定扩展点。
