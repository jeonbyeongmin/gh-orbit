package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func withCheckoutStubs(t *testing.T, c, cd func(context.Context, string, string) error, s func(context.Context, string, string) error) {
	t.Helper()
	prevC, prevCD, prevS := checkoutExec, checkoutDetachedExec, stashExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
		stashExec = prevS
	})
	if c != nil {
		checkoutExec = c
	}
	if cd != nil {
		checkoutDetachedExec = cd
	}
	if s != nil {
		stashExec = s
	}
}

// chainStubs swaps in stubs for every seam the p-key chains touch:
// checkout, detached checkout, stash, pull strategy resolve, pull exec,
// and stash pop. Returning nil for any field leaves that seam at its
// default. Cleanup restores all six on test exit.
type chainStubs struct {
	checkout         func(context.Context, string, string) error
	checkoutDetached func(context.Context, string, string) error
	stash            func(context.Context, string, string) error
	stashPop         func(context.Context, string) error
	pullResolve      func(context.Context, string, string) (git.PullStrategy, error)
	pull             func(context.Context, string, git.PullStrategy) error
}

func withChainStubs(t *testing.T, s chainStubs) {
	t.Helper()
	prevC, prevCD, prevS, prevSP := checkoutExec, checkoutDetachedExec, stashExec, stashPopExec
	prevPR, prevPE := pullResolveStrategy, pullExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
		stashExec = prevS
		stashPopExec = prevSP
		pullResolveStrategy = prevPR
		pullExec = prevPE
	})
	if s.checkout != nil {
		checkoutExec = s.checkout
	}
	if s.checkoutDetached != nil {
		checkoutDetachedExec = s.checkoutDetached
	}
	if s.stash != nil {
		stashExec = s.stash
	}
	if s.stashPop != nil {
		stashPopExec = s.stashPop
	}
	if s.pullResolve != nil {
		pullResolveStrategy = s.pullResolve
	}
	if s.pull != nil {
		pullExec = s.pull
	}
}

func TestCheckoutCmdSuccess(t *testing.T) {
	var seenRef string
	withCheckoutStubs(t, func(_ context.Context, _, ref string) error {
		seenRef = ref
		return nil
	}, nil, nil)

	msg := checkoutCmd("", "feat", false)()
	if seenRef != "feat" {
		t.Errorf("checkoutExec got ref=%q, want feat", seenRef)
	}
	got, ok := msg.(checkoutSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutSucceededMsg", msg)
	}
	if got.ref != "feat" || got.detached {
		t.Errorf("got = %+v, want {feat false}", got)
	}
}

func TestCheckoutCmdDetachedRoutesToDetachedExec(t *testing.T) {
	var calledRegular, calledDetached bool
	withCheckoutStubs(t,
		func(context.Context, string, string) error { calledRegular = true; return nil },
		func(context.Context, string, string) error { calledDetached = true; return nil },
		nil,
	)

	msg := checkoutCmd("", "abc1234", true)()
	if calledRegular {
		t.Error("regular Checkout should not be called for detached=true")
	}
	if !calledDetached {
		t.Error("CheckoutDetached should be called for detached=true")
	}
	got, ok := msg.(checkoutSucceededMsg)
	if !ok || !got.detached {
		t.Errorf("msg = %#v, want checkoutSucceededMsg{detached:true}", msg)
	}
}

func TestCheckoutCmdNeedsCleanTreeWraps(t *testing.T) {
	withCheckoutStubs(t, func(context.Context, string, string) error {
		return git.ErrCheckoutNeedsCleanTree
	}, nil, nil)

	msg := checkoutCmd("", "feat", false)()
	got, ok := msg.(checkoutNeedsCleanTreeMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutNeedsCleanTreeMsg", msg)
	}
	if got.ref != "feat" || got.detached {
		t.Errorf("got = %+v, want {feat false}", got)
	}
}

