// Stash write actions: package-level seams + cmds + msg shapes that drive
// the refs-pane `d` (drop) and graph-Enter (pop / apply) modals. The
// dirty-tree checkout chain keeps using git.Stash / git.StashPop in
// checkout.go; this file exists for user-explicit stash actions where the
// caller picks a specific stash@{N} label.
package tui

import (
	"context"
	"errors"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// stashActionTimeout matches checkoutTimeout shape — a stash pop/apply on a
// large working tree shouldn't outlive a user's patience and the modal's
// `…ing` status bar.
const stashActionTimeout = 30 * time.Second

// Package-level seams so tests can swap in stubs. Production defaults wire
// straight through to the git wrappers.
var (
	stashApplyExec = git.StashApply
	stashDropExec  = git.StashDrop
	stashPopAtExec = git.StashPopAt
)

// stashPopSucceededMsg / ConflictMsg / FailedMsg classify the outcome of a
// user-explicit `git stash pop <label>`. Conflict carries the original err
// so the status bar can include git's first-line message; the model
// branches on `errors.Is(err, git.ErrStashPopConflict)` to pick the conflict
// vs. failed handler. The stash entry is preserved on conflict (git's
// default), which the user-facing message advertises.
type stashPopSucceededMsg struct{ label string }
type stashPopConflictMsg struct {
	label string
	err   error
}
type stashPopFailedMsg struct {
	label string
	err   error
}

// stashApplySucceededMsg / ConflictMsg / FailedMsg mirror the pop messages.
// apply never drops, so the conflict and success variants both leave the
// entry in place — the status text reflects that.
type stashApplySucceededMsg struct{ label string }
type stashApplyConflictMsg struct {
	label string
	err   error
}
type stashApplyFailedMsg struct {
	label string
	err   error
}

// stashDropSucceededMsg / FailedMsg classify `git stash drop <label>`.
// Drop has no conflict mode — it either removes the slot or errors with
// "is not a valid reference" / lock failures.
type stashDropSucceededMsg struct{ label string }
type stashDropFailedMsg struct {
	label string
	err   error
}

// stashPopCmd dispatches the named-label pop. Errors are partitioned at
// the boundary so the Update handler can stay branch-light: conflict →
// ConflictMsg, anything else → FailedMsg.
func stashPopCmd(dir, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), stashActionTimeout)
		defer cancel()
		err := stashPopAtExec(ctx, dir, label)
		if err == nil {
			return stashPopSucceededMsg{label: label}
		}
		if errors.Is(err, git.ErrStashPopConflict) {
			return stashPopConflictMsg{label: label, err: err}
		}
		return stashPopFailedMsg{label: label, err: err}
	}
}

func stashApplyCmd(dir, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), stashActionTimeout)
		defer cancel()
		err := stashApplyExec(ctx, dir, label)
		if err == nil {
			return stashApplySucceededMsg{label: label}
		}
		if errors.Is(err, git.ErrStashApplyConflict) {
			return stashApplyConflictMsg{label: label, err: err}
		}
		return stashApplyFailedMsg{label: label, err: err}
	}
}

func stashDropCmd(dir, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), stashActionTimeout)
		defer cancel()
		err := stashDropExec(ctx, dir, label)
		if err != nil {
			return stashDropFailedMsg{label: label, err: err}
		}
		return stashDropSucceededMsg{label: label}
	}
}

// diffStashRefs derives the stash hash slice + (hash→label) map from the
// refs panel's stash section, and reports whether it differs from prev.
// Diffs trigger a reloadCmd so the graph picks up new stash tips; an
// unchanged set is a no-op so refsLoadedMsg doesn't spin in a reload loop.
func diffStashRefs(stashes []git.Ref, prev []string) (hashes []string, byHash map[string]string, changed bool) {
	hashes = make([]string, 0, len(stashes))
	byHash = make(map[string]string, len(stashes))
	for _, s := range stashes {
		if s.ObjectName == "" {
			continue
		}
		hashes = append(hashes, s.ObjectName)
		byHash[s.ObjectName] = s.ShortName
	}
	if len(hashes) == 0 {
		hashes = nil
	}
	changed = !slices.Equal(hashes, prev)
	return hashes, byHash, changed
}