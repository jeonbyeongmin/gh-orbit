package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/git/lanes"
)

// Each lane reserves cellWidth columns: one for the glyph, one for spacing.
// That keeps adjacent lanes visually distinct without sacrificing density.
const cellWidth = 2

// laneColors is the 6-color rotation. Lane index modulo this slice's length
// picks the foreground; lipgloss handles NO_COLOR / dumb terminals.
var laneColors = []lipgloss.Color{
	lipgloss.Color("212"), // pink
	lipgloss.Color("215"), // orange
	lipgloss.Color("228"), // yellow
	lipgloss.Color("120"), // green
	lipgloss.Color("117"), // cyan
	lipgloss.Color("183"), // lavender
}

func glyphFor(k lanes.CellKind) string {
	switch k {
	case lanes.CellPipe:
		return "│"
	case lanes.CellCommit:
		return "●"
	case lanes.CellMergeLeft, lanes.CellForkRight:
		return "╲"
	case lanes.CellMergeRight, lanes.CellForkLeft:
		return "╱"
	default:
		return " "
	}
}

// renderGraphRow returns the colored ASCII-art prefix for one row plus its
// visual column width. Width is len(Cells) * cellWidth — knowing the visual
// width up front lets the commit-line renderer reserve the column without
// re-stripping ANSI escapes.
func renderGraphRow(row lanes.Row) (text string, visualWidth int) {
	var b strings.Builder
	for _, cell := range row.Cells {
		if cell.Kind == lanes.CellEmpty {
			b.WriteString("  ")
			continue
		}
		color := laneColors[cell.Lane%len(laneColors)]
		styled := lipgloss.NewStyle().Foreground(color).Render(glyphFor(cell.Kind))
		b.WriteString(styled)
		b.WriteByte(' ')
	}
	return b.String(), len(row.Cells) * cellWidth
}
