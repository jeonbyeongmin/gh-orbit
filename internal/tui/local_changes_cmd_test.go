package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestLoadStatusCmdEmitsLoadedMsg(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	want := []git.StatusEntry{{Path: "f.txt", IndexState: 'M'}}
	statusExec = func(ctx context.Context, dir string) ([]git.StatusEntry, error) {
		return want, nil
	}
	diffNumstatExec = func(ctx context.Context, dir string, staged bool) ([]git.FileStat, error) {
		if staged {
			return []git.FileStat{{Path: "f.txt", Insertions: 3, Deletions: 1}}, nil
		}
		return nil, nil
	}

	msg := loadStatusCmd("/tmp/repo", false)()
	loaded, ok := msg.(localChangesStatusLoadedMsg)
	if !ok {
		t.Fatalf("want localChangesStatusLoadedMsg, got %T (%v)", msg, msg)
	}
	if len(loaded.entries) != 1 || loaded.entries[0].Path != "f.txt" {
		t.Fatalf("unexpected entries: %+v", loaded.entries)
	}
	if len(loaded.stagedStat) != 1 || loaded.stagedStat[0].Insertions != 3 {
		t.Fatalf("staged numstat not carried: %+v", loaded.stagedStat)
	}
}

func TestLoadStatusCmdEmitsFailedMsg(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	boom := errors.New("boom")
	statusExec = func(ctx context.Context, dir string) ([]git.StatusEntry, error) {
		return nil, boom
	}

	msg := loadStatusCmd("/tmp/repo", false)()
	failed, ok := msg.(localChangesStatusFailedMsg)
	if !ok {
		t.Fatalf("want localChangesStatusFailedMsg, got %T (%v)", msg, msg)
	}
	if !errors.Is(failed.err, boom) {
		t.Fatalf("err not propagated: %v", failed.err)
	}
}

func TestLoadDiffCmdRoutesUntrackedToNoIndex(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	calledUntracked := false
	diffUntrackedExec = func(ctx context.Context, dir, path string) (string, error) {
		calledUntracked = true
		return "+content", nil
	}
	diffFileExec = func(ctx context.Context, dir, path string, staged bool) (string, error) {
		t.Fatalf("DiffFile should not run for untracked")
		return "", nil
	}

	msg := loadDiffCmd("/tmp/repo", "u.txt", false, true, 42)()
	loaded, ok := msg.(localChangesDiffLoadedMsg)
	if !ok {
		t.Fatalf("want localChangesDiffLoadedMsg, got %T (%v)", msg, msg)
	}
	if !calledUntracked {
		t.Fatalf("DiffUntracked not called")
	}
	if loaded.reqID != 42 || loaded.path != "u.txt" || loaded.text != "+content" {
		t.Fatalf("unexpected msg: %+v", loaded)
	}
}

func TestLoadDiffCmdRoutesStagedToDiffFileCached(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	var sawStaged bool
	diffFileExec = func(ctx context.Context, dir, path string, staged bool) (string, error) {
		sawStaged = staged
		return "staged-text", nil
	}

	msg := loadDiffCmd("/tmp/repo", "f.txt", true, false, 7)()
	loaded, ok := msg.(localChangesDiffLoadedMsg)
	if !ok {
		t.Fatalf("want localChangesDiffLoadedMsg, got %T", msg)
	}
	if !sawStaged || !loaded.staged {
		t.Fatalf("staged flag not propagated: sawStaged=%v loaded=%+v", sawStaged, loaded)
	}
}

func TestAddCmdSuccess(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	addExec = func(ctx context.Context, dir, path string) error { return nil }
	msg := addCmd("/tmp/repo", "f.txt")()
	if got, ok := msg.(localChangesAddSucceededMsg); !ok || got.path != "f.txt" {
		t.Fatalf("unexpected: %T %v", msg, msg)
	}
}

func TestAddCmdFailure(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	boom := errors.New("boom")
	addExec = func(ctx context.Context, dir, path string) error { return boom }
	msg := addCmd("/tmp/repo", "f.txt")()
	if got, ok := msg.(localChangesAddFailedMsg); !ok || !errors.Is(got.err, boom) {
		t.Fatalf("unexpected: %T %v", msg, msg)
	}
}

func TestRestoreStagedCmdSuccess(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	restoreStagedExec = func(ctx context.Context, dir, path string) error { return nil }
	msg := restoreStagedCmd("/tmp/repo", "f.txt")()
	if _, ok := msg.(localChangesRestoreSucceededMsg); !ok {
		t.Fatalf("unexpected: %T", msg)
	}
}

