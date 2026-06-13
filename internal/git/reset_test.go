package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// resetFixture builds base(C1) → C2 (adds b.txt) and returns the dir and the
// C1 hash that the reset tests move HEAD back to.
func resetFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := initRepoWithFile(t, "a.txt", "base\n")
	base := gitOutput(t, dir, "rev-parse", "HEAD")
	mustWrite(t, dir, "b.txt", "work\n")
	gitRun(t, dir, "add", "b.txt")
	gitRun(t, dir, "commit", "-m", "add b")
	return dir, base
}

func TestResetSoftMovesHeadKeepsIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir, base := resetFixture(t)
	if err := Reset(context.Background(), dir, ResetSoft, base); err != nil {
		t.Fatalf("Reset soft: %v", err)
	}
	if got := gitOutput(t, dir, "rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD = %s, want base %s", got, base)
	}
	// Soft keeps the dropped commit's change staged.
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "A  b.txt" {
		t.Errorf("status = %q, want staged 'A  b.txt'", st)
	}
}

func TestResetMixedMovesHeadUnstages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir, base := resetFixture(t)
	if err := Reset(context.Background(), dir, ResetMixed, base); err != nil {
		t.Fatalf("Reset mixed: %v", err)
	}
	if got := gitOutput(t, dir, "rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD = %s, want base %s", got, base)
	}
	// Mixed keeps the working-tree file but drops it from the index.
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "?? b.txt" {
		t.Errorf("status = %q, want untracked '?? b.txt'", st)
	}
}

func TestResetHardDiscards(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir, base := resetFixture(t)
	if err := Reset(context.Background(), dir, ResetHard, base); err != nil {
		t.Fatalf("Reset hard: %v", err)
	}
	if got := gitOutput(t, dir, "rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD = %s, want base %s", got, base)
	}
	// Hard wipes the dropped commit entirely — file and index both clean.
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("status = %q, want clean tree", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Errorf("b.txt should be gone after hard reset, stat err = %v", err)
	}
}
