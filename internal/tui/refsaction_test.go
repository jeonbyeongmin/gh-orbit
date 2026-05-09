package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refsActionStubs swaps in stubs for every seam the n / d / m chains touch.
// nil leaves that seam at its default; Cleanup restores all on test exit.
type refsActionStubs struct {
	branchCreate       func(ctx context.Context, dir, name, base string) error
	branchDelete       func(ctx context.Context, dir, name string, force bool) error
	branchRename       func(ctx context.Context, dir, oldName, newName string) error
	remoteBranchDelete func(ctx context.Context, dir, remote, branch string) error
	checkRefFormat     func(ctx context.Context, dir, name string) error
}

func withRefsActionStubs(t *testing.T, s refsActionStubs) {
	t.Helper()
	prevC, prevD, prevR := branchCreateExec, branchDeleteExec, branchRenameExec
	prevRD, prevCRF := remoteBranchDeleteExec, checkRefFormatExec
	t.Cleanup(func() {
		branchCreateExec = prevC
		branchDeleteExec = prevD
		branchRenameExec = prevR
		remoteBranchDeleteExec = prevRD
		checkRefFormatExec = prevCRF
	})
	if s.branchCreate != nil {
		branchCreateExec = s.branchCreate
	}
	if s.branchDelete != nil {
		branchDeleteExec = s.branchDelete
	}
	if s.branchRename != nil {
		branchRenameExec = s.branchRename
	}
	if s.remoteBranchDelete != nil {
		remoteBranchDeleteExec = s.remoteBranchDelete
	}
	if s.checkRefFormat != nil {
		checkRefFormatExec = s.checkRefFormat
	}
}

func TestBranchCreateCmdSucceeds(t *testing.T) {
	var seenName, seenBase string
	calls := 0
	withRefsActionStubs(t, refsActionStubs{
		branchCreate: func(_ context.Context, _, name, base string) error {
			calls++
			seenName, seenBase = name, base
			return nil
		},
	})
	msg := branchCreateCmd("dir", "feat/foo", "abc1234")()
	if calls != 1 {
		t.Errorf("branchCreateExec calls = %d, want 1", calls)
	}
	if seenName != "feat/foo" || seenBase != "abc1234" {
		t.Errorf("forwarded name/base = (%q,%q), want (feat/foo, abc1234)", seenName, seenBase)
	}
	got, ok := msg.(branchCreateSucceededMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchCreateSucceededMsg", msg)
	}
	if got.name != "feat/foo" {
		t.Errorf("succeeded msg = %+v", got)
	}
}

func TestBranchCreateCmdFails(t *testing.T) {
	want := errors.New("boom")
	withRefsActionStubs(t, refsActionStubs{
		branchCreate: func(context.Context, string, string, string) error { return want },
	})
	msg := branchCreateCmd("dir", "feat/foo", "")()
	got, ok := msg.(branchCreateFailedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchCreateFailedMsg", msg)
	}
	if !errors.Is(got.err, want) {
		t.Errorf("err = %v, want wrapped %v", got.err, want)
	}
}

func TestBranchDeleteCmdLocalSafe(t *testing.T) {
	calls := 0
	var seenName string
	var seenForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, name string, force bool) error {
			calls++
			seenName, seenForce = name, force
			return nil
		},
		remoteBranchDelete: func(context.Context, string, string, string) error {
			t.Fatal("remoteBranchDeleteExec should not be called for scopeLocalSafe")
			return nil
		},
	})
	target := deleteTarget{localName: "feat/foo"}
	msg := branchDeleteCmd("dir", target, scopeLocalSafe)()
	if calls != 1 || seenName != "feat/foo" || seenForce {
		t.Errorf("delete forwarded (%q, force=%v), want (feat/foo, false)", seenName, seenForce)
	}
	got, ok := msg.(branchDeleteSucceededMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchDeleteSucceededMsg", msg)
	}
	if !got.localDeleted || got.remoteDeleted {
		t.Errorf("flags = (local=%v, remote=%v), want (true, false)", got.localDeleted, got.remoteDeleted)
	}
}

func TestBranchDeleteCmdLocalForceFlag(t *testing.T) {
	var seenForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, _ string, force bool) error {
			seenForce = force
			return nil
		},
	})
	branchDeleteCmd("dir", deleteTarget{localName: "x"}, scopeLocalForce)()
	if !seenForce {
		t.Errorf("scopeLocalForce should pass force=true to branchDeleteExec")
	}
}

func TestBranchDeleteCmdNotMergedSentinel(t *testing.T) {
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			return fmt.Errorf("git branch -d: %w", git.ErrBranchNotFullyMerged)
		},
	})
	msg := branchDeleteCmd("dir", deleteTarget{localName: "x"}, scopeLocalSafe)()
	got, ok := msg.(branchDeleteNotMergedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchDeleteNotMergedMsg", msg)
	}
	if got.scope != scopeLocalSafe || got.target.localName != "x" {
		t.Errorf("not-merged msg = %+v", got)
	}
}

