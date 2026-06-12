package tui

import "fmt"

// renderScrollWindow renders rows [top, top+visibleRows) of a count-sized
// list, sandwiched between "↑ N more" / "↓ N more" overflow markers when
// rows fall outside the window. top is clamped so the window always stays
// inside the list; callers own the window-origin policy (cursor-centered
// in the branches modal, lazy viewportTop in the branch picker).
func renderScrollWindow(top, visibleRows, count int, renderRow func(i int) string) []string {
	if top < 0 {
		top = 0
	}
	end := top + visibleRows
	if end > count {
		end = count
		top = end - visibleRows
		if top < 0 {
			top = 0
		}
	}

	var lines []string
	if top > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("↑ %d more", top)))
	}
	for i := top; i < end; i++ {
		lines = append(lines, renderRow(i))
	}
	if rest := count - end; rest > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("↓ %d more", rest)))
	}
	return lines
}
