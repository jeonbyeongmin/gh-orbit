package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// Chip backgrounds sit far enough from laneStyles' foreground colors
// (212/215/228/120/117/183) that the lane glyph and the chip box read as
// distinct visual elements in the same row.
const (
	colorChipLocal  = "39"
	colorChipRemote = "207"
	colorChipTag    = "220"
	colorChipHead   = "196"
	colorChipMore   = "240"
	colorChipFG     = "232"
	colorChipHeadFG = "231"

	// maxChipTextWidth caps a single chip's text so a really long branch
	// name doesn't push the subject off the row. Branch names longer than
	// this are truncated to "long-branch-na…".
	maxChipTextWidth = 20
)

func newChipStyle(bg, fg string) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(fg)).
		Padding(0, 1)
}

// pairedSideBorder draws a single-cell dotted glyph on the left and right of
// a paired-local chip. Top/Bottom strings stay empty because the chip is
// single-line; only BorderLeft/Right are enabled at the style level so the
// visible width stays at text + 2 (Padding(0,0) + 1-cell border × 2) — the
// same total cells a chip with Padding(0,1) takes, so totalW is unchanged.
var pairedSideBorder = lipgloss.Border{Left: "┊", Right: "┊"}

var (
	chipLocalStyle  = newChipStyle(colorChipLocal, colorChipFG)
	chipRemoteStyle = newChipStyle(colorChipRemote, colorChipFG)
	chipTagStyle    = newChipStyle(colorChipTag, colorChipFG)
	chipHeadStyle   = newChipStyle(colorChipHead, colorChipHeadFG).Bold(true)
	chipMoreStyle   = newChipStyle(colorChipMore, colorChipFG)
	// chipLocalPairedStyle: same colors as chipLocalStyle but Padding(0,0) +
	// dotted side-border to signal "local is in sync with origin/<same>"
	// without the misleading "↑" marker the previous implementation appended.
	chipLocalPairedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color(colorChipLocal)).
				Foreground(lipgloss.Color(colorChipFG)).
				Padding(0, 0).
				Border(pairedSideBorder, false, true, false, true).
				BorderBackground(lipgloss.Color(colorChipLocal)).
				BorderForeground(lipgloss.Color(colorChipFG))
	chipSelectedStyle = newChipStyle(colorSelected, colorChipFG)
)

// buildChips renders the chip cluster for one commit row. Returns ("", 0)
// when there's nothing to draw.
//
// Layout: optional HEAD chip + up to N body chips + optional "+M" chip.
// N is 1 when HEAD is present, 2 otherwise. selected=true paints every chip
// with the row's cursor color, deliberately overriding the kind palette.
func buildChips(refNames []string, selected bool) (string, int) {
	refs, headDetached := git.ParseDecoration(refNames)
	chips := git.MergeLocalRemotePairs(refs)

	hasHeadChip := headDetached
	if !hasHeadChip {
		for _, c := range chips {
			if c.IsHead {
				hasHeadChip = true
				break
			}
		}
	}
	if !hasHeadChip && len(chips) == 0 {
		return "", 0
	}

	bodyCap := 2
	if hasHeadChip {
		bodyCap = 1
	}
	overflow := 0
	visible := chips
	if len(visible) > bodyCap {
		overflow = len(visible) - bodyCap
		visible = visible[:bodyCap]
	}

	var b strings.Builder
	totalW := 0
	add := func(text string, base lipgloss.Style) {
		style := base
		if selected {
			style = chipSelectedStyle
		}
		b.WriteString(style.Render(text))
		// Each chip is text + Padding(0, 1) on both sides; adjacent chips
		// share no separator, so visual width is just text + 2.
		totalW += runewidth.StringWidth(text) + 2
	}

	if hasHeadChip {
		add("HEAD", chipHeadStyle)
	}
	for _, c := range visible {
		add(chipDisplay(c), chipStyleFor(c))
	}
	if overflow > 0 {
		add(fmt.Sprintf("+%d", overflow), chipMoreStyle)
	}

	if totalW == 0 {
		return "", 0
	}
	return b.String(), totalW
}

func chipStyleFor(c git.ChipRef) lipgloss.Style {
	switch c.Kind {
	case git.RefKindRemote:
		return chipRemoteStyle
	case git.RefKindTag:
		return chipTagStyle
	default:
		if c.PairedRemote {
			return chipLocalPairedStyle
		}
		return chipLocalStyle
	}
}

func chipDisplay(c git.ChipRef) string {
	name := c.DisplayName
	if runewidth.StringWidth(name) > maxChipTextWidth {
		name = runewidth.Truncate(name, maxChipTextWidth, "…")
	}
	return name
}
