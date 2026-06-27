// In-process unified-diff styling. gh-orbit used to let `git -c color.ui=always`
// paint the patch and just piped that ANSI into the viewport. To layer syntax
// highlighting and word-level (intra-line) emphasis on top, the cockpit now
// fetches the diff *uncolored* and renders it here: chroma colors each code
// line by language, a subtle red/green background marks removed/added lines,
// and the changed spans of a paired -/+ line get a brighter background so the
// reviewer's eye lands on what actually changed.
//
// Every input line maps to exactly one output line, so the caller's hunk- and
// file-boundary line indices (parseHunkStarts / parseFileBoundaries) stay valid
// against the rendered text. The result is width-independent — the viewport
// truncates long lines itself (MaxWidth), so the background sits behind the
// text only and never needs padding.
//
// Styling is threaded per segment (one lipgloss style carrying both the chroma
// foreground and the diff background), never by wrapping an already-styled
// string — a `.Render` over text that already carries ANSI breaks on the inner
// reset (see lcSelectedStyle's note).
package tui

import (
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// diffTheme bundles every color the diff renderer needs. syntaxStyle is the
// chroma style the code foreground comes from. base* tints a whole changed
// line; emph* (louder) marks the word-level span. *Marker is the leading +/-
// glyph. metaFg/hunkFg color the `diff --git`/`index`/`---`/`+++` headers and
// the `@@` hunk headers. emphFg overrides the syntax foreground on a word-level
// span so the changed text stays legible over the louder emph background even
// when its token color is dim (a gray comment is the case that breaks
// otherwise). Hex colors are truecolor — lipgloss degrades to the nearest 256
// color on lesser terminals; bare numbers are 256-color indices.
//
// key is the stable [diff] theme config value; name + dark drive the Settings
// picker row.
type diffTheme struct {
	key  string
	name string
	dark bool

	syntaxStyle string
	addBaseBg   string
	delBaseBg   string
	addEmphBg   string
	delEmphBg   string
	addMarker   string
	delMarker   string
	metaFg      string
	hunkFg      string
	emphFg      string

	// App-wide chrome colors. applyTheme rebuilds the shared lipgloss style
	// vars from these so a theme switch recolors the commit graph (lanes +
	// meta columns), chips/PR badges, the status line, modal borders, and the
	// page tabs alongside the diff. github-dark's fields point at the historical
	// color consts, so it renders exactly as the cockpit did before theming.
	laneColors     []string // commit-graph lane rotation, far-apart hues
	timeFg         string   // graph time column
	authorFg       string   // graph author column
	dimFg          string   // dimmed rows, help/hint text, unfocused border, inactive tab
	selectedFg     string   // cursor/selection accent, focused border, active tab, update hint
	chipLocalBg    string
	chipRemoteBg   string
	chipTagBg      string
	chipStashBg    string
	chipFg         string
	badgeBg        string
	badgePassFg    string
	badgeFailFg    string
	badgePendingFg string
	badgeNoneFg    string
	statusBusyFg   string
	statusOkFg     string
	statusErrFg    string
}

// diffThemes is the Settings picker order: every dark theme first, then every
// light one (the dark/light boundary sits between catppuccin-mocha and
// catppuccin-latte). Index 0 is the default for an unset / unknown theme key —
// Orbit Dark, the project's signature slate-purple palette (#7c6f9f accent).
// GitHub Dark (the pre-theming default, an exact match of the old chrome) is one
// slot over; the rest are muted, low-neon palettes (Nord, Gruvbox, One Dark,
// Tokyo Night, Rosé Pine) tuned so the graph/chips don't glow.
var diffThemes = []diffTheme{
	{
		key: "orbit-dark", name: "Orbit Dark", dark: true,
		syntaxStyle: "tokyonight-moon",
		addBaseBg:   "#16271c", delBaseBg: "#2b181c",
		addEmphBg: "#2c5e38", delEmphBg: "#6e2f33",
		addMarker: "#5cba74", delMarker: "#df6b71",
		metaFg: "#6a6480", hunkFg: "#9a8fc4", emphFg: "#e6e2f0",
		// The default theme (index 0). Signature accent #7c6f9f (the README badge
		// purple), kept out of laneColors so it reads as "cursor/selection".
		laneColors: []string{"#a98fc4", "#7fb0c4", "#c79a7a", "#8fbf9f", "#6f8fc4", "#cbb079", "#c98fb0", "#79b0a8"},
		timeFg:     "#6a6480", authorFg: "#8b86a0", dimFg: "#4a4660", selectedFg: "#7c6f9f",
		chipLocalBg: "#8b9fd1", chipRemoteBg: "#b79fd1", chipTagBg: "#cbb079",
		chipStashBg: "#88c0b0", chipFg: "#1b1a26",
		badgeBg: "#2a2738", badgePassFg: "#8fbf9f", badgeFailFg: "#c97b8e",
		badgePendingFg: "#cbb079", badgeNoneFg: "#8b86a0",
		statusBusyFg: "#cbb079", statusOkFg: "#8b86a0", statusErrFg: "#c97b8e",
	},
	{
		key: "github-dark", name: "GitHub Dark", dark: true,
		syntaxStyle: "github-dark",
		addBaseBg:   "#12261e", delBaseBg: "#25171c",
		addEmphBg: "#1f6f33", delEmphBg: "#7c2b2e",
		addMarker: "#3fb950", delMarker: "#f85149",
		metaFg: "#6e7681", hunkFg: "#58a6ff", emphFg: "#f0f6fc",
		// Chrome mirrors the historical consts, so GitHub Dark renders exactly as
		// the cockpit did before theming (it was the pre-Orbit default).
		laneColors: []string{"212", "117", "215", "120", "183", "228", "75", "168"},
		timeFg:     colorTime, authorFg: colorAuthor, dimFg: colorDim, selectedFg: colorSelected,
		chipLocalBg: colorChipLocal, chipRemoteBg: colorChipRemote, chipTagBg: colorChipTag,
		chipStashBg: colorChipStash, chipFg: colorChipFG,
		badgeBg: colorBadgeBG, badgePassFg: colorBadgePass, badgeFailFg: colorBadgeFail,
		badgePendingFg: colorBadgePending, badgeNoneFg: colorBadgeNone,
		statusBusyFg: "214", statusOkFg: "245", statusErrFg: "203",
	},
	{
		key: "nord", name: "Nord", dark: true,
		syntaxStyle: "nord",
		addBaseBg:   "#2c3a2c", delBaseBg: "#422a2e",
		addEmphBg: "#3d5a44", delEmphBg: "#5e3a3e",
		addMarker: "#a3be8c", delMarker: "#bf616a",
		metaFg: "#4c566a", hunkFg: "#88c0d0", emphFg: "#eceff4",
		laneColors: []string{"#b48ead", "#88c0d0", "#d08770", "#a3be8c", "#81a1c1", "#ebcb8b", "#8fbcbb", "#bf616a"},
		timeFg:     "#677089", authorFg: "#7b88a1", dimFg: "#4c566a", selectedFg: "#88c0d0",
		chipLocalBg: "#81a1c1", chipRemoteBg: "#b48ead", chipTagBg: "#ebcb8b",
		chipStashBg: "#8fbcbb", chipFg: "#2e3440",
		badgeBg: "#434c5e", badgePassFg: "#a3be8c", badgeFailFg: "#bf616a",
		badgePendingFg: "#ebcb8b", badgeNoneFg: "#9aa3b3",
		statusBusyFg: "#ebcb8b", statusOkFg: "#7b88a1", statusErrFg: "#bf616a",
	},
	{
		key: "gruvbox-dark", name: "Gruvbox Dark", dark: true,
		syntaxStyle: "gruvbox",
		addBaseBg:   "#313c1a", delBaseBg: "#402822",
		addEmphBg: "#4f5a23", delEmphBg: "#5a302a",
		addMarker: "#b8bb26", delMarker: "#fb4934",
		metaFg: "#928374", hunkFg: "#83a598", emphFg: "#fbf1c7",
		laneColors: []string{"#d3869b", "#8ec07c", "#fe8019", "#b8bb26", "#83a598", "#fabd2f", "#fb4934", "#b16286"},
		timeFg:     "#928374", authorFg: "#a89984", dimFg: "#665c54", selectedFg: "#fabd2f",
		chipLocalBg: "#83a598", chipRemoteBg: "#d3869b", chipTagBg: "#fabd2f",
		chipStashBg: "#8ec07c", chipFg: "#282828",
		badgeBg: "#3c3836", badgePassFg: "#b8bb26", badgeFailFg: "#fb4934",
		badgePendingFg: "#fabd2f", badgeNoneFg: "#a89984",
		statusBusyFg: "#fe8019", statusOkFg: "#a89984", statusErrFg: "#fb4934",
	},
	{
		key: "one-dark", name: "One Dark", dark: true,
		syntaxStyle: "onedark",
		addBaseBg:   "#263322", delBaseBg: "#382428",
		addEmphBg: "#3d5234", delEmphBg: "#5a3438",
		addMarker: "#98c379", delMarker: "#e06c75",
		metaFg: "#5c6370", hunkFg: "#61afef", emphFg: "#d7dae0",
		laneColors: []string{"#c678dd", "#56b6c2", "#d19a66", "#98c379", "#61afef", "#e5c07b", "#e06c75", "#a36ac7"},
		timeFg:     "#828997", authorFg: "#9098a5", dimFg: "#5c6370", selectedFg: "#61afef",
		chipLocalBg: "#61afef", chipRemoteBg: "#c678dd", chipTagBg: "#e5c07b",
		chipStashBg: "#56b6c2", chipFg: "#282c34",
		badgeBg: "#3a3f4b", badgePassFg: "#98c379", badgeFailFg: "#e06c75",
		badgePendingFg: "#e5c07b", badgeNoneFg: "#828997",
		statusBusyFg: "#d19a66", statusOkFg: "#828997", statusErrFg: "#e06c75",
	},
	{
		key: "tokyo-night", name: "Tokyo Night", dark: true,
		syntaxStyle: "tokyonight-night",
		addBaseBg:   "#1c2b22", delBaseBg: "#311d24",
		addEmphBg: "#2f4a3a", delEmphBg: "#5a2e38",
		addMarker: "#9ece6a", delMarker: "#f7768e",
		metaFg: "#565f89", hunkFg: "#7aa2f7", emphFg: "#c0caf5",
		laneColors: []string{"#bb9af7", "#7dcfff", "#ff9e64", "#9ece6a", "#7aa2f7", "#e0af68", "#f7768e", "#73daca"},
		timeFg:     "#6b73a0", authorFg: "#9aa5ce", dimFg: "#565f89", selectedFg: "#7aa2f7",
		chipLocalBg: "#7aa2f7", chipRemoteBg: "#bb9af7", chipTagBg: "#e0af68",
		chipStashBg: "#73daca", chipFg: "#1a1b26",
		badgeBg: "#292e42", badgePassFg: "#9ece6a", badgeFailFg: "#f7768e",
		badgePendingFg: "#e0af68", badgeNoneFg: "#7982b0",
		statusBusyFg: "#ff9e64", statusOkFg: "#6b73a0", statusErrFg: "#f7768e",
	},
	{
		key: "rose-pine", name: "Rosé Pine", dark: true,
		syntaxStyle: "rose-pine",
		addBaseBg:   "#16292b", delBaseBg: "#341d27",
		addEmphBg: "#2c4a4d", delEmphBg: "#5e2d3c",
		addMarker: "#9ccfd8", delMarker: "#eb6f92",
		metaFg: "#6e6a86", hunkFg: "#c4a7e7", emphFg: "#e0def4",
		laneColors: []string{"#c4a7e7", "#9ccfd8", "#f6c177", "#ebbcba", "#31748f", "#eb6f92", "#569fb5", "#d7827e"},
		timeFg:     "#807c9c", authorFg: "#908caa", dimFg: "#6e6a86", selectedFg: "#c4a7e7",
		chipLocalBg: "#9ccfd8", chipRemoteBg: "#c4a7e7", chipTagBg: "#f6c177",
		chipStashBg: "#ebbcba", chipFg: "#191724",
		badgeBg: "#26233a", badgePassFg: "#9ccfd8", badgeFailFg: "#eb6f92",
		badgePendingFg: "#f6c177", badgeNoneFg: "#908caa",
		statusBusyFg: "#f6c177", statusOkFg: "#908caa", statusErrFg: "#eb6f92",
	},
	{
		key: "catppuccin-mocha", name: "Catppuccin Mocha", dark: true,
		syntaxStyle: "catppuccin-mocha",
		addBaseBg:   "#243c1d", delBaseBg: "#4a1e1b",
		addEmphBg: "#3c6a32", delEmphBg: "#83372f",
		addMarker: "#a6e3a1", delMarker: "#f38ba8",
		metaFg: "240", hunkFg: "75", emphFg: "#f2f3f8",
		laneColors: []string{"#f5c2e7", "#89dceb", "#fab387", "#a6e3a1", "#b4befe", "#f9e2af", "#89b4fa", "#eba0ac"},
		timeFg:     "#9399b2", authorFg: "#a6adc8", dimFg: "#6c7086", selectedFg: "#cba6f7",
		chipLocalBg: "#89b4fa", chipRemoteBg: "#f5c2e7", chipTagBg: "#f9e2af",
		chipStashBg: "#cba6f7", chipFg: "#11111b",
		badgeBg: "#313244", badgePassFg: "#a6e3a1", badgeFailFg: "#f38ba8",
		badgePendingFg: "#fab387", badgeNoneFg: "#7f849c",
		statusBusyFg: "#fab387", statusOkFg: "#a6adc8", statusErrFg: "#f38ba8",
	},
	{
		key: "catppuccin-latte", name: "Catppuccin Latte", dark: false,
		syntaxStyle: "catppuccin-latte",
		addBaseBg:   "#e3f0e1", delBaseBg: "#fbe4e6",
		addEmphBg: "#c5e6bf", delEmphBg: "#f4c4ca",
		addMarker: "#40a02b", delMarker: "#d20f39",
		metaFg: "#8c8fa1", hunkFg: "#1e66f5", emphFg: "#4c4f69",
		laneColors: []string{"#ea76cb", "#04a5e5", "#fe640b", "#40a02b", "#7287fd", "#df8e1d", "#1e66f5", "#e64553"},
		// Light theme: pastel chip bgs carry dark text (chipFg) so even the
		// dim/overflow chip (dimFg bg) stays legible.
		timeFg: "#8c8fa1", authorFg: "#6c6f85", dimFg: "#9ca0b0", selectedFg: "#8839ef",
		chipLocalBg: "#b7c5f7", chipRemoteBg: "#f4c6ea", chipTagBg: "#f1dcab",
		chipStashBg: "#d9c5f6", chipFg: "#4c4f69",
		badgeBg: "#ccd0da", badgePassFg: "#40a02b", badgeFailFg: "#d20f39",
		badgePendingFg: "#df8e1d", badgeNoneFg: "#6c6f85",
		statusBusyFg: "#fe640b", statusOkFg: "#6c6f85", statusErrFg: "#d20f39",
	},
	{
		key: "github-light", name: "GitHub Light", dark: false,
		syntaxStyle: "github",
		addBaseBg:   "#e6ffec", delBaseBg: "#ffebe9",
		addEmphBg: "#abf2bc", delEmphBg: "#ffc1c0",
		addMarker: "#1a7f37", delMarker: "#cf222e",
		metaFg: "#6e7781", hunkFg: "#0550ae", emphFg: "#1f2328",
		laneColors: []string{"#bf3989", "#1b7c83", "#bc4c00", "#1a7f37", "#8250df", "#9a6700", "#0969da", "#cf222e"},
		timeFg:     "#6e7781", authorFg: "#57606a", dimFg: "#8c959f", selectedFg: "#0969da",
		chipLocalBg: "#cae8ff", chipRemoteBg: "#ffcadf", chipTagBg: "#fbe7a8",
		chipStashBg: "#e7d3ff", chipFg: "#24292f",
		badgeBg: "#eaeef2", badgePassFg: "#1a7f37", badgeFailFg: "#cf222e",
		badgePendingFg: "#9a6700", badgeNoneFg: "#6e7781",
		statusBusyFg: "#9a6700", statusOkFg: "#6e7781", statusErrFg: "#cf222e",
	},
	{
		key: "orbit-light", name: "Orbit Light", dark: false,
		syntaxStyle: "tokyonight-day",
		addBaseBg:   "#e2ecdf", delBaseBg: "#f5e0e6",
		addEmphBg: "#c8e0c2", delEmphBg: "#f0c8d2",
		addMarker: "#4f8f5f", delMarker: "#b05068",
		metaFg: "#8b86a0", hunkFg: "#6a5d8f", emphFg: "#2e2a3a",
		laneColors: []string{"#6f5d8f", "#3f7f97", "#b06a3a", "#4f8f5f", "#4f63a0", "#a07a2a", "#b05a7f", "#3f8f87"},
		timeFg:     "#8b86a0", authorFg: "#6e6880", dimFg: "#a59fb5", selectedFg: "#7c6f9f",
		chipLocalBg: "#cfd6f0", chipRemoteBg: "#e2d6f0", chipTagBg: "#f0e3c0",
		chipStashBg: "#cfe6e0", chipFg: "#2e2a3a",
		badgeBg: "#e8e4f0", badgePassFg: "#4f8f5f", badgeFailFg: "#b05068",
		badgePendingFg: "#a07a2a", badgeNoneFg: "#8b86a0",
		statusBusyFg: "#b06a3a", statusOkFg: "#8b86a0", statusErrFg: "#b05068",
	},
	{
		key: "gruvbox-light", name: "Gruvbox Light", dark: false,
		syntaxStyle: "gruvbox-light",
		addBaseBg:   "#e0ecc4", delBaseBg: "#f6dcd0",
		addEmphBg: "#c5dd9e", delEmphBg: "#f1c4b8",
		addMarker: "#79740e", delMarker: "#9d0006",
		metaFg: "#7c6f64", hunkFg: "#076678", emphFg: "#282828",
		laneColors: []string{"#8f3f71", "#427b58", "#af3a03", "#79740e", "#076678", "#b57614", "#9d0006", "#b16286"},
		timeFg:     "#7c6f64", authorFg: "#665c54", dimFg: "#a89984", selectedFg: "#af3a03",
		chipLocalBg: "#cfe0e6", chipRemoteBg: "#ecd4e0", chipTagBg: "#f3e3b0",
		chipStashBg: "#d3e6cf", chipFg: "#282828",
		badgeBg: "#ebdbb2", badgePassFg: "#79740e", badgeFailFg: "#9d0006",
		badgePendingFg: "#b57614", badgeNoneFg: "#7c6f64",
		statusBusyFg: "#af3a03", statusOkFg: "#7c6f64", statusErrFg: "#9d0006",
	},
	{
		key: "rose-pine-dawn", name: "Rosé Pine Dawn", dark: false,
		syntaxStyle: "rose-pine-dawn",
		addBaseBg:   "#d3ece6", delBaseBg: "#f8dade",
		addEmphBg: "#bce0d8", delEmphBg: "#f3c6cd",
		addMarker: "#56949f", delMarker: "#b4637a",
		metaFg: "#9893a5", hunkFg: "#907aa9", emphFg: "#575279",
		laneColors: []string{"#907aa9", "#56949f", "#ea9d34", "#d7827e", "#286983", "#b4637a", "#6f5f8c", "#c2723f"},
		timeFg:     "#797593", authorFg: "#6e6a86", dimFg: "#9893a5", selectedFg: "#907aa9",
		chipLocalBg: "#cfe3e6", chipRemoteBg: "#e6dcee", chipTagBg: "#f4e3c0",
		chipStashBg: "#f0d9d6", chipFg: "#575279",
		badgeBg: "#f2e9e1", badgePassFg: "#56949f", badgeFailFg: "#b4637a",
		badgePendingFg: "#ea9d34", badgeNoneFg: "#9893a5",
		statusBusyFg: "#ea9d34", statusOkFg: "#797593", statusErrFg: "#b4637a",
	},
}

// activeDiffTheme is the theme renderDiffContent paints with — a single
// app-wide setting (one theme at a time), set once in Model.New from prefs and
// re-pointed by applyTheme when the Settings picker cycles it. A package global
// to mirror the existing chrome globals (Version, the lipgloss style vars) and
// keep renderDiffContent's signature stable; it defaults to diffThemes[0]
// (Orbit Dark) so any render before New (tests) has a valid theme.
var activeDiffTheme = diffThemes[0]

// diffThemeIndex returns the diffThemes index for a config key, or 0 (the
// default dark theme) for "" / an unrecognized key.
func diffThemeIndex(key string) int {
	for i, t := range diffThemes {
		if t.key == key {
			return i
		}
	}
	return 0
}

// applyTheme makes th the app-wide theme. It re-points activeDiffTheme (which
// renderDiffContent reads) and rebuilds every shared chrome style var from th's
// fields: the commit-graph lane palette + meta columns, chips and PR badges, the
// status line, modal borders, and the page tabs. The graph and chips read these
// vars on each View, so a Settings switch repaints them on the next frame; the
// diff viewport caches its render, so cycleDiffTheme still calls RerenderTheme.
// github-dark's fields equal the historical const values, so applyTheme leaves
// the default theme pixel-identical to before.
func applyTheme(th diffTheme) {
	activeDiffTheme = th

	fg := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	}
	border := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(c))
	}

	// Commit-graph lanes.
	ls := make([]lipgloss.Style, len(th.laneColors))
	for i, c := range th.laneColors {
		ls[i] = fg(c)
	}
	laneStyles = ls

	// Graph meta + shared accent/dim (also feeds branches, worktrees, PR list).
	dimFGStyle = fg(th.dimFg)
	timeStyle = fg(th.timeFg)
	authorStyle = fg(th.authorFg)
	cursorStyle = fg(th.selectedFg)
	selectedStyle = fg(th.selectedFg).Bold(true)

	// Ref chips + in-chip PR badges.
	chipLocalStyle = newChipStyle(th.chipLocalBg, th.chipFg)
	chipRemoteStyle = newChipStyle(th.chipRemoteBg, th.chipFg)
	chipTagStyle = newChipStyle(th.chipTagBg, th.chipFg)
	chipStashStyle = newChipStyle(th.chipStashBg, th.chipFg)
	chipMoreStyle = newChipStyle(th.dimFg, th.chipFg)
	chipSelectedStyle = newChipStyle(th.selectedFg, th.chipFg)
	chipDimStyle = newChipStyle(th.dimFg, th.chipFg)
	badgePassStyle = newChipStyle(th.badgeBg, th.badgePassFg)
	badgeFailStyle = newChipStyle(th.badgeBg, th.badgeFailFg)
	badgePendingStyle = newChipStyle(th.badgeBg, th.badgePendingFg)
	badgeNoneStyle = newChipStyle(th.badgeBg, th.badgeNoneFg)

	// Status line + modal chrome + page tabs.
	statusBusyS = fg(th.statusBusyFg)
	statusOkS = fg(th.statusOkFg)
	statusErrS = fg(th.statusErrFg)
	confirmPromptS = statusBusyS.Bold(true)
	help = fg(th.dimFg)
	updateHintS = fg(th.selectedFg)
	pageTabActiveS = fg(th.selectedFg).Bold(true)
	pageTabInactiveS = fg(th.dimFg)
	borderUnfocused = border(th.dimFg)
	borderFocused = border(th.selectedFg)
	modalBoxStyle = borderFocused.Padding(0, 1)
}

