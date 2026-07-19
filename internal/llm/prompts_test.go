package llm

import (
	"strings"
	"testing"
)

func TestParseHandoffOutput(t *testing.T) {
	meta := `{"difficulty":"中等","difficulty_reason":"跨前后端","recommended":[{"executor":"Codex CLI（本机已安装）","reason":"性价比"}]}`
	body := "# 任务交接\n\n继续实现 AI 一期。\n\n## 背景与目标\n- x"

	tests := []struct {
		name         string
		raw          string
		wantContent  string
		wantMetadata string
	}{
		{
			name:         "well-formed",
			raw:          "```json\n" + meta + "\n```\n\n" + body,
			wantContent:  body,
			wantMetadata: meta,
		},
		{
			name:         "preamble before metadata is discarded",
			raw:          "我会核对当前工作区的改动与分支状态，再整理交接提示词。\n\n```json\n" + meta + "\n```\n\n" + body,
			wantContent:  body,
			wantMetadata: meta,
		},
		{
			name:         "no metadata block",
			raw:          body,
			wantContent:  body,
			wantMetadata: "",
		},
		{
			name:         "invalid json degrades to full content",
			raw:          "```json\n{oops\n```\n" + body,
			wantContent:  "```json\n{oops\n```\n" + body,
			wantMetadata: "",
		},
		{
			name:         "unclosed fence degrades to full content",
			raw:          "```json\n" + meta,
			wantContent:  "```json\n" + meta,
			wantMetadata: "",
		},
		{
			name:         "unrelated json in body is preserved",
			raw:          "先检查配置。\n\n```json\n{\"port\": 8080}\n```\n\n" + body,
			wantContent:  "先检查配置。\n\n```json\n{\"port\": 8080}\n```\n\n" + body,
			wantMetadata: "",
		},
		{
			name:         "null recommendations are preserved",
			raw:          "```json\n{\"difficulty\":\"中等\",\"recommended\":null}\n```\n\n" + body,
			wantContent:  "```json\n{\"difficulty\":\"中等\",\"recommended\":null}\n```\n\n" + body,
			wantMetadata: "",
		},
		{
			name:         "recommendation without executor is preserved",
			raw:          "```json\n{\"difficulty\":\"中等\",\"recommended\":[{}]}\n```\n\n" + body,
			wantContent:  "```json\n{\"difficulty\":\"中等\",\"recommended\":[{}]}\n```\n\n" + body,
			wantMetadata: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, metadata := ParseHandoffOutput(tt.raw)
			if content != strings.TrimSpace(tt.wantContent) {
				t.Errorf("content:\n got %q\nwant %q", content, tt.wantContent)
			}
			if metadata != tt.wantMetadata {
				t.Errorf("metadata:\n got %q\nwant %q", metadata, tt.wantMetadata)
			}
		})
	}
}

func TestBuildPromptTitleIncludesAllUserMessages(t *testing.T) {
	d := makeDetail(5, 20)
	msgs := []string{"早期运维操作", "第二条独立消息", "第三条独立消息", "第四条独立消息", "后期主要讨论推广"}
	for i, m := range msgs {
		d.Turns[i].UserMessage = m
	}
	prompt, err := BuildPrompt(KindTitle, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range msgs {
		if !strings.Contains(prompt, want) {
			t.Errorf("title prompt missing user message %q", want)
		}
	}
	if strings.Contains(prompt, "已省略") {
		t.Error("small session should not elide user messages")
	}
}

func TestBuildPromptTitleElidesMiddleWithinBudget(t *testing.T) {
	// Many long user messages: the head pair and as many trailing
	// messages as the budget allows must survive; the middle elides.
	d := makeDetail(40, 400)
	d.Turns[0].UserMessage = "开头第一条" + strings.Repeat("用", 400)
	d.Turns[1].UserMessage = "开头第二条" + strings.Repeat("用", 400)
	d.Turns[38].UserMessage = "结尾倒数第二条" + strings.Repeat("用", 380)
	d.Turns[39].UserMessage = "结尾最后一条" + strings.Repeat("用", 380)
	prompt, err := BuildPrompt(KindTitle, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "已省略") {
		t.Error("oversized session should carry an elision marker")
	}
	for _, want := range []string{"开头第一条", "开头第二条", "结尾倒数第二条", "结尾最后一条"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("title prompt should retain %q", want)
		}
	}
	if got := strings.Count(prompt, "用户消息: "); got >= 40 {
		t.Errorf("expected elision, but all 40 user messages present")
	}
}

func TestBuildPromptHandoffIncludesCandidates(t *testing.T) {
	detail := makeDetail(2, 100)
	prompt, err := BuildPrompt(KindHandoff, detail, []string{"Claude Code CLI（本机已安装）", "gpt-5（用户最近使用过的模型）"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Claude Code CLI（本机已安装）", "gpt-5（用户最近使用过的模型）", "```json"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
