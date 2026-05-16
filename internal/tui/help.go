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
// Categories mirror CLAUDE.md's key-bindings "Pane" column: global rows
// fold into Global; focused-pane / Changes-tab / Commit-tab rows fold into
// Tab; refs and graph each take their own category.
type helpCategory struct {
	title   string
	entries []helpEntry
}

// helpCategories is the source of truth for the expanded `?` panel. Keep
// row order in sync with the CLAUDE.md table — the panel and the doc are
// supposed to be readable side-by-side.
var helpCategories = []helpCategory{
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
			{"p", "checkout+pull"},
			{"o", "jump to tip"},
			{"a", "all refs"},
			{"n", "new branch"},
			{"d", "delete branch / drop stash"},
			{"m", "rename"},
		},
	},
	{
		title: "Graph",
		entries: []helpEntry{
			{"enter", "go (stash row → pop/apply)"},
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

func helpData() []helpCategory { return helpCategories }

// paneHintTexts is the focus-aware single-line bottom hint per pane. Every
// hint ends with `? help · q quit` so the user always sees how to expand
// the panel or quit, regardless of which pane has focus.
var paneHintTexts = map[pane]string{
	paneRefs:  "enter checkout · p +pull · n new · d del/drop · m ren · o jump · ? help · q quit",
	paneGraph: "enter checkout/ff/detach/stash · d patch · ? help · q quit",
	paneTab:   "h/l switch · y copy · ? help · q quit",
}

// paneHintsRendered is the pre-styled form of paneHintTexts. View() runs on
// every Update so re-applying the help style per frame would burn a Lipgloss
// render and a 3-entry map allocation for nothing.
var paneHintsRendered = func() map[pane]string {
	m := make(map[pane]string, len(paneHintTexts))
	for p, t := range paneHintTexts {
		m[p] = help.Render(t)
	}
	return m
}()

func paneHints() map[pane]string { return paneHintTexts }

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
