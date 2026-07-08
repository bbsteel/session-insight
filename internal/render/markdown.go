package render

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Markdown rendering for assistant text blocks.
//
// Design: assistant text is a *block* grammar with multi-line context, not a
// bag of independent lines. Parsing it line-by-line (the historical approach)
// meant every element — tables, nested lists, underscore emphasis, images,
// escapes — was a separate special case bolted onto one if/else chain, and it
// was never complete. Here goldmark (CommonMark + GFM) parses the whole block
// into an AST *once*; a single walk emits ANSI using the semantic palette
// slots (theme.go). Parsing (correct, complete, done once) and styling (which
// slot each node maps to — the "stylesheet") are separated.
//
// Line-count discipline: the position cache, MiniMap markers, fold extents and
// truncation anchors all key off exact line numbers, so this renderer emits
// output line by line and never reflows prose — a source soft/hard line break
// becomes exactly one output line break (goldmark exposes it on the Text node),
// mirroring the old 1:1 mapping. FormatVersion is bumped whenever this layout
// changes.

// md is the shared parser: CommonMark plus the GFM extension set (tables,
// strikethrough, linkify autolinks, task lists). Stateless and safe to reuse.
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// renderMarkdownDoc parses one assistant text block and returns its rendered
// ANSI lines (no trailing newline on each; the caller prefixes and terminates
// them). defaultFg is the base foreground for ordinary prose.
func renderMarkdownDoc(source string, defaultFg Color, termWidth int) []string {
	src := []byte(source)
	doc := md.Parser().Parse(text.NewReader(src))
	c := &mdCtx{source: src, defaultFg: defaultFg, termWidth: termWidth}
	c.renderChildren(doc, true)
	return c.finishLines()
}

// mdCtx accumulates rendered lines. Completed lines land in out; the line
// currently being built lives in cur until a break flushes it.
type mdCtx struct {
	source    []byte
	defaultFg Color
	termWidth int
	out       []string
	cur       strings.Builder
	curEmpty  bool // tracks whether cur has received any content

	// pending coalesces consecutive inline text sharing one style into a
	// single styled() run. goldmark hands back adjacent *ast.Text nodes for
	// one prose run ("plain" + " response"); styling each separately would
	// splice ANSI resets mid-word and bloat the output. Flushed on a style
	// change, a line break, or any non-coalescable inline (code span, etc.).
	pend       strings.Builder
	pendStyle  inlineStyle
	pendActive bool
}

// emit appends inline text under style st, coalescing with the pending run when
// the style matches.
func (c *mdCtx) emit(s string, st inlineStyle) {
	if s == "" {
		return
	}
	if c.pendActive && st == c.pendStyle {
		c.pend.WriteString(s)
		return
	}
	c.flushPending()
	c.pend.WriteString(s)
	c.pendStyle = st
	c.pendActive = true
}

func (c *mdCtx) flushPending() {
	if c.pendActive {
		c.push(styledInline(c.pend.String(), c.pendStyle))
		c.pend.Reset()
		c.pendActive = false
	}
}

func (c *mdCtx) sub() *mdCtx {
	return &mdCtx{source: c.source, defaultFg: c.defaultFg, termWidth: c.termWidth}
}

func (c *mdCtx) push(s string) {
	if s != "" {
		c.cur.WriteString(s)
		c.curEmpty = false
	}
}

// newline closes the current line (even if empty — an intentional blank line
// inside a paragraph must be preserved to keep the 1:1 source mapping).
func (c *mdCtx) newline() {
	c.flushPending()
	c.out = append(c.out, c.cur.String())
	c.cur.Reset()
	c.curEmpty = true
}

// pushLine appends a fully-formed line directly, bypassing cur. Callers must
// ensure cur is empty (true between blocks, which always end with newline).
func (c *mdCtx) pushLine(s string) {
	c.out = append(c.out, s)
}

func (c *mdCtx) pushBlank() {
	c.out = append(c.out, "")
}

