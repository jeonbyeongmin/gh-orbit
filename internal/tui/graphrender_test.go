package tui

import (
	"fmt"
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
	if stripped != "* " {
		t.Errorf("stripped = %q, want %q", stripped, "* ")
	}
}

// Merge / fork diagonals are rendered blank — verify no slash glyphs leak
// into the output and the column reservation is unchanged.
func TestRenderGraphRowMergeIsBlank(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellCommit, Lane: 0},
			{Kind: lanes.CellMergeRight, Lane: 1},
		},
		CommitLane: 0,
	}
	text, w := renderGraphRow(row)
	if w != 2*cellWidth {
		t.Errorf("visualWidth = %d, want %d", w, 2*cellWidth)
	}
	stripped := ansi.Strip(text)
	if strings.ContainsAny(stripped, `/\`) {
		t.Errorf("merge cell should render blank, got %q", stripped)
	}
}

func TestRenderGraphRowOctopusForkIsBlank(t *testing.T) {
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
	if strings.ContainsAny(stripped, `/\`) {
		t.Errorf("fork cells should render blank, got %q", stripped)
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

// At least 6 distinct lane styles (interview decision: 6-color rotation).
func TestLaneStylesAreSixUnique(t *testing.T) {
	if len(laneStyles) < 6 {
		t.Errorf("len(laneStyles) = %d, want >= 6 (interview decision)", len(laneStyles))
	}
	// GetForeground returns a TerminalColor whose stringer prints the underlying
	// color spec; that's enough to detect duplicates without poking internals.
	seen := map[string]bool{}
	for _, s := range laneStyles {
		key := fmt.Sprintf("%v", s.GetForeground())
		if seen[key] {
			t.Errorf("duplicate foreground in rotation: %s", key)
		}
		seen[key] = true
	}
}
