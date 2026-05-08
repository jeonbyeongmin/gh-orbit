package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// checkoutTimeout is shorter than pull's 5min — checkout is local-only and
// shouldn't block on network. A long-running case usually means index.lock
// contention; bailing keeps the TUI responsive.
const checkoutTimeout = 60 * time.Second

// stashLabelHEAD is the conventional ref the newest stash entry occupies.
// We surface this string back to the user instead of resolving the actual
// ref — git re-indexes stashes on push, so in a single-user TUI the newest
// is always slot 0.
const stashLabelHEAD = "stash@{0}"

type checkoutSucceededMsg struct {
	ref      string
	detached bool
}

type checkoutFailedMsg struct{ err error }

type checkoutNeedsCleanTreeMsg struct {
	ref      string
	detached bool
}

type stashThenCheckoutMsg struct {
	ref        string
	detached   bool
	stashLabel string
}

// checkoutThenPullSucceededMsg fires when `p` (refs pane) ran the checkout
// → pull chain to completion. pullSkipped=true plus a non-empty skipReason
// covers the tag / detached / no-upstream cases where pull was deliberately
// not invoked. The model uses (ref, detached) verbatim for the success
// status and arms a HEAD jump on the post-reload stream.
type checkoutThenPullSucceededMsg struct {
	ref         string
	detached    bool
	pullSkipped bool
	skipReason  string
}

// checkoutThenPullConflictMsg fires when checkout succeeded but pull came
// back as ErrPullConflict. The user is mid-merge — model surfaces the
// "resolve in your terminal" hint and refrains from arming a HEAD jump.
type checkoutThenPullConflictMsg struct {
	ref      string
	detached bool
	err      error
}

// stashThenCheckoutThenPullThenPopSucceededMsg fires when the dirty-tree
// `s` branch chained stash → checkout → (pull|skip) → stash pop without
// any conflict. stashLabel always carries stashLabelHEAD so the status bar
// can name the entry; the user sees "stashed → … → popped <label>".
type stashThenCheckoutThenPullThenPopSucceededMsg struct {
	ref         string
	detached    bool
	stashLabel  string
	pullSkipped bool
	skipReason  string
}

// stashThenCheckoutThenPullThenPopConflictMsg fires when the dirty-tree
// chain hit a non-success outcome after the checkout step. phase identifies
// which step failed:
//   - "pull"      — pull conflict OR generic pull failure (model branches on
//     errors.Is(err, ErrPullConflict)); stash is preserved and pop was
//     skipped on generic failure (the marker-laden tree would just collide).
//   - "stash-pop" — pop conflict; conflict markers are in the working tree
//     and the stash entry is preserved.
type stashThenCheckoutThenPullThenPopConflictMsg struct {
	ref        string
	detached   bool
	stashLabel string
	phase      string
	err        error
}

// Package-level seams over git.* so tests can stub the subprocess calls
// without touching a real repo. Mirrors pullExec / pullResolveStrategy.
var (
	checkoutExec         = git.Checkout
	checkoutDetachedExec = git.CheckoutDetached
	stashExec            = git.Stash
	stashPopExec         = git.StashPop
)

func checkoutCmd(dir, ref string, detached bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		err := runCheckoutExec(ctx, dir, ref, detached)
		if err == nil {
			return checkoutSucceededMsg{ref: ref, detached: detached}
		}
		if errors.Is(err, git.ErrCheckoutNeedsCleanTree) {
			return checkoutNeedsCleanTreeMsg{ref: ref, detached: detached}
		}
		return checkoutFailedMsg{err: err}
	}
}

func stashThenCheckoutCmd(dir, ref string, detached bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		if err := stashExec(ctx, dir, "gh-orbit: before checkout "+ref); err != nil {
			return checkoutFailedMsg{err: err}
		}
		if err := runCheckoutExec(ctx, dir, ref, detached); err != nil {
			return checkoutFailedMsg{err: err}
		}
		return stashThenCheckoutMsg{ref: ref, detached: detached, stashLabel: stashLabelHEAD}
	}
}

func runCheckoutExec(ctx context.Context, dir, ref string, detached bool) error {
	if detached {
		return checkoutDetachedExec(ctx, dir, ref)
	}
	return checkoutExec(ctx, dir, ref)
}

