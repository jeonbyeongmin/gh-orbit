package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Centered modal overlay composer. The two helpers below are deliberately
// the only piece of code that synthesises ANSI sequences directly — the rest
// of the package goes through lipgloss styles.
//
// The composer renders modals on top of the live 3-pane View output without
// reserving rows in paneSizes. Backdrop lines are rewritten with a faint +
// grayscale style; the modal box is overlaid at the screen center using
// ansi.Cut so the SGR state of the dim prefix survives the cuts.

// dimEnable is the SGR pair we wrap each backdrop segment in. Two separate
// sequences (faint, then 256-color fg) so terminals that ignore SGR 2 still
// fall back to a grayscale read, and so tests can substring-match the color
// half (`\x1b[38;5;240m`) without coupling to the faint half.
const (
	dimFaint  = "\x1b[2m"
	dimColor  = "\x1b[38;5;240m"
	dimEnable = dimFaint + dimColor
	dimReset  = "\x1b[0m"
)

// sgrSequence matches a single CSI ... m token (Select Graphic Rendition).
// dimLine drops every SGR token in the input so the dim wrap is the only
// active style; non-SGR escape sequences (cursor save/restore, OSC, etc.)
// are intentionally preserved so e.g. textinput cursor visibility isn't
// nuked by accident.
var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// dimLine returns the input line rewritten so that:
//   - every existing SGR token is dropped,
//   - the result is wrapped in a faint+grayscale prefix and a hard reset
//     suffix.
//
// Empty input is returned unchanged so the composer doesn't emit a
// pointless `\x1b[2m\x1b[38;5;240m\x1b[0m` that would still register as a
// non-empty line in lipgloss.Width.
func dimLine(line string) string {
	stripped := sgrSequence.ReplaceAllString(line, "")
	if stripped == "" {
		return line
	}
	return dimEnable + stripped + dimReset
}

// composeOverlay paints `modal` over `base` at screen center. The modal
// box is sized by the caller (it's already lipgloss-styled with a border);
// composeOverlay only places it.
//
// Layout:
//   - dim every base line first (so the row underneath the modal is also
//     dim — modal lines that are shorter than modalW get padded with
//     spaces, not bleed-through),
//   - on rows the modal occupies, splice the modal line between the
//     left/right slices of the dimmed base, using ansi.Cut to keep SGR
//     state intact across the splice points.
//
// Small-screen safety: if modal exceeds base in either axis, the modal is
// clamped to base dimensions (rows truncated, lines ansi.Truncate'd) and
// drawn at top=0 / left=0. The function never panics or returns a
// short-by-one frame.
func composeOverlay(base, modal string, baseW, baseH int) string {
	if baseW <= 0 || baseH <= 0 {
		return base
	}

	baseLines := strings.Split(base, "\n")
	if len(baseLines) > baseH {
		baseLines = baseLines[:baseH]
	}

	modalLines := strings.Split(modal, "\n")
	modalH := len(modalLines)
	modalW := 0
	for _, l := range modalLines {
		if w := lipgloss.Width(l); w > modalW {
			modalW = w
		}
	}

	if modalH > baseH {
		modalH = baseH
		modalLines = modalLines[:modalH]
	}
	if modalW > baseW {
		modalW = baseW
		clamped := make([]string, len(modalLines))
		for i, l := range modalLines {
			clamped[i] = ansi.Truncate(l, modalW, "")
		}
		modalLines = clamped
	}

	top := (baseH - modalH) / 2
	if top < 0 {
		top = 0
	}
	left := (baseW - modalW) / 2
	if left < 0 {
		left = 0
	}

	out := make([]string, len(baseLines))
	for i, line := range baseLines {
		padded := padToWidth(line, baseW)
		dimmed := dimLine(padded)
		if i >= top && i < top+modalH {
			modalLine := modalLines[i-top]
			modalLine = padToWidth(modalLine, modalW)
			leftPart := ansi.Cut(dimmed, 0, left)
			rightPart := ansi.Cut(dimmed, left+modalW, baseW)
			out[i] = leftPart + modalLine + rightPart
		} else {
			out[i] = dimmed
		}
	}
	return strings.Join(out, "\n")
}

// padToWidth right-pads s with spaces so its visible width equals w.
// Lines shorter than w would otherwise let the terminal's own background
// bleed through the dim wrap on rows underneath the modal box.
func padToWidth(s string, w int) string {
	cur := lipgloss.Width(s)
	if cur >= w {
		return s
	}
	return s + strings.Repeat(" ", w-cur)
}
