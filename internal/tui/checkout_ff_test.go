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

func TestStashThenFFCmdSuccess(t *testing.T) {
	var stashCalled, ffCalled bool
	withChainStubs(t, chainStubs{
		stash: func(context.Context, string, string) error { stashCalled = true; return nil },
		mergeFFOnly: func(context.Context, string, string) error {
			ffCalled = true
			if !stashCalled {
				t.Error("FF ran before stash")
			}
			return nil
		},
		countAhead: func(context.Context, string, string, string) (int, error) { return 2, nil },
	})

	msg := stashThenFFCmd("", "main", "abc1234")()
	if !stashCalled || !ffCalled {
		t.Errorf("stash=%v ff=%v, want both true", stashCalled, ffCalled)
	}
	got, ok := msg.(stashThenFFMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashThenFFMsg", msg)
	}
	if got.advance != 2 {
		t.Errorf("advance = %d, want 2", got.advance)
	}
	if got.stashLabel != stashLabelHEAD {
		t.Errorf("stashLabel = %q, want %q", got.stashLabel, stashLabelHEAD)
	}
}

func TestStashThenFFCmdStashFailureBlocksFF(t *testing.T) {
	boom := errors.New("stash refused")
	var ffCalled bool
	withChainStubs(t, chainStubs{
		stash:       func(context.Context, string, string) error { return boom },
		mergeFFOnly: func(context.Context, string, string) error { ffCalled = true; return nil },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 0, nil },
	})

	msg := stashThenFFCmd("", "main", "abc1234")()
	if ffCalled {
		t.Error("FF should not run if stash failed")
	}
	got, ok := msg.(ffFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want ffFailedMsg", msg)
	}
	if !errors.Is(got.err, boom) {
		t.Errorf("err = %v, want chain to include %v", got.err, boom)
	}
}
