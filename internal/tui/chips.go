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
	colorChipFG     = "232"
	// colorChipMore aliases colorDim — both the "+N" overflow chip and
	// the dim-band chip share the same neutral grey. Keeping one source
	// of truth so the palette can't drift.
	colorChipMore = colorDim

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

var (
	chipLocalStyle    = newChipStyle(colorChipLocal, colorChipFG)
	chipRemoteStyle   = newChipStyle(colorChipRemote, colorChipFG)
	chipTagStyle      = newChipStyle(colorChipTag, colorChipFG)
	chipMoreStyle     = newChipStyle(colorChipMore, colorChipFG)
	chipSelectedStyle = newChipStyle(colorSelected, colorChipFG)
	chipDimStyle      = newChipStyle(colorDim, colorChipFG)
)

// pairedPrefix is rendered inside the paired-local chip text, just before the
// branch name — the same shape Fork uses (icon-then-chip), expressed as a
// universally available Unicode glyph so terminal-theme variation can't hide
// it. The chip's own bg color extends through the prefix, so it reads as a
// "synced with remote" badge attached to the chip rather than a separate token.
const pairedPrefix = "☁ "

// buildChips renders the chip cluster for one commit row. Returns ("", 0)
// when there's nothing to draw.
//
// Layout: up to 2 body chips + optional "+M" overflow chip. selected
// paints every chip with the cursor color; dim swaps every kind to a
// neutral grey so above-HEAD rows still show chip silhouettes. selected
// wins when both apply. prs (head branch → open PR) appends a ` #N✓`-style
// badge inside the matching branch chip; nil/empty draws no badges.
func buildChips(refNames []string, prs map[string]prInfo, selected, dim bool) (string, int) {
	refs, _ := git.ParseDecoration(refNames)
	chips := git.MergeLocalRemotePairs(refs)
	if len(chips) == 0 {
		return "", 0
	}

	const bodyCap = 2
	overflow := 0
	visible := chips
	if len(visible) > bodyCap {
		overflow = len(visible) - bodyCap
		visible = visible[:bodyCap]
	}

	// Row-wide override: selected and dim are constant for the whole row,
	// so resolve once instead of per add() call.
	var override *lipgloss.Style
	switch {
	case selected:
		override = &chipSelectedStyle
	case dim:
		override = &chipDimStyle
	}

	var b strings.Builder
	totalW := 0
	add := func(text string, base lipgloss.Style) {
		style := base
		if override != nil {
			style = *override
		}
		b.WriteString(style.Render(text))
		// Each chip is text + Padding(0, 1) on both sides; adjacent chips
		// share no separator, so visual width is just text + 2.
		totalW += runewidth.StringWidth(text) + 2
	}

	for _, c := range visible {
		add(chipDisplay(c, prs), chipStyleFor(c))
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
		return chipLocalStyle
	}
}

func chipDisplay(c git.ChipRef, prs map[string]prInfo) string {
	name := c.DisplayName
	if runewidth.StringWidth(name) > maxChipTextWidth {
		name = runewidth.Truncate(name, maxChipTextWidth, "…")
	}
	if pr, ok := prForChip(c, prs); ok {
		// The badge survives name truncation — the PR number + CI verdict
		// is the part a reviewer scans for, the branch name is recoverable
		// from context.
		name += " " + prBadge(pr)
	}
	if c.PairedRemote {
		return pairedPrefix + name
	}
	return name
}

// prForChip resolves the open PR (if any) whose head branch this chip
// names. Remote chips match after their remote prefix is stripped —
// `origin/feat-x` and `feat-x` carry the same PR. Tags never match.
func prForChip(c git.ChipRef, prs map[string]prInfo) (prInfo, bool) {
	if len(prs) == 0 {
		return prInfo{}, false
	}
	switch c.Kind {
	case git.RefKindLocal:
		pr, ok := prs[c.DisplayName]
		return pr, ok
	case git.RefKindRemote:
		if i := strings.IndexByte(c.DisplayName, '/'); i >= 0 {
			pr, ok := prs[c.DisplayName[i+1:]]
			return pr, ok
		}
	}
	return prInfo{}, false
}

// prBadge renders the in-chip PR marker: number + 1-cell CI glyph. No
// glyph when the PR has no checks — `#N` alone still says "has an open
// PR", which is the load-bearing bit.
func prBadge(pr prInfo) string {
	badge := fmt.Sprintf("#%d", pr.Number)
	switch pr.Checks {
	case prChecksPassing:
		badge += "✓"
	case prChecksFailing:
		badge += "✗"
	case prChecksPending:
		badge += "○"
	}
	return badge
}