func TestBranchDeleteCmdNotMergedDuringForceStillFails(t *testing.T) {
	// scopeLocalForce should not route ErrBranchNotFullyMerged into NotMerged
	// (force already failed for some other reason, or the wrapper surfaced
	// the sentinel anyway).
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			return fmt.Errorf("git branch -d: %w", git.ErrBranchNotFullyMerged)
		},
	})
	msg := branchDeleteCmd("dir", deleteTarget{localName: "x"}, scopeLocalForce)()
	if _, ok := msg.(branchDeleteFailedMsg); !ok {
		t.Fatalf("msg type = %T, want branchDeleteFailedMsg under force", msg)
	}
}

func TestBranchDeleteCmdBothSafeSucceeds(t *testing.T) {
	var localCalled, remoteCalled bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			localCalled = true
			return nil
		},
		remoteBranchDelete: func(_ context.Context, _, remote, branch string) error {
			remoteCalled = true
			if remote != "origin" || branch != "feat/foo" {
				t.Errorf("remote args = (%q,%q), want (origin, feat/foo)", remote, branch)
			}
			return nil
		},
	})
	target := deleteTarget{localName: "feat/foo", remote: "origin", remoteBranch: "feat/foo"}
	msg := branchDeleteCmd("dir", target, scopeBothSafe)()
	if !localCalled || !remoteCalled {
		t.Errorf("local=%v remote=%v, want both true", localCalled, remoteCalled)
	}
	got, ok := msg.(branchDeleteSucceededMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchDeleteSucceededMsg", msg)
	}
	if !got.localDeleted || !got.remoteDeleted {
		t.Errorf("flags = (local=%v, remote=%v)", got.localDeleted, got.remoteDeleted)
	}
}

func TestBranchDeleteCmdBothPartialFailureEmitsPartialMsg(t *testing.T) {
	// local OK + remote FAIL → partial msg, no rollback.
	wantErr := errors.New("push failed")
	withRefsActionStubs(t, refsActionStubs{
		branchDelete:       func(context.Context, string, string, bool) error { return nil },
		remoteBranchDelete: func(context.Context, string, string, string) error { return wantErr },
	})
	target := deleteTarget{localName: "feat/foo", remote: "origin", remoteBranch: "feat/foo"}
	msg := branchDeleteCmd("dir", target, scopeBothSafe)()
	got, ok := msg.(branchDeletePartialMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchDeletePartialMsg", msg)
	}
	if !got.localDeleted || got.remoteDeleted {
		t.Errorf("flags = (local=%v, remote=%v), want (true,false)", got.localDeleted, got.remoteDeleted)
	}
	if !errors.Is(got.err, wantErr) {
		t.Errorf("err = %v, want %v", got.err, wantErr)
	}
}

func TestBranchDeleteCmdBothLocalFailureSkipsRemote(t *testing.T) {
	// local fails first → remote should NOT be invoked.
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			return errors.New("local failed")
		},
		remoteBranchDelete: func(context.Context, string, string, string) error {
			t.Fatal("remote step must be skipped when local fails")
			return nil
		},
	})
	target := deleteTarget{localName: "x", remote: "origin", remoteBranch: "x"}
	msg := branchDeleteCmd("dir", target, scopeBothSafe)()
	if _, ok := msg.(branchDeleteFailedMsg); !ok {
		t.Fatalf("msg type = %T, want branchDeleteFailedMsg", msg)
	}
}

func TestBranchDeleteCmdRemoteOnly(t *testing.T) {
	var remoteCalled bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			t.Fatal("local step must be skipped for scopeRemoteOnly")
			return nil
		},
		remoteBranchDelete: func(context.Context, string, string, string) error {
			remoteCalled = true
			return nil
		},
	})
	target := deleteTarget{remote: "origin", remoteBranch: "feat/foo"}
	msg := branchDeleteCmd("dir", target, scopeRemoteOnly)()
	if !remoteCalled {
		t.Error("remoteBranchDeleteExec should have been called")
	}
	got, ok := msg.(branchDeleteSucceededMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchDeleteSucceededMsg", msg)
	}
	if got.localDeleted || !got.remoteDeleted {
		t.Errorf("flags = (local=%v, remote=%v), want (false,true)", got.localDeleted, got.remoteDeleted)
	}
}

func TestBranchRenameCmd(t *testing.T) {
	calls := 0
	withRefsActionStubs(t, refsActionStubs{
		branchRename: func(_ context.Context, _, o, n string) error {
			calls++
			if o != "old" || n != "new" {
				t.Errorf("rename args = (%q,%q)", o, n)
			}
			return nil
		},
	})
	msg := branchRenameCmd("dir", "old", "new", true)()
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	got, ok := msg.(branchRenameSucceededMsg)
	if !ok {
		t.Fatalf("msg type = %T, want branchRenameSucceededMsg", msg)
	}
	if got.oldName != "old" || got.newName != "new" || !got.headWasOld {
		t.Errorf("succeeded msg = %+v", got)
	}
}

func TestCheckRefFormatCmd(t *testing.T) {
	wantErr := errors.New("invalid")
	withRefsActionStubs(t, refsActionStubs{
		checkRefFormat: func(context.Context, string, string) error { return wantErr },
	})
	msg := checkRefFormatCmd("dir", "  bad name  ")()
	got, ok := msg.(refNameValidatedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want refNameValidatedMsg", msg)
	}
	if got.name != "  bad name  " {
		t.Errorf("name = %q, want untrimmed (model needs original for inlineErr)", got.name)
	}
	if !errors.Is(got.err, wantErr) {
		t.Errorf("err = %v, want %v", got.err, wantErr)
	}
}