func TestCheckoutCmdGenericFailureSurfaces(t *testing.T) {
	boom := errors.New("network down")
	withCheckoutStubs(t, func(context.Context, string, string) error { return boom }, nil, nil)

	msg := checkoutCmd("", "feat", false)()
	got, ok := msg.(checkoutFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}

func TestStashThenCheckoutCmdSuccess(t *testing.T) {
	var stashCalled, checkoutCalled bool
	var stashMsg string
	withCheckoutStubs(t,
		func(_ context.Context, _, ref string) error {
			checkoutCalled = true
			if !stashCalled {
				t.Error("checkout ran before stash")
			}
			if ref != "feat" {
				t.Errorf("checkout ref=%q, want feat", ref)
			}
			return nil
		},
		nil,
		func(_ context.Context, _, msg string) error {
			stashCalled = true
			stashMsg = msg
			return nil
		},
	)

	msg := stashThenCheckoutCmd("", "feat", false)()
	if !stashCalled || !checkoutCalled {
		t.Errorf("stash=%v checkout=%v, want both true", stashCalled, checkoutCalled)
	}
	if stashMsg == "" {
		t.Error("stash message should be non-empty for traceability")
	}
	got, ok := msg.(stashThenCheckoutMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenCheckoutMsg", msg)
	}
	if got.stashLabel != stashLabelHEAD {
		t.Errorf("stashLabel = %q, want %q", got.stashLabel, stashLabelHEAD)
	}
}

func TestStashThenCheckoutCmdStashFailureBlocksCheckout(t *testing.T) {
	boom := errors.New("stash refused")
	var checkoutCalled bool
	withCheckoutStubs(t,
		func(context.Context, string, string) error { checkoutCalled = true; return nil },
		nil,
		func(context.Context, string, string) error { return boom },
	)

	msg := stashThenCheckoutCmd("", "feat", false)()
	if checkoutCalled {
		t.Error("checkout should not run if stash failed")
	}
	got, ok := msg.(checkoutFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}

func TestStashThenCheckoutCmdCheckoutFailureSurfacesError(t *testing.T) {
	boom := errors.New("ref vanished")
	withCheckoutStubs(t,
		func(context.Context, string, string) error { return boom },
		nil,
		func(context.Context, string, string) error { return nil },
	)

	msg := stashThenCheckoutCmd("", "feat", false)()
	got, ok := msg.(checkoutFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}

// noopPullResolveFFOnly is a chainStubs.pullResolve helper that always
// hands back PullStrategyFFOnly, the safest default. Most chain tests
// don't care which strategy was picked — they just need the resolve step
// to not error.
func noopPullResolveFFOnly(_ context.Context, _, _ string) (git.PullStrategy, error) {
	return git.PullStrategyFFOnly, nil
}

func TestCheckoutThenPullCmdSuccess(t *testing.T) {
	var coRan, puRan bool
	var puStrategy git.PullStrategy
	withChainStubs(t, chainStubs{
		checkout: func(_ context.Context, _, ref string) error {
			coRan = true
			if ref != "feat" {
				t.Errorf("checkout ref = %q, want feat", ref)
			}
			return nil
		},
		pullResolve: func(_ context.Context, _, prefs string) (git.PullStrategy, error) {
			if prefs != "rebase" {
				t.Errorf("pullResolve prefs = %q, want rebase", prefs)
			}
			return git.PullStrategyRebase, nil
		},
		pull: func(_ context.Context, _ string, strat git.PullStrategy) error {
			puRan = true
			puStrategy = strat
			if !coRan {
				t.Error("pull ran before checkout")
			}
			return nil
		},
	})

	msg := checkoutThenPullCmd("", "feat", false, "rebase", false, "")()
	if !coRan || !puRan {
		t.Errorf("co=%v pu=%v, want both true", coRan, puRan)
	}
	got, ok := msg.(checkoutThenPullSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutThenPullSucceededMsg", msg)
	}
	if got.ref != "feat" || got.detached || got.pullSkipped {
		t.Errorf("got = %+v, want {feat false false ''}", got)
	}
	if puStrategy != git.PullStrategyRebase {
		t.Errorf("pull strategy = %v, want rebase", puStrategy)
	}
}

func TestCheckoutThenPullCmdSkipsPullWhenIneligible(t *testing.T) {
	var coRan, puRan bool
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			coRan = true
			return nil
		},
		pull: func(context.Context, string, git.PullStrategy) error {
			puRan = true
			return nil
		},
	})

	msg := checkoutThenPullCmd("", "v1.0", false, "", true, "tag has no upstream")()
	if !coRan {
		t.Error("checkout should run even when pull is skipped")
	}
	if puRan {
		t.Error("pull should not run when skipPull=true")
	}
	got, ok := msg.(checkoutThenPullSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutThenPullSucceededMsg", msg)
	}
	if !got.pullSkipped || got.skipReason != "tag has no upstream" {
		t.Errorf("got = %+v, want pullSkipped=true skipReason='tag has no upstream'", got)
	}
}

