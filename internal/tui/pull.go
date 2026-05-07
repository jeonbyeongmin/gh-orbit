// Background `git pull` dispatch. Mirrors fetch.go's tea.Cmd → tea.Msg shape
// but takes longer to wait (rebase replay can dwarf a plain fetch) and forks
// the failure path into a CONFLICT branch so the TUI can surface a "resolve
// in your terminal" hint instead of a bare error string.
package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// pullTimeout is generous because a rebase can replay many commits over slow
// HTTPS. Fetch's 120s isn't enough for a 50k-commit repo on a tethered
// connection, but a hard cap is still nicer than a hung TUI.
const pullTimeout = 300 * time.Second

type pullSucceededMsg struct{}
type pullConflictMsg struct{ err error }
type pullFailedMsg struct{ err error }

// pullCmdFactory builds the actual tea.Cmd. Tests overwrite it with a stub
// to assert the prefs string flows from the Model through to the cmd —
// the real Resolve+Pull pair shells out to git, which we don't want to do
// from a unit test.
var pullCmdFactory = defaultPullCmd

func defaultPullCmd(dir string, prefs string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		defer cancel()
		strategy, err := git.ResolvePullStrategy(ctx, dir, prefs)
		if err != nil {
			return pullFailedMsg{err: err}
		}
		if _, err := git.Pull(ctx, dir, strategy); err != nil {
			if errors.Is(err, git.ErrPullConflict) {
				return pullConflictMsg{err: err}
			}
			return pullFailedMsg{err: err}
		}
		return pullSucceededMsg{}
	}
}
