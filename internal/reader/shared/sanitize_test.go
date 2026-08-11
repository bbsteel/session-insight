package shared

import (
	"strings"
	"testing"
)

func TestCollapseBinarySpansKeepsCleanText(t *testing.T) {
	cases := []string{
		"",
		"plain ascii text\nwith newlines\tand tabs",
		"日本語のテキストと絵文字 🔥\n複数行",
		"short stray � char stays", // below minBinarySpan
		"one\x01two",               // single control rune stays
		"ansi-ish \x1b[31m short",  // span of 1 (< 4) stays verbatim
	}
	for _, in := range cases {
		if got := CollapseBinarySpans(in); got != in {
			t.Fatalf("CollapseBinarySpans(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestCollapseBinarySpansCollapsesGarbage(t *testing.T) {
	// Decoded gzip-like garbage: control runes interleaved with short
	// printable islands and U+FFFD replacement chars.
	garbage := "\x1f\x8b\x08" + strings.Repeat("�a\x02u4\x16\x0fP�;s˖\x10J�", 20)
	in := "Highlights:\nreadable prefix\n" + garbage + "\nreadable suffix"
	got := CollapseBinarySpans(in)
	if !strings.Contains(got, "readable prefix") || !strings.Contains(got, "readable suffix") {
		t.Fatalf("surrounding text lost: %q", got)
	}
	if !strings.Contains(got, "[binary data omitted:") {
		t.Fatalf("missing omission marker: %q", got)
	}
	if strings.ContainsRune(got, '�') || strings.ContainsRune(got, '\x02') {
		t.Fatalf("garbage runes survived: %q", got)
	}
}

func TestCollapseBinarySpansMergesSeparateSpansIntoDistinctMarkers(t *testing.T) {
	spanA := strings.Repeat("�\x01x", 6)
	spanB := strings.Repeat("\x05y�", 6)
	in := "start " + spanA + strings.Repeat(" clean middle sentence.", 3) + spanB + " end"
	got := CollapseBinarySpans(in)
	if strings.Count(got, "[binary data omitted:") != 2 {
		t.Fatalf("want 2 distinct markers, got %q", got)
	}
	if !strings.Contains(got, "clean middle sentence.") {
		t.Fatalf("middle text lost: %q", got)
	}
}

func TestCollapseBinarySpansReportsRuneCount(t *testing.T) {
	span := strings.Repeat("�", 30)
	got := CollapseBinarySpans(span)
	if !strings.Contains(got, "[binary data omitted: 30 chars]") {
		t.Fatalf("marker should report rune count, got %q", got)
	}
}
