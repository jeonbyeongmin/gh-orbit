package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusCleanTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	mustWrite(t, dir, "f.txt", "a\n")
	gitRun(t, dir, "add", "f.txt")
	gitRun(t, dir, "commit", "-m", "first")

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("clean tree: want 0 entries, got %d: %+v", len(got), got)
	}
}

func TestStatusModifiedUnstaged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	mustWrite(t, dir, "f.txt", "a\nb\n")

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Path != "f.txt" || e.IndexState != '.' || e.WorktreeState != 'M' || e.Untracked || e.Conflict {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestStatusStagedAdd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initEmptyRepo(t)
	mustWrite(t, dir, "new.txt", "hello\n")
	gitRun(t, dir, "add", "new.txt")

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Path != "new.txt" || e.IndexState != 'A' || e.WorktreeState != '.' {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestStatusUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	mustWrite(t, dir, "u.txt", "untracked\n")

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Path != "u.txt" || !e.Untracked || e.IndexState != '?' || e.WorktreeState != '?' {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestStatusRename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "hello world\n")
	gitRun(t, dir, "mv", "a.txt", "b.txt")

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Path != "b.txt" || e.OrigPath != "a.txt" || !e.Renamed() || e.IndexState != 'R' {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestStatusDeletedUnstaged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	if err := os.Remove(filepath.Join(dir, "f.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Path != "f.txt" || e.WorktreeState != 'D' {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestStatusConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "base\n")
	// Create a branch that edits f.txt one way, switch back, edit the other
	// way, then merge to force a conflict.
	gitRun(t, dir, "checkout", "-b", "branch1")
	mustWrite(t, dir, "f.txt", "branch1\n")
	gitRun(t, dir, "commit", "-am", "branch1 change")

	gitRun(t, dir, "checkout", "main")
	mustWrite(t, dir, "f.txt", "main\n")
	gitRun(t, dir, "commit", "-am", "main change")

	// Merge should fail with conflict; we don't check the error, just inspect state.
	cmd := exec.Command("git", "merge", "branch1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	_ = cmd.Run()

	got, err := Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Path != "f.txt" || !e.Conflict || e.IndexState != 'U' || e.WorktreeState != 'U' {
		t.Fatalf("unexpected conflict entry: %+v", e)
	}
}

// TestStatusDoesNotWriteIndex pins the --no-optional-locks guard in runStatus.
// A refreshing `git status` rewrites .git/index to update its stat cache; the
// external-change watcher (internal/tui/worktreewatch.go) watches .git/index,
// so a self-induced index write here feeds a status → write → fsnotify event →
// reload → status flicker loop. Status must leave the index byte-identical.
func TestStatusDoesNotWriteIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")

	// Perturb only the file's mtime (content stays clean) so a stat-cache
	// refresh is the sole reason a non-guarded `git status` would rewrite the
	// index.
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "f.txt"), past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	idx := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("read index before: %v", err)
	}

	if _, err := Status(context.Background(), dir); err != nil {
		t.Fatalf("Status: %v", err)
	}

	after, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("read index after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("git status rewrote .git/index; runStatus must pass --no-optional-locks so the external-change watcher doesn't see a self-induced index write")
	}
}

// initEmptyRepo: bare init, no commit yet.
func initEmptyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	// Windows CI sets core.autocrlf=true globally; pin it off so committed LF
	// content round-trips byte-for-byte through checkout on every platform.
	gitRun(t, dir, "config", "core.autocrlf", "false")
	return dir
}

// initRepoWithFile: init repo + commit one file with the given contents.
func initRepoWithFile(t *testing.T, name, contents string) string {
	t.Helper()
	dir := initEmptyRepo(t)
	mustWrite(t, dir, name, contents)
	gitRun(t, dir, "add", name)
	gitRun(t, dir, "commit", "-m", "initial")
	return dir
}

func TestAddAndRestoreStagedRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	mustWrite(t, dir, "f.txt", "a\nb\n")
	ctx := context.Background()

	if err := Add(ctx, dir, "f.txt"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status post-Add: %v", err)
	}
	if len(got) != 1 || got[0].IndexState != 'M' || got[0].WorktreeState != '.' {
		t.Fatalf("after Add want index=M worktree=., got %+v", got)
	}

	if err := RestoreStaged(ctx, dir, "f.txt"); err != nil {
		t.Fatalf("RestoreStaged: %v", err)
	}
	got, err = Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status post-Restore: %v", err)
	}
	if len(got) != 1 || got[0].IndexState != '.' || got[0].WorktreeState != 'M' {
		t.Fatalf("after RestoreStaged want index=. worktree=M, got %+v", got)
	}
}

func TestDiffFileUnstagedAndStaged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	mustWrite(t, dir, "f.txt", "a\nb\n")
	ctx := context.Background()

	unstaged, err := DiffFile(ctx, dir, "f.txt", false)
	if err != nil {
		t.Fatalf("DiffFile unstaged: %v", err)
	}
	if !strings.Contains(unstaged, "diff --git a/f.txt b/f.txt") || !strings.Contains(unstaged, "+1,2") {
		t.Fatalf("unstaged diff unexpected shape: %q", unstaged)
	}

	if err := Add(ctx, dir, "f.txt"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	staged, err := DiffFile(ctx, dir, "f.txt", true)
	if err != nil {
		t.Fatalf("DiffFile staged: %v", err)
	}
	if !strings.Contains(staged, "diff --git a/f.txt b/f.txt") || !strings.Contains(staged, "+1,2") {
		t.Fatalf("staged diff unexpected shape: %q", staged)
	}
}

func TestDiffUntrackedShowsAllAdditions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	mustWrite(t, dir, "u.txt", "line1\nline2\n")
	ctx := context.Background()

	out, err := DiffUntracked(ctx, dir, "u.txt")
	if err != nil {
		t.Fatalf("DiffUntracked: %v", err)
	}
	if !strings.Contains(out, "+++ b/u.txt") || !strings.Contains(out, "line1") || !strings.Contains(out, "line2") {
		t.Fatalf("DiffUntracked unexpected shape: %q", out)
	}
}

func TestDiffUntrackedNumstatCountsAdditions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	mustWrite(t, dir, "new.txt", "one\ntwo\nthree\n")
	ctx := context.Background()

	fs, err := DiffUntrackedNumstat(ctx, dir, "new.txt")
	if err != nil {
		t.Fatalf("DiffUntrackedNumstat: %v", err)
	}
	if fs.Path != "new.txt" {
		t.Fatalf("path = %q, want new.txt", fs.Path)
	}
	if fs.Insertions != 3 || fs.Deletions != 0 {
		t.Fatalf("counts = +%d -%d, want +3 -0", fs.Insertions, fs.Deletions)
	}
	if fs.Binary() {
		t.Fatal("text file reported as binary")
	}
}

func TestCleanRemovesUntrackedKeepsTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "tracked.txt", "a\n")
	mustWrite(t, dir, "tracked.txt", "a\nb\n") // tracked edit — Clean must NOT touch
	mustWrite(t, dir, "new.txt", "x\n")        // untracked — Clean removes
	ctx := context.Background()

	if err := Clean(ctx, dir); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked new.txt should be gone, stat err = %v", err)
	}
	// The tracked edit survives Clean (only reset --hard would revert it).
	got, err := Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 || got[0].Path != "tracked.txt" || got[0].WorktreeState != 'M' {
		t.Fatalf("tracked edit should remain after Clean, got %+v", got)
	}
}

func mustWrite(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
