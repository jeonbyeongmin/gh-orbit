// Async cmds and msg shapes for the refs-pane `d` (delete branch) action.
// Mirrors checkout.go's seam pattern: every git wrapper is reachable through
// a package-level var so tests can stub the subprocess calls; the cmd emits
// a typed msg the root model branches on.
package tui

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refDeleteState backs the delete-confirm dialog. localName is the
// resolved branch name dispatched to BranchDelete; notMerged flags the
// post-`-d`-rejection state so the dialog can explain the [Y] force
// retry.
type refDeleteState struct {
	localName string
	notMerged bool
}

// Package-level seam over git.BranchDelete so tests can stub the subprocess
// call. Mirrors the checkoutExec pattern in checkout.go.
var branchDeleteExec = git.BranchDelete

// branchDeleteSucceededMsg fires when `git branch -d/-D <name>` returned
// without error. localName drives the post-reload cursor jump (next ref
// in the same section, or previous when last).
type branchDeleteSucceededMsg struct {
	localName string
	forced    bool
}

// branchDeleteFailedMsg fires for generic delete failures (missing ref,
// lock contention, etc.). The local branch is still present.
type branchDeleteFailedMsg struct {
	err       error
	localName string
}

// branchDeleteNotMergedMsg fires when `git branch -d` was rejected because
// the branch isn't fully merged into HEAD or its upstream. The model
// surfaces the "press [Y] to force" hint and leaves the confirm dialog
// re-armed; pressing `Y` retries with -D.
type branchDeleteNotMergedMsg struct {
	localName string
}

// branchDeleteCmd runs `git branch -d/-D <localName>` and routes the result
// onto the typed msg vocabulary above. force=true picks -D; false picks -d
// and routes ErrBranchNotFullyMerged into branchDeleteNotMergedMsg so the
// confirm dialog can ask the user to force.
func branchDeleteCmd(dir, localName string, force bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		err := branchDeleteExec(ctx, dir, localName, force)
		if err == nil {
			return branchDeleteSucceededMsg{localName: localName, forced: force}
		}
		if !force && errors.Is(err, git.ErrBranchNotFullyMerged) {
			return branchDeleteNotMergedMsg{localName: localName}
		}
		return branchDeleteFailedMsg{err: err, localName: localName}
	}
}
