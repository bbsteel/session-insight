package render

import (
	"fmt"
	"strings"
)

const TermWidth = 80

// Color is a semantic palette slot expressed as an ANSI color index (0-15).
//
// The backend deliberately emits indexed colors (\x1b[38;5;Nm / \x1b[48;5;Nm)
// rather than 24-bit truecolor: the actual RGB for each slot is resolved by the
// frontend xterm theme (see frontend/src/terminalTheme.ts), so switching theme
// (dark/light/…) is a palette swap on the client — instant, no re-render of the
// ANSI stream, and the position cache stays valid (visible layout is identical).
//
// Slot assignment mirrors terminalTheme.ts and must stay in sync with it.
type Color int

const (
	ColNone     Color = -1 // sentinel: no color (leave default)
	ColBg       Color = 0  // terminal background; as fg on an accent bg it contrasts in any theme
	ColError   Color = 1 // errors, ✗
	ColSuccess Color = 2 // ✓, success
	// ColUser gets its own slot (13, previously spare) instead of sharing
	// slot 2 with ColSuccess: per-agent terminal skins must be able to color
	// the user prompt independently of the ✓ marker (chrys uses pink user /
	// green success). Default themes map slot 13 to the same green as slot 2,
	// so agents without a skin render identically to before.
	ColUser Color = 13 // user prompt, list markers
	ColWarning  Color = 3  // warnings, code fences, truncation notes
	ColTool     Color = 4  // tool box borders, links
	ColSkill    Color = 5  // skills, headings
	ColSubagent Color = 6  // sub-agent / nested transcript (Claude terracotta)
	ColFg       Color = 7  // default foreground text
	ColMuted    Color = 8  // thinking, separators, blockquotes, dim text
	ColDiffDel  Color = 9  // diff deleted line background
	ColDiffAdd  Color = 10 // diff added line background
	ColBanner   Color = 12 // turn banner accent; theme-resolved and user-customizable client-side
)

const (
	resetCode  = "\x1b[0m"
	boldCode   = "\x1b[1m"
	italicCode = "\x1b[3m"
	strikeCode = "\x1b[9m"
)

func fg(c Color) string {
	if c == ColNone {
		return ""
	}
	return fmt.Sprintf("\x1b[38;5;%dm", int(c))
}

func bg(c Color) string {
	if c == ColNone {
		return ""
	}
	return fmt.Sprintf("\x1b[48;5;%dm", int(c))
}

func fgWrap(text string, c Color) string {
	return fg(c) + text + resetCode
}

func bgWrap(text string, c Color) string {
	return bg(c) + text + resetCode
}

func boldWrap(text string) string {
	return boldCode + text + resetCode
}

func italicWrap(text string) string {
	return italicCode + text + resetCode
}

func styled(text string, fgC Color, bgC Color, bold bool, italic bool) string {
	var sb strings.Builder
	sb.WriteString(resetCode)
	if bold {
		sb.WriteString(boldCode)
	}
	if italic {
		sb.WriteString(italicCode)
	}
	if fgC != ColNone {
		sb.WriteString(fg(fgC))
	}
	if bgC != ColNone {
		sb.WriteString(bg(bgC))
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
	sb.WriteString(fg(ColSubagent))
	for i := 0; i < depth; i++ {
		sb.WriteString("│ ")
	}
	sb.WriteString(resetCode)
	return sb.String()
}
