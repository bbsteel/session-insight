package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Syntax highlighting for fenced code blocks inside assistant text, emitted
// as xterm-256 ANSI. The block is tokenized whole (multi-line constructs keep
// their state) and the output is split back into lines; highlighting only adds
// color codes, so line counts and display widths are untouched and the
// position cache stays valid.
//
// terminal256 uses the fixed xterm 256-color cube (slots ≥16), deliberately
// bypassing the theme-remapped 16-slot palette — those slots are repurposed
// (banner, diff backgrounds) and would garble code colors.

// highlightCodeBody returns, aligned 1:1 with code, the ANSI-highlighted
// replacement for each code line, or nil when the language is unknown or the
// highlighter's line count drifts from the input (caller falls back to the
// flat code color). Each returned line ends in a reset so a multi-line token's
// SGR never leaks into the next terminal row's prefix/border.
func highlightCodeBody(code []string, lang string) []string {
	if lang == "" || len(code) == 0 {
		return nil
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return nil
	}
	lexer = chroma.Coalesce(lexer)
	src := strings.Join(code, "\n")
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return nil
	}
	var buf strings.Builder
	if err := formatters.Get("terminal256").Format(&buf, styles.Get("monokai"), it); err != nil {
		return nil
	}
	colored := strings.Split(buf.String(), "\n")
	// Many lexers have EnsureNL and append a trailing newline; drop it.
	if len(colored) == len(code)+1 && colored[len(colored)-1] == "" {
		colored = colored[:len(colored)-1]
	}
	if len(colored) != len(code) {
		return nil // line-count drift → flat fallback
	}
	out := make([]string, len(colored))
	for i, cline := range colored {
		out[i] = cline + "\x1b[0m"
	}
	return out
}
