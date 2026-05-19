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

type checkoutSucceededMsg struct {
	ref      string
	detached bool
}

type checkoutFailedMsg struct{ err error }

type checkoutNeedsCleanTreeMsg struct {
	ref      string
	detached bool
}

// Package-level seams over git.* so tests can stub the subprocess calls
// without touching a real repo. Mirrors pullExec / pullResolveStrategy.
var (
	checkoutExec         = git.Checkout
	checkoutDetachedExec = git.CheckoutDetached
	mergeFFOnlyExec      = git.MergeFFOnly
	countAheadExec       = git.CountAhead
)

// ffSucceededMsg fires when ffOnlyCmd's `git merge --ff-only` completed
// without a checkout step (Case 1: HEAD already on the branch we're
// advancing). branch is HEAD's local-branch name; advance is the +N commit
// count between the pre-FF tip and the post-FF tip, computed before the
// merge runs.
type ffSucceededMsg struct {
	branch  string
	advance int
}

// ffFailedMsg fires when ffOnlyCmd hit a non-recoverable error — divergent
// histories (ErrFFNotPossible), lock contention, missing ref, or a generic
// merge failure. Dirty-tree refusal is split out into ffNeedsCleanTreeMsg.
type ffFailedMsg struct{ err error }

// ffNeedsCleanTreeMsg fires when ffOnlyCmd's merge was refused because the
// working tree had uncommitted changes git would have to clobber. The
// model reuses viewModeCheckoutConfirm with pendingCheckout.withFF=true so
// the user gets the same abort modal as a dirty-tree checkout.
type ffNeedsCleanTreeMsg struct {
	branch string
	hash   string
}

// checkoutThenFFSucceededMsg fires when the cross-branch chain (checkout
// local + FF to cursor) completed without dirty-tree refusal. branch is
// the local that was checked out; advance is the +N commit count between
// the post-checkout tip and the FF target.
type checkoutThenFFSucceededMsg struct {
	branch  string
	advance int
}

// ffCheckoutNeedsCleanTreeMsg fires when checkoutThenFFCmd's checkout
// step was refused because the working tree had uncommitted changes
// git would have to clobber. Distinguished from ffNeedsCleanTreeMsg so
// the modal hint can name the cross-branch chain it's aborting.
type ffCheckoutNeedsCleanTreeMsg struct {
	branch string
	hash   string
}

// runFF stamps the +N advance count then invokes `git merge --ff-only`.
// The advance is computed before the merge so even an FF rejection can
// carry the count (callers ignore advance on the error path, but keeping
// the call consistent matches the success-path msg layout). A rev-list
// error is non-fatal: fall back to 0 and let the merge decide.
func runFF(ctx context.Context, dir, hash string) (advance int, err error) {
	advance, _ = countAheadExec(ctx, dir, "HEAD", hash)
	err = mergeFFOnlyExec(ctx, dir, hash)
	return
}

// ffOnlyCmd runs the no-checkout FF (Case 1: HEAD already on the branch
// being advanced). Failure wrapping: ErrCheckoutNeedsCleanTree →
// ffNeedsCleanTreeMsg (modal entry); anything else (incl. ErrFFNotPossible
// on divergence) → ffFailedMsg so the status line surfaces the reason.
// The modal is reserved for dirty-tree only.
func ffOnlyCmd(dir, branch, hash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		advance, err := runFF(ctx, dir, hash)
		if err != nil {
			if errors.Is(err, git.ErrCheckoutNeedsCleanTree) {
				return ffNeedsCleanTreeMsg{branch: branch, hash: hash}
			}
			return ffFailedMsg{err: err}
		}
		return ffSucceededMsg{branch: branch, advance: advance}
	}
}

// checkoutThenFFCmd runs the cross-branch chain: checkout local `branch`,
// then FF that branch up to `hash`. Used for the Fork "Checkout & Fast
// Forward" path the graph evaluator emits when the cursor row carries a
// remote chip whose upstream-tracking local isn't HEAD.
//
// Failure routing:
//   - checkout dirty → ffCheckoutNeedsCleanTreeMsg (modal entry, withCheckoutFF=true)
//   - checkout other err → ffFailedMsg (chain never advanced past checkout)
//   - FF err → ffFailedMsg (we're now on the new branch but it didn't advance)
//   - success → checkoutThenFFSucceededMsg
func checkoutThenFFCmd(dir, branch, hash string) tea.Cmd {
	return func() tea.Msg {
		coCtx, coCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		coErr := checkoutExec(coCtx, dir, branch)
		coCancel()
		if coErr != nil {
			if errors.Is(coErr, git.ErrCheckoutNeedsCleanTree) {
				return ffCheckoutNeedsCleanTreeMsg{branch: branch, hash: hash}
			}
			return ffFailedMsg{err: coErr}
		}
		ffCtx, ffCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer ffCancel()
		advance, err := runFF(ffCtx, dir, hash)
		if err != nil {
			return ffFailedMsg{err: err}
		}
		return checkoutThenFFSucceededMsg{branch: branch, advance: advance}
	}
}

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

func runCheckoutExec(ctx context.Context, dir, ref string, detached bool) error {
	if detached {
		return checkoutDetachedExec(ctx, dir, ref)
	}
	return checkoutExec(ctx, dir, ref)
}

// runPullStep performs the strategy-resolve + pull-exec ladder used by
// pullCmd. Returns (conflict=true, err) when the failure was an
// ErrPullConflict, (false, err) on a generic failure, (false, nil) on
// success.
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
