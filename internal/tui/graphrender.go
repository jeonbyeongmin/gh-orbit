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

// laneStyles is the 8-color rotation, pre-built once so per-cell rendering
// reuses the same Style instance instead of re-allocating on every row.
// Hues are ordered so rotation neighbors stay far apart on the wheel — an
// 8-lane repo never puts two similar colors side by side.
var laneStyles = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("212")), // pink
	lipgloss.NewStyle().Foreground(lipgloss.Color("117")), // cyan
	lipgloss.NewStyle().Foreground(lipgloss.Color("215")), // orange
	lipgloss.NewStyle().Foreground(lipgloss.Color("120")), // green
	lipgloss.NewStyle().Foreground(lipgloss.Color("183")), // lavender
	lipgloss.NewStyle().Foreground(lipgloss.Color("228")), // yellow
	lipgloss.NewStyle().Foreground(lipgloss.Color("75")),  // blue
	lipgloss.NewStyle().Foreground(lipgloss.Color("168")), // rose
}

// commitGlyph / mergeGlyph / headGlyph are the three commit-dot weights:
// a regular commit is solid, a merge commit is hollow (it's plumbing —
// the interesting work lives on the replayed/merged commits), and the
// HEAD row's dot is swapped to ◉ at render time by the delegate. All are
// single-cell.
const (
	commitGlyph = "●"
	mergeGlyph  = "○"
	headGlyph   = "◉"
)

// glyphFor maps a CellKind to its single-rune glyph. All glyphs are width 1
// so cellWidth = glyph + trailing — the trailing char is decided by
// renderGraphRow based on whether the cell connects horizontally to the
// next one.
func glyphFor(k lanes.CellKind) string {
	switch k {
	case lanes.CellCommit:
		return commitGlyph
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
// column width. Width is (last+1)*cellWidth where `last` is the rightmost
// non-empty cell — trailing CellEmpty slots that the lane allocator may
// keep around contribute neither glyph nor width, so the message column
// hugs the graph instead of sitting after a stretch of blank cells.
// In-the-middle CellEmpty cells (between active lanes) are preserved so
// lane lifelines stay visually consistent.
//
// The trailing column for each cell is `─` when both this cell and the
// next one have their facing sides open (e.g., `├` followed by `╮`); else
// it's a plain space.
//
// merge=true renders the row's commit cell hollow (mergeGlyph) — passed by
// the stream collector for 2+-parent commits; connector rows carry no
// commit cell so their callers pass false.
func renderGraphRow(row lanes.Row, merge bool) (text string, visualWidth int) {
	if len(row.Cells) == 0 {
		return "", 0
	}
	last := -1
	for i := len(row.Cells) - 1; i >= 0; i-- {
		if row.Cells[i].Kind != lanes.CellEmpty {
			last = i
			break
		}
	}
	if last < 0 {
		return "", 0
	}

	var b strings.Builder
	b.Grow((last + 1) * (cellWidth + 8))
	for i := 0; i <= last; i++ {
		cell := row.Cells[i]
		if cell.Kind == lanes.CellEmpty {
			b.WriteString("  ")
			continue
		}
		style := laneStyles[laneColorIdx(cell.Lane)]
		glyph := glyphFor(cell.Kind)
		if merge && cell.Kind == lanes.CellCommit {
			glyph = mergeGlyph
		}
		b.WriteString(style.Render(glyph))

		// Trailing column: ─ if this cell connects right and the next
		// cell connects left; else a plain space.
		if i < last &&
			isRightOpen(cell.Kind) &&
			isLeftOpen(row.Cells[i+1].Kind) {
			b.WriteString(style.Render("─"))
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String(), (last + 1) * cellWidth
}

func laneColorIdx(lane int) int {
	if lane < 0 {
		return 0
	}
	return lane % len(laneStyles)
}
