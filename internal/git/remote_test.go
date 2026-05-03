package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