// checkoutThenPullCmd runs the clean-tree variant of `p`: checkout, then
// (unless skipPull) pull. Each step gets its own deadline so a slow pull
// can't starve checkout. The pull half mirrors pullCmd — strategy resolution
// happens before the subprocess so the prefs string is honored identically.
//
// Failures route to the existing checkoutNeedsCleanTreeMsg / checkoutFailedMsg
// for the checkout phase; the pull phase emits checkoutThenPullConflictMsg
// (conflict) or pullFailedMsg (generic). Success emits one
// checkoutThenPullSucceededMsg with the right pullSkipped / skipReason
// stamped on it.
func checkoutThenPullCmd(dir, ref string, detached bool, prefs string, skipPull bool, skipReason string) tea.Cmd {
	return func() tea.Msg {
		// Step 1: checkout (60s budget, independent of the pull deadline).
		coCtx, coCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		coErr := runCheckoutExec(coCtx, dir, ref, detached)
		coCancel()
		if coErr != nil {
			if errors.Is(coErr, git.ErrCheckoutNeedsCleanTree) {
				return checkoutNeedsCleanTreeMsg{ref: ref, detached: detached}
			}
			return checkoutFailedMsg{err: coErr}
		}

		if skipPull {
			return checkoutThenPullSucceededMsg{
				ref:         ref,
				detached:    detached,
				pullSkipped: true,
				skipReason:  skipReason,
			}
		}

		// Step 2: pull. Strategy resolution shares the same context budget
		// as the pull itself — they're both network-bound and the user
		// asked for one logical operation.
		puCtx, puCancel := context.WithTimeout(context.Background(), pullTimeout)
		defer puCancel()
		strategy, err := pullResolveStrategy(puCtx, dir, prefs)
		if err != nil {
			return pullFailedMsg{err: err}
		}
		if err := pullExec(puCtx, dir, strategy); err != nil {
			if errors.Is(err, git.ErrPullConflict) {
				return checkoutThenPullConflictMsg{ref: ref, detached: detached, err: err}
			}
			return pullFailedMsg{err: err}
		}
		return checkoutThenPullSucceededMsg{ref: ref, detached: detached}
	}
}

// stashThenCheckoutThenPullThenPopCmd runs the dirty-tree `s` branch of
// `p`: stash → checkout → (pull|skip) → stash pop. The chain ordering is
// not negotiable — pull must happen before pop so the user's local edits
// always land on top of the freshly-pulled HEAD instead of a stale tip.
//
// Failure routing:
//   - stash failure: checkoutFailedMsg (chain never started — keep parity
//     with stashThenCheckoutCmd's existing surface).
//   - checkout failure: checkoutFailedMsg. Dirty-tree wrapping is impossible
//     here since stash already cleared the tree; this path catches missing
//     refs, index lock contention, etc.
//   - pull conflict: still attempt stash pop (interview decision — pop is
//     "the last step of the pull chain"). pop result decides the final msg:
//     pop ok → ConflictMsg{phase:"pull"}; pop conflict → ConflictMsg{phase:"stash-pop"}.
//   - pull generic failure: stash preserved, pop NOT attempted (popping onto
//     a half-failed pull just compounds confusion). Emits ConflictMsg{phase:"pull"}.
//   - pop conflict (after successful pull): ConflictMsg{phase:"stash-pop"}.
func stashThenCheckoutThenPullThenPopCmd(dir, ref string, detached bool, prefs string, skipPull bool, skipReason string) tea.Cmd {
	return func() tea.Msg {
		stCtx, stCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		if err := stashExec(stCtx, dir, "gh-orbit: before checkout "+ref); err != nil {
			stCancel()
			return checkoutFailedMsg{err: err}
		}
		stCancel()

		coCtx, coCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		if err := runCheckoutExec(coCtx, dir, ref, detached); err != nil {
			coCancel()
			return checkoutFailedMsg{err: err}
		}
		coCancel()

		// Pull phase. skipPull short-circuits straight to pop.
		var pullErr error
		if !skipPull {
			puCtx, puCancel := context.WithTimeout(context.Background(), pullTimeout)
			strategy, sErr := pullResolveStrategy(puCtx, dir, prefs)
			if sErr != nil {
				pullErr = sErr
			} else if eErr := pullExec(puCtx, dir, strategy); eErr != nil {
				pullErr = eErr
			}
			puCancel()
		}

		if pullErr != nil && !errors.Is(pullErr, git.ErrPullConflict) {
			// Generic pull failure: stash preserved, pop NOT attempted.
			return stashThenCheckoutThenPullThenPopConflictMsg{
				ref:        ref,
				detached:   detached,
				stashLabel: stashLabelHEAD,
				phase:      "pull",
				err:        pullErr,
			}
		}

		// Pop phase — runs whether pull succeeded or pull-conflicted.
		popCtx, popCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		popErr := stashPopExec(popCtx, dir)
		popCancel()

		if popErr != nil {
			return stashThenCheckoutThenPullThenPopConflictMsg{
				ref:        ref,
				detached:   detached,
				stashLabel: stashLabelHEAD,
				phase:      "stash-pop",
				err:        popErr,
			}
		}
		if pullErr != nil {
			// Pop succeeded, but pull was a conflict — surface that.
			return stashThenCheckoutThenPullThenPopConflictMsg{
				ref:        ref,
				detached:   detached,
				stashLabel: stashLabelHEAD,
				phase:      "pull",
				err:        pullErr,
			}
		}
		return stashThenCheckoutThenPullThenPopSucceededMsg{
			ref:         ref,
			detached:    detached,
			stashLabel:  stashLabelHEAD,
			pullSkipped: skipPull,
			skipReason:  skipReason,
		}
	}
}