func TestCheckoutThenPullCmdCheckoutFailsBlocksPull(t *testing.T) {
	var puRan bool
	boom := errors.New("ref vanished")
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error { return boom },
		pull: func(context.Context, string, git.PullStrategy) error {
			puRan = true
			return nil
		},
	})

	msg := checkoutThenPullCmd("", "feat", false, "", false, "")()
	if puRan {
		t.Error("pull should not run when checkout failed")
	}
	got, ok := msg.(checkoutFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}

func TestCheckoutThenPullCmdCheckoutDirtyTreeRoutesToConfirm(t *testing.T) {
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			return git.ErrCheckoutNeedsCleanTree
		},
	})

	msg := checkoutThenPullCmd("", "feat", false, "", false, "")()
	got, ok := msg.(checkoutNeedsCleanTreeMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutNeedsCleanTreeMsg", msg)
	}
	if got.ref != "feat" {
		t.Errorf("ref = %q, want feat", got.ref)
	}
}

func TestCheckoutThenPullCmdPullConflictReportsConflict(t *testing.T) {
	conflictErr := fmt.Errorf("git pull: %w: CONFLICT", git.ErrPullConflict)
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull: func(context.Context, string, git.PullStrategy) error {
			return conflictErr
		},
	})

	msg := checkoutThenPullCmd("", "feat", false, "", false, "")()
	got, ok := msg.(checkoutThenPullConflictMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutThenPullConflictMsg", msg)
	}
	if !errors.Is(got.err, git.ErrPullConflict) {
		t.Errorf("err %v should wrap ErrPullConflict", got.err)
	}
	if got.ref != "feat" {
		t.Errorf("ref = %q, want feat", got.ref)
	}
}

func TestCheckoutThenPullCmdPullGenericFailureSurfaces(t *testing.T) {
	boom := errors.New("could not resolve host github.com")
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull: func(context.Context, string, git.PullStrategy) error {
			return boom
		},
	})

	msg := checkoutThenPullCmd("", "feat", false, "", false, "")()
	got, ok := msg.(pullFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want pullFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}

func TestStashThenCheckoutThenPullThenPopCmdHappyPath(t *testing.T) {
	var seq []string
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			seq = append(seq, "checkout")
			return nil
		},
		stash: func(context.Context, string, string) error {
			seq = append(seq, "stash")
			return nil
		},
		stashPop: func(context.Context, string) error {
			seq = append(seq, "pop")
			return nil
		},
		pullResolve: noopPullResolveFFOnly,
		pull: func(context.Context, string, git.PullStrategy) error {
			seq = append(seq, "pull")
			return nil
		},
	})

	msg := stashThenCheckoutThenPullThenPopCmd("", "feat", false, "", false, "")()
	wantSeq := []string{"stash", "checkout", "pull", "pop"}
	if !slices.Equal(seq, wantSeq) {
		t.Errorf("seq = %v, want %v", seq, wantSeq)
	}
	got, ok := msg.(stashThenCheckoutThenPullThenPopSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenCheckoutThenPullThenPopSucceededMsg", msg)
	}
	if got.stashLabel != stashLabelHEAD {
		t.Errorf("stashLabel = %q, want %q", got.stashLabel, stashLabelHEAD)
	}
	if got.pullSkipped {
		t.Error("pullSkipped should be false on the happy path")
	}
}

func TestStashThenCheckoutThenPullThenPopCmdSkipsPullChain(t *testing.T) {
	var seq []string
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			seq = append(seq, "checkout")
			return nil
		},
		stash: func(context.Context, string, string) error {
			seq = append(seq, "stash")
			return nil
		},
		stashPop: func(context.Context, string) error {
			seq = append(seq, "pop")
			return nil
		},
		pull: func(context.Context, string, git.PullStrategy) error {
			seq = append(seq, "pull")
			return nil
		},
	})

	msg := stashThenCheckoutThenPullThenPopCmd("", "v1.0", false, "", true, "tag has no upstream")()
	wantSeq := []string{"stash", "checkout", "pop"}
	if !slices.Equal(seq, wantSeq) {
		t.Errorf("seq = %v, want %v (pull skipped, pop runs after checkout)", seq, wantSeq)
	}
	got, ok := msg.(stashThenCheckoutThenPullThenPopSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenCheckoutThenPullThenPopSucceededMsg", msg)
	}
	if !got.pullSkipped || got.skipReason != "tag has no upstream" {
		t.Errorf("got = %+v, want pullSkipped=true skipReason='tag has no upstream'", got)
	}
}