func (c *mdCtx) finishLines() []string {
	c.flushPending()
	if !c.curEmpty || c.cur.Len() > 0 {
		c.newline()
	}
	return c.out
}

// renderChildren renders a container's block children. When spaced is true a
// blank line is inserted between top-level blocks (paragraph spacing); list
// items and blockquote bodies render tight (spaced=false).
func (c *mdCtx) renderChildren(n ast.Node, spaced bool) {
	first := true
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if spaced && !first {
			c.pushBlank()
		}
		c.renderBlock(ch)
		first = false
	}
}

func (c *mdCtx) renderBlock(n ast.Node) {
	switch b := n.(type) {
	case *ast.Heading:
		c.renderInlineChildren(n, inlineStyle{bold: true, fg: ColSkill})
		c.newline()
	case *ast.Paragraph, *ast.TextBlock:
		c.renderInlineChildren(n, inlineStyle{fg: c.defaultFg})
		c.newline()
	case *ast.List:
		c.renderList(b, "")
	case *ast.Blockquote:
		c.renderBlockquote(b)
	case *ast.FencedCodeBlock:
		c.renderCode(b.Language(c.source), b.Lines(), true)
	case *ast.CodeBlock:
		c.renderCode(nil, b.Lines(), false)
	case *ast.ThematicBreak:
		c.pushLine(fgWrap(strings.Repeat("─", c.termWidth), ColMuted))
	case *extast.Table:
		c.renderTable(b)
	case *ast.HTMLBlock:
		c.renderRawBlock(b.Lines())
	default:
		// Unknown block: recurse so any inline content still surfaces.
		c.renderChildren(n, false)
	}
}

// ── Lists ────────────────────────────────────────────────────────────────────

func (c *mdCtx) renderList(list *ast.List, baseIndent string) {
	ordered := list.IsOrdered()
	num := list.Start
	if num == 0 {
		num = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		markerPlain := "•"
		if ordered {
			markerPlain = fmt.Sprintf("%d.", num)
			num++
		}
		marker := fgWrap(markerPlain, ColUser)
		contIndent := baseIndent + strings.Repeat(" ", displayWidth(markerPlain)+1)

		// Split the item's own content (rendered against the marker) from any
		// nested lists (rendered below, at the deeper continuation indent).
		lead := c.sub()
		var sublists []*ast.List
		for ch := item.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if sl, ok := ch.(*ast.List); ok {
				sublists = append(sublists, sl)
				continue
			}
			lead.renderBlock(ch)
		}
		leadLines := lead.finishLines()
		if len(leadLines) == 0 {
			c.pushLine(baseIndent + marker)
		}
		for i, ln := range leadLines {
			if i == 0 {
				c.pushLine(baseIndent + marker + " " + ln)
			} else {
				c.pushLine(contIndent + ln)
			}
		}
		for _, sl := range sublists {
			c.renderList(sl, contIndent)
		}
	}
}

// ── Blockquote ───────────────────────────────────────────────────────────────

func (c *mdCtx) renderBlockquote(bq *ast.Blockquote) {
	sub := c.sub()
	sub.renderChildren(bq, false)
	bar := fgWrap("│ ", ColMuted)
	for _, ln := range sub.finishLines() {
		c.pushLine(bar + ln)
	}
}

// ── Code blocks ──────────────────────────────────────────────────────────────

func (c *mdCtx) renderCode(langBytes []byte, segs *text.Segments, fenced bool) {
	lang := strings.ToLower(strings.TrimSpace(string(langBytes)))
	code := make([]string, 0, segs.Len())
	for i := 0; i < segs.Len(); i++ {
		s := segs.At(i)
		code = append(code, strings.TrimRight(string(s.Value(c.source)), "\n"))
	}
	if fenced {
		c.pushLine(fgWrap("```"+lang, ColMuted))
	}
	highlighted := highlightCodeBody(code, lang)
	for i, cl := range code {
		if highlighted != nil && highlighted[i] != "" {
			c.pushLine(highlighted[i])
		} else {
			c.pushLine(fgWrap(cl, ColWarning))
		}
	}
	if fenced {
		c.pushLine(fgWrap("```", ColMuted))
	}
}

