package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Syntax highlighting for fenced code blocks inside assistant text, emitted
// as xterm-256 ANSI. Blocks are tokenized whole (multi-line constructs keep
// their state) and the output is split back into lines; the replacement only
// adds color codes, so line counts and display widths are untouched and the
// position cache stays valid.
//
// terminal256 uses the fixed xterm 256-color cube (slots ≥16), deliberately
// bypassing the theme-remapped 16-slot palette — those slots are repurposed
// (banner, diff backgrounds) and would garble code colors.

// highlightFencedBlocks returns, aligned with lines, the ANSI-highlighted
// replacement for each fenced code block body line ("" = no highlight, the
// caller falls back to the flat code color).
func highlightFencedBlocks(lines []string) []string {
	out := make([]string, len(lines))
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		lang := ""
		if f := strings.Fields(info); len(f) > 0 {
			lang = strings.ToLower(f[0])
		}
		end := i + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "```") {
			end++
		}
		if lang != "" && end > i+1 {
			applyHighlight(lines, out, i+1, end, lang)
		}
		i = end // land on the closing fence; the loop increment moves past it
	}
	return out
}

func applyHighlight(lines, out []string, start, end int, lang string) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		return
	}
	lexer = chroma.Coalesce(lexer)
	src := strings.Join(lines[start:end], "\n")
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return
	}
	var buf strings.Builder
	if err := formatters.Get("terminal256").Format(&buf, styles.Get("monokai"), it); err != nil {
		return
	}
	colored := strings.Split(buf.String(), "\n")
	// Many lexers have EnsureNL and append a trailing newline; drop it.
	if len(colored) == end-start+1 && colored[len(colored)-1] == "" {
		colored = colored[:len(colored)-1]
	}
	if len(colored) != end-start {
		return // line-count drift → keep the un-highlighted fallback
	}
	for k, c := range colored {
		// Reset per line: a multi-line token's SGR must not leak into the
		// prefix/border of the next terminal row.
		out[start+k] = c + "\x1b[0m"
	}
}
