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

// helpCategory groups entries under a pane label. Categories mirror
// docs/architecture.md's key-bindings table.
type helpCategory struct {
	title   string
	entries []helpEntry
}

// helpCategories is the source of truth for the expanded `?` panel. Keep
// row order in sync with the docs/architecture.md table — the panel and
// the doc are supposed to be readable side-by-side.
var helpCategories = []helpCategory{
	{
		title: "Global",
		entries: []helpEntry{
			{"?", "help"},
			{"q", "quit"},
			{"F", "fetch"},
			{"p", "pull"},
			{"r", "reload"},
			{",", "local changes"},
			{"w", "worktrees modal"},
			{"b", "branches modal"},
			{"Z", "zombie cleanup"},
		},
	},
	{
		title: "Graph",
		entries: []helpEntry{
			{"j/k", "nav"},
			{"g/G", "top/bot"},
			{"enter", "checkout / ff / detach"},
			{"d", "patch overlay"},
			{"y", "copy hash"},
		},
	},
	{
		title: "Local Changes",
		entries: []helpEntry{
			{"space", "stage/unstage"},
			{"j/k", "nav"},
			{"g/G", "top/bot"},
			{"tab", "focus tree/diff"},
			{"r", "reload"},
			{",", "exit"},
		},
	},
}

func helpData() []helpCategory { return helpCategories }

// graphHintText is the single-line bottom hint for the graph pane (the
// only outer focus after the bottom tab pane retired). Ends with `? help
// · q quit` so the user always sees how to expand the panel or quit.
const graphHintText = "enter checkout/ff/detach · d patch · y copy · w worktree focus · b branches · Z zombies · ? help · q quit"

// graphHintRendered is the pre-styled form of graphHintText. View() runs
// on every Update so re-applying the help style per frame would burn a
// Lipgloss render for nothing.
var graphHintRendered = help.Render(graphHintText)

// localChangesHintText is the mode-specific bottom hint shown while
// viewModeLocalChanges owns the right column. It overrides the focused
// pane's hint because the keymap inside the mode is mode-scoped (space /
// tab cycle / r reload / , exit), not pane-scoped.
const localChangesHintText = "space stage/unstage · tab focus · r reload · , exit · ? help · q quit"

var localChangesHintRendered = help.Render(localChangesHintText)

// dashboardFocusHintText is the bottom hint shown while paneDashboard
// owns the cursor. Replaces the graph hint so the user can see the
// dashboard-scoped key matrix instead of repeating the graph one.
const dashboardFocusHintText = "dashboard: j/k 이동 · enter switch · a add · d remove · esc 종료"

var dashboardFocusHintRendered = help.Render(dashboardFocusHintText)

// fitHelpLine truncates text to width with an ellipsis when the rendered
// content overflows, then applies the help style. Shared by the focus-aware
// bottom hint (renderHelpStatus) and the expanded panel (renderHelpPanel)
// so both honor the same width budget identically.
func fitHelpLine(text string, width int) string {
	if lipgloss.Width(text) > width {
		text = runewidth.Truncate(text, width, "…")
	}
	return help.Render(text)
}

// helpTextBranchPicker is the bottom hint shown while the branch picker
// modal is open. The picker swallows everything but j/k/enter/esc, so
// the hint enumerates exactly what works.
const helpTextBranchPicker = "j/k navigate · enter checkout · esc cancel"

// renderHelpPanel composes the expanded `?` help panel as a multi-line
// string capped at `height` rows. Each category emits a `[Title]` header
// row plus a single entries row joined inline with `·`. Rows past the cap
// are dropped — clamped terminals show fewer categories rather than
// overflowing into the main area.
func renderHelpPanel(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	var lines []string
	for _, c := range helpCategories {
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
		rendered[i] = fitHelpLine(ln, width)
	}
	return strings.Join(rendered, "\n")
}
