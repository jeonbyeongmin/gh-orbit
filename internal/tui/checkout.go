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

// chainPhase identifies which step of the dirty-tree chain triggered the
// terminal failure msg. Constants below are the only legal values; the
// model's switch matches them exactly. A typed string keeps the value
// out of user-facing text while still printing readably in logs.
type chainPhase string

const (
	// chainPhasePull covers BOTH pull conflict and generic pull failure;
	// the model branches further on errors.Is(err, ErrPullConflict).
	// On generic failure, stash is preserved and pop was deliberately
	// skipped (the marker-laden tree would just collide).
	chainPhasePull chainPhase = "pull"
	// chainPhaseStashPop fires only after a successful pull whose pop
	// then collided with the popped changes. Conflict markers are in
	// the working tree and the stash entry is preserved.
	chainPhaseStashPop chainPhase = "stash-pop"
)

// stashThenCheckoutThenPullThenPopConflictMsg fires when the dirty-tree
// chain hit a non-success outcome after the checkout step.
type stashThenCheckoutThenPullThenPopConflictMsg struct {
	ref        string
	detached   bool
	stashLabel string
	phase      chainPhase
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

// runPullStep performs the strategy-resolve + pull-exec ladder shared by
// pullCmd and the chain commands. Returns (conflict=true, err) when the
// failure was an ErrPullConflict, (false, err) on a generic failure,
// (false, nil) on success. Pulling out this helper kills three copies of
// the same five-line ladder; each caller owns its own msg shape.
func runPullStep(ctx context.Context, dir, prefs string) (conflict bool, err error) {
	strategy, err := pullResolveStrategy(ctx, dir, prefs)
	if err != nil {
		return false, err
	}
	if err := pullExec(ctx, dir, strategy); err != nil {
		return errors.Is(err, git.ErrPullConflict), err
	}
	return false, nil
}

// checkoutThenPullCmd runs the clean-tree variant of `p`: checkout, then
// (unless skipReason names a reason to skip) pull. Each step gets its own
// deadline so a slow pull can't starve checkout. A non-empty skipReason
// is the single source of truth for "pull will not run" — the caller
// (resolvePullEligibility) stamps it for tags / detached / no-upstream
// locals; an empty skipReason means pull will run.
//
// Failures route to the existing checkoutNeedsCleanTreeMsg / checkoutFailedMsg
// for the checkout phase; the pull phase emits checkoutThenPullConflictMsg
// (conflict) or pullFailedMsg (generic). Success emits one
// checkoutThenPullSucceededMsg with pullSkipped/skipReason stamped on it.
func checkoutThenPullCmd(dir, ref string, detached bool, prefs, skipReason string) tea.Cmd {
	return func() tea.Msg {
		coCtx, coCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		coErr := runCheckoutExec(coCtx, dir, ref, detached)
		coCancel()
		if coErr != nil {
			if errors.Is(coErr, git.ErrCheckoutNeedsCleanTree) {
				return checkoutNeedsCleanTreeMsg{ref: ref, detached: detached}
			}
			return checkoutFailedMsg{err: coErr}
		}

		if skipReason != "" {
			return checkoutThenPullSucceededMsg{
				ref:         ref,
				detached:    detached,
				pullSkipped: true,
				skipReason:  skipReason,
			}
		}

		puCtx, puCancel := context.WithTimeout(context.Background(), pullTimeout)
		defer puCancel()
		conflict, err := runPullStep(puCtx, dir, prefs)
		if err != nil {
			if conflict {
				return checkoutThenPullConflictMsg{ref: ref, detached: detached, err: err}
			}
			return pullFailedMsg{err: err}
		}
		return checkoutThenPullSucceededMsg{ref: ref, detached: detached}
	}
}

// stashThenCheckoutThenPullThenPopCmd runs the dirty-tree `s` branch of
// `p`: stash → checkout → (pull|skip) → stash pop. Pull must happen before
// pop so the user's edits land on top of the freshly-pulled HEAD instead
// of a stale tip — this ordering is not negotiable.
//
// Failure routing:
//   - stash failure: checkoutFailedMsg (chain never started).
//   - checkout failure: checkoutFailedMsg (dirty-tree wrapping is impossible
//     after stash; this catches missing refs, lock contention, etc.).
//   - pull conflict: still attempt pop (interview decision — pop is "the
//     last step of the pull chain"). Pop result decides the final msg:
//     pop ok → ConflictMsg{phase: chainPhasePull},
//     pop conflict → ConflictMsg{phase: chainPhaseStashPop}.
//   - pull generic failure: stash preserved, pop NOT attempted; emits
//     ConflictMsg{phase: chainPhasePull}.
//   - pop conflict after successful pull: ConflictMsg{phase: chainPhaseStashPop}.
func stashThenCheckoutThenPullThenPopCmd(dir, ref string, detached bool, prefs, skipReason string) tea.Cmd {
	return func() tea.Msg {
		stCtx, stCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		stashErr := stashExec(stCtx, dir, "gh-orbit: before checkout "+ref)
		stCancel()
		if stashErr != nil {
			return checkoutFailedMsg{err: stashErr}
		}

		coCtx, coCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		coErr := runCheckoutExec(coCtx, dir, ref, detached)
		coCancel()
		if coErr != nil {
			return checkoutFailedMsg{err: coErr}
		}

		var pullErr error
		if skipReason == "" {
			puCtx, puCancel := context.WithTimeout(context.Background(), pullTimeout)
			_, pullErr = runPullStep(puCtx, dir, prefs)
			puCancel()
		}

		if pullErr != nil && !errors.Is(pullErr, git.ErrPullConflict) {
			return stashThenCheckoutThenPullThenPopConflictMsg{
				ref:        ref,
				detached:   detached,
				stashLabel: stashLabelHEAD,
				phase:      chainPhasePull,
				err:        pullErr,
			}
		}

		popCtx, popCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		popErr := stashPopExec(popCtx, dir)
		popCancel()

		if popErr != nil {
			return stashThenCheckoutThenPullThenPopConflictMsg{
				ref:        ref,
				detached:   detached,
				stashLabel: stashLabelHEAD,
				phase:      chainPhaseStashPop,
				err:        popErr,
			}
		}
		if pullErr != nil {
			return stashThenCheckoutThenPullThenPopConflictMsg{
				ref:        ref,
				detached:   detached,
				stashLabel: stashLabelHEAD,
				phase:      chainPhasePull,
				err:        pullErr,
			}
		}
		return stashThenCheckoutThenPullThenPopSucceededMsg{
			ref:         ref,
			detached:    detached,
			stashLabel:  stashLabelHEAD,
			pullSkipped: skipReason != "",
			skipReason:  skipReason,
		}
	}
}
