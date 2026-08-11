package shared

import (
	"fmt"
	"strings"
	"unicode"
)

// CollapseBinarySpans replaces maximal spans of non-text runes with a compact
// omission marker. Tool results occasionally embed compressed or otherwise
// binary payloads (for example a gzipped HTTP body relayed by a search API);
// rendered verbatim they appear as pages of mojibake and drown the readable
// content around them.
//
// ANSI escape sequences (colors, cursor moves) are presentation markup, not
// content, and are stripped outright first so colored shell output is not
// mistaken for binary garbage.
//
// A span starts at a suspect rune (control characters other than the common
// whitespace \n \t \r, DEL, and the U+FFFD replacement char left behind when
// invalid UTF-8 was lossy-decoded earlier in the pipeline) and extends across
// short printable islands, because binary garbage decoded as text is a mix of
// control runes and random printable ones. Spans shorter than minBinarySpan
// runes are kept verbatim so legitimate text with an occasional stray
// character is never rewritten.
func CollapseBinarySpans(s string) string {
	const (
		minBinarySpan   = 4
		maxPrintableGap = 8
	)
	s = stripANSIEscapes(s)
	if strings.IndexFunc(s, isBinarySuspect) < 0 {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	sb.Grow(len(s))
	prev := rune(-1) // last rune written to sb; -1 when empty
	i := 0
	for i < len(runes) {
		if !isBinarySuspect(runes[i]) {
			sb.WriteRune(runes[i])
			prev = runes[i]
			i++
			continue
		}
		// Extend the span while suspects are separated by short printable
		// islands; the span ends just past its final suspect rune.
		end := i + 1
		last := end
		for end < len(runes) && end-last <= maxPrintableGap {
			if isBinarySuspect(runes[end]) {
				last = end + 1
			}
			end++
		}
		span := runes[i:last]
		// Short spans are dropped: an isolated stray control/replacement
		// rune is not useful content, and carrying it downstream causes
		// terminal parsers to emit errors.
		if len(span) < minBinarySpan {
			i = last
			continue
		}
		// Pad the marker away from adjacent text, but don't introduce
		// whitespace at line/string boundaries.
		if prev >= 0 && !isSpaceRune(prev) {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "[binary data omitted: %d chars]", len(span))
		prev = ']'
		if last < len(runes) && !isSpaceRune(runes[last]) {
			sb.WriteByte(' ')
		}
		i = last
	}
	return sb.String()
}

func isBinarySuspect(r rune) bool {
	if r == '�' {
		return true
	}
	if r == '\n' || r == '\t' || r == '\r' {
		return false
	}
	return unicode.IsControl(r)
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\n' || r == '\t' || r == '\r'
}

// stripANSIEscapes removes ANSI escape sequences — CSI (ESC [ … final byte),
// OSC (ESC ] … terminated by BEL or ST), and two-byte ESC sequences. Tool
// output frequently embeds color/cursor markup; carrying it into span
// detection would collapse legitimate colored output as "binary".
func stripANSIEscapes(s string) string {
	const esc = '\x1b'
	if !strings.ContainsRune(s, esc) {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(runes); {
		if runes[i] != esc || i+1 >= len(runes) {
			sb.WriteRune(runes[i])
			i++
			continue
		}
		switch runes[i+1] {
		case '[': // CSI: ESC [ parameter/intermediate bytes, then 0x40–0x7E
			j := i + 2
			for j < len(runes) && (runes[j] < 0x40 || runes[j] > 0x7e) {
				j++
			}
			if j < len(runes) {
				j++ // consume the final byte
			}
			i = j
		case ']': // OSC: ESC ] … terminated by BEL or ESC \
			j := i + 2
			terminated := false
			for j < len(runes) {
				if runes[j] == '\a' {
					j++
					terminated = true
					break
				}
				if runes[j] == esc && j+1 < len(runes) && runes[j+1] == '\\' {
					j += 2
					terminated = true
					break
				}
				j++
			}
			if terminated {
				i = j
			} else {
				// Unterminated OSC: drop the incomplete escape and the rest.
				// We do not keep a bare ESC literal because downstream terminal
				// parsers would treat it as the start of a malformed sequence.
				i = len(runes)
			}
		default:
			// Two-byte sequence (ESC + final byte); anything else keeps the
			// ESC so span detection still sees a malformed/lone escape.
			if runes[i+1] >= 0x40 && runes[i+1] <= 0x7e {
				i += 2
			} else {
				sb.WriteRune(runes[i])
				i++
			}
		}
	}
	return sb.String()
}
