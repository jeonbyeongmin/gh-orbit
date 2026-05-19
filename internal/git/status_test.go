package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// initEmptyRepo: bare init, no commit yet.
func initEmptyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
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

func TestAggregateNumstat(t *testing.T) {
	tests := []struct {
		name  string
		stats []FileStat
		want  LocalChangesSummary
	}{
		{name: "empty", stats: nil, want: LocalChangesSummary{}},
		{name: "single", stats: []FileStat{{Path: "foo.go", Insertions: 3, Deletions: 1}}, want: LocalChangesSummary{FilesChanged: 1, Insertions: 3, Deletions: 1}},
		{name: "multi", stats: []FileStat{{Path: "a", Insertions: 3, Deletions: 1}, {Path: "b", Insertions: 10, Deletions: 2}}, want: LocalChangesSummary{FilesChanged: 2, Insertions: 13, Deletions: 3}},
		{name: "binary counts file only", stats: []FileStat{{Path: "img.png", Insertions: -1, Deletions: -1}, {Path: "new.go", Insertions: 4, Deletions: 0}}, want: LocalChangesSummary{FilesChanged: 2, Insertions: 4, Deletions: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateNumstat(tt.stats)
			if got != tt.want {
				t.Fatalf("aggregateNumstat = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLocalChangesNumstatLive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\nb\nc\n")
	mustWrite(t, dir, "f.txt", "a\nb\nc\nd\ne\n")
	mustWrite(t, dir, "new.txt", "x\n")
	ctx := context.Background()
	if err := Add(ctx, dir, "new.txt"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	summary, err := LocalChangesNumstat(ctx, dir)
	if err != nil {
		t.Fatalf("LocalChangesNumstat: %v", err)
	}
	if summary.FilesChanged != 2 {
		t.Fatalf("FilesChanged = %d, want 2", summary.FilesChanged)
	}
	if summary.Insertions != 3 {
		t.Fatalf("Insertions = %d, want 3", summary.Insertions)
	}
	if summary.Deletions != 0 {
		t.Fatalf("Deletions = %d, want 0", summary.Deletions)
	}
}

func mustWrite(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
