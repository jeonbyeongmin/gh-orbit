package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupCherryPickConflict builds the TODO repro topology: two branches edit the
// same line, so cherry-picking one onto the other conflicts. It drives the
// conflict through the production CherryPick wrapper (which leaves the mid-pick
// state in place) and returns the parked repo + the picked hash.
func setupCherryPickConflict(t *testing.T) string {
	t.Helper()
	dir := initRepoWithFile(t, "app.go", "base\n")
	// main: rewrite line 2.
	mustWrite(t, dir, "app.go", "base\nmain\n")
	gitRun(t, dir, "commit", "-am", "main edits line2")
	// feature branches off the initial commit, rewrites line 2 differently.
	gitRun(t, dir, "checkout", "-b", "feature", "HEAD~1")
	mustWrite(t, dir, "app.go", "base\nfeature\n")
	gitRun(t, dir, "commit", "-am", "feature edits line2")
	pickHash := gitOutput(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "main")
	if err := CherryPick(context.Background(), dir, pickHash); err == nil {
		t.Fatal("cherry-pick onto a conflicting line should fail")
	}
	return dir
}

func TestDetectSequencerNoneOnCleanRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "base\n")
	kind, err := DetectSequencer(context.Background(), dir)
	if err != nil {
		t.Fatalf("DetectSequencer: %v", err)
	}
	if kind != SequencerNone {
		t.Fatalf("clean repo should report SequencerNone, got %v", kind)
	}
}

func TestDetectSequencerCherryPick(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := setupCherryPickConflict(t)
	kind, err := DetectSequencer(context.Background(), dir)
	if err != nil {
		t.Fatalf("DetectSequencer: %v", err)
	}
	if kind != SequencerCherryPick {
		t.Fatalf("mid cherry-pick should report SequencerCherryPick, got %v", kind)
	}
}

func TestSequencerContinueCompletesCherryPick(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := setupCherryPickConflict(t)
	ctx := context.Background()
	// Resolve + stage the conflict, then continue. SequencerContinue commits
	// through the wrapper (GIT_EDITOR suppressed), relying on the repo-local
	// identity initEmptyRepo pins.
	mustWrite(t, dir, "app.go", "base\nresolved\n")
	gitRun(t, dir, "add", "app.go")
	if err := SequencerContinue(ctx, dir, SequencerCherryPick); err != nil {
		t.Fatalf("SequencerContinue: %v", err)
	}
	kind, err := DetectSequencer(ctx, dir)
	if err != nil {
		t.Fatalf("DetectSequencer post-continue: %v", err)
	}
	if kind != SequencerNone {
		t.Fatalf("continue should clear the sequencer, got %v", kind)
	}
	// The cherry-picked commit landed on top of main's edit.
	if got := gitOutput(t, dir, "log", "--oneline"); len(got) == 0 {
		t.Fatal("expected commits after continue")
	}
	body, err := os.ReadFile(filepath.Join(dir, "app.go"))
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	if string(body) != "base\nresolved\n" {
		t.Errorf("app.go = %q, want resolved content committed", string(body))
	}
}

func TestSequencerAbortRestores(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := setupCherryPickConflict(t)
	ctx := context.Background()
	before := gitOutput(t, dir, "rev-parse", "HEAD")
	if err := SequencerAbort(ctx, dir, SequencerCherryPick); err != nil {
		t.Fatalf("SequencerAbort: %v", err)
	}
	kind, err := DetectSequencer(ctx, dir)
	if err != nil {
		t.Fatalf("DetectSequencer post-abort: %v", err)
	}
	if kind != SequencerNone {
		t.Fatalf("abort should clear the sequencer, got %v", kind)
	}
	// HEAD is back where the pick started and the tree is clean.
	if after := gitOutput(t, dir, "rev-parse", "HEAD"); after != before {
		t.Errorf("abort should leave HEAD at %s, got %s", before, after)
	}
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("abort should leave a clean tree, got %q", st)
	}
}

func TestDetectSequencerRevert(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "a.txt", "base\n")
	mustWrite(t, dir, "a.txt", "v2\n")
	gitRun(t, dir, "commit", "-am", "v2")
	target := gitOutput(t, dir, "rev-parse", "HEAD")
	// A later commit rewrites the same line, so reverting v2 conflicts and
	// leaves REVERT_HEAD behind.
	mustWrite(t, dir, "a.txt", "v3\n")
	gitRun(t, dir, "commit", "-am", "v3")
	if err := Revert(context.Background(), dir, target); err == nil {
		t.Fatal("conflicting revert should fail")
	}
	kind, err := DetectSequencer(context.Background(), dir)
	if err != nil {
		t.Fatalf("DetectSequencer: %v", err)
	}
	if kind != SequencerRevert {
		t.Fatalf("mid revert should report SequencerRevert, got %v", kind)
	}
}

func TestDetectSequencerMerge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initRepoWithFile(t, "app.go", "base\n")
	mustWrite(t, dir, "app.go", "base\nmain\n")
	gitRun(t, dir, "commit", "-am", "main edits line2")
	gitRun(t, dir, "checkout", "-b", "feature", "HEAD~1")
	mustWrite(t, dir, "app.go", "base\nfeature\n")
	gitRun(t, dir, "commit", "-am", "feature edits line2")
	gitRun(t, dir, "checkout", "main")
	// `git merge feature` conflicts on line 2 and leaves MERGE_HEAD. Driven
	// straight through git (no production merge wrapper exists yet).
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("merge should conflict, got success: %s", out)
	}
	kind, err := DetectSequencer(context.Background(), dir)
	if err != nil {
		t.Fatalf("DetectSequencer: %v", err)
	}
	if kind != SequencerMerge {
		t.Fatalf("mid merge should report SequencerMerge, got %v", kind)
	}
}

// Guard the abort/continue guards: a no-op kind builds no git command.
func TestSequencerNoneRejectsContinueAndAbort(t *testing.T) {
	ctx := context.Background()
	if err := SequencerContinue(ctx, t.TempDir(), SequencerNone); err == nil {
		t.Error("SequencerContinue(None) should error")
	}
	if err := SequencerAbort(ctx, t.TempDir(), SequencerNone); err == nil {
		t.Error("SequencerAbort(None) should error")
	}
}
