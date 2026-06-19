// PR checks modal: viewModePRChecks lists every CI context of one open PR (the
// per-check rows prs.go preserved before worseCheckState collapsed them into
// the single badge glyph), so a reviewer can see *which* check failed without
// leaving the terminal. `C` opens it for the cursor PR — from the graph (the
// cursor row's PR-badged chip, via prForCursorRow) and from the Pull Requests
// page (the cursor card) alike, the same matching enter / m use.
//
// `enter` on a row opens that check's log in the browser. The cockpit is a gh
// extension, but gh has no "open an arbitrary URL" verb, so the per-check
// detailsUrl / targetUrl rides the OS launcher local_changes_open.go already
// uses (open / xdg-open / cmd start) instead of a gh subcommand.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// prChecksState backs viewModePRChecks. rows is snapshotted from the PR at
// open time (CheckRows is already failures-first). returnMode records the page
// that armed it so the centered modal composes over — and closes back to — the
// graph or the Pull Requests page, mirroring mergeReturnMode.
type prChecksState struct {
	number     int
	rows       []prCheck
	cursor     int
	returnMode viewMode
}

type prCheckOpenedMsg struct{ name string }
type prCheckOpenFailedMsg struct{ err error }

// openURLExec is the package-level seam over the OS "open this URL" launcher —
// the URL twin of openFileExec, minus the file-exists check. Stubbed in tests.
var openURLExec = func(ctx context.Context, url string) error {
	return osLaunch(ctx, url)
}

func openURLCmd(url, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openCmdTimeout)
		defer cancel()
		if err := openURLExec(ctx, url); err != nil {
			return prCheckOpenFailedMsg{err: err}
		}
		return prCheckOpenedMsg{name: name}
	}
}

// beginPRChecksFor opens viewModePRChecks for an explicit PR. A PR whose rollup
// is empty (no CI configured — the chip shows no glyph either) has nothing to
// list, so it reports on the status line and stays put. returnMode is whatever
// page armed it.
func (m Model) beginPRChecksFor(pr prInfo) (Model, tea.Cmd) {
	if len(pr.CheckRows) == 0 {
		m.status = fmt.Sprintf("no CI checks on #%d", pr.Number)
		m.statusStyle = statusErrS
		return m, nil
	}
	m.prChecks = prChecksState{
		number:     pr.Number,
		rows:       pr.CheckRows,
		returnMode: m.mode,
	}
	m.mode = viewModePRChecks
	m.status = ""
	return m, nil
}

// beginPRChecksForCursor is graph `C`: open the checks modal for the cursor
// row's PR, mirroring enter / m. Rows without a PR-bearing chip report.
func (m Model) beginPRChecksForCursor() (Model, tea.Cmd) {
	pr, ok := m.prForCursorRow()
	if !ok {
		m.status = "no open PR on this commit"
		m.statusStyle = statusErrS
		return m, nil
	}
	return m.beginPRChecksFor(pr)
}

// beginPRChecksForCursorPR is the Pull Requests page `C`: open the checks modal
// for the cursor card's PR.
func (m Model) beginPRChecksForCursorPR() (Model, tea.Cmd) {
	pr, ok := m.cursorPR()
	if !ok {
		return m, nil
	}
	return m.beginPRChecksFor(pr)
}

// prChecksMoveCursor bounds the cursor to [0, len-1].
func (m Model) prChecksMoveCursor(delta int) Model {
	if len(m.prChecks.rows) == 0 {
		return m
	}
	c := m.prChecks.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.prChecks.rows) {
		c = len(m.prChecks.rows) - 1
	}
	m.prChecks.cursor = c
	return m
}

// prChecksOpenSelected is `enter`: open the cursor check's log in the browser.
// A check with no details URL (a bare commit status carrying no targetUrl)
// reports instead of launching an empty tab.
func (m Model) prChecksOpenSelected() (Model, tea.Cmd) {
	if m.prChecks.cursor < 0 || m.prChecks.cursor >= len(m.prChecks.rows) {
		return m, nil
	}
	row := m.prChecks.rows[m.prChecks.cursor]
	if row.URL == "" {
		m.status = fmt.Sprintf("no log URL for %s", row.Name)
		m.statusStyle = statusErrS
		return m, nil
	}
	m.setBusyStatus(fmt.Sprintf("opening %s…", row.Name))
	return m, openURLCmd(row.URL, row.Name)
}

// closePRChecks returns to the launching page and clears the modal state.
func (m *Model) closePRChecks() {
	m.mode = m.prChecks.returnMode
	m.prChecks = prChecksState{}
}

const helpTextPRChecks = "[↑/↓] navigate · [enter] open log · [esc] close"

// renderPRChecksInner is the centered checks modal: a header naming the PR, a
// scroll-windowed list of `<glyph> <name>` rows (failures first, from
// CheckRows), and the action hint. Same renderModalBox vocabulary and
// renderScrollWindow window math as the branches modal.
func (m Model) renderPRChecksInner() string {
	header := modalHeaderS.Render(fmt.Sprintf("[Checks · #%d]", m.prChecks.number))
	rows := m.prChecks.rows

	const visibleBudget = 16
	lines := []string{header}
	lines = append(lines, renderScrollWindow(
		m.prChecks.cursor-visibleBudget/2, visibleBudget, len(rows),
		func(i int) string {
			r := rows[i]
			marker, name := "  ", r.Name
			if i == m.prChecks.cursor {
				marker = selectedStyle.Render("> ")
				name = selectedStyle.Render(name)
			}
			return marker + ciStyle(r.State).Render(prCheckGlyph(r.State)) + " " + name
		})...)
	lines = append(lines, help.Render(helpTextPRChecks))
	return strings.Join(lines, "\n")
}