func (c *mdCtx) renderRawBlock(segs *text.Segments) {
	for i := 0; i < segs.Len(); i++ {
		s := segs.At(i)
		line := strings.TrimRight(string(s.Value(c.source)), "\n")
		c.pushLine(fgWrap(line, ColMuted))
	}
}

// ── Tables ───────────────────────────────────────────────────────────────────

const maxTableCol = 60

// minTableCol floors how narrow shrink-to-fit may squeeze a column. The
// truncator reserves one column for the ellipsis, so 3 is the smallest width
// that still shows "one glyph + …" rather than a bare ellipsis.
const minTableCol = 3

// tableCell keeps a cell's styled ANSI form alongside its *visible* width
// (measured on the plain text). All column math is done on plain width; the
// styled form is only sliced by an ANSI-aware truncator. Measuring width on the
// styled string was a bug: it counted escape bytes as columns, inflating the
// width and truncating mid-escape (which split "\x1b[38;5;3m" into garbage and
// desynced per-row column widths).
type tableCell struct {
	styled string
	plain  string
	width  int
}

func (c *mdCtx) renderTable(tbl *extast.Table) {
	var rows [][]tableCell
	var isHeader []bool
	for r := tbl.FirstChild(); r != nil; r = r.NextSibling() {
		_, header := r.(*extast.TableHeader)
		var cells []tableCell
		for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
			sub := c.sub()
			sub.renderInlineChildren(cell, inlineStyle{fg: c.defaultFg})
			styledCell := strings.Join(sub.finishLines(), " ")
			plain := stripANSI(styledCell)
			cells = append(cells, tableCell{styled: styledCell, plain: plain, width: displayWidth(plain)})
		}
		rows = append(rows, cells)
		isHeader = append(isHeader, header)
	}
	if len(rows) == 0 {
		return
	}

	ncol := 0
	for _, row := range rows {
		if len(row) > ncol {
			ncol = len(row)
		}
	}
	widths := make([]int, ncol)
	for _, row := range rows {
		for i, cell := range row {
			if cell.width > widths[i] {
				widths[i] = cell.width
			}
		}
	}
	for i := range widths {
		if widths[i] > maxTableCol {
			widths[i] = maxTableCol
		}
	}

	// Shrink to the terminal width so xterm never soft-wraps a row (which
	// shatters the box-drawing grid on resize). A full row is
	// Σ(widths[i]+2) verticals + (ncol+1) bars = Σwidths + 3·ncol + 1, so the
	// content budget is termWidth − 3·ncol − 1. Columns already narrower than
	// their fair share are left untouched; only the widest ones get shaved
	// (down to minTableCol), and the overflowing cells then hit the existing
	// truncator below. If even minTableCol per column can't fit (terminal
	// narrower than the table's floor), pin to minTableCol and let xterm wrap
	// as the last resort — no column layout can do better there.
	if c.termWidth > 0 {
		avail := c.termWidth - 3*ncol - 1
		total := 0
		for _, w := range widths {
			total += w
		}
		floor := minTableCol
		if avail < ncol*floor {
			for i := range widths {
				if widths[i] > floor {
					widths[i] = floor
				}
			}
		} else {
			for total > avail {
				// Reduce the current widest column that is still above the floor.
				widest := -1
				for i, w := range widths {
					if w > floor && (widest < 0 || w > widths[widest]) {
						widest = i
					}
				}
				if widest < 0 {
					break
				}
				widths[widest]--
				total--
			}
		}
	}

	// border draws a full-width horizontal rule with the given corner/junction
	// glyphs (top: ┌┬┐, row split: ├┼┤, bottom: └┴┘). Once cells wrap onto
	// several lines a rule between every row (a full grid) keeps them legible.
	border := func(left, mid, right string) string {
		var sb strings.Builder
		sb.WriteString(left)
		for i := 0; i < ncol; i++ {
			sb.WriteString(strings.Repeat("─", widths[i]+2))
			if i < ncol-1 {
				sb.WriteString(mid)
			}
		}
		sb.WriteString(right)
		return fgWrap(sb.String(), ColMuted)
	}

	bar := fgWrap("│", ColMuted)
	c.pushLine(border("┌", "┬", "┐"))
	for ri, row := range rows {
		// Wrap each cell to its column width so no text is ever lost (the old
		// behaviour truncated with an ellipsis). The row's height is its tallest
		// wrapped cell; shorter cells pad with blank lines. Header cells wrap the
		// plain text then re-style each line, so the outer bold/violet isn't
		// cancelled by a cell's own inline resets.
		cellLines := make([][]string, ncol)
		height := 1
		for i := 0; i < ncol; i++ {
			if i >= len(row) {
				cellLines[i] = []string{""}
				continue
			}
			if isHeader[ri] {
				var styledLines []string
				for _, p := range wrapPlainToWidth(row[i].plain, widths[i]) {
					styledLines = append(styledLines, styled(p, ColSkill, ColNone, true, false))
				}
				cellLines[i] = styledLines
			} else {
				cellLines[i] = wrapStyledToWidth(row[i].styled, widths[i])
			}
			if len(cellLines[i]) > height {
				height = len(cellLines[i])
			}
		}
		for k := 0; k < height; k++ {
			var sb strings.Builder
			sb.WriteString(bar)
			for i := 0; i < ncol; i++ {
				content := ""
				if k < len(cellLines[i]) {
					content = cellLines[i][k]
				}
				vis := displayWidth(stripANSI(content))
				pad := widths[i] - vis
				if pad < 0 {
					pad = 0
				}
				sb.WriteString(" " + content + strings.Repeat(" ", pad) + " ")
				sb.WriteString(bar)
			}
			c.pushLine(sb.String())
		}
		if ri < len(rows)-1 {
			c.pushLine(border("├", "┼", "┤"))
		}
	}
	c.pushLine(border("└", "┴", "┘"))
}