// Render guards: above these the cost of per-line tokenisation + LCS isn't
// worth it, so the line (or whole patch) falls back to plain +/- backgrounds
// with no syntax/word-level work.
const (
	maxDiffRenderLines = 4000
	maxDiffLineRunes   = 2000
	diffTabWidth       = 4
)

type diffLineKind int

const (
	dlMeta    diffLineKind = iota // diff --git, index, ---/+++, \ No newline, Binary, blank
	dlHunk                        // @@ -a,b +c,d @@
	dlContext                     // leading space
	dlDel                         // leading -
	dlAdd                         // leading +
)

// renderDiffContent styles a plain (uncolored) unified diff for the viewport.
// width is the viewport content width: removed/added lines pad their background
// out to it so each reads as a continuous full-width bar (width ≤ 0 skips the
// padding — background sits behind the text only). Empty in → empty out.
// Cached per load + width; the caller re-applies only the selected-hunk accent
// on refresh, and re-renders when the width changes.
func renderDiffContent(patch string, width int) string {
	if patch == "" {
		return ""
	}
	lines := strings.Split(patch, "\n")
	syntax := len(lines) <= maxDiffRenderLines

	th := activeDiffTheme
	r := newDiffRenderer(th)

	kinds := make([]diffLineKind, len(lines))
	codes := make([]string, len(lines)) // tab-expanded code (content lines only)
	lex := make([]chroma.Lexer, len(lines))

	var cur chroma.Lexer
	for i, ln := range lines {
		k := classifyDiffLine(ln)
		kinds[i] = k
		switch k {
		case dlMeta:
			if path, ok := parseFileDiffHeader(ln); ok {
				cur = r.lexerFor(path)
			}
		case dlContext, dlAdd, dlDel:
			codes[i] = expandTabs(ln[1:], diffTabWidth)
			lex[i] = cur
		}
	}

	changed := wordLevelRanges(kinds, codes, syntax)

	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch kinds[i] {
		case dlHunk:
			b.WriteString(styledSeg(ln, th.hunkFg, ""))
		case dlMeta:
			b.WriteString(styledSeg(ln, th.metaFg, ""))
		case dlContext:
			b.WriteString(r.contentLine(' ', codes[i], lex[i], "", "", "", nil, syntax, width))
		case dlAdd:
			b.WriteString(r.contentLine('+', codes[i], lex[i], th.addBaseBg, th.addEmphBg, th.addMarker, changed[i], syntax, width))
		case dlDel:
			b.WriteString(r.contentLine('-', codes[i], lex[i], th.delBaseBg, th.delEmphBg, th.delMarker, changed[i], syntax, width))
		}
	}
	return b.String()
}

