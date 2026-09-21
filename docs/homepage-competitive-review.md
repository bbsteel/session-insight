# 首页竞品对照与 Session Insight 实现

> 核对日期：2026-09-21。对照 `../external/` 中的源码快照及各项目公开说明。这里讨论的是应用打开后的工作台，不是产品官网。

## 竞品首页实际承担什么任务

| 产品 | 首页/默认入口 | 主要内容 | 可借鉴的交互 |
| --- | --- | --- | --- |
| [AgentsView](https://github.com/kenn-io/agentsview/blob/main/docs/usage.md) | 未选会话时在会话浏览器主区域显示 Analytics Dashboard | 时间和项目等筛选、六张摘要卡、活动日历和时间线、项目与 Agent 分布、工具/技能、会话形态、健康信号、Top Sessions；侧栏仍可直接打开会话 | 首页是可下钻的历史入口；筛选、图表和会话列表联动；提供刷新与数据更新时间 |
| [Agent Trail](https://github.com/camtrik/agent-trail/blob/main/README.md) | `/` 跳转到跨 Agent Overview | 时间窗、Token 与估算成本、模型和项目排行、活动时间线、收藏会话；按 Agent 来源切换 | 强调“最近在哪里投入了时间与额度”；关键数字与可继续工作的会话放在同屏 |
| [Code Insights](https://github.com/melagiri/code-insights/blob/main/dashboard/src/pages/DashboardPage.tsx) | 独立 Dashboard 页 | 摘要指标、近期活动曲线、最近会话/洞察 feed、待分析会话提醒 | 从概览直接回到近期工作；加载、失败和空状态是首页的一部分 |
| [Claude Code History Viewer](https://github.com/jhlee0409/claude-code-history-viewer) | 项目和会话浏览为主，另有 Analytics Dashboard | 全局/项目/单会话统计、Token/费用、来源分布、日趋势和热力图 | 项目树与会话导航始终是主要入口，分析页按范围下钻 |

AgentsView 的范围最广，不能把它的图表原样搬到 SI：它的用量、工具、技能和健康面板依赖已有的跨会话聚合契约。SI 当前的 `/api/sessions` 能可靠提供会话、项目、Agent、收藏、活跃状态和最后更新时间；Token、费用、工具与错误仍主要是会话级数据。首页不能把这些字段缺失的聚合伪装成准确数字。

## 本次 SI 首页

- 保留全局搜索和左侧会话列表；未选会话时主区域展示首页，从侧栏 **SI** 按钮随时返回。
- 用近 7 / 30 天最后更新的会话计算会话数、项目数、Agent 数、按最后更新时间分组的日图和项目排行；“活跃会话”明确统计全量当前状态。
- 最近会话和项目排行都能打开原会话；项目排行打开该项目最近更新的一条会话。Ctrl/Cmd 点击会话沿用现有新标签行为。
- 首次索引尚无数据、API 失败和重试均有可见状态；首页跟随会话变更事件刷新。

### 后续扩展条件

若要进一步接近 AgentsView 的用量与工具面板，先定义跨会话聚合 API 的范围、精确度与缺失语义，再加入时间窗下钻。活动图目前按**每个会话最后更新的日期**计数，不能解释为每天发生的所有消息或工具调用。