func TestStashThenCheckoutThenPullThenPopCmdPullConflictStillPops(t *testing.T) {
	var popRan bool
	conflictErr := fmt.Errorf("git pull: %w: CONFLICT", git.ErrPullConflict)
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		stash:       func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull:        func(context.Context, string, git.PullStrategy) error { return conflictErr },
		stashPop: func(context.Context, string) error {
			popRan = true
			return nil
		},
	})

	msg := stashThenCheckoutThenPullThenPopCmd("", "feat", false, "", false, "")()
	if !popRan {
		t.Error("pop must still run after pull conflict (interview decision)")
	}
	got, ok := msg.(stashThenCheckoutThenPullThenPopConflictMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenCheckoutThenPullThenPopConflictMsg", msg)
	}
	if got.phase != "pull" {
		t.Errorf("phase = %q, want pull", got.phase)
	}
	if !errors.Is(got.err, git.ErrPullConflict) {
		t.Errorf("err %v should wrap ErrPullConflict", got.err)
	}
}

func TestStashThenCheckoutThenPullThenPopCmdPullGenericFailureKeepsStash(t *testing.T) {
	var popRan bool
	boom := errors.New("could not resolve host github.com")
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		stash:       func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull:        func(context.Context, string, git.PullStrategy) error { return boom },
		stashPop: func(context.Context, string) error {
			popRan = true
			return nil
		},
	})

	msg := stashThenCheckoutThenPullThenPopCmd("", "feat", false, "", false, "")()
	if popRan {
		t.Error("pop must NOT run on generic pull failure — stash is preserved")
	}
	got, ok := msg.(stashThenCheckoutThenPullThenPopConflictMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenCheckoutThenPullThenPopConflictMsg", msg)
	}
	if got.phase != "pull" {
		t.Errorf("phase = %q, want pull", got.phase)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}

func TestStashThenCheckoutThenPullThenPopCmdPopConflictPreservesStash(t *testing.T) {
	popConflictErr := fmt.Errorf("git stash pop: %w: CONFLICT", git.ErrStashPopConflict)
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		stash:       func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull:        func(context.Context, string, git.PullStrategy) error { return nil },
		stashPop:    func(context.Context, string) error { return popConflictErr },
	})

	msg := stashThenCheckoutThenPullThenPopCmd("", "feat", false, "", false, "")()
	got, ok := msg.(stashThenCheckoutThenPullThenPopConflictMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenCheckoutThenPullThenPopConflictMsg", msg)
	}
	if got.phase != "stash-pop" {
		t.Errorf("phase = %q, want stash-pop", got.phase)
	}
	if !errors.Is(got.err, git.ErrStashPopConflict) {
		t.Errorf("err %v should wrap ErrStashPopConflict", got.err)
	}
	if got.stashLabel != stashLabelHEAD {
		t.Errorf("stashLabel = %q, want %q", got.stashLabel, stashLabelHEAD)
	}
}

func TestStashThenCheckoutThenPullThenPopCmdStashFailsBlocksChain(t *testing.T) {
	boom := errors.New("stash refused")
	var coRan, puRan, popRan bool
	withChainStubs(t, chainStubs{
		stash:       func(context.Context, string, string) error { return boom },
		checkout:    func(context.Context, string, string) error { coRan = true; return nil },
		pullResolve: noopPullResolveFFOnly,
		pull:        func(context.Context, string, git.PullStrategy) error { puRan = true; return nil },
		stashPop:    func(context.Context, string) error { popRan = true; return nil },
	})

	msg := stashThenCheckoutThenPullThenPopCmd("", "feat", false, "", false, "")()
	if coRan || puRan || popRan {
		t.Errorf("nothing should run after stash failure (co=%v pu=%v pop=%v)", coRan, puRan, popRan)
	}
	got, ok := msg.(checkoutFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}
