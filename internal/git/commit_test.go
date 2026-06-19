package git

import (
	"context"
	"os/exec"
	"testing"
)

// TestCommitStagedAdvancesHead stages a worktree edit, commits it, and asserts
// HEAD moved to a new commit, the tree is clean, and the message was recorded.
func TestCommitStagedAdvancesHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	before := gitOutput(t, dir, "rev-parse", "HEAD")
	mustWrite(t, dir, "f.txt", "a\nb\n")
	gitRun(t, dir, "add", "f.txt")
	ctx := context.Background()

	if err := CommitStaged(ctx, dir, "add b"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if after := gitOutput(t, dir, "rev-parse", "HEAD"); after == before {
		t.Errorf("HEAD did not advance, still %s", after)
	}
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("status = %q, want clean tree after commit", st)
	}
	if subj := gitOutput(t, dir, "log", "-1", "--format=%s"); subj != "add b" {
		t.Errorf("subject = %q, want %q", subj, "add b")
	}
}

// TestCommitEmptyIndexFails confirms git rejects a commit with nothing staged —
// the failure the TUI's StagedCount gate exists to pre-empt, surfaced here so a
// regression in that gate still can't silently produce an empty commit.
func TestCommitEmptyIndexFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "f.txt", "a\n")
	if err := CommitStaged(context.Background(), dir, "nothing"); err == nil {
		t.Fatal("Commit with empty index should fail")
	}
}
