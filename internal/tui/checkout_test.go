package tui

import (
	"context"
	"errors"
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

// chainStubs swaps in stubs for the FF chain seams (checkout / detached
// checkout / mergeFFOnly / countAhead). Returning nil for any field leaves
// that seam at its default. Cleanup restores all of them on test exit.
type chainStubs struct {
	checkout         func(context.Context, string, string) error
	checkoutDetached func(context.Context, string, string) error
	mergeFFOnly      func(context.Context, string, string) error
	countAhead       func(context.Context, string, string, string) (int, error)
}

func withChainStubs(t *testing.T, s chainStubs) {
	t.Helper()
	prevC, prevCD := checkoutExec, checkoutDetachedExec
	prevFF, prevCA := mergeFFOnlyExec, countAheadExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
		mergeFFOnlyExec = prevFF
		countAheadExec = prevCA
	})
	if s.checkout != nil {
		checkoutExec = s.checkout
	}
	if s.checkoutDetached != nil {
		checkoutDetachedExec = s.checkoutDetached
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
