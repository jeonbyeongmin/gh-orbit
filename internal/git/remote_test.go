package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFetchReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	err := Fetch(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error running git fetch outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

// TestFetchIntegration wires up a self-hosted bare remote so the happy path
// stays deterministic — no network, no credentials.
func TestFetchIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "push", "origin", "main")

	if err := Fetch(context.Background(), work); err != nil {
		t.Fatalf("Fetch happy path: %v", err)
	}
}

func TestPullStrategyArgs(t *testing.T) {
	cases := []struct {
		s    PullStrategy
		want []string
	}{
		{PullStrategyFFOnly, []string{"--ff-only"}},
		{PullStrategyMerge, []string{"--no-rebase"}},
		{PullStrategyRebase, []string{"--rebase"}},
	}
	for _, c := range cases {
		got := c.s.args()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("PullStrategy(%d).args() = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestPullReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	err := Pull(context.Background(), dir, PullStrategyFFOnly)
	if err == nil {
		t.Fatal("expected error running git pull outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func TestPullIntegrationFastForward(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	other := filepath.Join(root, "other")

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "push", "origin", "main")
	gitRun(t, work, "branch", "--set-upstream-to=origin/main", "main")

	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	gitRun(t, other, "clone", bare, ".")
	gitRun(t, other, "config", "user.name", "Other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	gitRun(t, other, "commit", "--allow-empty", "-m", "second from other")
	gitRun(t, other, "push", "origin", "main")

	if err := Pull(context.Background(), work, PullStrategyFFOnly); err != nil {
		t.Fatalf("Pull happy path: %v", err)
	}
}

func TestPullIntegrationConflictWraps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	other := filepath.Join(root, "other")

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")
	gitRun(t, work, "push", "origin", "main")
	gitRun(t, work, "branch", "--set-upstream-to=origin/main", "main")

	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	gitRun(t, other, "clone", bare, ".")
	gitRun(t, other, "config", "user.name", "Other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	if err := os.WriteFile(filepath.Join(other, "f.txt"), []byte("remote-side\n"), 0o644); err != nil {
		t.Fatalf("write other f.txt: %v", err)
	}
	gitRun(t, other, "add", "f.txt")
	gitRun(t, other, "commit", "-m", "remote change")
	gitRun(t, other, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("local-side\n"), 0o644); err != nil {
		t.Fatalf("write work f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "local change")

	err := Pull(context.Background(), work, PullStrategyMerge)
	if err == nil {
		t.Fatal("expected pull conflict error")
	}
	if !errors.Is(err, ErrPullConflict) {
		t.Errorf("error %q should wrap ErrPullConflict", err)
	}
}
