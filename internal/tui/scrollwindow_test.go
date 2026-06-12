package tui

import (
	"strconv"
	"strings"
	"testing"
)

// rowID renders a row as its index so window membership is assertable
// without styling noise.
func rowID(i int) string { return strconv.Itoa(i) }

func TestRenderScrollWindowAllRowsFit(t *testing.T) {
	lines := renderScrollWindow(0, 5, 3, rowID)
	if len(lines) != 3 {
		t.Fatalf("want 3 rows, no markers; got %d lines: %v", len(lines), lines)
	}
	for i, l := range lines {
		if l != strconv.Itoa(i) {
			t.Errorf("line %d = %q, want %q", i, l, strconv.Itoa(i))
		}
	}
}

func TestRenderScrollWindowOverflowBothSides(t *testing.T) {
	lines := renderScrollWindow(3, 4, 10, rowID)
	if len(lines) != 6 {
		t.Fatalf("want ↑ marker + 4 rows + ↓ marker; got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "↑ 3 more") {
		t.Errorf("top marker = %q, want ↑ 3 more", lines[0])
	}
	if !strings.Contains(lines[5], "↓ 3 more") {
		t.Errorf("bottom marker = %q, want ↓ 3 more", lines[5])
	}
	for i := 0; i < 4; i++ {
		if lines[1+i] != strconv.Itoa(3+i) {
			t.Errorf("row %d = %q, want %q", i, lines[1+i], strconv.Itoa(3+i))
		}
	}
}

func TestRenderScrollWindowClampsPastEnd(t *testing.T) {
	// top beyond the list bottom slides back so the window stays full.
	lines := renderScrollWindow(9, 4, 10, rowID)
	if !strings.Contains(lines[0], "↑ 6 more") {
		t.Fatalf("top marker = %q, want ↑ 6 more", lines[0])
	}
	last := lines[len(lines)-1]
	if last != "9" {
		t.Errorf("last row = %q, want 9 (no ↓ marker)", last)
	}
}

func TestRenderScrollWindowClampsNegativeTop(t *testing.T) {
	lines := renderScrollWindow(-2, 4, 10, rowID)
	if lines[0] != "0" {
		t.Fatalf("first line = %q, want row 0 (no ↑ marker)", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "↓ 6 more") {
		t.Errorf("bottom marker = %q, want ↓ 6 more", lines[len(lines)-1])
	}
}
