// `P` — push the current branch. The cockpit's missing third of the
// network triad (F fetch · p pull · P push). Never forces; a first-push
// branch gets `--set-upstream origin <branch>` automatically inside the
// wrapper.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// pushTimeout mirrors pullTimeout — a large first push over slow HTTPS
// can take a while; a hard cap beats a hung TUI.
const pushTimeout = 300 * time.Second

type pushSucceededMsg struct{ branch string }
type pushFailedMsg struct{ err error }

// pushExec is the package-level seam over git.Push for tests.
var pushExec = git.Push

func pushCmd(dir, branch string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		if err := pushExec(ctx, dir, branch); err != nil {
			return pushFailedMsg{err: err}
		}
		return pushSucceededMsg{branch: branch}
	}
}

// beginPush dispatches a push for the current branch. Rejected on a
// detached HEAD (nothing to push) and gated by pushInFlight so a held-down
// P can't stack pushes.
func (m Model) beginPush() (Model, tea.Cmd) {
	if m.gitMutationInFlight() {
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
		m.status = "push: detached HEAD — checkout a branch first"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.pushInFlight = true
	m.setBusyStatus("pushing " + headBranch + "…")
	return m, pushCmd(m.workdir, headBranch)
}
