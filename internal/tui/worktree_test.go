package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// runGit shells out to git with deterministic identity env so the
// integration fixture below doesn't depend on ~/.gitconfig.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
}

func TestSwitchWorktreeRejectsMissingPath(t *testing.T) {
	m := New()
	prevWorkdir := m.workdir

	updated, cmd := m.Update(switchWorktreeMsg{path: filepath.Join(t.TempDir(), "missing")})
	got := updated.(Model)

	if got.workdir != prevWorkdir {
		t.Errorf("workdir mutated on validation failure: got %q want %q", got.workdir, prevWorkdir)
	}
	if got.statusStyle.GetForeground() != statusErrS.GetForeground() {
		t.Errorf("expected statusErrS on rejection, got style %v", got.statusStyle)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd on rejection, got %T", cmd)
	}
}

func TestSwitchWorktreeRejectsNonGitDirectory(t *testing.T) {
	m := New()
	// A real directory but no .git child → validateWorktreePath fails.
	dir := t.TempDir()

	updated, _ := m.Update(switchWorktreeMsg{path: dir})
	got := updated.(Model)

	if got.workdir == dir {
		t.Errorf("workdir should not have switched to non-git dir")
	}
	if got.statusStyle.GetForeground() != statusErrS.GetForeground() {
		t.Errorf("expected statusErrS, got %v", got.statusStyle)
	}
}

func TestSwitchWorktreeUpdatesWorkdirAndDispatchesReload(t *testing.T) {
	// Build a real git worktree fixture using the package-level git wrapper
	// so the switch handler's validateWorktreePath gate clears.
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, main, "init", "--initial-branch=main")
	runGit(t, main, "config", "user.email", "test@example.com")
	runGit(t, main, "config", "user.name", "Test")
	runGit(t, main, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, main, "add", "f")
	runGit(t, main, "commit", "-m", "init")

	feat := filepath.Join(root, "feat-a")
	if err := git.WorktreeAdd(context.Background(), main, feat, "feat-a", true); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = main

	updated, cmd := m.Update(switchWorktreeMsg{path: feat})
	got := updated.(Model)

	if got.workdir != feat {
		t.Errorf("workdir not updated: got %q want %q", got.workdir, feat)
	}
	if got.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("HEAD jump not armed: got %q", got.pendingHEADHash)
	}
	if got.statusStyle.GetForeground() != statusOkS.GetForeground() {
		t.Errorf("expected statusOkS on success, got %v", got.statusStyle)
	}
	if cmd == nil {
		t.Fatal("expected reload cmd batch, got nil")
	}
}

func TestSwitchWorktreeNoopOnSamePath(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, main, "init", "--initial-branch=main")
	runGit(t, main, "config", "user.email", "test@example.com")
	runGit(t, main, "config", "user.name", "Test")
	runGit(t, main, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, main, "add", "f")
	runGit(t, main, "commit", "-m", "init")

	m := New()
	m.workdir = main

	updated, cmd := m.Update(switchWorktreeMsg{path: main})
	got := updated.(Model)

	if cmd != nil {
		t.Errorf("same-path switch should not dispatch reload")
	}
	if got.workdir != main {
		t.Errorf("workdir changed on same-path switch")
	}
}
