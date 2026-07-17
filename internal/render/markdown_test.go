package render

import (
	"strings"
	"testing"
)

// render one assistant text block the way writeTextChunk does, then join.
func mdLines(text string) []string {
	return renderMarkdownDoc(text, ColFg, TermWidth)
}

func mdString(text string) string {
	return strings.Join(mdLines(text), "\n")
}

// hasFg reports whether s carries the indexed foreground escape for slot c.
func hasFg(s string, c Color) bool {
	return strings.Contains(s, "\x1b[38;5;"+itoa(int(c))+"m")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestMarkdownProseCoalescesOneRun(t *testing.T) {
	// goldmark splits "plain response" into two Text nodes; the renderer must
	// re-join same-style runs so the visible text stays contiguous.
	out := mdString("plain response text here")
	if !strings.Contains(out, "plain response text here") {
		t.Errorf("prose run was fragmented by styling:\n%q", out)
	}
}

func TestMarkdownUnderscoreEmphasis(t *testing.T) {
	// The old line parser only understood '*'; underscores rendered literally.
	out := mdString("this is _italic_ and __bold__ text")
	if strings.Contains(out, "_italic_") || strings.Contains(out, "__bold__") {
		t.Errorf("underscore markers leaked into output:\n%q", out)
	}
	if !strings.Contains(out, "italic") || !strings.Contains(out, "bold") {
		t.Errorf("emphasis text missing:\n%q", out)
	}
	if !strings.Contains(out, italicCode) || !strings.Contains(out, boldCode) {
		t.Errorf("expected italic and bold ANSI codes:\n%q", out)
	}
}

func TestMarkdownGfmTable(t *testing.T) {
	// Tables were never rendered by the old parser (the doc comment claimed
	// support the code never had); pipes leaked as literal text.
	text := "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |"
	out := mdString(text)
	for _, want := range []string{"Name", "Age", "Alice", "30", "Bob", "25"} {
		if !strings.Contains(out, want) {
			t.Errorf("table cell %q missing:\n%s", want, out)
		}
	}
	// A complete box is drawn: top (┌┬┐), header split (├┼┤), bottom (└┴┘).
	for _, glyph := range []string{"┌", "┬", "┐", "├", "┼", "┤", "└", "┴", "┘"} {
		if !strings.Contains(out, glyph) {
			t.Errorf("table missing box glyph %q (incomplete frame):\n%s", glyph, out)
		}
	}
	// The GFM separator row (| --- |) must not survive as literal dashes+pipes.
	if strings.Contains(out, "| --- |") {
		t.Errorf("raw separator row leaked:\n%s", out)
	}
}

func TestMarkdownTableWithInlineCodeCellsAlign(t *testing.T) {
	// A cell containing inline code carries ANSI escapes. Column widths must be
	// measured on the *visible* text, not the escape-laden string — otherwise
	// rows with more markup get truncated at a different point and every row
	// ends up a different total width (borders drift). Regression for the bug
	// where displayWidth/truncate ran on the styled string and split an escape
	// sequence into "\x1b[38;5;" + "…".
	text := "| Action | Path |\n| --- | --- |\n" +
		"| plain | `Settings -> Editor -> Inspections` |\n" +
		"| bold **x** | `Settings -> Editor -> General` |\n" +
		"| z | short |"
	lines := mdLines(text)
	var tableLines []string
	for _, ln := range lines {
		p := stripANSI(ln)
		if strings.ContainsAny(p, "┌│└├") {
			tableLines = append(tableLines, p)
		}
	}
	if len(tableLines) < 5 {
		t.Fatalf("expected a full table, got:\n%s", strings.Join(lines, "\n"))
	}
	// No half-escape garbage must survive the strip.
	for _, p := range tableLines {
		if strings.Contains(p, "[38;5;") || strings.Contains(p, "[0m") {
			t.Errorf("leaked partial escape in table line: %q", p)
		}
	}
	// Every table line must render to the identical visible width.
	w0 := displayWidth(tableLines[0])
	for _, p := range tableLines {
		if displayWidth(p) != w0 {
			t.Errorf("table rows differ in width (%d vs %d) — columns misaligned:\n%s",
				w0, displayWidth(p), strings.Join(tableLines, "\n"))
		}
	}
}

func TestMarkdownTableFitsTermWidth(t *testing.T) {
	// A table whose natural width far exceeds the terminal must shrink so xterm
	// never soft-wraps a row (wrapping shatters the box-drawing grid on browser
	// resize) AND must never drop text — wide cells wrap onto extra lines rather
	// than truncate. Regression for both the content-width render (ignored
	// termWidth) and the truncate-to-fit version that lost text.
	const termWidth = 40
	const longDesc = "this is a very long description that easily exceeds the terminal width"
	text := "| ID | Description |\n| --- | --- |\n" +
		"| 1 | " + longDesc + " |\n" +
		"| 22 | short |"
	lines := renderMarkdownDoc(text, ColFg, termWidth)
	var tableLines []string
	for _, ln := range lines {
		p := stripANSI(ln)
		if strings.ContainsAny(p, "┌│└├") {
			tableLines = append(tableLines, p)
		}
	}
	if len(tableLines) < 5 {
		t.Fatalf("expected a full table, got:\n%s", strings.Join(lines, "\n"))
	}
	// Every row fits the terminal (else xterm soft-wraps it) and all rows share
	// one visible width (borders line up).
	w0 := displayWidth(tableLines[0])
	for _, p := range tableLines {
		if displayWidth(p) > termWidth {
			t.Errorf("table row width %d exceeds termWidth %d (will wrap):\n%q", displayWidth(p), termWidth, p)
		}
		if displayWidth(p) != w0 {
			t.Errorf("table rows differ in width (%d vs %d) — misaligned:\n%s",
				w0, displayWidth(p), strings.Join(tableLines, "\n"))
		}
	}
	// No text is lost: wrapping never truncates, so no ellipsis appears and the
	// full long description survives across the wrapped lines. Reconstruct the
	// Description column (the 3rd field of each data row) and compare ignoring
	// spaces, which wrapping may shuffle across line boundaries.
	body := strings.Join(tableLines, "\n")
	if strings.Contains(body, "…") {
		t.Errorf("wrapping must not truncate — found an ellipsis:\n%s", body)
	}
	var desc strings.Builder
	for _, p := range tableLines {
		if strings.ContainsAny(p, "┌┬┐├┼┤└┴┘") {
			continue // border/separator rows
		}
		if cols := strings.Split(p, "│"); len(cols) >= 3 {
			desc.WriteString(cols[2])
		}
	}
	want := strings.ReplaceAll(longDesc, " ", "")
	got := strings.ReplaceAll(desc.String(), " ", "")
	if !strings.Contains(got, want) {
		t.Errorf("description text lost in wrapping:\nwant substring %q\ngot %q", want, got)
	}
	// Narrow column stays intact.
	if !strings.Contains(body, "22") {
		t.Errorf("narrow ID column should not be truncated:\n%s", body)
	}
}

func TestMarkdownNestedList(t *testing.T) {
	text := "- top one\n  - nested a\n  - nested b\n- top two"
	lines := mdLines(text)
	var nestedIndent, topIndent = -1, -1
	for _, ln := range lines {
		plain := stripANSI(ln)
		if strings.Contains(plain, "nested a") {
			nestedIndent = len(plain) - len(strings.TrimLeft(plain, " "))
		}
		if strings.Contains(plain, "top one") {
			topIndent = len(plain) - len(strings.TrimLeft(plain, " "))
		}
	}
	if topIndent < 0 || nestedIndent < 0 {
		t.Fatalf("list items missing:\n%s", strings.Join(lines, "\n"))
	}
	if nestedIndent <= topIndent {
		t.Errorf("nested item (indent %d) should be indented past top (indent %d):\n%s",
			nestedIndent, topIndent, strings.Join(lines, "\n"))
	}
	// Bullets use the user-prompt slot.
	if !hasFg(strings.Join(lines, "\n"), ColUser) {
		t.Errorf("expected bullet markers on the user slot:\n%s", strings.Join(lines, "\n"))
	}
}

func TestMarkdownOrderedListNumbers(t *testing.T) {
	out := mdString("1. first\n2. second\n3. third")
	for _, want := range []string{"1.", "2.", "3.", "first", "second", "third"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Errorf("ordered list missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdownImageRendersAlt(t *testing.T) {
	// Images were unsupported: "![alt](url)" surfaced a stray "!" + link text.
	out := mdString("see ![a diagram](http://x/y.png) here")
	if strings.Contains(out, "![") || strings.Contains(out, "](") {
		t.Errorf("image markup leaked:\n%q", out)
	}
	if !strings.Contains(out, "a diagram") {
		t.Errorf("image alt text missing:\n%q", out)
	}
	if !hasFg(out, ColTool) {
		t.Errorf("expected image alt on the tool/link slot:\n%q", out)
	}
}

func TestMarkdownHeadingOnSkillSlot(t *testing.T) {
	out := mdString("# Title\n\nbody")
	if strings.Contains(out, "# Title") {
		t.Errorf("heading marker not stripped:\n%q", out)
	}
	if !strings.Contains(out, "Title") || !hasFg(out, ColSkill) {
		t.Errorf("heading should render on the skill slot:\n%q", out)
	}
}

func TestMarkdownSoftBreaksPreserveLineCount(t *testing.T) {
	// Each source line in a paragraph must map to exactly one output line, so
	// the position cache / MiniMap line math stays valid.
	text := "line one\nline two\nline three"
	lines := mdLines(text)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines from 3 soft-broken source lines, got %d:\n%q", len(lines), lines)
	}
}

func TestMarkdownBlockquote(t *testing.T) {
	out := mdString("> quoted text\n> more quote")
	if !strings.Contains(out, "quoted text") {
		t.Errorf("blockquote content missing:\n%q", out)
	}
	if !strings.Contains(stripANSI(out), "│") {
		t.Errorf("expected blockquote bar prefix:\n%q", out)
	}
}

func TestMarkdownInlineCodeOnWarningSlot(t *testing.T) {
	out := mdString("run `go build` now")
	if strings.Contains(out, "`go build`") {
		t.Errorf("backticks leaked:\n%q", out)
	}
	if !strings.Contains(out, "go build") || !hasFg(out, ColWarning) {
		t.Errorf("inline code should render on the warning slot:\n%q", out)
	}
}
