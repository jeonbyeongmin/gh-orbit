package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// Chip colors deliberately sit far enough from laneStyles (212/215/228/120/117/183)
// that lane glyphs in the same row stay distinguishable. Lane glyphs are
// foreground-only; chips set the background, so the visual separation comes
// from text vs box rather than competing colors alone.
const (
	colorChipLocal  = "39"  // bright cyan
	colorChipRemote = "207" // bright magenta
	colorChipTag    = "220" // gold
	colorChipHead   = "196" // red
	colorChipMore   = "240" // dim gray
	colorChipFG     = "232" // near-black, for light-on-dark contrast
	colorChipHeadFG = "231" // white, for legibility on red
)

var (
	chipLocalStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorChipLocal)).
			Foreground(lipgloss.Color(colorChipFG)).
			Padding(0, 1)
	chipRemoteStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorChipRemote)).
			Foreground(lipgloss.Color(colorChipFG)).
			Padding(0, 1)
	chipTagStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorChipTag)).
			Foreground(lipgloss.Color(colorChipFG)).
			Padding(0, 1)
	chipHeadStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorChipHead)).
			Foreground(lipgloss.Color(colorChipHeadFG)).
			Bold(true).
			Padding(0, 1)
	chipMoreStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorChipMore)).
			Foreground(lipgloss.Color(colorChipFG)).
			Padding(0, 1)
	chipSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color(colorSelected)).
				Foreground(lipgloss.Color(colorChipFG)).
				Padding(0, 1)
)

// buildChips renders the chip cluster for one commit row.
//
// Truncation policy (matches the interview answers):
//   - When a HEAD chip is shown, the body keeps at most 1 ref chip; the rest
//     collapse into a "+N" chip → cluster is HEAD + 1 + (+N).
//   - With no HEAD chip the body keeps 2 ref chips → 2 + (+N).
//   - selected=true forces every chip's background to colorSelected so the
//     row's cursor highlight sweeps over them too. The kind colors are
//     intentionally lost on the selected row.
//
// Returns ("", 0) when there's nothing to draw — empty refNames and no
// detached HEAD.
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

	var parts []string
	if hasHeadChip {
		parts = append(parts, renderChip("HEAD", chipHeadStyle, selected))
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

	for _, c := range visible {
		parts = append(parts, renderChip(chipDisplay(c), chipStyleFor(c), selected))
	}
	if overflow > 0 {
		parts = append(parts, renderChip(fmt.Sprintf("+%d", overflow), chipMoreStyle, selected))
	}

	// Adjacent chips already each contribute 1 cell of left+right padding,
	// so concatenating without an extra separator gives a 2-cell visual gap
	// between chips — enough for the eye to read them as distinct boxes.
	out := strings.Join(parts, "")
	return out, lipgloss.Width(out)
}

func renderChip(text string, base lipgloss.Style, selected bool) string {
	if selected {
		return chipSelectedStyle.Render(text)
	}
	return base.Render(text)
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
	if c.PairedRemote {
		return c.DisplayName + "↑"
	}
	return c.DisplayName
}
