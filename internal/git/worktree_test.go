package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorktreePorcelainEmpty(t *testing.T) {
	got, err := parseWorktreePorcelain("")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestParseWorktreePorcelainSingleMain(t *testing.T) {
	raw := "worktree /repo/main\x00HEAD abc123\x00branch refs/heads/main\x00\x00"
	got, err := parseWorktreePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	wt := got[0]
	if wt.Path != "/repo/main" || wt.HEAD != "abc123" || wt.Branch != "main" || !wt.IsMain {
		t.Fatalf("unexpected entry: %+v", wt)
	}
	if wt.Detached || wt.Locked || wt.Prunable {
		t.Errorf("flags should be zero: %+v", wt)
	}
}

func TestParseWorktreePorcelainMultiple(t *testing.T) {
	raw := "worktree /repo/main\x00HEAD abc\x00branch refs/heads/main\x00\x00" +
		"worktree /repo/feat-a\x00HEAD def\x00branch refs/heads/feat-a\x00\x00" +
		"worktree /repo/detached\x00HEAD ghi\x00detached\x00\x00"
	got, err := parseWorktreePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if !got[0].IsMain {
		t.Errorf("first entry should be main")
	}
	if got[1].IsMain || got[2].IsMain {
		t.Errorf("non-first entries should not be main")
	}
	if got[1].Branch != "feat-a" {
		t.Errorf("feat-a branch: %q", got[1].Branch)
	}
	if !got[2].Detached || got[2].Branch != "" {
		t.Errorf("third entry should be detached with no branch: %+v", got[2])
	}
}

func TestParseWorktreePorcelainLocked(t *testing.T) {
	tt := []struct {
		name       string
		raw        string
		wantLocked bool
		wantReason string
	}{
		{
			name:       "locked no reason",
			raw:        "worktree /repo/main\x00HEAD abc\x00branch refs/heads/main\x00locked\x00\x00",
			wantLocked: true,
			wantReason: "",
		},
		{
			name:       "locked with reason",
			raw:        "worktree /repo/main\x00HEAD abc\x00branch refs/heads/main\x00locked on external drive\x00\x00",
			wantLocked: true,
			wantReason: "on external drive",
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWorktreePorcelain(tc.raw)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(got))
			}
			if got[0].Locked != tc.wantLocked || got[0].LockReason != tc.wantReason {
				t.Errorf("got Locked=%v reason=%q, want %v %q",
					got[0].Locked, got[0].LockReason, tc.wantLocked, tc.wantReason)
			}
		})
	}
}

func TestParseWorktreePorcelainPrunable(t *testing.T) {
	raw := "worktree /repo/missing\x00HEAD abc\x00branch refs/heads/old\x00prunable gitdir file points to non-existent location\x00\x00"
	got, err := parseWorktreePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || !got[0].Prunable {
		t.Fatalf("expected prunable entry: %+v", got)
	}
	if got[0].PrunableReason != "gitdir file points to non-existent location" {
		t.Errorf("reason: %q", got[0].PrunableReason)
	}
}

func TestParseWorktreePorcelainPathWithSpace(t *testing.T) {
	raw := "worktree /repo/with space/main\x00HEAD abc\x00branch refs/heads/main\x00\x00"
	got, err := parseWorktreePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got[0].Path != "/repo/with space/main" {
		t.Errorf("path: %q", got[0].Path)
	}
}

func TestParseWorktreePorcelainBare(t *testing.T) {
	// A bare main worktree has `bare` instead of HEAD/branch. We accept it
	// (IsMain true, Path populated) without populating HEAD/Branch.
	raw := "worktree /repo/bare\x00bare\x00\x00" +
		"worktree /repo/work\x00HEAD abc\x00branch refs/heads/main\x00\x00"
	got, err := parseWorktreePorcelain(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if !got[0].IsMain || got[0].HEAD != "" || got[0].Branch != "" {
		t.Errorf("bare main: %+v", got[0])
	}
	if got[1].IsMain || got[1].Branch != "main" {
		t.Errorf("second worktree: %+v", got[1])
	}
}

func TestParseWorktreePorcelainMissingPath(t *testing.T) {
	raw := "HEAD abc\x00branch refs/heads/foo\x00\x00"
	_, err := parseWorktreePorcelain(raw)
	if err == nil {
		t.Fatalf("expected error for missing path")
	}
}

// Integration tests below: real git fixture with `git worktree add` and
// friends. setupWorktreeFixture mirrors helpers_test.go's gitRun pattern.

func setupWorktreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gitRun(t, main, "init", "--initial-branch=main")
	gitRun(t, main, "config", "commit.gpgsign", "false")
	gitRun(t, main, "config", "user.email", "test@example.com")
	gitRun(t, main, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, main, "add", "f")
	gitRun(t, main, "commit", "-m", "init")
	return main
}

