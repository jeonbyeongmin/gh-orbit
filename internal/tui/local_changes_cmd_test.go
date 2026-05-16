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

	msg := loadStatusCmd("/tmp/repo")()
	loaded, ok := msg.(localChangesStatusLoadedMsg)
	if !ok {
		t.Fatalf("want localChangesStatusLoadedMsg, got %T (%v)", msg, msg)
	}
	if len(loaded.entries) != 1 || loaded.entries[0].Path != "f.txt" {
		t.Fatalf("unexpected entries: %+v", loaded.entries)
	}
}

func TestLoadStatusCmdEmitsFailedMsg(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	boom := errors.New("boom")
	statusExec = func(ctx context.Context, dir string) ([]git.StatusEntry, error) {
		return nil, boom
	}

	msg := loadStatusCmd("/tmp/repo")()
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

func restoreLocalChangesExec(t *testing.T) func() {
	t.Helper()
	origStatus := statusExec
	origDiffFile := diffFileExec
	origDiffUntracked := diffUntrackedExec
	origAdd := addExec
	origRestore := restoreStagedExec
	return func() {
		statusExec = origStatus
		diffFileExec = origDiffFile
		diffUntrackedExec = origDiffUntracked
		addExec = origAdd
		restoreStagedExec = origRestore
	}
}