type diffRenderer struct {
	theme    diffTheme
	style    *chroma.Style
	lexCache map[string]chroma.Lexer
}

func newDiffRenderer(th diffTheme) *diffRenderer {
	return &diffRenderer{
		theme:    th,
		style:    styles.Get(th.syntaxStyle), // falls back to a registered style if unknown
		lexCache: map[string]chroma.Lexer{},
	}
}

// lexerFor resolves (and caches) a coalesced lexer for a destination path,
// falling back to the plain-text lexer for unknown extensions.
func (r *diffRenderer) lexerFor(path string) chroma.Lexer {
	if l, ok := r.lexCache[path]; ok {
		return l
	}
	l := lexers.Match(path)
	if l == nil {
		l = lexers.Fallback
	}
	l = chroma.Coalesce(l)
	r.lexCache[path] = l
	return l
}

// fgFor returns the chroma foreground hex for a token type, or "" when the
// style leaves it at the default.
func (r *diffRenderer) fgFor(tt chroma.TokenType) string {
	if e := r.style.Get(tt); e.Colour.IsSet() {
		return e.Colour.String()
	}
	return ""
}

// contentLine renders one context/add/del line: a marker cell, then the code
// split into segments that each carry a chroma foreground and the line's
// background (the louder emph background over word-level changed runes). When
// syntax is off (huge diff / over-long line) the code renders as one plain
// segment with just the backgrounds.
func (r *diffRenderer) contentLine(marker rune, code string, lexer chroma.Lexer, baseBg, emphBg, markerFg string, changed [][2]int, syntax bool, width int) string {
	var b strings.Builder
	b.WriteString(styledSeg(string(marker), markerFg, baseBg))

	runes := []rune(code)
	if !syntax || lexer == nil || len(runes) > maxDiffLineRunes {
		r.writeSpans(&b, runes, 0, "", baseBg, emphBg, changed)
	} else {
		pos := 0
		for _, t := range tokenize(lexer, code) {
			tr := []rune(t.Value)
			r.writeSpans(&b, tr, pos, r.fgFor(t.Type), baseBg, emphBg, changed)
			pos += len(tr)
		}
	}

	// Pad the background out to the viewport width so a changed line reads as a
	// continuous bar. Context lines (no background) are left unpadded.
	if baseBg != "" && width > 0 {
		if pad := width - 1 - runewidth.StringWidth(code); pad > 0 {
			b.WriteString(styledSeg(strings.Repeat(" ", pad), "", baseBg))
		}
	}
	return b.String()
}

