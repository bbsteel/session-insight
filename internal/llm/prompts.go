package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// GenerationKind selects one of the phase-1 AI tasks.
type GenerationKind string

const (
	KindSummary GenerationKind = "summary"
	KindTitle   GenerationKind = "title"
	KindHandoff GenerationKind = "handoff"
)

func ValidKind(kind string) bool {
	switch GenerationKind(kind) {
	case KindSummary, KindTitle, KindHandoff:
		return true
	}
	return false
}

const summaryInstruction = `你是一名资深工程师,正在复盘一段 AI 编程助手的会话记录。请用中文输出一份 markdown 总结。

输出格式(严格遵守):

第一段不加任何标题,用 1~2 句话直接说明这个会话做了什么、最终做到了什么程度。然后按以下固定模板输出:

## 做了什么

- 要点列表,每条以 **加粗短语** 开头概括一件事,后接一句话展开

## 关键结论与决策

- 每条写清 **决策或结论本身**,后接理由;没有明显决策就写该节下一行"无重大决策"

## 遗留问题

- 记录中能看出的未完成项、已知缺陷、被搁置的方向;看不出来就写"无明显遗留"

其他要求:只基于记录中的事实,不要推测编造;提到文件路径、命令、分支、函数名时用反引号包裹并保留原文;需要引用代码时必须用带语言标注的围栏代码块(如 ` + "```go" + `)。直接输出 markdown,不要任何前言、结语或解释。`

const titleInstruction = `请为下面这段 AI 编程助手的会话起一个中文标题,概括会话的核心任务。要求:以整个会话中占比最大、最具代表性的主题为准——会话主题可能随时间漂移,靠后集中讨论的主题往往才是会话的真正核心,开头的零散命令或一次性运维操作不算核心任务;不超过 20 个字;直接输出标题文本本身——不要引号、不要句号、不要任何解释或前缀。`

const handoffInstructionHead = `你是一名资深工程师,要把下面这段 AI 编程助手会话的工作交接给一个全新的 AI 会话继续。你的输出分为两部分。

第一部分:输出的最开头必须是一个 ` + "```json" + ` 围栏代码块,内容是交接元数据(不属于交接提示词本身),格式严格如下:

` + "```json" + `
{
  "difficulty": "简单|中等|困难 三选一",
  "difficulty_reason": "一句话说明难度判断依据",
  "recommended": [
    {"executor": "候选执行者原文", "reason": "一句话推荐理由"}
  ]
}
` + "```" + `

recommended 只能从下面的候选执行者列表中选,按优先级从高到低排列(最合适的排第一),只保留真正值得推荐的 1~3 个;结合剩余任务的难度与各模型的能力/成本权衡(简单任务优先便宜快的,困难任务才推荐最强模型)。

候选执行者列表:
`

const handoffInstructionTail = `
第二部分:json 代码块之后,输出中文"交接提示词"正文。正文会被原样复制粘贴给新会话,所以正文中绝不能出现难度评估、模型推荐等元信息,也不能出现"以上/如上"之类指代第一部分的话。正文必须自包含——新会话只凭这段文字就能无缝接手,不依赖任何未写明的上下文。结构固定:

# 任务交接

紧接标题的第一段不加小节标题,用 1~2 句话说明要接手的任务是什么、当前进展到哪。然后:

## 背景与目标
## 已完成
## 未完成 / 下一步
## 踩过的坑与注意事项
## 协作约束

其他要求:各节用要点列表,关键点 **加粗**;协作约束一节必须写明仓库路径、分支、工作目录等环境事实(从记录中提取,记录里没有就写"未知,接手后先确认");"踩过的坑"只写记录中真实出现过的失败与纠正;文件路径、命令、分支用反引号包裹保留原文。除这两部分外不要输出任何前言或解释。`

