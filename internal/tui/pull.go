package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// pullTimeout is generous because a rebase can replay many commits over slow
// HTTPS — fetch's 120s isn't enough on a tethered connection. A hard cap is
// still nicer than a hung TUI.
const pullTimeout = 300 * time.Second

type pullSucceededMsg struct{}
type pullConflictMsg struct{ err error }
type pullFailedMsg struct{ err error }

// pullResolveStrategy and pullExec are package-level seams over git.* so
// tests can stub the subprocess calls. Mirrors the clipboardWrite pattern
// in model.go.
var (
	pullResolveStrategy = git.ResolvePullStrategy
	pullExec            = git.Pull
)

func pullCmd(dir, prefs string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		defer cancel()
		conflict, err := runPullStep(ctx, dir, prefs)
		if err != nil {
			if conflict {
				return pullConflictMsg{err: err}
			}
			return pullFailedMsg{err: err}
		}
		return pullSucceededMsg{}
	}
}

// chainPullAfterAction consumes the pull-after flag armed by a remote-chip
// graph Enter. When armed (and no pull is already running) it flips the
// pull gate, paints "<outcome> · pulling…", and returns reload+pull in one
// batch so the checkout result renders while the network round-trip runs.
// ok=false means the caller should fall through to its plain reload.
func (m Model) chainPullAfterAction(outcome string) (Model, tea.Cmd, bool) {
	if !m.pullAfterAction {
		return m, nil, false
	}
	m.pullAfterAction = false
	if m.pullInFlight {
		return m, nil, false
	}
	m.pullInFlight = true
	m.setBusyStatus(outcome + " · pulling…")
	return m, tea.Batch(m.reloadCmd(), pullCmd(m.workdir, m.pullPrefStrategy)), true
}
