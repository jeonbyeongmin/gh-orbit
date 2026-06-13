package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRevertLiveUndoesCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "base\n")
	// Revert records a new commit through the production wrapper (no injected
	// identity), so the runner needs a repo-local one.
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	mustWrite(t, dir, "a.txt", "changed\n")
	gitRun(t, dir, "commit", "-am", "change a")
	target := gitOutput(t, dir, "rev-parse", "HEAD")

	if err := Revert(context.Background(), dir, target); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	// A new commit lands on top — HEAD moved past the reverted commit.
	if got := gitOutput(t, dir, "rev-parse", "HEAD"); got == target {
		t.Fatal("revert should add a commit, HEAD unchanged")
	}
	// The reverted change is gone from the working tree.
	body, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(body) != "base\n" {
		t.Errorf("a.txt = %q, want base restored", string(body))
	}
}

func TestRevertLiveConflictCarriesSentinel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "base\n")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	mustWrite(t, dir, "a.txt", "v2\n")
	gitRun(t, dir, "commit", "-am", "v2")
	target := gitOutput(t, dir, "rev-parse", "HEAD")
	// A later commit rewrites the same line, so reverting v2 conflicts.
	mustWrite(t, dir, "a.txt", "v3\n")
	gitRun(t, dir, "commit", "-am", "v3")

	err := Revert(context.Background(), dir, target)
	if err == nil {
		t.Fatal("conflicting revert should fail")
	}
	if !errors.Is(err, ErrRevertConflict) {
		t.Fatalf("error should carry ErrRevertConflict, got %v", err)
	}
}
