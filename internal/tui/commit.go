// `c` — commit the staged index from the Local Changes page. The message input
// reuses branchcreate.go's input-modal mechanics (textinput, inFlight, inline
// error, package seam), but rides on a flag inside viewModeLocalChanges (like
// lcDiscardOpen) rather than its own viewMode — the page must stay as the modal
// backdrop, and the pane sizes / poll / spinner all key on viewModeLocalChanges.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const commitTimeout = 30 * time.Second

type commitSucceededMsg struct{}
type commitFailedMsg struct{ err error }

// commitExec is the package-level seam over git.CommitStaged.
var commitExec = git.CommitStaged

// commitInputState backs the commit modal. open gates it inside
// viewModeLocalChanges; inFlight freezes the modal keys (only ctrl+c) while the
// commit runs; inlineErr surfaces git's refusal so the user can fix it (a
// rejecting hook, a signing failure) without losing the typed message.
type commitInputState struct {
	open      bool
	inFlight  bool
	input     textinput.Model
	inlineErr string
}

func commitCmd(dir, msg string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commitTimeout)
		defer cancel()
		if err := commitExec(ctx, dir, msg); err != nil {
			return commitFailedMsg{err: err}
		}
		return commitSucceededMsg{}
	}
}

// beginCommit opens the message input over the Local Changes page. An empty
// index is refused with a status line rather than letting git fail on "nothing
// to commit". Repeat-open while a commit (or a stash/discard) is mid-flight is
// dropped so two index writers can't race.
func (m Model) beginCommit() (tea.Model, tea.Cmd) {
	if m.commitInput.inFlight || m.lcActionInFlight {
		return m, nil
	}
	if m.localChanges.StagedCount() == 0 {
		m.status = "nothing staged to commit"
		m.statusStyle = statusOkS
		return m, nil
	}
	ti := textinput.New()
	ti.Placeholder = "commit message"
	ti.CharLimit = 500
	ti.Width = 50
	ti.Focus()
	m.commitInput.input = ti
	m.commitInput.inlineErr = ""
	m.commitInput.open = true
	m.status = ""
	return m, textinput.Blink
}

func (m Model) handleCommitInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the commit runs only ctrl+c is honored — a second key can't fork a
	// parallel git call or close the modal out from under the in-flight reply.
	if m.commitInput.inFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.commitInput.open = false
		m.commitInput.input = textinput.Model{}
		m.commitInput.inlineErr = ""
		m.status = "commit: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "enter":
		text := strings.TrimSpace(m.commitInput.input.Value())
		if text == "" {
			m.commitInput.inlineErr = "commit message is empty"
			return m, nil
		}
		m.commitInput.inFlight = true
		m.commitInput.inlineErr = ""
		// setBusyStatus drives the footer spinner (statusIsBusy) while git runs;
		// the modal shows its own spinner line via renderCommitInputInner.
		m.setBusyStatus("committing…")
		return m, commitCmd(m.workdir, text)
	}
	var cmd tea.Cmd
	m.commitInput.input, cmd = m.commitInput.input.Update(msg)
	return m, cmd
}

// updateCommitMsg folds the commit reply back in. Success closes the modal,
// drops any drilled-in diff (the staged side it showed is now committed), and
// reloads both the graph (new commit + HEAD jump) and the working-tree status
// (the Staged section empties). Failure keeps the modal open with git's message
// inline so the typed text survives a retry.
func (m Model) updateCommitMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case commitSucceededMsg:
		m.commitInput.open = false
		m.commitInput.inFlight = false
		m.commitInput.input = textinput.Model{}
		m.commitInput.inlineErr = ""
		m.clearBusy()
		m.localChanges.ClosePatch()
		m.localChanges.SetFocus(paneLCTree)
		m.status = "committed"
		m.statusStyle = statusOkS
		// reloadCmd refreshes the graph/refs/worktrees; the Local Changes status
		// snapshot is separate (reloadCmd doesn't touch it), so reload it too. The
		// HEAD-jump sentinel parks the graph cursor on the new commit.
		m.pendingHEADHash = pendingHEADSentinel
		return m, tea.Batch(m.reloadCmd(), loadStatusCmd(m.workdir, false))

	case commitFailedMsg:
		m.commitInput.inFlight = false
		m.commitInput.inlineErr = firstLine(msg.err.Error())
		m.clearBusy()
		return m, nil
	}
	return m, nil
}

const helpTextCommit = "[enter] commit · [esc] cancel"

// renderCommitInputInner — header (naming the staged count), textinput row,
// inline error / busy spinner or spacer, action hint. Constant row count so the
// hint never bounces. Mirrors renderBranchCreateInputInner.
func (m Model) renderCommitInputInner() string {
	header := modalHeaderS.Render(fmt.Sprintf("[Commit %d staged]", m.localChanges.StagedCount()))
	input := m.commitInput.input.View()
	errLine := " "
	switch {
	case m.commitInput.inlineErr != "":
		errLine = statusErrS.Render(m.commitInput.inlineErr)
	case m.commitInput.inFlight:
		errLine = statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " committing…")
	}
	hint := help.Render(helpTextCommit)
	return strings.Join([]string{header, input, errLine, hint}, "\n")
}
