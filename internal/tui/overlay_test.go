package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestDimLineWrapsPlainInput checks the simple path: input with no SGR
// tokens. The dim prefix and reset suffix should appear once each, and
// the visible text should be unchanged after stripping.
func TestDimLineWrapsPlainInput(t *testing.T) {
	got := dimLine("hello world")
	if !strings.HasPrefix(got, dimEnable) {
		t.Errorf("dimLine missing dim prefix: %q", got)
	}
	if !strings.HasSuffix(got, dimReset) {
		t.Errorf("dimLine missing reset suffix: %q", got)
	}
	if !strings.Contains(got, "\x1b[38;5;240m") {
		t.Errorf("dimLine missing 256-color SGR: %q", got)
	}
	if !strings.Contains(got, "\x1b[2m") {
		t.Errorf("dimLine missing faint SGR: %q", got)
	}
	if ansi.Strip(got) != "hello world" {
		t.Errorf("dimLine altered visible text: stripped=%q", ansi.Strip(got))
	}
}

// TestDimLineDropsExistingSGR makes sure pre-existing color SGRs in the
// input are removed before the dim wrap, otherwise they'd leak through
// (e.g. a green status chip stays green even under the modal).
func TestDimLineDropsExistingSGR(t *testing.T) {
	colored := "\x1b[31mred\x1b[0m and \x1b[1;32mbold green\x1b[0m"
	got := dimLine(colored)
	// Only the dim wrap's SGR tokens should remain; the original red /
	// bold-green / mid-string reset must be gone.
	stripped := ansi.Strip(got)
	if stripped != "red and bold green" {
		t.Errorf("dimLine altered visible text: %q", stripped)
	}
	if strings.Contains(got, "\x1b[31m") || strings.Contains(got, "\x1b[1;32m") {
		t.Errorf("dimLine left original SGR in place: %q", got)
	}
}

// TestDimLineEmptyPassthrough — emitting a dim wrap around nothing would
// register as a non-empty line and throw off line counts. dimLine returns
// the input verbatim for blank lines.
func TestDimLineEmptyPassthrough(t *testing.T) {
	if got := dimLine(""); got != "" {
		t.Errorf("dimLine(\"\") = %q, want empty", got)
	}
}

// TestComposeOverlayPlacesModalCenter verifies the modal box lands at the
// arithmetic center of the base, that the row count is preserved, and
// that backdrop rows around the modal are dimmed.
func TestComposeOverlayPlacesModalCenter(t *testing.T) {
	const baseW, baseH = 40, 10
	base := strings.Repeat(strings.Repeat("x", baseW)+"\n", baseH-1) + strings.Repeat("x", baseW)
	modal := "AAAAA\nBBBBB\nCCCCC"

	got := composeOverlay(base, modal, baseW, baseH)
	lines := strings.Split(got, "\n")
	if len(lines) != baseH {
		t.Fatalf("composeOverlay returned %d lines, want %d", len(lines), baseH)
	}

	// modalH=3 → top = (10-3)/2 = 3 (rows 3,4,5). modalW=5 → left = (40-5)/2 = 17.
	wantRows := []string{"AAAAA", "BBBBB", "CCCCC"}
	for i, want := range wantRows {
		row := lines[3+i]
		stripped := ansi.Strip(row)
		if !strings.Contains(stripped, want) {
			t.Errorf("row %d missing modal content %q: stripped=%q", 3+i, want, stripped)
		}
	}

	// Backdrop rows (row 0, 1, 2, 6, 7, 8, 9) must be dimmed.
	for _, idx := range []int{0, 1, 2, 6, 7, 8, 9} {
		if !strings.Contains(lines[idx], "\x1b[38;5;240m") {
			t.Errorf("backdrop row %d not dimmed: %q", idx, lines[idx])
		}
	}
}

// TestComposeOverlayPreservesWidth — the visible width of every row in
// the composed output must equal baseW. A drift here would visibly tear
// the right border of the 3-pane layout.
func TestComposeOverlayPreservesWidth(t *testing.T) {
	const baseW, baseH = 30, 6
	base := strings.Repeat(strings.Repeat("x", baseW)+"\n", baseH-1) + strings.Repeat("x", baseW)
	modal := "MMMM\nMMMM"
	got := composeOverlay(base, modal, baseW, baseH)
	for i, row := range strings.Split(got, "\n") {
		if w := lipgloss.Width(row); w != baseW {
			t.Errorf("row %d width = %d, want %d (row=%q)", i, w, baseW, ansi.Strip(row))
		}
	}
}

// TestComposeOverlayClampsOversizedModal — a modal larger than the base
// should be clamped to base dimensions instead of panicking, and the
// resulting frame should still match base height/width.
func TestComposeOverlayClampsOversizedModal(t *testing.T) {
	base := "abc\ndef\nghi"
	const baseW, baseH = 3, 3
	modal := strings.Repeat("M", 20) + "\n" + strings.Repeat("M", 20) + "\n" +
		strings.Repeat("M", 20) + "\n" + strings.Repeat("M", 20) + "\n" +
		strings.Repeat("M", 20)
	got := composeOverlay(base, modal, baseW, baseH)
	rows := strings.Split(got, "\n")
	if len(rows) != baseH {
		t.Fatalf("clamped overlay row count = %d, want %d", len(rows), baseH)
	}
	for i, row := range rows {
		if w := lipgloss.Width(row); w != baseW {
			t.Errorf("clamped row %d width = %d, want %d", i, w, baseW)
		}
	}
}

// TestComposeOverlayStrippedShapeMatches — the strip of the composed
// frame should be a drop-in shape for the base (same row count, same
// row widths). Belt-and-braces protection against an SGR cut bug that
// silently shortens a row.
func TestComposeOverlayStrippedShapeMatches(t *testing.T) {
	const baseW, baseH = 24, 8
	base := strings.Repeat(strings.Repeat("x", baseW)+"\n", baseH-1) + strings.Repeat("x", baseW)
	modal := "AAA\nBBB"
	got := composeOverlay(base, modal, baseW, baseH)
	gotRows := strings.Split(got, "\n")
	for i, row := range gotRows {
		stripped := ansi.Strip(row)
		if w := lipgloss.Width(stripped); w != baseW {
			t.Errorf("stripped row %d width = %d, want %d (%q)", i, w, baseW, stripped)
		}
	}
}
