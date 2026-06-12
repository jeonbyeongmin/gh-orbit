package git

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestRebaseLiveFastForwardAndReplay(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "base\n")
	base := gitOutput(t, dir, "rev-parse", "HEAD")
	// main advances with a non-conflicting file; feature branches off base.
	gitRun(t, dir, "checkout", "-b", "feature", base)
	mustWrite(t, dir, "b.txt", "feature work\n")
	gitRun(t, dir, "add", "b.txt")
	gitRun(t, dir, "commit", "-m", "feature: add b")
	gitRun(t, dir, "checkout", "-")
	mustWrite(t, dir, "c.txt", "main work\n")
	gitRun(t, dir, "add", "c.txt")
	gitRun(t, dir, "commit", "-m", "main: add c")
	mainTip := gitOutput(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "feature")

	if err := Rebase(context.Background(), dir, mainTip); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	// feature now sits on top of mainTip: mainTip must be an ancestor.
	if got := gitOutput(t, dir, "merge-base", "HEAD", mainTip); got != mainTip {
		t.Errorf("after rebase, merge-base = %s, want %s (onto-commit as ancestor)", got, mainTip)
	}
}

func TestRebaseLiveConflictCarriesSentinel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "base\n")
	base := gitOutput(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "-b", "feature", base)
	mustWrite(t, dir, "a.txt", "feature edit\n")
	gitRun(t, dir, "commit", "-am", "feature: edit a")
	gitRun(t, dir, "checkout", "-")
	mustWrite(t, dir, "a.txt", "main edit\n")
	gitRun(t, dir, "commit", "-am", "main: edit a")
	mainTip := gitOutput(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "feature")

	err := Rebase(context.Background(), dir, mainTip)
	if err == nil {
		t.Fatal("conflicting rebase should fail")
	}
	if !errors.Is(err, ErrRebaseConflict) {
		t.Fatalf("error should carry ErrRebaseConflict, got %v", err)
	}
	// Leave-in-place contract: REBASE_HEAD must exist mid-rebase —
	// gitOutput fails the test itself if the ref is gone.
	_ = gitOutput(t, dir, "rev-parse", "--verify", "REBASE_HEAD")
}
