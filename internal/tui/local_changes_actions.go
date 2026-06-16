// Whole-tree actions for the Local Changes page: `s` stashes every change
// (tracked + untracked) and `r` opens the discard confirm. Per-file staging
// lives in model.go; this file owns only the two tree-wide mutations and the
// inline discard dialog. The dialog is a model flag (lcDiscardOpen), not a
// viewMode, so the page renders unchanged underneath it.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// dispatchLCStashAll fires `git stash push --include-untracked` for the whole
// tree. Stash is reversible (pop in the terminal) so it skips a confirm; an
// empty tree short-circuits with a status line instead of git's error.
func (m Model) dispatchLCStashAll() (tea.Model, tea.Cmd) {
	if m.lcActionInFlight {
		return m, nil
	}
	if !m.localChanges.HasChanges() {
		m.status = "nothing to stash"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.lcActionInFlight = true
	m.setBusyStatus("stashing all changes…")
	return m, stashAllCmd(m.workdir)
}

// openLCDiscard arms the inline discard confirm. An empty tree reports
// "nothing to discard" rather than opening a dialog that would no-op.
func (m Model) openLCDiscard() (tea.Model, tea.Cmd) {
	if m.lcActionInFlight {
		return m, nil
	}
	if !m.localChanges.HasChanges() {
		m.status = "nothing to discard"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.lcDiscardOpen = true
	m.status = ""
	return m, nil
}

// handleLCDiscardKey drives the discard confirm. `t` discards tracked edits +
// the index only (reset --hard); `a` also removes untracked new files
// (reset --hard + clean -fd). While the action runs only ctrl+c is honored so
// a second key can't fork a parallel git call.
func (m Model) handleLCDiscardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.lcActionInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "t":
		return m.dispatchLCDiscard(false)
	case "a":
		return m.dispatchLCDiscard(true)
	case "q", "esc":
		m.lcDiscardOpen = false
		m.status = "discard: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

// dispatchLCDiscard fires the reset (+ optional clean). The dialog stays open
// showing the spinner until the terminal handler closes it — mirroring the
// graph stash dialog's in-place progress.
func (m Model) dispatchLCDiscard(includeUntracked bool) (tea.Model, tea.Cmd) {
	m.lcActionInFlight = true
	if includeUntracked {
		m.setBusyStatus("discarding all changes…")
	} else {
		m.setBusyStatus("discarding tracked changes…")
	}
	return m, discardAllCmd(m.workdir, includeUntracked)
}

// updateLCActionMsg folds the stash-all / discard-all replies back in. Both
// leave the tree clean, so each reloads the status snapshot + the sidebar
// dirty marker and drops focus back to the (now empty) tree. A stash also
// reloads the graph so the new stash@{0} row appears.
func (m Model) updateLCActionMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case localChangesStashAllDoneMsg:
		m.lcActionInFlight = false
		m.clearBusy()
		// Tree is clean now; drop any diff the user had drilled into so a large
		// patch for a stashed-away file doesn't sit resident behind the tree.
		m.localChanges.ClosePatch()
		m.localChanges.SetFocus(paneLCTree)
		m.status = "stashed all changes — pop in your terminal (git stash pop)"
		m.statusStyle = statusOkS
		return m, tea.Batch(m.reloadCmd(), loadStatusCmd(m.workdir, false))

	case localChangesStashAllFailedMsg:
		m.lcActionInFlight = false
		m.clearBusy()
		m.status = "stash failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesDiscardDoneMsg:
		m.lcActionInFlight = false
		m.lcDiscardOpen = false
		m.clearBusy()
		m.localChanges.ClosePatch()
		m.localChanges.SetFocus(paneLCTree)
		if msg.includeUntracked {
			m.status = "discarded all changes (incl. new files)"
		} else {
			m.status = "discarded tracked changes"
		}
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, tea.Batch(
			loadStatusCmd(m.workdir, false),
			loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		)

	case localChangesDiscardFailedMsg:
		m.lcActionInFlight = false
		m.lcDiscardOpen = false
		m.clearBusy()
		m.status = "discard failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	}
	return m, nil
}

// renderLCDiscardInner is the centered discard confirm, composed over the Local
// Changes page by View(). It swaps to a spinner line while the reset runs.
func (m Model) renderLCDiscardInner() string {
	if m.lcActionInFlight {
		return strings.Join([]string{
			confirmPromptS.Render("discard all changes"),
			statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " discarding…"),
		}, "\n")
	}
	return strings.Join([]string{
		confirmPromptS.Render("Discard all local changes?"),
		help.Render("[t] tracked only · [a] all incl. new files · [esc] cancel"),
	}, "\n")
}
