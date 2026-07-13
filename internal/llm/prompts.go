package llm

import (
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

const summaryInstruction = `你是一名资深工程师,正在复盘一段 AI 编程助手的会话记录。请用中文输出 markdown 总结,结构固定为三节:

## 做了什么
## 关键结论与决策
## 遗留问题

要求:只基于记录中的事实,不要推测编造;提到文件、命令、分支时保留原文;遗留问题若记录中看不出来就写"无明显遗留"。直接输出 markdown,不要任何前言。`

const titleInstruction = `请为下面这段 AI 编程助手的会话起一个中文标题,概括会话的核心任务。要求:不超过 20 个字;直接输出标题文本本身——不要引号、不要句号、不要任何解释或前缀。`

const handoffInstruction = `你是一名资深工程师,要把下面这段 AI 编程助手会话的工作交接给一个全新的 AI 会话继续。请用中文生成一段"交接提示词",新会话只凭这段文字就能无缝接手(自包含,不依赖任何未写明的上下文)。结构固定:

# 任务交接

## 背景与目标
## 已完成
## 未完成 / 下一步
## 踩过的坑与注意事项
## 协作约束

要求:协作约束一节必须写明仓库路径、分支、工作目录等环境事实(从记录中提取,记录里没有就写"未知,接手后先确认");"踩过的坑"只写记录中真实出现过的失败与纠正;直接输出 markdown,不要任何前言。`

// BuildPrompt assembles the full prompt for one generation kind. Title
// generation deliberately feeds a slim transcript — first and last turns
// carry the task identity, and a cheap prompt keeps retries painless.
func BuildPrompt(kind GenerationKind, detail *model.SessionDetail) (string, error) {
	switch kind {
	case KindSummary:
		return summaryInstruction + "\n\n" + BuildSessionContext(detail), nil
	case KindHandoff:
		return handoffInstruction + "\n\n" + BuildSessionContext(detail), nil
	case KindTitle:
		return titleInstruction + "\n\n" + buildTitleContext(detail), nil
	default:
		return "", fmt.Errorf("unknown generation kind %q", kind)
	}
}

// buildTitleContext keeps only what identifies the task: metadata, the first
// couple of user messages, and the last turn.
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

	written := 0
	for _, t := range detail.Turns {
		if t.UserMessage == "" {
			continue
		}
		fmt.Fprintf(&b, "用户消息: %s\n", truncateRunes(t.UserMessage, 500))
		written++
		if written >= 2 {
			break
		}
	}
	if n := len(detail.Turns); n > 0 {
		last := detail.Turns[n-1]
		if last.AssistantMessage != "" {
			fmt.Fprintf(&b, "最后一轮助手输出: %s\n", truncateRunes(last.AssistantMessage, 500))
		}
	}
	return b.String()
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
