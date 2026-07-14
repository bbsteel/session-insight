package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Syntax highlighting for fenced code blocks inside assistant text, emitted
// as indexed ANSI. The block is tokenized whole (multi-line constructs keep
// their state) and the output is split back into lines; highlighting only adds
// color codes, so line counts and display widths are untouched and the
// position cache stays valid.
//
// The style deliberately uses exact base colours from terminal256's first 16
// entries. The formatter therefore emits only slots 0-15, which xterm remaps
// through frontend/src/terminalTheme.ts. Using a dark-only style such as
// Monokai here would emit fixed 256-cube whites that disappear on the light
// terminal background.

var codeStyle = chroma.MustNewStyle("session-insight", chroma.StyleEntries{
	chroma.Text:              "#c0c0c0", // slot 7: normal foreground
	chroma.Error:             "#800000", // slot 1: error red
	chroma.Keyword:           "#000080", // slot 4: keyword blue
	chroma.NameBuiltin:       "#008080", // slot 6: builtin accent
	chroma.NameClass:         "#800080", // slot 5: declaration accent
	chroma.NameDecorator:     "#008080", // slot 6: decorator accent
	chroma.NameException:     "#800000", // slot 1: error red
	chroma.NameFunction:      "#800080", // slot 5: declaration accent
	chroma.Literal:           "#808000", // slot 3: literals/strings
	chroma.LiteralNumber:     "#008080", // slot 6: numeric accent
	chroma.Operator:          "#800000", // slot 1: operators
	chroma.Comment:           "#808080", // slot 8: muted text
	chroma.CommentPreproc:    "#800080", // slot 5: directives
	chroma.GenericDeleted:    "#800000", // slot 1: deleted/error
	chroma.GenericError:      "#800000", // slot 1: deleted/error
	chroma.GenericHeading:    "#800080", // slot 5: heading accent
	chroma.GenericInserted:   "#008000", // slot 2: inserted/success
	chroma.GenericSubheading: "#800080", // slot 5: heading accent
})

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
	if err := formatters.Get("terminal256").Format(&buf, codeStyle, it); err != nil {
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