// writeSpans emits runes[0:] (whose first rune sits at absolute offset base in
// the line) as the fewest segments possible, breaking only where the
// background flips between baseBg and emphBg per the changed rune ranges.
func (r *diffRenderer) writeSpans(b *strings.Builder, runes []rune, base int, fg, baseBg, emphBg string, changed [][2]int) {
	k := 0
	for k < len(runes) {
		bg := bgAt(base+k, changed, baseBg, emphBg)
		j := k + 1
		for j < len(runes) && bgAt(base+j, changed, baseBg, emphBg) == bg {
			j++
		}
		// On a word-level changed span, force the theme's emph foreground so the
		// text stays legible over the emph background regardless of its token color.
		segFg := fg
		if emphBg != "" && bg == emphBg {
			segFg = r.theme.emphFg
		}
		b.WriteString(styledSeg(string(runes[k:j]), segFg, bg))
		k = j
	}
}

// bgAt is the background for the rune at offset i: the louder emph background
// inside a word-level changed range, the line's base background otherwise.
func bgAt(i int, changed [][2]int, baseBg, emphBg string) string {
	for _, rg := range changed {
		if i >= rg[0] && i < rg[1] {
			return emphBg
		}
	}
	return baseBg
}

// styledSeg renders text under an optional foreground/background. Empty color
// strings leave that attribute unset, so context lines (no background) and
// default-colored tokens stay plain.
func styledSeg(text, fg, bg string) string {
	st := lipgloss.NewStyle()
	if fg != "" {
		st = st.Foreground(lipgloss.Color(fg))
	}
	if bg != "" {
		st = st.Background(lipgloss.Color(bg))
	}
	return st.Render(text)
}

