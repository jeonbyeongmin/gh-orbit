// Stash write actions launched from the graph: `space` on a stash row opens
// the pop / apply dialog, `d` opens the drop confirm. The graph already
// renders each `git stash list` entry as a single labelled row (see
// loadStashOverlay); this file owns only the modals + git dispatch.
package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// stashActionTimeout caps a pop / apply / drop the same way checkout chains
// are capped — a stash apply over a large tree shouldn't outlive the dialog's
// busy spinner.
const stashActionTimeout = 30 * time.Second

// Package-level seams so tests can stub the git calls without a real repo.
var (
	stashPopExec   = git.StashPop
	stashApplyExec = git.StashApply
	stashDropExec  = git.StashDrop
)

// stashActionDoneMsg / ConflictMsg / FailedMsg classify a pop / apply / drop.
// verb is "pop" | "apply" | "drop"; label is the "stash@{N}" slot acted on.
// Conflict only fires for pop / apply (drop has no conflict mode) and means
// markers landed in the tree with the entry preserved.
type stashActionDoneMsg struct {
	verb  string
	label string
}
type stashActionConflictMsg struct {
	verb  string
	label string
	err   error
}
type stashActionFailedMsg struct {
	verb  string
	label string
	err   error
}

// stashActionCmd runs one stash git call behind the action timeout and maps
// its outcome to the typed msgs. conflictSentinel routes a post-failure
// errors.Is check to the conflict msg; nil disables it (drop).
func stashActionCmd(verb, dir, ref string, exec func(context.Context, string, string) error, conflictSentinel error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), stashActionTimeout)
		defer cancel()
		err := exec(ctx, dir, ref)
		if err == nil {
			return stashActionDoneMsg{verb: verb, label: ref}
		}
		if conflictSentinel != nil && errors.Is(err, conflictSentinel) {
			return stashActionConflictMsg{verb: verb, label: ref, err: err}
		}
		return stashActionFailedMsg{verb: verb, label: ref, err: err}
	}
}

// cursorStashLabel returns the "stash@{N}" label of the graph cursor row, or
// ok=false when the cursor isn't on a stash row. The label rides on the
// injected RefNames token (see collectBatchCmd).
func (m Model) cursorStashLabel() (string, bool) {
	c, ok := m.graph.Selected()
	if !ok {
		return "", false
	}
	for _, t := range c.RefNames {
		if strings.HasPrefix(t, "stash@{") {
			return t, true
		}
	}
	return "", false
}

// beginStashAction arms the pop / apply dialog for the cursor stash. The
// label is pinned now because pop / drop re-index the remaining slots.
func (m Model) beginStashAction(label string) (tea.Model, tea.Cmd) {
	if m.gitMutationInFlight() || m.graph.pendingSwap {
		return m, nil
	}
	m.stashTarget = label
	m.mode = viewModeStashAction
	m.status = ""
	return m, nil
}

// beginStashDrop arms the destructive drop confirm for the cursor stash.
func (m Model) beginStashDrop(label string) (tea.Model, tea.Cmd) {
	if m.gitMutationInFlight() || m.graph.pendingSwap {
		return m, nil
	}
	m.stashTarget = label
	m.mode = viewModeStashDropConfirm
	m.status = ""
	return m, nil
}

// dispatchStash fires the chosen verb behind stashInFlight (which gates the
// dialog to ctrl+c) and a busy status that drives the spinner.
func (m Model) dispatchStash(verb string) (tea.Model, tea.Cmd) {
	m.stashInFlight = true
	ref := m.stashTarget
	var cmd tea.Cmd
	switch verb {
	case "pop":
		m.setBusyStatus("stash pop " + ref + "…")
		cmd = stashActionCmd("pop", m.workdir, ref, stashPopExec, git.ErrStashPopConflict)
	case "apply":
		m.setBusyStatus("stash apply " + ref + "…")
		cmd = stashActionCmd("apply", m.workdir, ref, stashApplyExec, git.ErrStashApplyConflict)
	case "drop":
		m.setBusyStatus("stash drop " + ref + "…")
		cmd = stashActionCmd("drop", m.workdir, ref, stashDropExec, nil)
	}
	return m, cmd
}

// closeStashDialog returns to the graph and clears the dialog state.
func (m *Model) closeStashDialog() {
	m.stashInFlight = false
	m.mode = viewModeNormal
	m.stashTarget = ""
}

// updateStashMsg folds a pop / apply / drop reply back into the model. Done
// and conflict both reload (the stash list and / or working tree changed);
// a flat failure changed nothing, so it only reports.
func (m Model) updateStashMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stashActionDoneMsg:
		m.closeStashDialog()
		m.clearBusy()
		m.status = stashDoneVerb(msg.verb) + " " + msg.label
		m.statusStyle = statusOkS
		return m, m.reloadCmd()
	case stashActionConflictMsg:
		m.closeStashDialog()
		m.clearBusy()
		m.status = "stash " + msg.verb + " conflict: markers in tree, " + msg.label + " kept — resolve in your terminal"
		m.statusStyle = statusErrS
		return m, m.reloadCmd()
	case stashActionFailedMsg:
		m.closeStashDialog()
		m.clearBusy()
		m.status = "stash " + msg.verb + " failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	}
	return m, nil
}

// stashDoneVerb is the past-tense status word for a completed action.
func stashDoneVerb(verb string) string {
	switch verb {
	case "pop":
		return "popped"
	case "apply":
		return "applied"
	case "drop":
		return "dropped"
	}
	return verb
}

// handleStashActionKey drives the pop / apply dialog. While a call runs only
// ctrl+c is honored so a second key can't fork a parallel git stash.
func (m Model) handleStashActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.stashInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "p":
		return m.dispatchStash("pop")
	case "a":
		return m.dispatchStash("apply")
	case "q", "esc":
		m.closeStashDialog()
		m.status = "stash: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

// handleStashDropConfirmKey drives the drop confirm. `d` (or `y`) commits the
// destructive drop; esc / q backs out.
func (m Model) handleStashDropConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.stashInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "d", "y":
		return m.dispatchStash("drop")
	case "q", "esc":
		m.closeStashDialog()
		m.status = "stash drop: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

// renderStashActionInner is the centered pop / apply dialog, composed over the
// graph by View(). It swaps to a spinner line while the call runs.
func (m Model) renderStashActionInner() string {
	if m.stashInFlight {
		return strings.Join([]string{
			confirmPromptS.Render(m.stashTarget),
			statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " working…"),
		}, "\n")
	}
	return strings.Join([]string{
		confirmPromptS.Render(m.stashTarget),
		help.Render("[p] pop · [a] apply · [esc] cancel"),
	}, "\n")
}

// renderStashDropConfirmInner is the centered drop confirm.
func (m Model) renderStashDropConfirmInner() string {
	if m.stashInFlight {
		return strings.Join([]string{
			confirmPromptS.Render("drop " + m.stashTarget),
			statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " dropping…"),
		}, "\n")
	}
	return strings.Join([]string{
		confirmPromptS.Render("drop " + m.stashTarget + "?"),
		help.Render("[d] drop · [esc] cancel"),
	}, "\n")
}
