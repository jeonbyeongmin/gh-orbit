// `c` — cherry-pick the cursor commit onto the current branch. Confirm-
// first like rebase (history-writing action); conflicts delegate to the
// terminal with the mid-pick state left in place.
package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const cherryPickTimeout = 60 * time.Second

type cherryPickSucceededMsg struct{}
type cherryPickConflictMsg struct{ err error }
type cherryPickFailedMsg struct{ err error }

// cherryPickExec is the package-level seam over git.CherryPick for tests.
var cherryPickExec = git.CherryPick

// pendingCherryPick backs viewModeCherryPickConfirm: the cursor hash to
// apply and the HEAD branch it lands on.
type pendingCherryPick struct {
	hash   string
	branch string
}

func cherryPickCmd(dir, hash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cherryPickTimeout)
		defer cancel()
		if err := cherryPickExec(ctx, dir, hash); err != nil {
			if errors.Is(err, git.ErrCherryPickConflict) {
				return cherryPickConflictMsg{err: err}
			}
			return cherryPickFailedMsg{err: err}
		}
		return cherryPickSucceededMsg{}
	}
}

// beginCherryPick arms viewModeCherryPickConfirm for the cursor commit.
// Same up-front rejections as rebase: detached HEAD, cursor==HEAD,
// anything already in flight.
func (m Model) beginCherryPick() (Model, tea.Cmd) {
	if m.gitMutationInFlight() {
		return m, nil
	}
	c, ok := m.graph.Selected()
	if !ok {
		return m, nil
	}
	var headBranch, headHash string
	for _, r := range m.refs.LocalRefs() {
		if r.IsHead {
			headBranch = r.ShortName
			headHash = r.ObjectName
			break
		}
	}
	if headBranch == "" {
		m.status = "cherry-pick: detached HEAD — checkout a branch first"
		m.statusStyle = statusErrS
		return m, nil
	}
	if c.Hash == headHash {
		m.status = "cherry-pick: cursor is already HEAD"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.pendingCherryPick = pendingCherryPick{hash: c.Hash, branch: headBranch}
	m.mode = viewModeCherryPickConfirm
	m.status = ""
	return m, nil
}

// renderCherryPickConfirmInner returns the cherry-pick confirm dialog
// content — same surface as the rebase confirm.
func (m Model) renderCherryPickConfirmInner() string {
	p := m.pendingCherryPick
	return confirmPromptS.Render("cherry-pick "+shortHash(p.hash)+" onto "+p.branch+"?") + "\n" +
		help.Render("[y] pick · [esc] cancel")
}

func (m Model) handleCherryPickConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.mode = viewModeNormal
		m.pendingCherryPick = pendingCherryPick{}
		m.status = "cherry-pick: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y":
		p := m.pendingCherryPick
		m.mode = viewModeNormal
		m.cherryPickInFlight = true
		m.setBusyStatus("cherry-pick: " + shortHash(p.hash) + " onto " + p.branch + " …")
		return m, cherryPickCmd(m.workdir, p.hash)
	}
	return m, nil
}
