package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git/lanes"
)

func TestRenderGraphRowLinear(t *testing.T) {
	row := lanes.Row{
		Cells:      []lanes.Cell{{Kind: lanes.CellCommit, Lane: 0}},
		CommitLane: 0,
	}
	text, w := renderGraphRow(row)
	if w != cellWidth {
		t.Errorf("visualWidth = %d, want %d", w, cellWidth)
	}
	stripped := ansi.Strip(text)
	if stripped != "● " {
		t.Errorf("stripped = %q, want %q", stripped, "● ")
	}
}

func TestRenderGraphRowMergeFromRight(t *testing.T) {
	// "●╱" pattern — commit at lane 0, lane 1 merges in.
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellCommit, Lane: 0},
			{Kind: lanes.CellMergeRight, Lane: 1},
		},
		CommitLane: 0,
	}
	text, _ := renderGraphRow(row)
	stripped := ansi.Strip(text)
	if !strings.Contains(stripped, "●") || !strings.Contains(stripped, "╱") {
		t.Errorf("stripped = %q, want ● and ╱", stripped)
	}
	if strings.Index(stripped, "●") >= strings.Index(stripped, "╱") {
		t.Errorf("● should come before ╱ in %q", stripped)
	}
}

func TestRenderGraphRowOctopusFork(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellCommit, Lane: 0},
			{Kind: lanes.CellForkRight, Lane: 1},
			{Kind: lanes.CellForkRight, Lane: 2},
		},
		CommitLane: 0,
	}
	text, w := renderGraphRow(row)
	if w != 3*cellWidth {
		t.Errorf("visualWidth = %d, want %d", w, 3*cellWidth)
	}
	stripped := ansi.Strip(text)
	if !strings.Contains(stripped, "●╲") && !strings.Contains(stripped, "● ╲") {
		t.Errorf("expected dot followed by ╲ in %q", stripped)
	}
	if strings.Count(stripped, "╲") != 2 {
		t.Errorf("expected 2 fork glyphs in %q", stripped)
	}
}

func TestRenderGraphRowEmptyCellPadsTwoColumns(t *testing.T) {
	// Lane 0 has a pipe, lane 1 is empty (a freed slot), lane 2 has a pipe.
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellPipe, Lane: 0},
			{Kind: lanes.CellEmpty},
			{Kind: lanes.CellPipe, Lane: 2},
		},
		CommitLane: -1,
	}
	text, _ := renderGraphRow(row)
	stripped := ansi.Strip(text)
	if w := runewidth.StringWidth(stripped); w != 3*cellWidth {
		t.Errorf("rendered width = %d, want %d (text=%q)", w, 3*cellWidth, stripped)
	}
}

// All glyphs we emit must be single-cell so visual width math stays honest.
func TestGlyphsAreSingleCell(t *testing.T) {
	for _, k := range []lanes.CellKind{
		lanes.CellPipe, lanes.CellCommit,
		lanes.CellMergeLeft, lanes.CellMergeRight,
		lanes.CellForkLeft, lanes.CellForkRight,
	} {
		g := glyphFor(k)
		if w := runewidth.StringWidth(g); w != 1 {
			t.Errorf("glyphFor(%v) = %q, runewidth = %d, want 1", k, g, w)
		}
	}
}

// We can't easily assert ANSI escapes in a non-tty test environment
// (lipgloss strips them). Instead pin the rotation: at least 6 distinct
// colors and stable modulo behaviour.
func TestLaneColorsAreSixUnique(t *testing.T) {
	if len(laneColors) < 6 {
		t.Errorf("len(laneColors) = %d, want >= 6 (interview decision)", len(laneColors))
	}
	seen := map[string]bool{}
	for _, c := range laneColors {
		if seen[string(c)] {
			t.Errorf("duplicate color in rotation: %s", c)
		}
		seen[string(c)] = true
	}
}
