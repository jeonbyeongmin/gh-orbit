// Sequencer continue / abort on the Local Changes page. When a cherry-pick /
// revert / merge / rebase stops mid-conflict, the page shows an in-progress
// banner (local_changes.go) and `C` resumes / `ctrl+x` unwinds — so a conflict
// no longer forces the reviewer out to a shell. abort is destructive and rides
// the same inline-confirm pattern as discard (lcAbortOpen, a model flag inside
// viewModeLocalChanges, so the page stays the backdrop); continue is gated on
// every conflict being resolved + staged.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// beginSequencerContinue resumes the in-progress op. It refuses (status line)
// while any conflict is still unresolved — git would reject `--continue` with
// unmerged paths anyway, but catching it here gives a legible reason instead of
// a raw stderr dump. No-op when no op is in progress; repeat-fire while another
// tree-wide mutation runs is dropped so two index writers can't race.
func (m Model) beginSequencerContinue() (tea.Model, tea.Cmd) {
	if m.lcActionInFlight || m.commitInput.inFlight {
		return m, nil
	}
	kind := m.localChanges.Sequencer()
	if kind == git.SequencerNone {
		return m, nil
	}
	if m.localChanges.ConflictCount() > 0 {
		m.status = "resolve & stage all conflicts before continuing"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.lcActionInFlight = true
	m.setBusyStatus(kind.String() + " --continue…")
	return m, sequencerContinueCmd(m.workdir, kind)
}

// openSequencerAbort arms the inline abort confirm. No-op when no op is in
// progress or another mutation is mid-flight.
func (m Model) openSequencerAbort() (tea.Model, tea.Cmd) {
	if m.lcActionInFlight || m.commitInput.inFlight {
		return m, nil
	}
	if m.localChanges.Sequencer() == git.SequencerNone {
		return m, nil
	}
	m.lcAbortOpen = true
	m.status = ""
	return m, nil
}

// handleSequencerAbortKey drives the abort confirm. `y` fires the abort; while
// it runs only ctrl+c is honored so a second key can't fork a parallel git
// call.
func (m Model) handleSequencerAbortKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.lcActionInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "y":
		kind := m.localChanges.Sequencer()
		if kind == git.SequencerNone {
			m.lcAbortOpen = false
			return m, nil
		}
		m.lcActionInFlight = true
		m.setBusyStatus(kind.String() + " --abort…")
		return m, sequencerAbortCmd(m.workdir, kind)
	case "n", "q", "esc":
		m.lcAbortOpen = false
		m.status = "abort: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

// updateSequencerMsg folds the continue / abort replies back in. Continue
// success commits the resolved step and can advance HEAD, so it jumps the graph
// cursor to the new HEAD; a continue that re-conflicts keeps the banner (the
// poll re-detects it on reload). Abort unwinds to the pre-start state. All
// terminal cases reload the graph + the working-tree status, which also clears
// the banner once the op is gone.
func (m Model) updateSequencerMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sequencerContinueDoneMsg:
		m.lcActionInFlight = false
		m.clearBusy()
		m.localChanges.ClosePatch()
		m.localChanges.SetFocus(paneLCTree)
		m.status = "continued"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, tea.Batch(m.reloadCmd(), loadStatusCmd(m.workdir, false))

	case sequencerContinueConflictMsg:
		m.lcActionInFlight = false
		m.clearBusy()
		m.status = "still conflicting — resolve & press C"
		m.statusStyle = statusErrS
		return m, tea.Batch(m.reloadCmd(), loadStatusCmd(m.workdir, false))

	case sequencerContinueFailedMsg:
		m.lcActionInFlight = false
		m.clearBusy()
		m.status = "continue failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case sequencerAbortDoneMsg:
		m.lcActionInFlight = false
		m.lcAbortOpen = false
		m.clearBusy()
		m.localChanges.ClosePatch()
		m.localChanges.SetFocus(paneLCTree)
		m.status = msg.op + ": aborted"
		m.statusStyle = statusOkS
		return m, tea.Batch(m.reloadCmd(), loadStatusCmd(m.workdir, false))

	case sequencerAbortFailedMsg:
		m.lcActionInFlight = false
		m.lcAbortOpen = false
		m.clearBusy()
		m.status = "abort failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	}
	return m, nil
}

const helpTextSequencerAbort = "[y] abort · [esc] cancel"

// renderSequencerAbortInner is the centered abort confirm, composed over the
// Local Changes page by View(). It swaps to a spinner line while the abort runs.
func (m Model) renderSequencerAbortInner() string {
	op := m.localChanges.Sequencer().String()
	if m.lcActionInFlight {
		return strings.Join([]string{
			confirmPromptS.Render("aborting " + op),
			statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " aborting…"),
		}, "\n")
	}
	return strings.Join([]string{
		confirmPromptS.Render("Abort " + op + "? Discards the in-progress operation."),
		help.Render(helpTextSequencerAbort),
	}, "\n")
}
