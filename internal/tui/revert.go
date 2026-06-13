// `v` — revert the cursor commit on the current branch. Confirm-first like
// cherry-pick (it records a new commit); conflicts delegate to the terminal
// with the mid-revert state left in place. Revert preserves history and is
// safe on already-pushed commits — it is the "scrap a shared commit" half of
// the revert/reset pair, where reset owns the local-only discard.
package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const revertTimeout = 60 * time.Second

type revertSucceededMsg struct{}
type revertConflictMsg struct{ err error }
type revertFailedMsg struct{ err error }

// revertExec is the package-level seam over git.Revert for tests.
var revertExec = git.Revert

// pendingRevert backs viewModeRevertConfirm: the cursor hash to revert and
// the HEAD branch the new commit lands on.
type pendingRevert struct {
	hash   string
	branch string
}

func revertCmd(dir, hash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), revertTimeout)
		defer cancel()
		if err := revertExec(ctx, dir, hash); err != nil {
			if errors.Is(err, git.ErrRevertConflict) {
				return revertConflictMsg{err: err}
			}
			return revertFailedMsg{err: err}
		}
		return revertSucceededMsg{}
	}
}

// beginRevert arms viewModeRevertConfirm for the cursor commit. Detached HEAD
// is rejected (the revert commit would have no branch to land on) and any
// in-flight mutation swallows the key. Unlike rebase/cherry-pick the
// cursor==HEAD case is allowed — reverting the tip commit is a normal undo.
func (m Model) beginRevert() (Model, tea.Cmd) {
	if m.gitMutationInFlight() {
		return m, nil
	}
	c, ok := m.graph.Selected()
	if !ok {
		return m, nil
	}
	var headBranch string
	for _, r := range m.refs.LocalRefs() {
		if r.IsHead {
			headBranch = r.ShortName
			break
		}
	}
	if headBranch == "" {
		m.status = "revert: detached HEAD — checkout a branch first"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.pendingRevert = pendingRevert{hash: c.Hash, branch: headBranch}
	m.mode = viewModeRevertConfirm
	m.status = ""
	return m, nil
}

// renderRevertConfirmInner returns the revert confirm dialog content — the
// same single-y surface as the cherry-pick confirm.
func (m Model) renderRevertConfirmInner() string {
	p := m.pendingRevert
	return confirmPromptS.Render("revert "+shortHash(p.hash)+" on "+p.branch+"?") + "\n" +
		help.Render("[y] revert · [esc] cancel")
}

func (m Model) handleRevertConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.mode = viewModeNormal
		m.pendingRevert = pendingRevert{}
		m.status = "revert: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y":
		p := m.pendingRevert
		m.mode = viewModeNormal
		m.revertInFlight = true
		m.setBusyStatus("revert: " + shortHash(p.hash) + " on " + p.branch + " …")
		return m, revertCmd(m.workdir, p.hash)
	}
	return m, nil
}
