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
	if strings.IndexFunc(s, isBinarySuspect) < 0 {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	sb.Grow(len(s))
	i := 0
	for i < len(runes) {
		if !isBinarySuspect(runes[i]) {
			sb.WriteRune(runes[i])
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
		if len(span) >= minBinarySpan {
			fmt.Fprintf(&sb, " [binary data omitted: %d chars] ", len(span))
		} else {
			sb.WriteString(string(span))
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
