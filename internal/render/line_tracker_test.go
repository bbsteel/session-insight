package render

import "testing"

func TestLineTrackerHardNewline(t *testing.T) {
	tr := newLineTracker(80)
	tr.Feed("hello\nworld\n")
	if tr.CurrentLine() != 2 {
		t.Fatalf("want 2, got %d", tr.CurrentLine())
	}
}

func TestLineTrackerSoftWrap(t *testing.T) {
	tr := newLineTracker(5)
	tr.Feed("abcde") // exactly 5 cols, no wrap
	if tr.CurrentLine() != 0 {
		t.Fatalf("no wrap expected, got line %d", tr.CurrentLine())
	}
	tr.Feed("f") // 6th char → soft wrap, now on line 1
	if tr.CurrentLine() != 1 {
		t.Fatalf("want 1 after soft wrap, got %d", tr.CurrentLine())
	}
}

func TestLineTrackerCJKSoftWrap(t *testing.T) {
	// Each CJK rune = 2 cols. cols=4 → 2 CJK per line.
	tr := newLineTracker(4)
	tr.Feed("中文") // 4 cols, no wrap
	if tr.CurrentLine() != 0 {
		t.Fatalf("want 0, got %d", tr.CurrentLine())
	}
	tr.Feed("字") // 2 cols needed, but only 0 left (col==4==cols) → soft wrap
	if tr.CurrentLine() != 1 {
		t.Fatalf("want 1, got %d", tr.CurrentLine())
	}
}

func TestLineTrackerANSISkipped(t *testing.T) {
	tr := newLineTracker(80)
	// ESC[32m green ESC[0m should not move the line counter
	tr.Feed("\x1b[32mhello\x1b[0m")
	if tr.CurrentLine() != 0 {
		t.Fatalf("ANSI codes must not advance line, got %d", tr.CurrentLine())
	}
}

func TestLineTrackerMixed(t *testing.T) {
	// 40 ASCII + 1 hard newline + 2 CJK(=4 cols on width=4) → wraps once
	tr := newLineTracker(40)
	tr.Feed("hello\nworld") // line 1 after \n, "world" = 5 cols < 40 → still line 1
	if tr.CurrentLine() != 1 {
		t.Fatalf("want 1, got %d", tr.CurrentLine())
	}
}

func TestLineTrackerExactFit(t *testing.T) {
	tr := newLineTracker(5)
	tr.Feed("abcde") // exactly fills cols, col == cols, no wrap yet
	if tr.CurrentLine() != 0 {
		t.Fatalf("exact fit: want line 0, got %d", tr.CurrentLine())
	}
	// Next rune triggers wrap
	tr.Feed("x")
	if tr.CurrentLine() != 1 {
		t.Fatalf("after overflow: want line 1, got %d", tr.CurrentLine())
	}
}
