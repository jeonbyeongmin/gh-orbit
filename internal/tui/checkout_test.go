package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func withCheckoutStubs(t *testing.T, c, cd func(context.Context, string, string) error) {
	t.Helper()
	prevC, prevCD := checkoutExec, checkoutDetachedExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
	})
	if c != nil {
		checkoutExec = c
	}
	if cd != nil {
		checkoutDetachedExec = cd
	}
}

// chainStubs swaps in stubs for every seam the p-key chain touches:
// checkout, detached checkout, pull strategy resolve, pull exec, and the
// FF wrappers (mergeFFOnly / countAhead). Returning nil for any field
// leaves that seam at its default. Cleanup restores all of them on test
// exit.
type chainStubs struct {
	checkout         func(context.Context, string, string) error
	checkoutDetached func(context.Context, string, string) error
	pullResolve      func(context.Context, string, string) (git.PullStrategy, error)
	pull             func(context.Context, string, git.PullStrategy) error
	mergeFFOnly      func(context.Context, string, string) error
	countAhead       func(context.Context, string, string, string) (int, error)
}

func withChainStubs(t *testing.T, s chainStubs) {
	t.Helper()
	prevC, prevCD := checkoutExec, checkoutDetachedExec
	prevPR, prevPE := pullResolveStrategy, pullExec
	prevFF, prevCA := mergeFFOnlyExec, countAheadExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
		pullResolveStrategy = prevPR
		pullExec = prevPE
		mergeFFOnlyExec = prevFF
		countAheadExec = prevCA
	})
	if s.checkout != nil {
		checkoutExec = s.checkout
	}
	if s.checkoutDetached != nil {
		checkoutDetachedExec = s.checkoutDetached
	}
	if s.pullResolve != nil {
		pullResolveStrategy = s.pullResolve
	}
	if s.pull != nil {
		pullExec = s.pull
	}
	if s.mergeFFOnly != nil {
		mergeFFOnlyExec = s.mergeFFOnly
	}
	if s.countAhead != nil {
		countAheadExec = s.countAhead
	}
}

func TestCheckoutCmdSuccess(t *testing.T) {
	var seenRef string
	withCheckoutStubs(t, func(_ context.Context, _, ref string) error {
		seenRef = ref
		return nil
	}, nil)

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
	}, nil)

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
	withCheckoutStubs(t, func(context.Context, string, string) error { return boom }, nil)

	msg := checkoutCmd("", "feat", false)()
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

	msg := checkoutThenPullCmd("", "feat", false, "rebase", "")()
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

	msg := checkoutThenPullCmd("", "v1.0", false, "", "tag has no upstream")()
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

	msg := checkoutThenPullCmd("", "feat", false, "", "")()
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

	msg := checkoutThenPullCmd("", "feat", false, "", "")()
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

	msg := checkoutThenPullCmd("", "feat", false, "", "")()
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

	msg := checkoutThenPullCmd("", "feat", false, "", "")()
	got, ok := msg.(pullFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want pullFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}