// BuildPrompt assembles the full prompt for one generation kind. Title
// generation feeds every user message (truncated, budget-capped): topic
// share across the whole session identifies the task better than the first
// turns alone, which misleads when a long session drifts to a new topic.
// handoffCandidates lists executor candidates (installed CLIs + recently
// used models) for the handoff recommendation metadata; ignored for other
// kinds.
func BuildPrompt(kind GenerationKind, detail *model.SessionDetail, handoffCandidates []string) (string, error) {
	switch kind {
	case KindSummary:
		return summaryInstruction + "\n\n" + BuildSessionContext(detail), nil
	case KindHandoff:
		var b strings.Builder
		b.WriteString(handoffInstructionHead)
		if len(handoffCandidates) == 0 {
			b.WriteString("(无候选信息——recommended 输出空数组)\n")
		}
		for _, c := range handoffCandidates {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString(handoffInstructionTail)
		b.WriteString("\n\n")
		b.WriteString(BuildHandoffContext(detail))
		return b.String(), nil
	case KindTitle:
		return titleInstruction + "\n\n" + buildTitleContext(detail), nil
	default:
		return "", fmt.Errorf("unknown generation kind %q", kind)
	}
}

// ParseHandoffOutput splits a handoff generation into the pasteable prompt
// body and the metadata JSON the model was asked to emit as a leading
// fenced json block. A few providers prepend a one-line acknowledgement
// before the block, so the first JSON fence is also accepted when it is the
// first code fence in the response. Models are unreliable formatters: when
// the block is missing or malformed the whole output is kept as content and
// metadata is empty — the feature degrades, never fails the generation.
func ParseHandoffOutput(raw string) (content, metadataJSON string) {
	trimmed := strings.TrimSpace(raw)
	rest, block := stripLeadingJSONFence(trimmed)
	if block == "" {
		return trimmed, ""
	}
	if !isHandoffMetadataJSON(block) {
		return trimmed, ""
	}
	return strings.TrimSpace(rest), block
}

// stripLeadingJSONFence removes the first ```json ... ``` fence when it is
// the first code fence in the response, returning the remainder and the
// fence body ("" when no matching fence is present). Any prose before a
// valid metadata fence is formatting noise and is deliberately dropped.
func stripLeadingJSONFence(s string) (rest, body string) {
	start := -1
	for _, open := range []string{"```json\n", "```json\r\n"} {
		if i := strings.Index(s, open); i >= 0 && (start < 0 || i < start) {
			start = i
		}
	}
	if start < 0 || strings.Contains(s[:start], "```") {
		return s, ""
	}
	openLen := len("```json\n")
	if strings.HasPrefix(s[start:], "```json\r\n") {
		openLen = len("```json\r\n")
	}
	inner := s[start+openLen:]
	end := strings.Index(inner, "```")
	if end < 0 {
		return s, ""
	}
	return inner[end+3:], strings.TrimSpace(inner[:end])
}

// isHandoffMetadataJSON only accepts the schema we asked the model to emit.
// This prevents a JSON example in the handoff body from being stripped.
func isHandoffMetadataJSON(raw string) bool {
	var metadata struct {
		Difficulty  string `json:"difficulty"`
		Recommended []struct {
			Executor string `json:"executor"`
		} `json:"recommended"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata.Difficulty == "" || metadata.Recommended == nil {
		return false
	}
	for _, recommendation := range metadata.Recommended {
		if strings.TrimSpace(recommendation.Executor) == "" {
			return false
		}
	}
	return true
}

// buildTitleContext keeps only what identifies the task: metadata, every
// user message (each truncated, overall budget-capped), and the last turn.
// User messages carry topic share — a session that drifts from an early
// one-off task to a dominant later theme must show the model that whole
// arc, not just the opening turns.
func buildTitleContext(detail *model.SessionDetail) string {
	var b strings.Builder
	b.WriteString("# 会话概要\n\n")
	if detail.Project != "" {
		fmt.Fprintf(&b, "- 项目: %s\n", detail.Project)
	}
	if detail.PreviewText != "" {
		fmt.Fprintf(&b, "- 预览: %s\n", truncateRunes(detail.PreviewText, 200))
	}
	b.WriteString("\n")

	var users []string
	for _, t := range detail.Turns {
		if t.UserMessage != "" {
			users = append(users, truncateRunes(t.UserMessage, titleUserMsgRunes))
		}
	}
	head, tail, elided := titleUserWindow(users)
	for _, m := range head {
		fmt.Fprintf(&b, "用户消息: %s\n", m)
	}
	if elided > 0 {
		fmt.Fprintf(&b, "…(中间已省略 %d 条用户消息)…\n", elided)
	}
	for _, m := range tail {
		fmt.Fprintf(&b, "用户消息: %s\n", m)
	}
	if n := len(detail.Turns); n > 0 {
		last := detail.Turns[n-1]
		if last.AssistantMessage != "" {
			fmt.Fprintf(&b, "最后一轮助手输出: %s\n", truncateRunes(last.AssistantMessage, 500))
		}
	}
	return b.String()
}

const (
	// titleUserMsgRunes caps one user message in the title context; user
	// queries are usually short, so a small cap keeps the topic signal
	// without letting one pasted log dominate the budget.
	titleUserMsgRunes = 150
	// titleUserBudgetRunes caps the total runes spent on user messages.
	// The opening messages (task origin) are always kept; the rest of the
	// budget is filled from the end backwards so the dominant late topic
	// survives, with middle messages elided.
	titleUserBudgetRunes = 3000
	// titleUserHeadCount is how many leading user messages are always kept.
	titleUserHeadCount = 2
)

// titleUserWindow picks which user messages fit the budget: the first
// titleUserHeadCount always, then as many trailing messages as the budget
// allows. Returns head, tail, and how many middle messages were elided.
func titleUserWindow(users []string) (head, tail []string, elided int) {
	if len(users) <= titleUserHeadCount {
		return users, nil, 0
	}
	head = users[:titleUserHeadCount]
	rest := users[titleUserHeadCount:]
	budget := titleUserBudgetRunes
	for _, m := range head {
		budget -= len([]rune(m))
	}
	i := len(rest)
	for i > 0 {
		w := len([]rune(rest[i-1]))
		if w > budget {
			break
		}
		budget -= w
		i--
	}
	return head, rest[i:], i
}

// SanitizeTitle normalizes a model-generated title: one line, quotes and
// trailing punctuation stripped, hard length cap.
func SanitizeTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if i := strings.IndexAny(title, "\r\n"); i >= 0 {
		title = title[:i]
	}
	title = strings.Trim(title, "\"'“”‘’「」『』 ")
	title = strings.TrimRight(title, "。.!！?？")
	runes := []rune(title)
	if len(runes) > 40 {
		title = string(runes[:40])
	}
	return strings.TrimSpace(title)
}
