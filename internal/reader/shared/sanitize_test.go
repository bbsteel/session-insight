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
	}
	for _, in := range cases {
		if got := CollapseBinarySpans(in); got != in {
			t.Fatalf("CollapseBinarySpans(%q) = %q, want unchanged", in, got)
		}
	}
}

// Isolated stray control/replacement runes are removed even when they do not
// form a collapsible span, so terminal renderers never see them.
func TestCollapseBinarySpansStripsIsolatedStrays(t *testing.T) {
	if got := CollapseBinarySpans("one\x01two"); got != "onetwo" {
		t.Fatalf("single control rune should be stripped, got %q", got)
	}
	if got := CollapseBinarySpans("short stray � char stays"); got != "short stray  char stays" {
		t.Fatalf("single replacement char should be stripped, got %q", got)
	}
}

// ANSI color/cursor markup is presentation, not content: strip it instead of
// collapsing the surrounding text as binary garbage.
func TestCollapseBinarySpansStripsANSIEscapes(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred file\x1b[0m":                         "red file",
		"ls \x1b[1;34mdir\x1b[0m done":                    "ls dir done",
		"\x1b]8;;https://example.com\alink\x1b]8;;\x1b\\": "link",
		"plain":                               "plain",
		"\x1b]unterminated but keep text\x1b": "",
	}
	for in, want := range cases {
		if got := CollapseBinarySpans(in); got != want {
			t.Fatalf("CollapseBinarySpans(%q) = %q, want %q", in, got, want)
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
	// The marker must not introduce whitespace at the newline boundary.
	if strings.Contains(got, "]\n") == false {
		t.Fatalf("marker should sit directly before the newline: %q", got)
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
	if got != "[binary data omitted: 30 chars]" {
		t.Fatalf("whole-string span should produce a bare marker, got %q", got)
	}
}