func TestWorktreesIntegration(t *testing.T) {
	main := setupWorktreeFixture(t)
	got, err := Worktrees(context.Background(), main)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if !got[0].IsMain || got[0].Branch != "main" {
		t.Errorf("unexpected main entry: %+v", got[0])
	}
}

func TestWorktreeAddListRemove(t *testing.T) {
	main := setupWorktreeFixture(t)
	feat := filepath.Join(filepath.Dir(main), "feat-a")
	if err := WorktreeAdd(context.Background(), main, feat, "feat-a", true); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	got, err := Worktrees(context.Background(), main)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	var feat_a *Worktree
	for i := range got {
		if got[i].Branch == "feat-a" {
			feat_a = &got[i]
		}
	}
	if feat_a == nil {
		t.Fatalf("feat-a not found: %+v", got)
	}
	if feat_a.IsMain {
		t.Errorf("feat-a should not be main")
	}
	// macOS resolves /var → /private/var so git emits a canonical path
	// that differs from the t.TempDir() literal. Compare via EvalSymlinks
	// so the assertion stays portable.
	wantPath, err := filepath.EvalSymlinks(feat)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if feat_a.Path != wantPath {
		t.Errorf("path mismatch: got %q want %q", feat_a.Path, wantPath)
	}

	if err := WorktreeRemove(context.Background(), main, feat, false); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	got, err = Worktrees(context.Background(), main)
	if err != nil {
		t.Fatalf("Worktrees after remove: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d", len(got))
	}
}

func TestWorktreeRemoveDirty(t *testing.T) {
	main := setupWorktreeFixture(t)
	feat := filepath.Join(filepath.Dir(main), "feat-dirty")
	if err := WorktreeAdd(context.Background(), main, feat, "feat-dirty", true); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(feat, "g"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := WorktreeRemove(context.Background(), main, feat, false)
	if !errors.Is(err, ErrWorktreeDirty) {
		t.Fatalf("expected ErrWorktreeDirty, got %v", err)
	}
	if err := WorktreeRemove(context.Background(), main, feat, true); err != nil {
		t.Fatalf("WorktreeRemove force: %v", err)
	}
}

func TestWorktreeRemoveLocked(t *testing.T) {
	main := setupWorktreeFixture(t)
	feat := filepath.Join(filepath.Dir(main), "feat-locked")
	if err := WorktreeAdd(context.Background(), main, feat, "feat-locked", true); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	gitRun(t, main, "worktree", "lock", feat)
	err := WorktreeRemove(context.Background(), main, feat, false)
	if !errors.Is(err, ErrWorktreeLocked) {
		t.Fatalf("expected ErrWorktreeLocked, got %v", err)
	}
	gitRun(t, main, "worktree", "unlock", feat)
	if err := WorktreeRemove(context.Background(), main, feat, false); err != nil {
		t.Fatalf("WorktreeRemove after unlock: %v", err)
	}
}

func TestWorktreeListShowsLocked(t *testing.T) {
	main := setupWorktreeFixture(t)
	feat := filepath.Join(filepath.Dir(main), "feat-locklist")
	if err := WorktreeAdd(context.Background(), main, feat, "feat-locklist", true); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	gitRun(t, main, "worktree", "lock", "--reason", "on external drive", feat)
	got, err := Worktrees(context.Background(), main)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	var locked *Worktree
	for i := range got {
		if got[i].Branch == "feat-locklist" {
			locked = &got[i]
		}
	}
	if locked == nil {
		t.Fatalf("feat-locklist not found")
	}
	if !locked.Locked {
		t.Errorf("expected Locked=true: %+v", *locked)
	}
	if locked.LockReason != "on external drive" {
		t.Errorf("LockReason: %q", locked.LockReason)
	}
}