// tokenize runs the lexer over one line, returning its tokens. On a lexer error
// the whole line comes back as a single default-colored token so rendering
// still succeeds.
func tokenize(lexer chroma.Lexer, code string) []chroma.Token {
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return []chroma.Token{{Type: chroma.Text, Value: code}}
	}
	return it.Tokens()
}

// wordLevelRanges finds, for each line index, the changed rune ranges to
// emphasise. It pairs each maximal run of removed lines with the immediately
// following run of added lines, line-by-line, and word-diffs each pair. Lines
// with no pairing (or too-dissimilar pairs) get no emphasis (nil). Returns nil
// for the whole patch when syntax work is disabled.
func wordLevelRanges(kinds []diffLineKind, codes []string, syntax bool) [][][2]int {
	out := make([][][2]int, len(kinds))
	if !syntax {
		return out
	}
	i := 0
	for i < len(kinds) {
		if kinds[i] != dlDel {
			i++
			continue
		}
		dStart := i
		for i < len(kinds) && kinds[i] == dlDel {
			i++
		}
		dEnd := i
		if i >= len(kinds) || kinds[i] != dlAdd {
			continue
		}
		aStart := i
		for i < len(kinds) && kinds[i] == dlAdd {
			i++
		}
		aEnd := i
		n := min(dEnd-dStart, aEnd-aStart)
		for j := 0; j < n; j++ {
			delIdx, addIdx := dStart+j, aStart+j
			dr, ar := wordLevelChanged([]rune(codes[delIdx]), []rune(codes[addIdx]))
			out[delIdx] = dr
			out[addIdx] = ar
		}
	}
	return out
}

