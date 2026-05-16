// Stash write actions: package-level seams + cmds + msg shapes that drive
// the refs-pane `d` (drop) and graph-Enter (pop / apply) modals. The
// dirty-tree checkout chain shares stashPopExec with this file (label="").
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

// Package-level seams so tests can swap in stubs. stashPopExec is shared
// with checkout.go's dirty-tree chain (the chain passes label="" to pop
// stash@{0}; user-explicit actions pass the chosen slot).
var (
	stashApplyExec = git.StashApply
	stashDropExec  = git.StashDrop
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

// stashWriteExec is the signature shared by pop / apply / drop wrappers
// once `dir` is fixed — the closure used by stashWriteCmd just needs
// `(ctx, label)`.
type stashWriteExec func(ctx context.Context, label string) error

// stashWriteCmd is the shared dispatch shape for the three user-explicit
// stash actions. exec runs the git call; conflictSentinel routes a
// post-failure `errors.Is` check to the conflict msg; ok / conflict / fail
// build the typed msg variants. nil conflictSentinel disables the
// conflict branch (drop has no conflict mode).
func stashWriteCmd(label string, exec stashWriteExec, conflictSentinel error, ok, conflict, fail func(string, error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), stashActionTimeout)
		defer cancel()
		err := exec(ctx, label)
		if err == nil {
			return ok(label, nil)
		}
		if conflictSentinel != nil && errors.Is(err, conflictSentinel) {
			return conflict(label, err)
		}
		return fail(label, err)
	}
}

func stashPopCmd(dir, label string) tea.Cmd {
	return stashWriteCmd(label,
		func(ctx context.Context, l string) error { return stashPopExec(ctx, dir, l) },
		git.ErrStashPopConflict,
		func(l string, _ error) tea.Msg { return stashPopSucceededMsg{label: l} },
		func(l string, e error) tea.Msg { return stashPopConflictMsg{label: l, err: e} },
		func(l string, e error) tea.Msg { return stashPopFailedMsg{label: l, err: e} },
	)
}

func stashApplyCmd(dir, label string) tea.Cmd {
	return stashWriteCmd(label,
		func(ctx context.Context, l string) error { return stashApplyExec(ctx, dir, l) },
		git.ErrStashApplyConflict,
		func(l string, _ error) tea.Msg { return stashApplySucceededMsg{label: l} },
		func(l string, e error) tea.Msg { return stashApplyConflictMsg{label: l, err: e} },
		func(l string, e error) tea.Msg { return stashApplyFailedMsg{label: l, err: e} },
	)
}

func stashDropCmd(dir, label string) tea.Cmd {
	return stashWriteCmd(label,
		func(ctx context.Context, l string) error { return stashDropExec(ctx, dir, l) },
		nil,
		func(l string, _ error) tea.Msg { return stashDropSucceededMsg{label: l} },
		nil,
		func(l string, e error) tea.Msg { return stashDropFailedMsg{label: l, err: e} },
	)
}

// stashTipsFromRefs builds the (hash → label, hash → subject) maps the
// graph needs from the refs panel's stash section. Subjects come from the
// model's stored subject map keyed by hash, falling back to the label when
// missing (which happens before the first graph stream catches up).
//
// hashes preserves the StashList newest-first order so slices.Equal is a
// valid diff against the previously-dispatched set.
func stashTipsFromRefs(stashes []git.Ref) (hashes []string, byHash map[string]string) {
	if len(stashes) == 0 {
		return nil, nil
	}
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
		return nil, nil
	}
	return hashes, byHash
}

// diffStashRefs reports whether a fresh hash slice differs from the
// snapshot the running stream was started with. Empty + nil short-circuits
// before any allocation — the common case for repos without stashes.
func diffStashRefs(stashes []git.Ref, prev []string) (hashes []string, byHash map[string]string, changed bool) {
	if len(stashes) == 0 && len(prev) == 0 {
		return nil, nil, false
	}
	hashes, byHash = stashTipsFromRefs(stashes)
	return hashes, byHash, !slices.Equal(hashes, prev)
}
