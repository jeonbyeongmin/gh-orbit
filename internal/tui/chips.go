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
	// colorChipAI is the cyan slot for the AI-vendor chip rendered next
	// to ref chips. CEO Q24 D14 — picked so the chip reads as a peer of
	// the local/remote/tag palette but distinct enough that the eye
	// catches "this commit was co-authored by an AI" at a glance.
	colorChipAI = "51"
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
	chipAIStyle       = newChipStyle(colorChipAI, colorChipFG)
	chipMoreStyle     = newChipStyle(colorChipMore, colorChipFG)
	chipSelectedStyle = newChipStyle(colorSelected, colorChipFG)
	chipDimStyle      = newChipStyle(colorDim, colorChipFG)
	// aiChipPlaceholderStyle paints the dim-dot fetch placeholder. No
	// padding — the dot sits exactly where the chip will land so the row
	// doesn't reflow when the lazy fetch completes.
	aiChipPlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))
)

// aiChipState carries the AI-vendor cache result for one row. fetched=false
// means "not yet in cache — render dim dot"; fetched=true with empty
// vendors means "fetched, no AI vendor — render nothing"; fetched=true
// with vendors renders one chip per vendor. Pulled out of buildChips so
// the cockpit's three render states stay enumerable in one spot.
type aiChipState struct {
	fetched bool
	vendors []git.AIVendor
}

// pairedPrefix is rendered inside the paired-local chip text, just before the
// branch name — the same shape Fork uses (icon-then-chip), expressed as a
// universally available Unicode glyph so terminal-theme variation can't hide
// it. The chip's own bg color extends through the prefix, so it reads as a
// "synced with remote" badge attached to the chip rather than a separate token.
const pairedPrefix = "☁ "

// buildChips renders the chip cluster for one commit row. Returns ("", 0)
// when there's nothing to draw.
//
// Layout (left to right): ref body chips (up to 2) → optional "+M" overflow
// chip → AI-vendor chip (cyan) if known → dim-dot placeholder if AI status
// is still being fetched. selected paints every chip with the cursor color;
// dim swaps every kind to a neutral grey so above-HEAD rows still show
// chip silhouettes. selected wins when both apply.
func buildChips(refNames []string, selected, dim bool, ai aiChipState) (string, int) {
	refs, _ := git.ParseDecoration(refNames)
	chips := git.MergeLocalRemotePairs(refs)

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
		add(chipDisplay(c), chipStyleFor(c))
	}
	if overflow > 0 {
		add(fmt.Sprintf("+%d", overflow), chipMoreStyle)
	}

	// AI chip cluster — appended after ref chips so the eye reads "what
	// is this commit identified by" left to right (ref labels first, AI
	// attribution second). selected/dim still override per the existing
	// rules.
	switch {
	case ai.fetched && len(ai.vendors) > 0:
		for _, v := range ai.vendors {
			add(string(v), chipAIStyle)
		}
	case ai.fetched:
		// fetched + no AI: render nothing. The cockpit's signal is
		// "either this row is AI-authored, or it isn't worth a chip" —
		// reserving a permanent spacer would dilute the cyan as the
		// "AI here" flag.
	default:
		// not fetched yet — dim dot placeholder. 1 cell wide so the
		// surrounding subject doesn't shift when the real chip lands.
		// The placeholder has no Padding so the visual width is exactly
		// runewidth(`·`) == 1; account for it manually.
		b.WriteString(aiChipPlaceholderStyle.Render("·"))
		totalW++
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

func chipDisplay(c git.ChipRef) string {
	name := c.DisplayName
	if runewidth.StringWidth(name) > maxChipTextWidth {
		name = runewidth.Truncate(name, maxChipTextWidth, "…")
	}
	if c.PairedRemote {
		return pairedPrefix + name
	}
	return name
}
