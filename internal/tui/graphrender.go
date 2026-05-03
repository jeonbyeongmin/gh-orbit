package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/git/lanes"
)

// Each lane reserves cellWidth columns: one for the glyph, one for spacing.
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

func glyphFor(k lanes.CellKind) string {
	switch k {
	case lanes.CellPipe:
		return "|"
	case lanes.CellCommit:
		return "*"
	case lanes.CellMergeLeft, lanes.CellForkRight:
		return "\\"
	case lanes.CellMergeRight, lanes.CellForkLeft:
		return "/"
	default:
		return " "
	}
}

// renderGraphRow returns the colored prefix for one row plus its visual
// column width. Width is len(Cells) * cellWidth — having it up front lets
// the commit-line renderer pad without re-parsing ANSI escapes.
func renderGraphRow(row lanes.Row) (text string, visualWidth int) {
	var b strings.Builder
	b.Grow(len(row.Cells) * (cellWidth + 8))
	for _, cell := range row.Cells {
		if cell.Kind == lanes.CellEmpty {
			b.WriteString("  ")
			continue
		}
		styled := laneStyles[cell.Lane%len(laneStyles)].Render(glyphFor(cell.Kind))
		b.WriteString(styled)
		b.WriteByte(' ')
	}
	return b.String(), len(row.Cells) * cellWidth
}