// wrapPlainToWidth soft-wraps unstyled text into lines of at most maxWidth
// display columns, breaking on the column boundary (there is no word notion for
// CJK). An empty string yields a single empty line so a blank cell still
// occupies one row.
func wrapPlainToWidth(s string, maxWidth int) []string {
	if maxWidth < 1 {
		maxWidth = 1
	}
	if s == "" {
		return []string{""}
	}
	var lines []string
	remaining := s
	for displayWidth(remaining) > maxWidth {
		chunk, rest := splitAtWidth(remaining, maxWidth)
		if chunk == "" { // a single rune wider than maxWidth (wide char, tiny col)
			runes := []rune(remaining)
			chunk = string(runes[0])
			rest = string(runes[1:])
		}
		lines = append(lines, chunk)
		remaining = rest
	}
	return append(lines, remaining)
}

// wrapStyledToWidth soft-wraps an ANSI-styled string into lines of at most
// maxWidth *visible* columns. Escape sequences (\x1b[…m) are copied through
// untouched, never counted toward width and never split. Each produced line
// ends with a reset so color can't leak into the cell padding or the border,
// and any style still active at a wrap point is re-opened at the start of the
// next line so a span that spills across lines keeps its color.
func wrapStyledToWidth(s string, maxWidth int) []string {
	if maxWidth < 1 {
		maxWidth = 1
	}
	var lines []string
	var cur strings.Builder    // current line being built
	var active strings.Builder // SGR sequences still in effect (re-opened per line)
	w := 0
	flush := func() {
		lines = append(lines, cur.String()+resetCode)
		cur.Reset()
		cur.WriteString(active.String()) // carry the active style onto the next line
		w = 0
	}
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] == '\x1b' {
			j := i
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j < len(runes) {
				j++ // include the 'm'
			}
			esc := string(runes[i:j])
			cur.WriteString(esc)
			if esc == resetCode {
				active.Reset()
			} else {
				active.WriteString(esc)
			}
			i = j
			continue
		}
		rw := 1
		if isWideRune(runes[i]) {
			rw = 2
		}
		if w+rw > maxWidth {
			flush()
		}
		cur.WriteRune(runes[i])
		w += rw
		i++
	}
	lines = append(lines, cur.String()+resetCode)
	return lines
}

