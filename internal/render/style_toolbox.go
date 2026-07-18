package render

import (
	"fmt"
	"strings"
)

// standardToolBox renders the legacy tool input box: the box header is a full
// "Tool: name · summary · duration · ts" line. Used by default and claude.
type standardToolBox struct{}

func (standardToolBox) BoxPrefix(p *Profile, prefix string) string { return prefix }

func (standardToolBox) BuildHeader(p *Profile, input map[string]any, toolName, promotedPurpose string,
	durationMs int64, ts string) (header string, suppress bool) {
	parts := []string{fmt.Sprintf("Tool: %s", toolName)}
	if s := toolSummary("", input); s != "" {
		parts = append(parts, truncateToWidth(s, 48))
	}
	if durationMs > 0 {
		parts = append(parts, fmtDurationShort(durationMs))
	}
	if ts != "" {
		parts = append(parts, ts)
	}
	return " " + strings.Join(parts, " · ") + " ", false
}

// chrysToolBox promotes the human-readable "reason"/purpose into the box
// header so the box top reads "╭── reason ╮" rather than "Tool: bash".
type chrysToolBox struct{}

func (chrysToolBox) BoxPrefix(p *Profile, prefix string) string { return prefix }

func (chrysToolBox) BuildHeader(p *Profile, input map[string]any, toolName, promotedPurpose string,
	durationMs int64, ts string) (header string, suppress bool) {
	if promotedPurpose != "" {
		return " " + sanitizeControlChars(promotedPurpose) + " ", false
	}
	return "", false
}

// grokToolBox suppresses the inner tool box header because the bullet line
// already shows the description. It also indents the box two columns under the
// bullet.
type grokToolBox struct{}

func (grokToolBox) BoxPrefix(p *Profile, prefix string) string { return prefix + "  " }

func (grokToolBox) BuildHeader(p *Profile, input map[string]any, toolName, promotedPurpose string,
	durationMs int64, ts string) (header string, suppress bool) {
	return "", true
}
