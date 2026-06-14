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

// Help categories are split by the page they apply to. Only helpGlobal works
// on every page (page cycle / quit / help); the rest are page-scoped. The `?`
// panel shows Global + the current page's categories (helpCategoriesFor), so a
// reviewer sees only the keys that do something where they are. Keep row order
// in sync with the docs/architecture.md table.
var (
	// helpGlobal — the only truly cross-page keys (handled in every page's
	// key handler, or in updateKey itself for ctrl+c).
	helpGlobal = helpCategory{
		title: "Global",
		entries: []helpEntry{
			{"?", "help"},
			{"^C ^C", "quit"},
			{"tab/⇧tab", "next/prev page"},
		},
	}
	// helpGraph — cursor-driven graph actions (graph page only).
	helpGraph = helpCategory{
		title: "Graph",
		entries: []helpEntry{
			{"j/k", "nav"},
			{"g/G", "top/bot"},
			{"enter", "checkout / ff / detach"},
			{"R", "rebase onto cursor"},
			{"c", "cherry-pick cursor"},
			{"v", "revert cursor"},
			{"x", "reset to cursor"},
			{"n", "new branch @ cursor"},
			{"o", "open PR on GitHub"},
			{"d", "patch overlay"},
			{"y", "copy hash"},
		},
	}
	// helpSync — repo sync + list/cleanup modals. Reachable only from the
	// graph page (handleNormalKey), split out of Graph so the column stays
	// short rather than one tall list.
	helpSync = helpCategory{
		title: "Sync",
		entries: []helpEntry{
			{"F", "fetch"},
			{"p", "pull"},
			{"P", "push"},
			{"r", "reload"},
			{"b", "branches modal"},
			{"l", "PR list modal"},
			{"Z", "zombie cleanup"},
		},
	}
	// helpWorktree — worktree page cursor actions (mirrors helpTextWorktreesModal).
	helpWorktree = helpCategory{
		title: "Worktree",
		entries: []helpEntry{
			{"j/k", "nav"},
			{"enter", "switch"},
			{"O", "review PR"},
			{"a", "add"},
			{"d", "remove"},
			{"s", "sort"},
		},
	}
	// helpLocalChanges — local changes page tree + diff keys.
	helpLocalChanges = helpCategory{
		title: "Local Changes",
		entries: []helpEntry{
			{"j/k", "nav"},
			{"g/G", "top/bot"},
			{"enter", "open diff"},
			{"space", "stage/unstage (file / hunk)"},
			{"[/]", "prev/next hunk (diff)"},
			{"esc", "diff → tree"},
			{"r", "reload"},
		},
	}
)

// helpCategoriesFor returns the panel categories for a page index (0 graph, 1
// worktree, 2 local changes) — always led by Global.
func helpCategoriesFor(page int) []helpCategory {
	switch page {
	case 1:
		return []helpCategory{helpGlobal, helpWorktree}
	case 2:
		return []helpCategory{helpGlobal, helpLocalChanges}
	default:
		return []helpCategory{helpGlobal, helpGraph, helpSync}
	}
}

// helpData returns every category, for the coverage test that guards against
// silently dropping a binding.
func helpData() []helpCategory {
	return []helpCategory{helpGlobal, helpGraph, helpSync, helpWorktree, helpLocalChanges}
}

// collapsedHintText is the entire bottom line in normal operation: a single
// pressable `? help` token. The full key reference lives behind the `?`
// overlay modal (renderHelpModalInner), so the footer no longer carries a
// focus-aware key matrix — it stays minimal and the modal owns discovery.
const collapsedHintText = "? help"

// collapsedHintRendered is the pre-styled form. View() runs on every Update
// so re-applying the help style per frame would burn a Lipgloss render for
// nothing.
var collapsedHintRendered = help.Render(collapsedHintText)

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

// renderHelpPanel composes the stacked-rows help layout as a multi-line
// string capped at `height` rows. Each category emits a `[Title]` header
// row plus a single entries row joined inline with `·`. Rows past the cap
// are dropped. This is the narrow-terminal fallback for renderHelpExpanded
// when the side-by-side columns are wider than the terminal.
func renderHelpPanel(cats []helpCategory, width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	var lines []string
	for _, c := range cats {
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

// helpColumnGutter is the blank-space width between adjacent columns in the
// expanded panel's side-by-side layout.
const helpColumnGutter = 2

// renderHelpColumns lays the categories out as side-by-side vertical lists:
// a bold title row over one `keys action` row per entry. lipgloss pads each
// column to its own widest line and to the tallest column, so the gutter
// stays aligned regardless of how many entries a category has.
func renderHelpColumns(cats []helpCategory) string {
	blocks := make([]string, 0, len(cats)*2-1)
	gutter := strings.Repeat(" ", helpColumnGutter)
	for i, c := range cats {
		if i > 0 {
			blocks = append(blocks, gutter)
		}
		lines := make([]string, 0, len(c.entries)+1)
		lines = append(lines, modalHeaderS.Render(c.title))
		for _, e := range c.entries {
			lines = append(lines, e.keys+" "+e.action)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// renderHelpExpanded renders the `?` reference for the inline bottom panel
// that grows out of the footer (it is not a modal — the base view stays put
// and shortcuts keep working). cats is the current page's category set. Wide
// terminals get the side-by-side column layout, clamped to `height` rows; when
// the columns are wider than the terminal it falls back to the stacked
// renderHelpPanel form. The page above shrinks by `height` rows (see paneSizes
// / helpReservedRows).
func renderHelpExpanded(cats []helpCategory, width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	if columns := renderHelpColumns(cats); lipgloss.Width(columns) <= width {
		lines := strings.Split(columns, "\n")
		if len(lines) > height {
			lines = lines[:height]
		}
		return strings.Join(lines, "\n")
	}
	// Too narrow for columns: reuse the stacked-rows layout.
	return renderHelpPanel(cats, width, height)
}