// ── Inline ───────────────────────────────────────────────────────────────────

// inlineStyle is the active inline formatting while walking inline nodes.
type inlineStyle struct {
	bold   bool
	italic bool
	fg     Color
}

func (c *mdCtx) renderInlineChildren(n ast.Node, st inlineStyle) {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		c.renderInline(ch, st)
	}
}

func (c *mdCtx) renderInline(n ast.Node, st inlineStyle) {
	switch v := n.(type) {
	case *ast.Text:
		c.emit(string(v.Value(c.source)), st)
		if v.HardLineBreak() || v.SoftLineBreak() {
			c.newline()
		}
	case *ast.String:
		c.emit(string(v.Value), st)
	case *ast.CodeSpan:
		// Code spans render on the warning slot; routing through emit keeps
		// them from coalescing with adjacent prose (different style) while
		// still merging with neighbouring code text.
		c.emit(collectText(v, c.source), inlineStyle{fg: ColWarning})
	case *ast.Emphasis:
		st2 := st
		if v.Level >= 2 {
			st2.bold = true
		} else {
			st2.italic = true
		}
		c.renderInlineChildren(v, st2)
	case *extast.Strikethrough:
		c.flushPending()
		c.push(strikeCode + fgWrap(collectText(v, c.source), ColMuted) + resetCode)
	case *ast.Link:
		st2 := st
		st2.fg = ColTool
		c.renderInlineChildren(v, st2)
	case *ast.Image:
		c.emit(collectText(v, c.source), inlineStyle{fg: ColTool})
	case *ast.AutoLink:
		c.emit(string(v.URL(c.source)), inlineStyle{fg: ColTool})
	case *extast.TaskCheckBox:
		if v.IsChecked {
			c.emit("☑ ", inlineStyle{fg: ColSuccess})
		} else {
			c.emit("☐ ", inlineStyle{fg: ColMuted})
		}
	case *ast.RawHTML:
		c.emit(collectText(v, c.source), st)
	default:
		c.renderInlineChildren(n, st)
	}
}

// styledInline emits text with the active inline style. Empty text yields no
// ANSI at all, so a run of formatting markers with no content adds nothing.
// Callers always set st.fg to a real slot, so it is emitted verbatim.
func styledInline(s string, st inlineStyle) string {
	if s == "" {
		return ""
	}
	return styled(s, st.fg, ColNone, st.bold, st.italic)
}

// collectText concatenates the plain text under a node (used for code spans,
// link/image labels and strikethrough, where inner formatting is dropped).
func collectText(n ast.Node, source []byte) string {
	var sb strings.Builder
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		switch v := node.(type) {
		case *ast.Text:
			sb.Write(v.Value(source))
			if v.HardLineBreak() || v.SoftLineBreak() {
				sb.WriteByte('\n')
			}
		case *ast.String:
			sb.Write(v.Value)
		case *ast.AutoLink:
			sb.Write(v.URL(source))
		default:
			for ch := node.FirstChild(); ch != nil; ch = ch.NextSibling() {
				walk(ch)
			}
		}
	}
	walk(n)
	return sb.String()
}

// stripANSI removes SGR escape sequences so cell padding can be measured on the
// visible text. Table cells carry inline styling; their display width is the
// width of the text without the escape codes.
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var sb strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}
