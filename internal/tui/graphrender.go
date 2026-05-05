package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/git/lanes"
)

// Each lane reserves cellWidth columns: one for the glyph, one for the
// trailing space (or `─` when the cell connects horizontally to its right
// neighbor).
const cellWidth = 2

// laneStyles is the 6-color rotation, pre-built once so per-cell rendering
// reuses the same Style instance instead of re-allocating on every row.
var laneStyles = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("212")), // pink
	lipgloss.NewStyle().Foreground(lipgloss.Color("215")), // orange
	lipgloss.NewStyle().Foreground(lipgloss.Color("228")), // yellow
	lipgloss.NewStyle().Foreground(lipgloss.Color("120")), // green
	lipgloss.NewStyle().Foreground(lipgloss.Color("117")), // cyan
	lipgloss.NewStyle().Foreground(lipgloss.Color("183")), // lavender
}

// glyphFor maps a CellKind to its single-rune glyph. All glyphs are width 1
// so cellWidth = glyph + trailing — the trailing char is decided by
// renderGraphRow based on whether the cell connects horizontally to the
// next one.
func glyphFor(k lanes.CellKind) string {
	switch k {
	case lanes.CellCommit:
		return "●"
	case lanes.CellPipe:
		return "│"
	case lanes.CellHoriz:
		return "─"
	case lanes.CellCornerTL:
		return "╭"
	case lanes.CellCornerTR:
		return "╮"
	case lanes.CellCornerBL:
		return "╰"
	case lanes.CellCornerBR:
		return "╯"
	case lanes.CellTeeRight:
		return "├"
	case lanes.CellTeeLeft:
		return "┤"
	case lanes.CellTeeDown:
		return "┬"
	case lanes.CellTeeUp:
		return "┴"
	case lanes.CellCross:
		return "┼"
	default:
		return " "
	}
}

// cellSides records the four open sides of every routing CellKind in one
// place — single source of truth so adding a kind needs only one edit.
// Empty zero-value (all false) handles CellEmpty / CellCommit / unknown
// kinds without explicit entries.
var cellSides = map[lanes.CellKind]struct{ up, down, left, right bool }{
	lanes.CellPipe:     {up: true, down: true},
	lanes.CellHoriz:    {left: true, right: true},
	lanes.CellCornerTL: {down: true, right: true},
	lanes.CellCornerTR: {down: true, left: true},
	lanes.CellCornerBL: {up: true, right: true},
	lanes.CellCornerBR: {up: true, left: true},
	lanes.CellTeeRight: {up: true, down: true, right: true},
	lanes.CellTeeLeft:  {up: true, down: true, left: true},
	lanes.CellTeeDown:  {down: true, left: true, right: true},
	lanes.CellTeeUp:    {up: true, left: true, right: true},
	lanes.CellCross:    {up: true, down: true, left: true, right: true},
}

// isRightOpen / isLeftOpen drive the horizontal trail decision in
// renderGraphRow: the trailing column gets `─` only when this cell's
// right opens onto the next cell's left.
func isRightOpen(k lanes.CellKind) bool { return cellSides[k].right }
func isLeftOpen(k lanes.CellKind) bool  { return cellSides[k].left }

// renderGraphRow returns the colored prefix for one row plus its visual
// column width. Width is len(Cells) * cellWidth — having it up front lets
// the commit-line renderer pad without re-parsing ANSI escapes.
//
// The trailing column for each cell is `─` when both this cell and the
// next one have their facing sides open (e.g., `├` followed by `╮`); else
// it's a plain space.
func renderGraphRow(row lanes.Row) (text string, visualWidth int) {
	if len(row.Cells) == 0 {
		return "", 0
	}
	var b strings.Builder
	b.Grow(len(row.Cells) * (cellWidth + 8))
	for i, cell := range row.Cells {
		if cell.Kind == lanes.CellEmpty {
			b.WriteString("  ")
			continue
		}
		style := laneStyles[laneColorIdx(cell.Lane)]
		b.WriteString(style.Render(glyphFor(cell.Kind)))

		// Trailing column: ─ if this cell connects right and the next
		// cell connects left; else a plain space.
		if i < len(row.Cells)-1 &&
			isRightOpen(cell.Kind) &&
			isLeftOpen(row.Cells[i+1].Kind) {
			b.WriteString(style.Render("─"))
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String(), len(row.Cells) * cellWidth
}

func laneColorIdx(lane int) int {
	if lane < 0 {
		return 0
	}
	return lane % len(laneStyles)
}
