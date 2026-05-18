package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestFFOnlyCmdSuccessStampsAdvance(t *testing.T) {
	var ffCalled bool
	var ffHash string
	withChainStubs(t, chainStubs{
		mergeFFOnly: func(_ context.Context, _, hash string) error {
			ffCalled = true
			ffHash = hash
			return nil
		},
		countAhead: func(context.Context, string, string, string) (int, error) {
			return 4, nil
		},
	})

	msg := ffOnlyCmd("", "main", "abc1234")()
	if !ffCalled {
		t.Error("mergeFFOnlyExec should be called")
	}
	if ffHash != "abc1234" {
		t.Errorf("ff hash = %q, want abc1234", ffHash)
	}
	got, ok := msg.(ffSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want ffSucceededMsg", msg)
	}
	if got.branch != "main" {
		t.Errorf("branch = %q, want main", got.branch)
	}
	if got.advance != 4 {
		t.Errorf("advance = %d, want 4", got.advance)
	}
}

func TestFFOnlyCmdDirtyTreeWrapsToNeedsCleanTree(t *testing.T) {
	withChainStubs(t, chainStubs{
		mergeFFOnly: func(context.Context, string, string) error {
			return fmt.Errorf("git merge --ff-only: %w", git.ErrCheckoutNeedsCleanTree)
		},
		countAhead: func(context.Context, string, string, string) (int, error) { return 0, nil },
	})

	msg := ffOnlyCmd("", "main", "abc1234")()
	got, ok := msg.(ffNeedsCleanTreeMsg)
	if !ok {
		t.Fatalf("msg = %T, want ffNeedsCleanTreeMsg", msg)
	}
	if got.branch != "main" || got.hash != "abc1234" {
		t.Errorf("got = %+v, want {main abc1234}", got)
	}
}

func TestFFOnlyCmdGenericFailureSurfaces(t *testing.T) {
	boom := fmt.Errorf("git merge --ff-only: %w", git.ErrFFNotPossible)
	withChainStubs(t, chainStubs{
		mergeFFOnly: func(context.Context, string, string) error { return boom },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 0, nil },
	})

	msg := ffOnlyCmd("", "main", "abc1234")()
	got, ok := msg.(ffFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want ffFailedMsg", msg)
	}
	if !errors.Is(got.err, git.ErrFFNotPossible) {
		t.Errorf("err = %v, want chain to include ErrFFNotPossible", got.err)
	}
}

func TestCheckoutThenFFCmdSuccess(t *testing.T) {
	var coRan, ffRan bool
	var coBranch, ffHash string
	withChainStubs(t, chainStubs{
		checkout: func(_ context.Context, _, branch string) error {
			coRan = true
			coBranch = branch
			return nil
		},
		mergeFFOnly: func(_ context.Context, _, hash string) error {
			ffRan = true
			ffHash = hash
			if !coRan {
				t.Error("FF ran before checkout")
			}
			return nil
		},
		countAhead: func(context.Context, string, string, string) (int, error) { return 4, nil },
	})

	msg := checkoutThenFFCmd("", "develop", "abc1234")()
	if !coRan || !ffRan {
		t.Errorf("co=%v ff=%v, want both true", coRan, ffRan)
	}
	if coBranch != "develop" || ffHash != "abc1234" {
		t.Errorf("checkout branch=%q ff hash=%q, want develop/abc1234", coBranch, ffHash)
	}
	got, ok := msg.(checkoutThenFFSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want checkoutThenFFSucceededMsg", msg)
	}
	if got.branch != "develop" || got.advance != 4 {
		t.Errorf("got = %+v, want {develop 4}", got)
	}
}

func TestCheckoutThenFFCmdDirtyTreeRoutesToFFCheckoutNeedsCleanTree(t *testing.T) {
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			return fmt.Errorf("git checkout: %w", git.ErrCheckoutNeedsCleanTree)
		},
		mergeFFOnly: func(context.Context, string, string) error { return nil },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 0, nil },
	})

	msg := checkoutThenFFCmd("", "develop", "abc1234")()
	got, ok := msg.(ffCheckoutNeedsCleanTreeMsg)
	if !ok {
		t.Fatalf("msg = %T, want ffCheckoutNeedsCleanTreeMsg", msg)
	}
	if got.branch != "develop" || got.hash != "abc1234" {
		t.Errorf("got = %+v, want {develop abc1234}", got)
	}
}

func TestCheckoutThenFFCmdFFFailureSurfaces(t *testing.T) {
	boom := fmt.Errorf("git merge --ff-only: %w", git.ErrFFNotPossible)
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		mergeFFOnly: func(context.Context, string, string) error { return boom },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 0, nil },
	})

	msg := checkoutThenFFCmd("", "develop", "abc1234")()
	got, ok := msg.(ffFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want ffFailedMsg", msg)
	}
	if !errors.Is(got.err, git.ErrFFNotPossible) {
		t.Errorf("err = %v, want ErrFFNotPossible chain", got.err)
	}
}
