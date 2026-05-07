package tui

import (
	"context"
	"errors"
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
		strategy, err := pullResolveStrategy(ctx, dir, prefs)
		if err != nil {
			return pullFailedMsg{err: err}
		}
		if err := pullExec(ctx, dir, strategy); err != nil {
			if errors.Is(err, git.ErrPullConflict) {
				return pullConflictMsg{err: err}
			}
			return pullFailedMsg{err: err}
		}
		return pullSucceededMsg{}
	}
}
