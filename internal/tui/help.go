package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// helpEntry pairs a key chord with its action for help-panel rendering.
type helpEntry struct {
	keys, action string
}

// helpCategory groups entries under a pane label (Global / Refs / Graph / Tab).
// Categories are derived from CLAUDE.md's key-bindings "Pane" column: global
// rows go to Global, focused-pane / Changes-tab / Commit-tab rows fold into
// Tab, and refs / graph rows take their own categories.
type helpCategory struct {
	title   string
	entries []helpEntry
}

// helpData returns the categories rendered in the expanded `?` help panel,
// in display order. The shape of each row mirrors the action description
// in CLAUDE.md so the rendered panel and the doc stay in lock-step.
func helpData() []helpCategory {
	return []helpCategory{
		{
			title: "Global",
			entries: []helpEntry{
				{"tab", "focus"},
				{"?", "help"},
				{"q", "quit"},
				{"F", "fetch"},
				{"P", "pull"},
				{"r", "reload"},
				{"d", "patch"},
				{"ctrl+↑/↓", "resize"},
			},
		},
		{
			title: "Refs",
			entries: []helpEntry{
				{"enter", "checkout"},
				{"o", "jump to tip"},
				{"a", "all refs"},
			},
		},
		{
			title: "Graph",
			entries: []helpEntry{
				{"C", "detach"},
			},
		},
		{
			title: "Tab",
			entries: []helpEntry{
				{"h/l", "switch"},
				{"j/k", "nav"},
				{"g/G", "top/bot"},
				{"y", "copy"},
				{"ctrl+d/u", "scroll patch"},
			},
		},
	}
}

// paneHints returns the focus-aware single-line hint for each pane. Every
// hint ends with `? help · q quit` so the user always sees how to expand
// the panel or quit, regardless of which pane has focus.
func paneHints() map[pane]string {
	return map[pane]string{
		paneRefs:  "enter checkout · o jump · ? help · q quit",
		paneGraph: "enter/d patch · C detach · ? help · q quit",
		paneTab:   "h/l switch · y copy · ? help · q quit",
	}
}

// renderHelpPanel composes the expanded `?` help panel as a multi-line
// string capped at `height` rows. Each category occupies two rows: a
// `[Title]` header followed by its entries joined inline with `·`. Rows
// past the cap are dropped (clamped terminals show fewer categories rather
// than overflow). Width truncation is row-by-row.
func renderHelpPanel(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	var lines []string
	for _, c := range helpData() {
		lines = append(lines, "["+c.title+"]")
		parts := make([]string, len(c.entries))
		for i, e := range c.entries {
			parts[i] = e.keys + " " + e.action
		}
		lines = append(lines, "  "+strings.Join(parts, " · "))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	rendered := make([]string, len(lines))
	for i, ln := range lines {
		if lipgloss.Width(ln) > width {
			ln = runewidth.Truncate(ln, width, "…")
		}
		rendered[i] = help.Render(ln)
	}
	return strings.Join(rendered, "\n")
}