// wordMatchFloor is the minimum share of non-whitespace runes a paired -/+ line
// must hold in common before per-word emphasis is drawn. Below it the lines are
// more rewritten than edited — most words would light up, which reads as noise
// — so the full-width base bar carries the "this line changed" signal alone and
// emphasis is reserved for small, targeted edits (a renamed identifier, a
// changed number). Whitespace is excluded so shared indentation doesn't inflate
// the measure.
const wordMatchFloor = 0.40

// wordLevelChanged word-diffs two code lines and returns the changed rune
// ranges in each. A token is a maximal run of word runes or a single other
// rune; the LCS of the token streams is the unchanged part. Too-dissimilar
// pairs (see wordMatchFloor) return nil,nil.
func wordLevelChanged(a, b []rune) (aRanges, bRanges [][2]int) {
	at, bt := wordTokens(a), wordTokens(b)
	n, m := len(at), len(bt)
	if n == 0 || m == 0 {
		return nil, nil
	}
	// LCS length table (suffix DP).
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if at[i].text == bt[j].text {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	aMatch, bMatch := make([]bool, n), make([]bool, m)
	matchedNonWS := 0
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case at[i].text == bt[j].text:
			aMatch[i], bMatch[j] = true, true
			if !isBlankTok(at[i]) {
				matchedNonWS += at[i].n
			}
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	denom := max(nonWSRunes(at), nonWSRunes(bt))
	if denom == 0 || float64(matchedNonWS)/float64(denom) < wordMatchFloor {
		return nil, nil
	}
	return unmatchedRanges(at, aMatch), unmatchedRanges(bt, bMatch)
}

func isBlankTok(t wtok) bool { return strings.TrimSpace(t.text) == "" }

// nonWSRunes counts the runes in non-whitespace tokens.
func nonWSRunes(toks []wtok) int {
	total := 0
	for _, t := range toks {
		if !isBlankTok(t) {
			total += t.n
		}
	}
	return total
}

type wtok struct {
	text  string
	start int // rune offset of the token in its line
	n     int // rune count
}

// wordTokens splits a line into word tokens (maximal [\p{L}\p{N}_] runs) and
// single-rune punctuation/space tokens, tracking each token's rune offset.
func wordTokens(runes []rune) []wtok {
	var out []wtok
	i := 0
	for i < len(runes) {
		if isWordRune(runes[i]) {
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			out = append(out, wtok{text: string(runes[i:j]), start: i, n: j - i})
			i = j
		} else {
			out = append(out, wtok{text: string(runes[i]), start: i, n: 1})
			i++
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// unmatchedRanges merges runs of consecutive unmatched tokens into rune ranges
// [start, end). Tokens partition the line contiguously, so adjacent unmatched
// tokens collapse into one span.
func unmatchedRanges(toks []wtok, match []bool) [][2]int {
	var out [][2]int
	k := 0
	for k < len(toks) {
		if match[k] {
			k++
			continue
		}
		start := toks[k].start
		end := toks[k].start + toks[k].n
		k++
		for k < len(toks) && !match[k] {
			end = toks[k].start + toks[k].n
			k++
		}
		out = append(out, [2]int{start, end})
	}
	return out
}

// expandTabs replaces tabs with spaces to the next diffTabWidth stop so width
// math and word-level offsets stay rune-counted (a literal tab inside a
// background-styled segment renders unpredictably). Cheap no-op when tab-free.
func expandTabs(s string, tw int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			pad := tw - col%tw
			for n := 0; n < pad; n++ {
				b.WriteByte(' ')
			}
			col += pad
			continue
		}
		b.WriteRune(r)
		col += runewidth.RuneWidth(r)
	}
	return b.String()
}

// classifyDiffLine buckets one unified-diff line. ---/+++ file headers are
// caught before the +/- content check; everything that isn't a hunk header,
// content, or context line (diff --git, index, \ No newline, Binary, the blank
// trailing split element) is meta.
func classifyDiffLine(line string) diffLineKind {
	if line == "" {
		return dlMeta
	}
	if strings.HasPrefix(line, "@@") {
		return dlHunk
	}
	if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
		return dlMeta
	}
	switch line[0] {
	case '+':
		return dlAdd
	case '-':
		return dlDel
	case ' ':
		return dlContext
	default:
		return dlMeta
	}
}
