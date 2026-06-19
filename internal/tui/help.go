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

// helpCategory groups entries under a pane label. entries are split into
// subcategory groups (e.g. movement vs actions) so the panel can render a
// blank row between them; categories with too few keys to split stay a single
// group. Mirrors docs/architecture.md's key bindings.
type helpCategory struct {
	title  string
	groups [][]helpEntry
}

// Help categories are split by the page they apply to. Only helpGlobal works
// on every page (page cycle / quit / help); the rest are page-scoped. The `?`
// panel shows Global + the current page's categories (helpCategoriesFor), so a
// reviewer sees only the keys that do something where they are. Within a
// category, entries are grouped into subcategories (movement keys apart from
// the mutating actions); see docs/architecture.md for the full key bindings.
var (
	// helpGlobal — the only truly cross-page keys (handled in every page's
	// key handler, or in updateKey itself for ctrl+c). One group.
	helpGlobal = helpCategory{
		title: "Global",
		groups: [][]helpEntry{{
			{"?", "help"},
			{",", "settings"},
			{"^C ^C", "quit"},
			{"tab/⇧tab", "next/prev page"},
		}},
	}
	// helpGraph — cursor-driven graph actions (graph page only): movement
	// keys grouped apart from the mutating actions.
	helpGraph = helpCategory{
		title: "Graph",
		groups: [][]helpEntry{
			{
				{"↑/↓", "nav"},
				{"g/G", "top/bot"},
				{"[/]", "prev/next page"},
			},
			{
				{"space", "checkout / ff / detach"},
				{"enter", "open PR (web)"},
				{"m", "merge PR"},
				{"C", "PR checks"},
				{"R", "rebase onto cursor"},
				{"c", "cherry-pick cursor"},
				{"v", "revert cursor"},
				{"x", "reset to cursor"},
				{"n", "new branch @ cursor"},
				{"→", "open diff"},
				{"y", "copy hash"},
			},
			{
				{"space", "stash: pop / apply"},
				{"d", "stash: drop"},
			},
		},
	}
	// helpSync — repo sync + the branch-list modal, reachable only from the
	// graph page (handleNormalKey): remote transfers apart from local
	// refresh / branch list. Split out of Graph so the column stays short.
	helpSync = helpCategory{
		title: "Sync",
		groups: [][]helpEntry{
			{
				{"F", "fetch"},
				{"p", "pull"},
				{"P", "push"},
			},
			{
				{"r", "reload"},
				{"b", "branches modal"},
			},
		},
	}
	// helpWorktree — worktree page cursor actions: list movement apart from
	// the worktree mutations.
	helpWorktree = helpCategory{
		title: "Worktree",
		groups: [][]helpEntry{
			{
				{"↑/↓", "nav"},
				{"s", "sort"},
			},
			{
				{"space", "switch"},
				{"enter", "open PR (web)"},
				{"a", "add"},
				{"d", "remove"},
			},
		},
	}
	// helpLCTree — local changes tree (file list) pane keys. `s` / `r` are
	// page-level (work from the diff pane too); listed here as the primary
	// surface. The page auto-reloads on a poll, so `r` is discard, not reload.
	helpLCTree = helpCategory{
		title: "Tree",
		groups: [][]helpEntry{
			{
				{"↑/↓", "nav"},
				{"g/G", "top/bot"},
			},
			{
				{"space", "stage/unstage"},
				{"→", "open diff"},
				{"enter", "open file"},
				{"c", "commit staged"},
				{"s", "stash all"},
				{"r", "discard all"},
			},
			{
				{"C", "continue (mid-conflict)"},
				{"^X", "abort (mid-conflict)"},
			},
		},
	}
	// helpLCDiff — local changes diff pane keys.
	helpLCDiff = helpCategory{
		title: "Diff",
		groups: [][]helpEntry{
			{
				{"↑/↓", "scroll"},
				{"[/]", "prev/next hunk"},
			},
			{
				{"space", "stage/unstage hunk"},
				{"←", "back to tree"},
			},
		},
	}
	// helpDiff — graph commit / PR patch keys (the diff page, opened with →):
	// movement keys beside a single close action, kept as one group.
	helpDiff = helpCategory{
		title: "Diff",
		groups: [][]helpEntry{{
			{"↑/↓", "scroll"},
			{"[/]", "prev/next hunk"},
			{"{/}", "prev/next file"},
			{"←", "close"},
		}},
	}
	// helpPRs — Pull Requests page cursor actions (the 4th tab). One nav key
	// beside the actions, kept as one group.
	helpPRs = helpCategory{
		title: "Pull Requests",
		groups: [][]helpEntry{{
			{"↑/↓", "nav"},
			{"enter", "open PR (web)"},
			{"m", "merge PR"},
			{"C", "PR checks"},
			{"r", "refresh"},
		}},
	}
)

// helpCategoriesFor returns the panel categories for a page index (0 graph, 1
// worktree, 2 local changes, 3 pull requests) — always led by Global.
func helpCategoriesFor(page int) []helpCategory {
	switch page {
	case 1:
		return []helpCategory{helpGlobal, helpWorktree}
	case 2:
		return []helpCategory{helpGlobal, helpLCTree, helpLCDiff}
	case 3:
		return []helpCategory{helpGlobal, helpPRs}
	default:
		return []helpCategory{helpGlobal, helpGraph, helpSync}
	}
}

// helpData returns every category, for the coverage test that guards against
// silently dropping a binding.
func helpData() []helpCategory {
	return []helpCategory{helpGlobal, helpGraph, helpSync, helpWorktree, helpLCTree, helpLCDiff, helpDiff, helpPRs}
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
// modal is open. The picker swallows everything but ↑/↓/enter/esc, so
// the hint enumerates exactly what works.
const helpTextBranchPicker = "↑/↓ navigate · enter checkout · esc cancel"

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
		var parts []string
		for _, g := range c.groups {
			for _, e := range g {
				parts = append(parts, e.keys+" "+e.action)
			}
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
// a title row over each subcategory group's rows, with a blank row between
// groups. lipgloss pads each column to its own widest line and to the tallest
// column, so the gutter stays aligned regardless of how many entries a
// category has.
func renderHelpColumns(cats []helpCategory) string {
	blocks := make([]string, 0, len(cats)*2-1)
	gutter := strings.Repeat(" ", helpColumnGutter)
	for i, c := range cats {
		if i > 0 {
			blocks = append(blocks, gutter)
		}
		blocks = append(blocks, renderHelpCategory(c))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// renderHelpCategory renders one category as a vertical block: a styled title
// over each group's `keys  action` rows, blank-line separated. Key chords are
// padded to the category's widest chord so the action column lines up across
// every group, and keys / actions get distinct gray tones so the columns read
// without shouting.
func renderHelpCategory(c helpCategory) string {
	keyW := 0
	for _, g := range c.groups {
		for _, e := range g {
			if w := runewidth.StringWidth(e.keys); w > keyW {
				keyW = w
			}
		}
	}
	var lines []string
	lines = append(lines, helpTitleS.Render(c.title))
	for gi, g := range c.groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		for _, e := range g {
			pad := strings.Repeat(" ", keyW-runewidth.StringWidth(e.keys))
			lines = append(lines, helpKeyS.Render(e.keys+pad)+"  "+help.Render(e.action))
		}
	}
	return strings.Join(lines, "\n")
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
