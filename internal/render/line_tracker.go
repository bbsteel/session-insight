package render

import "strings"

// terminalLineTracker tracks the current terminal line number as text is
// written through the formatter. It mirrors xterm.js line-breaking rules:
//
//   - ANSI SGR sequences (ESC [ ... m) do not consume visible columns.
//   - '\n' is a hard line break → advance one line, reset column to 0.
//   - Printable runes contribute their display width (2 for wide CJK, 1 otherwise).
//   - When the current column + rune width exceeds cols, a soft wrap occurs
//     first (line++, col=0), then the rune is written.
type terminalLineTracker struct {
	cols    int // terminal column count (from fitAddon.fit())
	line    int // 0-based current terminal line (display row, soft wraps included)
	col     int // 0-based current column
	logical int // 0-based current logical line ('\n' count only, no soft wraps)
}

func newLineTracker(cols int) *terminalLineTracker {
	if cols <= 0 {
		cols = TermWidth
	}
	return &terminalLineTracker{cols: cols}
}

// CurrentLine returns the current 0-based terminal line number.
func (t *terminalLineTracker) CurrentLine() int {
	return t.line
}

// CurrentLogicalLine returns the current 0-based logical line index — the
// position in the '\n'-split source text, unaffected by soft wrapping. Fold
// composition on the client slices the raw ANSI by logical lines while
// shifting display rows by the tracker's display counts, so it never has to
// re-implement wrap simulation.
func (t *terminalLineTracker) CurrentLogicalLine() int {
	return t.logical
}

// Feed updates the tracker state for the text s. s must be the exact bytes
// that will be written to the terminal (after ANSI wrapping). Callers must
// feed text in the same order it is written to the output builder.
func (t *terminalLineTracker) Feed(s string) {
	inEscape := false
	for i := 0; i < len(s); {
		b := s[i]

		// Detect ESC [ (CSI) — consume until a final byte [0x40,0x7E].
		if b == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			inEscape = true
			i += 2
			continue
		}
		if inEscape {
			if b >= 0x40 && b <= 0x7e {
				inEscape = false
			}
			i++
			continue
		}

		// Hard line break.
		if b == '\n' {
			t.line++
			t.logical++
			t.col = 0
			i++
			continue
		}

		// Decode one UTF-8 rune.
		r, size := decodeRune(s, i)
		i += size

		rw := 1
		if isWideRune(r) {
			rw = 2
		}

		// Soft wrap when rune would overflow current line.
		if t.col+rw > t.cols {
			t.line++
			t.col = 0
		}
		t.col += rw
	}
}

// decodeRune decodes one UTF-8 rune from s[i:]. Returns the rune and its byte
// size. Invalid/short sequences return (RuneError, 1) like the stdlib.
func decodeRune(s string, i int) (rune, int) {
	b := s[i]
	if b < 0x80 {
		return rune(b), 1
	}
	// Use strings.NewReader approach via standard library via a small slice.
	// Avoids importing unicode/utf8 separately (already used in index_store.go).
	r, size := rune(0), 0
	switch {
	case b&0xe0 == 0xc0 && i+1 < len(s):
		r = rune(b&0x1f)<<6 | rune(s[i+1]&0x3f)
		size = 2
	case b&0xf0 == 0xe0 && i+2 < len(s):
		r = rune(b&0x0f)<<12 | rune(s[i+1]&0x3f)<<6 | rune(s[i+2]&0x3f)
		size = 3
	case b&0xf8 == 0xf0 && i+3 < len(s):
		r = rune(b&0x07)<<18 | rune(s[i+1]&0x3f)<<12 | rune(s[i+2]&0x3f)<<6 | rune(s[i+3]&0x3f)
		size = 4
	default:
		return '�', 1
	}
	return r, size
}

// trackingBuilder wraps strings.Builder and feeds every write through a
// terminalLineTracker so positions stay in sync with actual output.
type trackingBuilder struct {
	sb      strings.Builder
	tracker *terminalLineTracker
}

func newTrackingBuilder(cols int) *trackingBuilder {
	return &trackingBuilder{tracker: newLineTracker(cols)}
}

func (tb *trackingBuilder) WriteString(s string) {
	tb.sb.WriteString(s)
	tb.tracker.Feed(s)
}

func (tb *trackingBuilder) String() string {
	return tb.sb.String()
}

func (tb *trackingBuilder) CurrentLine() int {
	return tb.tracker.CurrentLine()
}

func (tb *trackingBuilder) CurrentLogicalLine() int {
	return tb.tracker.CurrentLogicalLine()
}