func TestLoadStatusCmdMergesUntrackedNumstat(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	statusExec = func(ctx context.Context, dir string) ([]git.StatusEntry, error) {
		return []git.StatusEntry{
			{Path: "tracked.txt", WorktreeState: 'M'},
			{Path: "new.txt", IndexState: '?', WorktreeState: '?', Untracked: true},
		}, nil
	}
	diffNumstatExec = func(ctx context.Context, dir string, staged bool) ([]git.FileStat, error) {
		if staged {
			return nil, nil
		}
		return []git.FileStat{{Path: "tracked.txt", Insertions: 2, Deletions: 1}}, nil
	}
	var probed string
	diffUntrackedNumstatExec = func(ctx context.Context, dir, path string) (git.FileStat, error) {
		probed = path
		return git.FileStat{Path: path, Insertions: 7}, nil
	}

	msg := loadStatusCmd("/tmp/repo", true)()
	loaded, ok := msg.(localChangesStatusLoadedMsg)
	if !ok {
		t.Fatalf("want localChangesStatusLoadedMsg, got %T (%v)", msg, msg)
	}
	if !loaded.preserveCursor {
		t.Fatal("preserveCursor not carried through")
	}
	if probed != "new.txt" {
		t.Fatalf("untracked numstat probed %q, want new.txt", probed)
	}
	// The untracked stat must be folded into the unstaged side so statText
	// finds it (untracked rows render Unstaged).
	var got *git.FileStat
	for i := range loaded.unstagedStat {
		if loaded.unstagedStat[i].Path == "new.txt" {
			got = &loaded.unstagedStat[i]
		}
	}
	if got == nil {
		t.Fatalf("untracked stat not merged into unstaged: %+v", loaded.unstagedStat)
	}
	if got.Insertions != 7 {
		t.Fatalf("untracked insertions = %d, want 7", got.Insertions)
	}
}

func TestStashAllCmd(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	var called bool
	stashAllExec = func(ctx context.Context, dir string) error { called = true; return nil }
	if _, ok := stashAllCmd("/tmp/repo")().(localChangesStashAllDoneMsg); !ok || !called {
		t.Fatalf("stash all did not succeed (called=%v)", called)
	}

	boom := errors.New("boom")
	stashAllExec = func(ctx context.Context, dir string) error { return boom }
	got, ok := stashAllCmd("/tmp/repo")().(localChangesStashAllFailedMsg)
	if !ok || !errors.Is(got.err, boom) {
		t.Fatalf("stash all failure not propagated: %T %v", got, got)
	}
}

func TestDiscardAllCmdTrackedOnlySkipsClean(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	var resetMode git.ResetMode
	var resetHash string
	resetHardExec = func(ctx context.Context, dir string, mode git.ResetMode, hash string) error {
		resetMode, resetHash = mode, hash
		return nil
	}
	cleanExec = func(ctx context.Context, dir string) error {
		t.Fatal("clean must not run for tracked-only discard")
		return nil
	}

	done, ok := discardAllCmd("/tmp/repo", false)().(localChangesDiscardDoneMsg)
	if !ok || done.includeUntracked {
		t.Fatalf("unexpected msg: %T %+v", done, done)
	}
	if resetMode != git.ResetHard || resetHash != "HEAD" {
		t.Fatalf("reset args = (%v, %q), want (ResetHard, HEAD)", resetMode, resetHash)
	}
}

func TestDiscardAllCmdIncludeUntrackedRunsClean(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	resetHardExec = func(ctx context.Context, dir string, mode git.ResetMode, hash string) error { return nil }
	var cleaned bool
	cleanExec = func(ctx context.Context, dir string) error { cleaned = true; return nil }

	done, ok := discardAllCmd("/tmp/repo", true)().(localChangesDiscardDoneMsg)
	if !ok || !done.includeUntracked || !cleaned {
		t.Fatalf("clean not run for full discard: ok=%v done=%+v cleaned=%v", ok, done, cleaned)
	}
}

func TestDiscardAllCmdCleanFailurePropagates(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	resetHardExec = func(ctx context.Context, dir string, mode git.ResetMode, hash string) error { return nil }
	boom := errors.New("clean boom")
	cleanExec = func(ctx context.Context, dir string) error { return boom }

	got, ok := discardAllCmd("/tmp/repo", true)().(localChangesDiscardFailedMsg)
	if !ok || !errors.Is(got.err, boom) {
		t.Fatalf("clean failure not propagated: %T %v", got, got)
	}
}

func restoreLocalChangesExec(t *testing.T) func() {
	t.Helper()
	origStatus := statusExec
	origDiffNumstat := diffNumstatExec
	origDiffUntrackedNumstat := diffUntrackedNumstatExec
	origDiffFile := diffFileExec
	origDiffUntracked := diffUntrackedExec
	origAdd := addExec
	origRestore := restoreStagedExec
	origStashAll := stashAllExec
	origResetHard := resetHardExec
	origClean := cleanExec
	return func() {
		statusExec = origStatus
		diffNumstatExec = origDiffNumstat
		diffUntrackedNumstatExec = origDiffUntrackedNumstat
		diffFileExec = origDiffFile
		diffUntrackedExec = origDiffUntracked
		addExec = origAdd
		restoreStagedExec = origRestore
		stashAllExec = origStashAll
		resetHardExec = origResetHard
		cleanExec = origClean
	}
}
