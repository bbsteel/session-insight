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
