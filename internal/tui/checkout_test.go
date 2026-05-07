package tui

import (
	"context"
	"errors"
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
