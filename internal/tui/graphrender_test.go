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
	if stripped != "● " {
		t.Errorf("stripped = %q, want %q", stripped, "● ")
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

// TestRenderConnectorForkSpansHorizontal — a fork connector (├ followed by
// ╮) should fill the trailing space with `─`, producing `├─╮` visually.
func TestRenderConnectorForkSpansHorizontal(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellTeeRight, Lane: 0},
			{Kind: lanes.CellCornerTR, Lane: 1},
		},
		CommitLane: -1,
	}
	text, _ := renderGraphRow(row)
	stripped := ansi.Strip(text)
	// Expect ├─╮ followed by a trailing space (last cell gets a plain
	// space, not a horizontal).
	if !strings.HasPrefix(stripped, "├─╮") {
		t.Errorf("stripped = %q, want prefix %q (horizontal should fill the gap)",
			stripped, "├─╮")
	}
	if !strings.Contains(stripped, "─") {
		t.Errorf("stripped = %q, expected `─` between connecting cells", stripped)
	}
}

// TestRenderConnectorMergeArmHorizontal — a merge connector (├ followed
// by ╯) also fills the trailing column, producing `├─╯`.
func TestRenderConnectorMergeArmHorizontal(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellTeeRight, Lane: 0},
			{Kind: lanes.CellCornerBR, Lane: 1},
		},
		CommitLane: -1,
	}
	text, _ := renderGraphRow(row)
	stripped := ansi.Strip(text)
	if !strings.HasPrefix(stripped, "├─╯") {
		t.Errorf("stripped = %q, want prefix %q", stripped, "├─╯")
	}
}

// TestRenderConnectorOctopusFork — three-way fork lays out as ├─┬─╮ with
// horizontal fills in both gaps.
func TestRenderConnectorOctopusFork(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellTeeRight, Lane: 0},
			{Kind: lanes.CellTeeDown, Lane: 1},
			{Kind: lanes.CellCornerTR, Lane: 2},
		},
		CommitLane: -1,
	}
	text, _ := renderGraphRow(row)
	stripped := ansi.Strip(text)
	if !strings.HasPrefix(stripped, "├─┬─╮") {
		t.Errorf("stripped = %q, want prefix %q", stripped, "├─┬─╮")
	}
}

// TestRenderConnectorPipesNoHorizontal — two pass-through pipes do NOT
// connect horizontally; the trailing column stays a plain space.
func TestRenderConnectorPipesNoHorizontal(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellPipe, Lane: 0},
			{Kind: lanes.CellPipe, Lane: 1},
		},
		CommitLane: -1,
	}
	text, _ := renderGraphRow(row)
	stripped := ansi.Strip(text)
	if strings.Contains(stripped, "─") {
		t.Errorf("stripped = %q, plain pipes should not be connected by `─`",
			stripped)
	}
}

// Trailing CellEmpty slots are dropped — the graph cell shrinks to the
// rightmost non-empty cell so the message column hugs the graph.
func TestRenderGraphRowTrimsTrailingEmpty(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellPipe, Lane: 0},
			{Kind: lanes.CellCommit, Lane: 1},
			{Kind: lanes.CellEmpty},
			{Kind: lanes.CellEmpty},
		},
		CommitLane: 1,
	}
	_, w := renderGraphRow(row)
	if want := 2 * cellWidth; w != want {
		t.Errorf("visualWidth = %d, want %d (trailing empty trimmed)", w, want)
	}
}

// Active lanes to the right of the commit (e.g., another branch's pipe)
// must be preserved — only trailing empties get trimmed.
func TestRenderGraphRowKeepsActiveLanesAfterCommit(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellPipe, Lane: 0},
			{Kind: lanes.CellCommit, Lane: 1},
			{Kind: lanes.CellPipe, Lane: 2},
		},
		CommitLane: 1,
	}
	text, w := renderGraphRow(row)
	if want := 3 * cellWidth; w != want {
		t.Errorf("visualWidth = %d, want %d (active lane 2 must stay)", w, want)
	}
	stripped := ansi.Strip(text)
	if strings.Count(stripped, "│") != 2 {
		t.Errorf("stripped = %q, want both lane 0 and lane 2 pipes preserved", stripped)
	}
}

// An entirely empty row renders as zero width.
func TestRenderGraphRowAllEmptyIsZeroWidth(t *testing.T) {
	row := lanes.Row{
		Cells: []lanes.Cell{
			{Kind: lanes.CellEmpty},
			{Kind: lanes.CellEmpty},
		},
		CommitLane: -1,
	}
	text, w := renderGraphRow(row)
	if w != 0 || text != "" {
		t.Errorf("all-empty row should render as zero width; got w=%d text=%q", w, text)
	}
}

// All glyphs we emit must be single-cell so visual width math stays honest.
func TestGlyphsAreSingleCell(t *testing.T) {
	for _, k := range []lanes.CellKind{
		lanes.CellPipe, lanes.CellCommit,
		lanes.CellHoriz,
		lanes.CellCornerTL, lanes.CellCornerTR,
		lanes.CellCornerBL, lanes.CellCornerBR,
		lanes.CellTeeRight, lanes.CellTeeLeft,
		lanes.CellTeeDown, lanes.CellTeeUp,
		lanes.CellCross,
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
