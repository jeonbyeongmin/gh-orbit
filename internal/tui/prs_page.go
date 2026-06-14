// Pull Requests page: viewModePRsPage is a full-screen tab (the 4th, after
// Local Changes) reached via the tab/shift+tab cycle. It lists every open PR
// `gh pr list` returned — including ones whose head branch isn't checked out
// locally, which the graph cursor can't reach. `enter` opens the cursor PR on
// GitHub in the browser; `m` opens the merge confirm. The data is m.prList —
// prs.go's prListCmd already loads it for the chip badges; this surface only
// owns its own cursor.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// prsPageState backs viewModePRsPage. cursor indexes into m.prList.
type prsPageState struct {
	cursor int
}

// enterPRsPage flips to the Pull Requests tab. Unlike the old `l` modal there
// is no empty guard — a tab you cycle to always shows, rendering an empty state
// when there are no open PRs.
func (m Model) enterPRsPage() (Model, tea.Cmd) {
	if m.prsPage.cursor >= len(m.prList) {
		m.prsPage.cursor = 0
	}
	m.mode = viewModePRsPage
	m.status = ""
	return m, nil
}

func (m Model) prsPageMoveCursor(delta int) Model {
	if len(m.prList) == 0 {
		return m
	}
	c := m.prsPage.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.prList) {
		c = len(m.prList) - 1
	}
	m.prsPage.cursor = c
	return m
}

// cursorPR returns the PR under the page cursor, or ok=false when the list is
// empty / the cursor is out of range. Shared by the enter / merge handlers.
func (m Model) cursorPR() (prInfo, bool) {
	if m.prsPage.cursor < 0 || m.prsPage.cursor >= len(m.prList) {
		return prInfo{}, false
	}
	return m.prList[m.prsPage.cursor], true
}

// prsPageOpenWeb is `enter`: open the cursor PR on GitHub in the browser.
func (m Model) prsPageOpenWeb() (Model, tea.Cmd) {
	pr, ok := m.cursorPR()
	if !ok {
		return m, nil
	}
	return m.openPRWeb(pr.Number)
}

// prsPageMerge is `m`: arm the merge confirm dialog for the cursor PR.
func (m Model) prsPageMerge() (Model, tea.Cmd) {
	pr, ok := m.cursorPR()
	if !ok {
		return m, nil
	}
	return m.beginMergeFor(pr.Number)
}

// renderPRsView renders the full-screen Pull Requests page sized to fit inside
// the page box (width × height), mirroring renderWorktreesView: a header, a
// scroll-windowed list, padded to exactly height so the box frame never jumps.
func (m Model) renderPRsView(width, height int) string {
	title := fmt.Sprintf("[Pull requests · %d]", len(m.prList))
	fresh := ""
	if !m.refs.lastFetchAt.IsZero() {
		fresh = "fetched " + relativeShortAt(m.refs.lastFetchAt, time.Now())
		if !strings.HasSuffix(fresh, "just now") {
			fresh += " ago"
		}
	}
	header := layoutLeftRight(modalHeaderS.Render(title), help.Render(fresh), width)

	lines := make([]string, 0, height)
	lines = append(lines, header, "")
	if len(m.prList) == 0 {
		lines = append(lines, help.Render("(no open PRs)"))
	} else {
		// Reserve two rows for the ↑/↓ overflow markers when the list spills
		// past the window, so the final clamp never cuts a marker.
		areaH := height - 2
		if areaH < 1 {
			areaH = 1
		}
		visible := areaH
		if len(m.prList) > areaH && visible > 2 {
			visible = areaH - 2
		}
		rowW := width - 2
		if rowW < 1 {
			rowW = 1
		}
		lines = append(lines, renderScrollWindow(
			m.prsPage.cursor-visible/2, visible, len(m.prList),
			func(i int) string {
				return renderPRRow(m.prList[i], i == m.prsPage.cursor, rowW)
			})...)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// renderPRRow formats one PR row: cursor marker, `#N`, the CI rollup glyph, the
// title, and `· author`. The glyph sits up front (it's why you'd pick one PR
// over another) so it always survives; the title then the author absorb
// truncation when the row overflows — the same "keep the load-bearing bits,
// shrink the prose" order the commit graph uses.
func renderPRRow(pr prInfo, selected bool, width int) string {
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
