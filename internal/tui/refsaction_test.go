package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refsActionStubs swaps in a stub for the branchDelete seam. nil leaves it
// at its default; Cleanup restores it on test exit.
type refsActionStubs struct {
	branchDelete func(ctx context.Context, dir, name string, force bool) error
}

func withRefsActionStubs(t *testing.T, s refsActionStubs) {
	t.Helper()
	prev := branchDeleteExec
	t.Cleanup(func() {
		branchDeleteExec = prev
	})
	if s.branchDelete != nil {
		branchDeleteExec = s.branchDelete
	}
}

func TestBranchDeleteCmdSafeSucceeds(t *testing.T) {
	var seenName string
	var seenForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, name string, force bool) error {
			seenName, seenForce = name, force
			return nil
		},
	})
	msg := branchDeleteCmd("dir", "feat/foo", false)()
	if seenName != "feat/foo" || seenForce {
		t.Errorf("delete called with (%q, force=%v), want (feat/foo, false)", seenName, seenForce)
	}
	got, ok := msg.(branchDeleteSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want branchDeleteSucceededMsg", msg)
	}
	if got.localName != "feat/foo" || got.forced {
		t.Errorf("succeeded msg = %+v, want {feat/foo false}", got)
	}
}

func TestBranchDeleteCmdForceFlag(t *testing.T) {
	var seenForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, _ string, force bool) error {
			seenForce = force
			return nil
		},
	})
	msg := branchDeleteCmd("dir", "x", true)()
	if !seenForce {
		t.Error("force=true should propagate to branchDeleteExec")
	}
	got, ok := msg.(branchDeleteSucceededMsg)
	if !ok {
		t.Fatalf("msg = %T, want branchDeleteSucceededMsg", msg)
	}
	if !got.forced {
		t.Errorf("succeeded msg forced=%v, want true", got.forced)
	}
}

func TestBranchDeleteCmdNotMergedRoutesToSentinel(t *testing.T) {
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			return fmt.Errorf("git branch -d: %w", git.ErrBranchNotFullyMerged)
		},
	})
	msg := branchDeleteCmd("dir", "x", false)()
	got, ok := msg.(branchDeleteNotMergedMsg)
	if !ok {
		t.Fatalf("msg = %T, want branchDeleteNotMergedMsg", msg)
	}
	if got.localName != "x" {
		t.Errorf("localName = %q, want x", got.localName)
	}
}

func TestBranchDeleteCmdNotMergedDuringForceStillFails(t *testing.T) {
	// force=true should not route ErrBranchNotFullyMerged into the
	// not-merged sentinel — the user explicitly asked for -D, so a
	// not-merged error in that branch is a real failure (probably a lock
	// or wrapper-level glitch), not a guided retry.
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			return fmt.Errorf("git branch -D: %w", git.ErrBranchNotFullyMerged)
		},
	})
	msg := branchDeleteCmd("dir", "x", true)()
	if _, ok := msg.(branchDeleteFailedMsg); !ok {
		t.Fatalf("msg = %T, want branchDeleteFailedMsg under force", msg)
	}
}

func TestBranchDeleteCmdGenericFailureSurfaces(t *testing.T) {
	want := errors.New("lock held")
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error { return want },
	})
	msg := branchDeleteCmd("dir", "x", false)()
	got, ok := msg.(branchDeleteFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want branchDeleteFailedMsg", msg)
	}
	if !errors.Is(got.err, want) {
		t.Errorf("err = %v, want chain to include %v", got.err, want)
	}
}
