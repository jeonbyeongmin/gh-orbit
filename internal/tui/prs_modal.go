// PR list modal: viewModePRsModal hosts a centered overlay listing every open
// PR `gh pr list` returned — including PRs whose head branch isn't checked out
// locally, which the cursor `enter` path can't reach. `l` opens it from
// viewModeNormal; `enter` pulls the cursor PR's diff into the same review
// overlay (beginPRReviewFor), so approve / merge work identically.
// The data is m.prList — prs.go's prListCmd already loads it for the chip
// badges; this surface only owns its own cursor.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// prsModalState backs viewModePRsModal. cursor indexes into m.prList as
// observed at modal-open time; a merge from inside the modal refreshes the
// list, so any re-entry recomputes cursor afresh via beginPRsModal.
type prsModalState struct {
	cursor int
}

const helpTextPRsModal = "[j/k] navigate · [enter] review · [esc] close"

// beginPRsModal opens viewModePRsModal. Empty list surfaces an inline error
// and stays in viewModeNormal so the user never enters a modal with nothing
// to act on — same guard as the branches / worktrees modals.
func (m Model) beginPRsModal() (Model, tea.Cmd) {
	if len(m.prList) == 0 {
		m.status = "no open PRs"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.prsModal.cursor = 0
	m.mode = viewModePRsModal
	m.status = ""
	return m, nil
}

func (m Model) prsModalMoveCursor(delta int) Model {
	if len(m.prList) == 0 {
		return m
	}
	c := m.prsModal.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.prList) {
		c = len(m.prList) - 1
	}
	m.prsModal.cursor = c
	return m
}

// prsModalEnter closes the modal and opens the cursor PR in the review
// overlay — the same overlay graph `enter` uses, via the shared beginPRReviewFor.
func (m Model) prsModalEnter() (Model, tea.Cmd) {
	if m.prsModal.cursor < 0 || m.prsModal.cursor >= len(m.prList) {
		return m, nil
	}
	pr := m.prList[m.prsModal.cursor]
	m.prsModal = prsModalState{}
	return m.beginPRReviewFor(pr.Number)
}

// renderPRsModalInner returns the centered overlay content: bold header,
// scroll-windowed PR rows, and the action hint — same vocabulary as the
// branches / worktrees modals.
func (m Model) renderPRsModalInner() string {
	header := modalHeaderS.Render("[Pull requests]")
	if len(m.prList) == 0 {
		return strings.Join([]string{
			header,
			help.Render("(no open PRs)"),
			help.Render(helpTextPRsModal),
		}, "\n")
	}

	rowW := m.width - 12
	if rowW > 76 {
		rowW = 76
	}
	if rowW < 40 {
		rowW = 40
	}

	const visibleBudget = 16
	visibleRows := visibleBudget
	if len(m.prList) < visibleRows {
		visibleRows = len(m.prList)
	}

	lines := []string{header}
	lines = append(lines, renderScrollWindow(
		m.prsModal.cursor-visibleRows/2, visibleRows, len(m.prList),
		func(i int) string {
			return renderPRModalRow(m.prList[i], i == m.prsModal.cursor, rowW)
		})...)
	lines = append(lines, help.Render(helpTextPRsModal))
	return strings.Join(lines, "\n")
}

// renderPRModalRow formats one PR row: cursor marker, `#N`, the CI rollup
// glyph, the title, and `· author`. The glyph sits up front (it's why you'd
// pick one PR over another) so it always survives; the title then the author
// absorb truncation when the row overflows — the same "keep the load-bearing
// bits, shrink the prose" order the commit graph uses.
func renderPRModalRow(pr prInfo, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	row := marker + fmt.Sprintf("#%d", pr.Number)
	if g := prCheckGlyph(pr.Checks); g != "" {
		row += " " + g
	}
	row += "  " + pr.Title
	if pr.Author != "" {
		row += " · " + pr.Author
	}
	row = runewidth.Truncate(row, width, "…")
	if selected {
		return selectedStyle.Render(row)
	}
	return row
}
