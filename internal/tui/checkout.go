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
// the user gets the same `s` (stash) / `a` (abort) modal as a dirty-tree
// checkout.
type ffNeedsCleanTreeMsg struct {
	branch string
	hash   string
}

// stashThenFFMsg fires when the dirty-tree `s` branch chained stash → FF
// to completion. stashLabel mirrors the post-checkout pattern — git keeps
// the entry at stash@{0} so the user can pop it on their own time.
type stashThenFFMsg struct {
	branch     string
	advance    int
	stashLabel string
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
// the modal's `s` branch knows to chain stash → checkout → FF instead
// of stash → FF (no checkout step).
type ffCheckoutNeedsCleanTreeMsg struct {
	branch string
	hash   string
}

// stashThenCheckoutThenFFMsg fires when the dirty-tree `s` branch chained
// stash → checkout → FF for the cross-branch case. Mirrors stashThenFFMsg
// in shape; the status text shows "stashed before checkout+fast-forward"
// so the user knows both steps ran post-stash.
type stashThenCheckoutThenFFMsg struct {
	branch     string
	advance    int
	stashLabel string
}

// runFF stamps the +N advance count then invokes `git merge --ff-only`.
// Shared body between ffOnlyCmd and stashThenFFCmd — the advance is
// computed before the merge so even an FF rejection can carry the count
// (callers ignore advance on the error path, but keeping the call
// consistent matches the success-path msg layout). A rev-list error is
// non-fatal: fall back to 0 and let the merge decide.
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

// stashThenFFCmd runs the dirty-tree `s` branch of an FF: stash → FF.
// Mirrors stashThenCheckoutCmd's policy — no automatic pop. The stash
// stays at stash@{0} for the user to handle on their own time, which
// matches how the existing checkout chain treats post-stash entries.
func stashThenFFCmd(dir, branch, hash string) tea.Cmd {
	return func() tea.Msg {
		stCtx, stCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		stashErr := stashExec(stCtx, dir, "gh-orbit: before fast-forward "+branch)
		stCancel()
		if stashErr != nil {
			return ffFailedMsg{err: stashErr}
		}

		ffCtx, ffCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer ffCancel()
		advance, err := runFF(ffCtx, dir, hash)
		if err != nil {
			return ffFailedMsg{err: err}
		}
		return stashThenFFMsg{branch: branch, advance: advance, stashLabel: stashLabelHEAD}
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

// stashThenCheckoutThenFFCmd runs the dirty-tree `s` branch of the
// cross-branch chain: stash → checkout → FF. Same no-auto-pop policy
// as stashThenCheckoutCmd / stashThenFFCmd — the stash stays at
// stash@{0} for the user to handle.
func stashThenCheckoutThenFFCmd(dir, branch, hash string) tea.Cmd {
	return func() tea.Msg {
		stCtx, stCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		stashErr := stashExec(stCtx, dir, "gh-orbit: before checkout+fast-forward "+branch)
		stCancel()
		if stashErr != nil {
			return ffFailedMsg{err: stashErr}
		}
		coCtx, coCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		coErr := checkoutExec(coCtx, dir, branch)
		coCancel()
		if coErr != nil {
			return ffFailedMsg{err: coErr}
		}
		ffCtx, ffCancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer ffCancel()
		advance, err := runFF(ffCtx, dir, hash)
		if err != nil {
			return ffFailedMsg{err: err}
		}
		return stashThenCheckoutThenFFMsg{branch: branch, advance: advance, stashLabel: stashLabelHEAD}
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
		popErr := stashPopExec(popCtx, dir, "")
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
