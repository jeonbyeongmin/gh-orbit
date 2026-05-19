// Async cmds + msg shapes for the zombie-branch cleanup flow. Mirrors the
// refsaction.go seam pattern: every git wrapper is reachable through a
// package-level var so tests can stub the subprocess calls.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const zombieCmdTimeout = 30 * time.Second

// Package-level seams over git.* — tests stub these so the cleanup flow
// stays hermetic.
var (
	zombieDetectExec   = git.DetectZombieBranches
	zombieBaselineExec = git.ResolveDefaultBranch
)

// zombieDetectRequestedMsg fires when refs.go observes `Z` on the refs
// pane and dispatches a background detect run. Kept as a distinct seam
// (rather than calling git directly from refs.Update) so the cleanup flow
// stays consistent with the refsaction.go / local_changes_cmd.go pattern.
type zombieDetectRequestedMsg struct{}

// zombieDetectedMsg carries the detection result back to the root model.
// branches is empty when nothing qualifies; the root model surfaces a
// "no zombies" status line instead of opening the confirm modal.
type zombieDetectedMsg struct {
	baseline string
	branches []git.ZombieBranch
}

// zombieDetectFailedMsg fires when the detect dispatch itself errored
// (subprocess failure, malformed for-each-ref output, etc.). Failure
// surfaces to the status line; no modal is opened.
type zombieDetectFailedMsg struct {
	err error
}

// zombieDeletedMsg carries the bulk-delete result. deleted lists branches
// that `git branch -d` accepted; failed pairs the rest with the per-branch
// error so the post-delete summary can name what remains and why.
type zombieDeletedMsg struct {
	deleted []string
	failed  []zombieDeleteFailure
}

type zombieDeleteFailure struct {
	name string
	err  error
}

// detectZombieBranchesCmd resolves the baseline and walks the 3-condition
// guard. Baseline resolution always succeeds (the wrapper falls back to
// "develop") so detect errors only come from the git invocation.
func detectZombieBranchesCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), zombieCmdTimeout)
		defer cancel()
		baseline := zombieBaselineExec(ctx, dir)
		branches, err := zombieDetectExec(ctx, dir, baseline)
		if err != nil {
			return zombieDetectFailedMsg{err: err}
		}
		return zombieDetectedMsg{baseline: baseline, branches: branches}
	}
}

// deleteZombieBranchesCmd runs `git branch -d <name>` for every branch
// the user accepted. Errors are collected per-branch — a single rejection
// (e.g. "not fully merged" due to a race against a non-merged commit
// landing between detect and confirm) doesn't abort the rest. force=false
// so any branch that no longer passes git's own safety check is rejected
// rather than silently nuked.
func deleteZombieBranchesCmd(dir string, branches []git.ZombieBranch) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), zombieCmdTimeout)
		defer cancel()
		var (
			deleted []string
			failed  []zombieDeleteFailure
		)
		for _, b := range branches {
			if err := branchDeleteExec(ctx, dir, b.Name, false); err != nil {
				failed = append(failed, zombieDeleteFailure{name: b.Name, err: err})
				continue
			}
			deleted = append(deleted, b.Name)
		}
		return zombieDeletedMsg{deleted: deleted, failed: failed}
	}
}
