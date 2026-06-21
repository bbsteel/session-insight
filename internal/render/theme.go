package render

import (
	"fmt"
	"strconv"
	"strings"
)

const TermWidth = 80

const (
	// OneDark Terminal palette
	HexBg        = "#1a1b26"
	HexFg        = "#c0caf5"
	HexUser      = "#9ece6a"
	HexThinking  = "#565f89"
	HexTool      = "#7aa2f7"
	HexSuccess   = "#9ece6a"
	HexError     = "#f7768e"
	HexDiffAdd   = "#9ece6a"
	HexDiffDel   = "#f7768e"
	HexSubagent  = "#ff9e64"
	HexSeparator = "#565f89"
	HexWarning   = "#e0af68"
	HexSkill     = "#bb9af7"
)

const (
	resetCode  = "\x1b[0m"
	boldCode   = "\x1b[1m"
	italicCode = "\x1b[3m"
	strikeCode = "\x1b[9m"
)

func fg(hex string) string {
	r, g, b := parseHex(hex)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func bg(hex string) string {
	r, g, b := parseHex(hex)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

func fgWrap(text, hex string) string {
	return fg(hex) + text + resetCode
}

func bgWrap(text, bgHex string) string {
	return bg(bgHex) + text + resetCode
}

func boldWrap(text string) string {
	return boldCode + text + resetCode
}

func italicWrap(text string) string {
	return italicCode + text + resetCode
}

func styled(text string, fgHex string, bgHex string, bold bool, italic bool) string {
	var sb strings.Builder
	sb.WriteString(resetCode)
	if bold {
		sb.WriteString(boldCode)
	}
	if italic {
		sb.WriteString(italicCode)
	}
	if fgHex != "" {
		sb.WriteString(fg(fgHex))
	}
	if bgHex != "" {
		sb.WriteString(bg(bgHex))
	}
	sb.WriteString(text)
	sb.WriteString(resetCode)
	return sb.String()
}

func depthPrefix(depth int) string {
	if depth <= 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fg(HexSubagent))
	for i := 0; i < depth; i++ {
		sb.WriteString("│ ")
	}
	sb.WriteString(resetCode)
	return sb.String()
}

func parseHex(hex string) (int, int, int) {
	h := strings.TrimPrefix(hex, "#")
	r, _ := strconv.ParseInt(h[0:2], 16, 32)
	g, _ := strconv.ParseInt(h[2:4], 16, 32)
	b, _ := strconv.ParseInt(h[4:6], 16, 32)
	return int(r), int(g), int(b)
}
