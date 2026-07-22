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

const summaryInstructionEN = `You are a senior engineer reviewing an AI coding assistant session. Produce a Markdown summary in English.

Start with one or two untitled sentences explaining what the session did and its final outcome. Then use exactly these sections:

## What happened

- Use bullets beginning with a **bold short phrase**.

## Key conclusions and decisions

- State each **decision or conclusion** and its rationale. Write "No significant decisions" when none are evident.

## Remaining work

- Record unfinished work, known defects, or deferred directions. Write "No obvious remaining work" when none are evident.

Only use facts from the record; do not invent. Keep file paths, commands, branches, and function names verbatim in backticks. Quote code only in fenced Markdown blocks with a language tag (for example, ` + "```go" + `). Output Markdown only, with no preface or closing explanation.`

const titleInstruction = `请为下面这段 AI 编程助手的会话起一个中文标题,概括会话的核心任务。要求:以整个会话中占比最大、最具代表性的主题为准——会话主题可能随时间漂移,靠后集中讨论的主题往往才是会话的真正核心,开头的零散命令或一次性运维操作不算核心任务;不超过 20 个字;直接输出标题文本本身——不要引号、不要句号、不要任何解释或前缀。`

const titleInstructionEN = `Give the following AI coding assistant session a concise English title that captures its core task. Choose the most representative topic by share across the whole session; concentrated later work often reflects the real task, while isolated opening commands or one-off operations do not. Use at most 10 words. Output only the title, with no quotation marks, period, explanation, or prefix.`

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

const handoffInstructionHeadEN = `You are a senior engineer handing this AI coding assistant session to a fresh AI session. Your output has two parts.

The first part must begin with a ` + "```json" + ` fenced block containing handoff metadata (not part of the pasteable handoff prompt), using exactly this schema:

` + "```json" + `
{
  "difficulty": "simple|medium|hard",
  "difficulty_reason": "One sentence explaining the rating",
  "recommended": [
    {"executor": "Candidate executor verbatim", "reason": "One-sentence rationale"}
  ]
}
` + "```" + `

Choose recommended entries only from the candidate list below, highest priority first, and keep only one to three genuinely useful choices. Balance capability and cost against the remaining work: prefer fast, economical choices for simple tasks and reserve the strongest models for hard work.

Candidate executors:
`

const handoffInstructionTailEN = `
After the JSON block, output the English pasteable handoff prompt. It will be copied verbatim into a new session, so it must not mention difficulty, model recommendations, the metadata block, or phrases such as "above" that refer to it. It must be self-contained and use exactly this structure:

# Task handoff

Start with one or two untitled sentences explaining the task and current progress, then include:

## Background and goal
## Completed
## Remaining work / next steps
## Pitfalls and cautions
## Collaboration constraints

Use bullets in every section and begin key points with **bold text**. Collaboration constraints must state repository path, branch, working directory, and other environment facts found in the record; when absent, write "Unknown; confirm before continuing." Include only failures and corrections that actually occurred. Keep paths, commands, and branches verbatim in backticks. Output no preface or explanation outside the two required parts.`

// BuildPrompt assembles the full prompt for one generation kind. Title
// generation feeds every user message (truncated, budget-capped): topic
// share across the whole session identifies the task better than the first
// turns alone, which misleads when a long session drifts to a new topic.
// handoffCandidates lists executor candidates (installed CLIs + recently
// used models) for the handoff recommendation metadata; ignored for other
// kinds.
func BuildPrompt(kind GenerationKind, detail *model.SessionDetail, handoffCandidates []string, locale ...string) (string, error) {
	switch kind {
	case KindSummary:
		instruction := summaryInstruction
		if len(locale) > 0 && locale[0] == "en" {
			instruction = summaryInstructionEN
		}
		return instruction + "\n\n" + BuildSessionContext(detail), nil
	case KindHandoff:
		var b strings.Builder
		english := len(locale) > 0 && locale[0] == "en"
		if english {
			b.WriteString(handoffInstructionHeadEN)
		} else {
			b.WriteString(handoffInstructionHead)
		}
		if len(handoffCandidates) == 0 {
			if english {
				b.WriteString("(No candidates available; output an empty recommended array.)\n")
			} else {
				b.WriteString("(无候选信息——recommended 输出空数组)\n")
			}
		}
		for _, c := range handoffCandidates {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		if english {
			b.WriteString(handoffInstructionTailEN)
		} else {
			b.WriteString(handoffInstructionTail)
		}
		b.WriteString("\n\n")
		b.WriteString(BuildHandoffContext(detail))
		return b.String(), nil
	case KindTitle:
		instruction := titleInstruction
		if len(locale) > 0 && locale[0] == "en" {
			instruction = titleInstructionEN
		}
		return instruction + "\n\n" + buildTitleContext(detail), nil
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
	trimmed := unwrapMarkdownFence(strings.TrimSpace(raw))
	rest, block := stripLeadingJSONFence(trimmed)
	if block == "" {
		return normalizeHandoffBody(trimmed)
	}
	if !isHandoffMetadataJSON(block) || !startsHandoffHeading(rest) {
		return trimmed, ""
	}
	content, effectiveMetadata := normalizeHandoffBody(rest)
	if effectiveMetadata != "" {
		return content, effectiveMetadata
	}
	return content, block
}

// normalizeHandoffBody repairs two common formatting slips without changing
// ordinary Markdown in the generated prompt:
//   - some models wrap the complete answer in one ```markdown fence;
//   - some models restart their structured answer after a false start, leaving
//     another valid metadata fence immediately before a fresh handoff heading.
//
// The second case is deliberately strict so JSON examples in the handoff body
// remain untouched.
func normalizeHandoffBody(raw string) (content, metadataJSON string) {
	body := strings.TrimSpace(raw)
	body = unwrapMarkdownFence(body)

	searchFrom := 0
	for searchFrom < len(body) {
		rel := strings.Index(body[searchFrom:], "```json")
		if rel < 0 {
			break
		}
		start := searchFrom + rel
		rest, block := stripLeadingJSONFence(body[start:])
		if block != "" && isHandoffMetadataJSON(block) && startsHandoffHeading(rest) {
			content, nestedMetadata := normalizeHandoffBody(rest)
			if nestedMetadata != "" {
				return content, nestedMetadata
			}
			return content, block
		}
		searchFrom = start + len("```json")
	}
	return body, ""
}

func startsHandoffHeading(s string) bool {
	trimmed := unwrapMarkdownFence(strings.TrimSpace(s))
	return strings.HasPrefix(trimmed, "# 任务交接\n") || trimmed == "# 任务交接" ||
		strings.HasPrefix(trimmed, "# Task handoff\n") || trimmed == "# Task handoff"
}

func unwrapMarkdownFence(s string) string {
	for _, open := range []string{"```markdown\n", "```markdown\r\n", "```md\n", "```md\r\n"} {
		if !strings.HasPrefix(s, open) || !strings.HasSuffix(s, "```") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(s, open), "```")
		return strings.TrimSpace(inner)
	}
	return s
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
